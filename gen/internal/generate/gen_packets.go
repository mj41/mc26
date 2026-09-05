// gen_packets generates the protocol/<state> packages (one Go struct with
// ReadFrom/WriteTo per packet) and protocol/types/{enums,structs}_gen.go from
// packet_schema.json (format 2, GenPacketSchema) joined with packets.json.
package generate

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type schemaNode map[string]any

type schemaEntry struct {
	State    string     `json:"state"`
	Class    string     `json:"class"`
	Coverage string     `json:"coverage"`
	Type     schemaNode `json:"type"`
}

type packetSchemaFile struct {
	Version int                    `json:"version"`
	Packets map[string]schemaEntry `json:"packets"`
	Structs map[string]schemaEntry `json:"structs"`
}

// packetsReport is packets.json: state -> flow -> name -> {protocol_id}.
type packetsReport map[string]map[string]map[string]struct {
	ProtocolID int `json:"protocol_id"`
}

// genState collects the enums and shared structs discovered while typing.
type genState struct {
	enums      map[string]*pktEnumDef // Go name -> def
	structs    map[string]*structDef  // Go name -> def
	order      []string               // struct emission order (dependencies first)
	fixedBits  map[int]bool           // sizes of fixed bit sets seen (readFixedBitSet(n))
	fixedBytes map[int]bool           // sizes of fixed byte blocks seen (readBytes(n))
}

type pktEnumDef struct {
	GoName string
	Java   string
	Values []string
}

type structDef struct {
	GoName string
	Java   string
	Fields []goField
}

type goField struct {
	Name, Type, Comment string
}

// handWrittenTypes are structures kept by hand in protocol/types/types.go; a
// schema struct with one of these names is not generated but mapped to it.
var handWrittenTypes = map[string]bool{
	"Vec3": true, "Vector3f": true, "Quaternionf": true, "GlobalPos": true, "ChunkPos": true,
	"SectionPos": true, "LpVec3": true, "ItemStack": true, "GameProfile": true, "Property": true,
	"MessageSignature": true, "BlockHitResult": true, "NBT": true, "OptionalNBT": true,
	"ComponentPatch": true, "AddedComponent": true, "Empty": true, "Text": true, "IDSet": true,
	"ChatTypeBound": true, // chat.Type: holder or inline chat type with the decoration logic
}

// primTypes maps schema primitives to Go types (import alias: pk or types).
var primTypes = map[string]string{
	"BOOL": "pk.Boolean", "BYTE": "pk.Byte", "UNSIGNED_BYTE": "pk.UnsignedByte", "SHORT": "pk.Short",
	"UNSIGNED_SHORT": "pk.UnsignedShort", "INT": "pk.Int", "UNSIGNED_INT": "pk.Int", "LONG": "pk.Long",
	"FLOAT": "pk.Float", "DOUBLE": "pk.Double", "VAR_INT": "pk.VarInt", "VAR_LONG": "pk.VarLong",
	"STRING": "pk.String", "UUID": "pk.UUID", "IDENTIFIER": "pk.Identifier", "RESOURCE_KEY": "pk.Identifier",
	"REGISTRY_KEY": "pk.Identifier", "BYTE_ARRAY": "pk.ByteArray", "BIT_SET": "pk.BitSet",
	"INSTANT": "types.Instant", "BLOCK_POS": "pk.Position", "CHUNK_POS": "types.ChunkPos",
	"SECTION_POS": "types.SectionPos", "GLOBAL_POS": "types.GlobalPos", "VEC3": "types.Vec3",
	"LP_VEC3": "types.LpVec3", "VECTOR3F": "types.Vector3f", "QUATERNIONF": "types.Quaternionf",
	"NBT": "types.NBT", "OPTIONAL_NBT": "types.OptionalNBT", "TEXT": "types.Text",
	"OPTIONAL_TEXT": "pk.Option[types.Text, *types.Text]", "ITEM_STACK": "types.ItemStack",
	"OPTIONAL_ITEM_STACK": "types.ItemStack", "OPTIONAL_ITEM_STACK_LIST": "types.List[types.ItemStack, *types.ItemStack]",
	"COMPONENT_PATCH": "types.ComponentPatch", "GAME_PROFILE": "types.GameProfile", "PUBLIC_KEY": "types.PublicKey",
	"MESSAGE_SIGNATURE": "types.MessageSignature", "JSON_TEXT": "pk.String", "JSON": "pk.String",
	"RAW_BYTES": "types.RestBytes", "REST_BYTES": "types.RestBytes", "CONTAINER_ID": "pk.VarInt",
	"OPTIONAL_VAR_INT": "types.OptionalVarInt", "VAR_INT_LIST": "types.List[pk.VarInt, *pk.VarInt]",
	"VAR_INT_ARRAY": "types.List[pk.VarInt, *pk.VarInt]", "LONG_ARRAY": "types.List[pk.Long, *pk.Long]",
	"ROTATION_BYTE": "pk.Angle", "CHAR": "pk.UnsignedShort", "BLOCK_HIT_RESULT": "types.BlockHitResult",
}

func genPackets(jsonDir, goMCRoot string) error {
	var schema packetSchemaFile
	if err := readJSON(filepath.Join(jsonDir, "packet_schema.json"), &schema); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	if schema.Version != 2 {
		return fmt.Errorf("genPackets: packet_schema.json format %d, need 2 (re-run extraction)", schema.Version)
	}
	var ids packetsReport
	if err := readJSON(filepath.Join(jsonDir, "packets.json"), &ids); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	phases, err := loadPacketPhases(goMCRoot)
	if err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}

	// Packets implemented by hand are skipped whatever the schema says; when
	// the schema types one of them fully, that is reported so the hand-written
	// version can be retired.
	var handPackets map[string]string
	if err := readHandCrafted(goMCRoot, "hand_packets.json", &handPackets); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	delete(handPackets, "_comment")

	gs := &genState{enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, fixedBits: map[int]bool{}, fixedBytes: map[int]bool{}}
	total, generated := 0, 0
	for _, state := range sortedKeys(ids) {
		if state == "handshake" {
			continue // no packetid constants for the handshake; the framework writes it by hand
		}
		abbrev, ok := phases[state]
		if !ok {
			continue
		}
		for _, flow := range []string{"clientbound", "serverbound"} {
			names := ids[state][flow]
			if len(names) == 0 {
				continue
			}
			var pkts []packetDef
			var skipped []string
			for _, name := range sortedKeys(names) {
				total++
				entry, ok := schema.Packets[flow+"/"+name]
				if !ok {
					skipped = append(skipped, name+" (not in packet_schema.json)")
					continue
				}
				if reason, hand := handPackets[state+"/"+flow+"/"+name]; hand {
					skipped = append(skipped, name+" (hand-written: "+reason+")")
					if entry.Coverage == "full" {
						logf("genPackets: %s/%s/%s is fully typed in the schema now; the hand-written version in hand.go could be retired", state, flow, name)
					}
					continue
				}
				if entry.Coverage != "full" {
					skipped = append(skipped, name+" ("+holeSummary(entry.Type)+")")
					continue
				}
				def, err := gs.packetDef(state, flow, name, names[name].ProtocolID, entry, abbrev)
				if err != nil {
					skipped = append(skipped, name+" ("+err.Error()+")")
					continue
				}
				pkts = append(pkts, def)
				generated++
			}
			sort.Slice(pkts, func(i, j int) bool { return pkts[i].ID < pkts[j].ID })
			gs.resolveNameCollisions(pkts, ids[state], state)
			out := filepath.Join(goMCRoot, "protocol", state, flow+"_gen.go")
			if err := writeGo(out, renderPacketFile(state, flow, pkts, skipped)); err != nil {
				return fmt.Errorf("genPackets: %w", err)
			}
			if err := writeGo(filepath.Join(goMCRoot, "protocol", state, flow+"_gen_test.go"), renderPacketTest(state, flow, pkts)); err != nil {
				return fmt.Errorf("genPackets: %w", err)
			}
		}
	}
	if err := writeGo(filepath.Join(goMCRoot, "protocol", "types", "enums_gen.go"), gs.renderEnums()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	if err := writeGo(filepath.Join(goMCRoot, "protocol", "types", "structs_gen.go"), gs.renderStructs()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	logf("genPackets: wrote protocol/<state>/*_gen.go (%d of %d packets, %d enums, %d shared structs)", generated, total, len(gs.enums), len(gs.structs))
	return nil
}

// writeGo formats Go source and writes it; on a format error the raw text is
// written so the compiler shows the problem.
func writeGo(path string, src string) error {
	formatted, err := format.Source([]byte(src))
	if err != nil {
		logf("genPackets: %s: not gofmt-clean (%v), writing raw", filepath.Base(path), err)
		formatted = []byte(src)
	}
	return writeFile(path, formatted)
}

func loadPacketPhases(goMCRoot string) (map[string]string, error) {
	var phases []struct {
		Name     string `json:"name"`
		GoPrefix string `json:"go_prefix"`
	}
	if err := readJSON(filepath.Join(assetsDir, "hand-crafted", "packet_phases.json"), &phases); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range phases {
		out[p.Name] = p.GoPrefix
	}
	return out, nil
}

type packetDef struct {
	GoName   string // AddEntity
	Java     string // ClientboundAddEntityPacket
	MCName   string // minecraft:add_entity
	ID       int
	IDConst  string // packetid.ClientboundAddEntity
	IDType   string // packetid.ClientboundPacketID
	Fields   []goField
	baseName string
}

func (gs *genState) packetDef(state, flow, name string, id int, e schemaEntry, abbrev string) (packetDef, error) {
	var fields []goField
	switch e.Type["k"] {
	case "struct":
		var err error
		fields, err = gs.fieldsOf(e.Type, e.Class)
		if err != nil {
			return packetDef{}, err
		}
	case "unit":
	default:
		// a packet whose whole payload is one value (a text, a map, a registry id…)
		typ, comment, err := gs.goType(e.Type, e.Class)
		if err != nil {
			return packetDef{}, err
		}
		fields = []goField{{Name: "Value", Type: typ, Comment: comment}}
	}
	base := goPacketName(e.Class)
	idType := "packetid.ClientboundPacketID"
	if flow == "serverbound" {
		idType = "packetid.ServerboundPacketID"
	}
	return packetDef{
		GoName:   base,
		baseName: base,
		Java:     e.Class,
		MCName:   name,
		ID:       id,
		IDConst:  "packetid." + packetGoName(name, dirPrefix(flow), abbrev),
		IDType:   idType,
		Fields:   fields,
	}, nil
}

// resolveNameCollisions prefixes the flow when the same Go name exists in both
// flows of a state (KeepAlive, Ping, CustomPayload, …).
func (gs *genState) resolveNameCollisions(pkts []packetDef, flows map[string]map[string]struct {
	ProtocolID int `json:"protocol_id"`
}, state string) {
	other := map[string]bool{}
	for flow, names := range flows {
		for name := range names {
			other[flow+"/"+goPacketNameFromMC(name)] = true
		}
	}
	for i := range pkts {
		base := pkts[i].baseName
		if other["clientbound/"+base] && other["serverbound/"+base] {
			if strings.HasPrefix(pkts[i].Java, "net.minecraft.network.protocol.") && strings.Contains(pkts[i].Java, ".Clientbound") {
				pkts[i].GoName = "Clientbound" + base
			} else {
				pkts[i].GoName = "Serverbound" + base
			}
		}
	}
}

// goPacketName derives the Go type name from the Java class name:
// ClientboundAddEntityPacket → AddEntity, ClientboundMoveEntityPacket$Pos → MoveEntityPos.
func goPacketName(javaClass string) string {
	short := javaClass[strings.LastIndex(javaClass, ".")+1:]
	parts := strings.Split(short, "$")
	var sb strings.Builder
	for _, p := range parts {
		p = strings.TrimPrefix(strings.TrimPrefix(p, "Clientbound"), "Serverbound")
		p = strings.TrimSuffix(p, "Packet")
		sb.WriteString(p)
	}
	return sb.String()
}

// goPacketNameFromMC is the same derivation from the registry name (for collision checks).
func goPacketNameFromMC(mcName string) string {
	return packetGoName(mcName, "", "")
}

// fieldsOf types the fields of a struct node.
func (gs *genState) fieldsOf(n schemaNode, owner string) ([]goField, error) {
	raw, _ := n["fields"].([]any)
	var out []goField
	used := map[string]int{}
	for i, f := range raw {
		fm := f.(map[string]any)
		jname, _ := fm["name"].(string)
		if strings.HasPrefix(jname, "lambda$") || strings.ContainsAny(jname, "$") {
			jname = fmt.Sprintf("v%d", i) // synthetic getter: no field name in the bytecode
		}
		name := goFieldName(jname)
		if used[name] > 0 {
			name = fmt.Sprintf("%s%d", name, used[name]+1)
		}
		used[name]++
		typ, comment, err := gs.goType(schemaNode(fm["type"].(map[string]any)), owner)
		if err != nil {
			return nil, err
		}
		out = append(out, goField{Name: name, Type: typ, Comment: comment})
	}
	return out, nil
}

// goType returns the Go type expression for a node, registering enums and
// structs as it goes.
func (gs *genState) goType(n schemaNode, owner string) (string, string, error) {
	switch n["k"] {
	case "prim":
		t := n["t"].(string)
		if t == "FIXED_BIT_SET" {
			bits, ok := n["bits"].(float64)
			if !ok || bits <= 0 {
				return "", "", fmt.Errorf("fixed bit set without size")
			}
			gs.fixedBits[int(bits)] = true
			return fmt.Sprintf("types.FixedBits%d", int(bits)), "", nil
		}
		if t == "FIXED_BYTES" {
			l, ok := n["len"].(float64)
			if !ok || l <= 0 {
				return "", "", fmt.Errorf("fixed byte block without size")
			}
			if int(l) == 256 {
				return "types.MessageSignature", "", nil
			}
			gs.fixedBytes[int(l)] = true
			return fmt.Sprintf("types.FixedBytes%d", int(l)), "", nil
		}
		if gt, ok := primTypes[t]; ok {
			return gt, "", nil
		}
		return "", "", fmt.Errorf("prim %s", t)
	case "string":
		return "pk.String", "", nil
	case "unit":
		return "types.Empty", "", nil
	case "nbt":
		return "types.NBT", "", nil
	case "registry":
		return "pk.VarInt", "registry " + str(n["registry"]), nil
	case "resourcekey":
		return "pk.Identifier", "resource key in " + str(n["registry"]), nil
	case "holderset":
		return "types.IDSet", "registry " + str(n["registry"]), nil
	case "holder":
		if d, ok := n["direct"].(map[string]any); ok {
			dt, _, err := gs.goType(schemaNode(d), owner)
			if err != nil {
				return "", "", err
			}
			return fmt.Sprintf("types.Holder[%s, *%s]", dt, dt), "registry " + str(n["registry"]), nil
		}
		return "pk.VarInt", "holder in " + str(n["registry"]), nil
	case "list":
		et, _, err := gs.goType(schemaNode(n["elem"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("types.List[%s, *%s]", et, et), "", nil
	case "optional":
		et, _, err := gs.goType(schemaNode(n["elem"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("pk.Option[%s, *%s]", et, et), "", nil
	case "map":
		kt, _, err := gs.goType(schemaNode(n["key"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		vt, _, err := gs.goType(schemaNode(n["val"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("types.Map[%s, *%s, %s, *%s]", kt, kt, vt, vt), "", nil
	case "enum":
		vals, _ := n["values"].([]any)
		if len(vals) == 0 {
			return "", "", fmt.Errorf("enum %v without values", n["name"])
		}
		name := gs.enum(str(n["name"]), vals)
		return "types." + name, "", nil
	case "enumset":
		vals, _ := n["values"].([]any)
		if len(vals) == 0 {
			return "", "", fmt.Errorf("enumset %v without values", n["name"])
		}
		name := gs.enum(str(n["name"]), vals)
		return "types.EnumSet[types." + name + "]", "", nil
	case "struct":
		if goName := goTypeName(str(n["name"])); handWrittenTypes[goName] {
			return "types." + goName, "", nil // shape kept by hand in protocol/types/types.go
		}
		name, err := gs.structType(n, owner)
		if err != nil {
			return "", "", err
		}
		return "types." + name, "", nil
	default:
		return "", "", fmt.Errorf("node %v", n["k"])
	}
}

func (gs *genState) enum(java string, vals []any) string {
	goName := goTypeName(java)
	var values []string
	for _, v := range vals {
		values = append(values, v.(string))
	}
	for {
		if e, ok := gs.enums[goName]; ok {
			if strings.Join(e.Values, ",") == strings.Join(values, ",") {
				return goName
			}
			if e.Java == java {
				return goName
			}
			goName += "_" // different enum with the same short name
			continue
		}
		break
	}
	gs.enums[goName] = &pktEnumDef{GoName: goName, Java: java, Values: values}
	return goName
}

func (gs *genState) structType(n schemaNode, owner string) (string, error) {
	java := str(n["name"])
	goName := goTypeName(java)
	fields, err := gs.fieldsOf(n, java)
	if err != nil {
		return "", err
	}
	sig := fieldsSig(fields)
	for {
		if s, ok := gs.structs[goName]; ok {
			if fieldsSig(s.Fields) == sig {
				return goName, nil
			}
			goName += "_"
			continue
		}
		break
	}
	gs.structs[goName] = &structDef{GoName: goName, Java: java, Fields: fields}
	gs.order = append(gs.order, goName)
	return goName, nil
}

func fieldsSig(fs []goField) string {
	var sb strings.Builder
	for _, f := range fs {
		sb.WriteString(f.Name + ":" + f.Type + ";")
	}
	return sb.String()
}

// goTypeName turns a Java short name (with $ for inner classes) into a Go type name.
func goTypeName(java string) string {
	java = java[strings.LastIndex(java, "/")+1:]
	java = java[strings.LastIndex(java, ".")+1:]
	var sb strings.Builder
	for _, p := range strings.Split(java, "$") {
		p = strings.TrimPrefix(strings.TrimPrefix(p, "Clientbound"), "Serverbound")
		p = strings.TrimSuffix(p, "Packet")
		if p == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return sb.String()
}

var initialisms = map[string]string{"id": "ID", "uuid": "UUID", "url": "URL", "x": "X", "y": "Y", "z": "Z", "nbt": "NBT", "json": "JSON", "xp": "XP", "html": "HTML", "ip": "IP"}

// goFieldName exports a Java field name: entityId → EntityID, xRot → XRot.
func goFieldName(java string) string {
	if java == "" {
		return "Field"
	}
	if v, ok := initialisms[java]; ok {
		return v
	}
	// accessor-style getter names (getContainerId) name the property; "is…" stays,
	// record components are literally named isDebug/isFlat
	if strings.HasPrefix(java, "get") && len(java) > 3 && unicode.IsUpper(rune(java[3])) {
		java = java[3:]
	}
	// split camelCase into words, then re-join with initialisms applied
	var words []string
	start := 0
	for i := 1; i < len(java); i++ {
		if unicode.IsUpper(rune(java[i])) && !unicode.IsUpper(rune(java[i-1])) {
			words = append(words, java[start:i])
			start = i
		}
	}
	words = append(words, java[start:])
	var sb strings.Builder
	for _, w := range words {
		lw := strings.ToLower(w)
		if v, ok := initialisms[lw]; ok {
			sb.WriteString(v)
			continue
		}
		sb.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	name := sb.String()
	if !unicode.IsLetter(rune(name[0])) {
		name = "F" + name
	}
	return name
}

func holeSummary(n schemaNode) string {
	var holes []string
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		switch n["k"] {
		case "opaque":
			holes = append(holes, "opaque:"+str(n["java"]))
		case "dispatch", "either":
			holes = append(holes, str(n["k"]))
		}
		if b, _ := n["conditional"].(bool); b {
			holes = append(holes, "conditional reader")
		}
		for _, v := range n {
			switch t := v.(type) {
			case map[string]any:
				if _, ok := t["k"]; ok {
					walk(t)
				}
			case []any:
				for _, f := range t {
					if fm, ok := f.(map[string]any); ok {
						if tt, ok := fm["type"].(map[string]any); ok {
							walk(tt)
						}
					}
				}
			}
		}
	}
	walk(n)
	seen := map[string]bool{}
	var out []string
	for _, h := range holes {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return strings.Join(out, ", ")
}

func str(v any) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprint(v)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- rendering ---------------------------------------------------------------

func renderPacketFile(state, flow string, pkts []packetDef, skipped []string) string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_packets.go", "packet_schema.json + packets.json"))
	fmt.Fprintf(&sb, "// Package %s holds the generated %s packets of the %s state.\n", state, jsonVersion, state)
	fmt.Fprintf(&sb, "package %s\n\n", state)
	sb.WriteString("import (\n\t\"io\"\n\n\t\"github.com/mj41/go-mc26/data/packetid\"\n\tpk \"github.com/mj41/go-mc26/net/packet\"\n\t\"github.com/mj41/go-mc26/protocol/types\"\n)\n\n")
	sb.WriteString("var (\n\t_ = io.EOF\n\t_ pk.Field\n\t_ types.Empty\n)\n\n")
	if len(skipped) > 0 {
		fmt.Fprintf(&sb, "// Not generated for %s %s (needs a hand rule):\n", state, flow)
		for _, s := range skipped {
			fmt.Fprintf(&sb, "//   - %s\n", s)
		}
		sb.WriteString("\n")
	}
	for _, p := range pkts {
		fmt.Fprintf(&sb, "// %s is %s/%s (0x%02X), Java %s.\n", p.GoName, flow, p.MCName, p.ID, p.Java[strings.LastIndex(p.Java, ".")+1:])
		if len(p.Fields) == 0 {
			fmt.Fprintf(&sb, "type %s struct{ types.Empty }\n\n", p.GoName)
		} else {
			fmt.Fprintf(&sb, "type %s struct {\n", p.GoName)
			for _, f := range p.Fields {
				if f.Comment != "" {
					fmt.Fprintf(&sb, "\t%s %s // %s\n", f.Name, f.Type, f.Comment)
				} else {
					fmt.Fprintf(&sb, "\t%s %s\n", f.Name, f.Type)
				}
			}
			sb.WriteString("}\n\n")
		}
		fmt.Fprintf(&sb, "// PacketID returns the %s id of %s.\nfunc (%s) PacketID() %s { return %s }\n\n", flow, p.GoName, p.GoName, p.IDType, p.IDConst)
		if len(p.Fields) > 0 {
			fmt.Fprintf(&sb, "func (p *%s) ReadFrom(r io.Reader) (int64, error) {\n\treturn pk.Tuple{%s}.ReadFrom(r)\n}\n\n", p.GoName, fieldRefs(p.Fields, "&p."))
			fmt.Fprintf(&sb, "func (p %s) WriteTo(w io.Writer) (int64, error) {\n\treturn pk.Tuple{%s}.WriteTo(w)\n}\n\n", p.GoName, fieldRefs(p.Fields, "p."))
		}
	}
	return sb.String()
}

func fieldRefs(fs []goField, prefix string) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = prefix + f.Name
	}
	return strings.Join(parts, ", ")
}

func renderPacketTest(state, flow string, pkts []packetDef) string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_packets.go", "packet_schema.json + packets.json"))
	fmt.Fprintf(&sb, "package %s\n\nimport (\n\t\"bytes\"\n\t\"io\"\n\t\"testing\"\n)\n\n", state)
	fmt.Fprintf(&sb, "// Test%sRoundTrip encodes the zero value of every generated %s packet,\n// decodes it and encodes it again; the two encodings must match.\n", strings.ToUpper(flow[:1])+flow[1:], flow)
	fmt.Fprintf(&sb, "func Test%sRoundTrip(t *testing.T) {\n", strings.ToUpper(flow[:1])+flow[1:])
	sb.WriteString("\ttype codec interface {\n\t\tio.WriterTo\n\t}\n\tcases := []struct {\n\t\tname string\n\t\tzero codec\n\t\tread func(io.Reader) (codec, error)\n\t}{\n")
	for _, p := range pkts {
		if len(p.Fields) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\t\t{%q, %s{}, func(r io.Reader) (codec, error) { var v %s; _, err := v.ReadFrom(r); return v, err }},\n", p.GoName, p.GoName, p.GoName)
	}
	sb.WriteString("\t}\n\tfor _, c := range cases {\n\t\tt.Run(c.name, func(t *testing.T) {\n\t\t\tvar a bytes.Buffer\n\t\t\tif _, err := c.zero.WriteTo(&a); err != nil {\n\t\t\t\tt.Fatalf(\"encode: %v\", err)\n\t\t\t}\n\t\t\tv, err := c.read(bytes.NewReader(a.Bytes()))\n\t\t\tif err != nil {\n\t\t\t\tt.Fatalf(\"decode %x: %v\", a.Bytes(), err)\n\t\t\t}\n\t\t\tvar b bytes.Buffer\n\t\t\tif _, err := v.WriteTo(&b); err != nil {\n\t\t\t\tt.Fatalf(\"re-encode: %v\", err)\n\t\t\t}\n\t\t\tif !bytes.Equal(a.Bytes(), b.Bytes()) {\n\t\t\t\tt.Fatalf(\"round trip differs: %x vs %x\", a.Bytes(), b.Bytes())\n\t\t\t}\n\t\t})\n\t}\n}\n")
	return sb.String()
}

func (gs *genState) renderEnums() string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_packets.go", "packet_schema.json"))
	sb.WriteString("package types\n\nimport (\n\t\"io\"\n\n\tpk \"github.com/mj41/go-mc26/net/packet\"\n)\n\n")
	for _, name := range sortedKeys(gs.enums) {
		e := gs.enums[name]
		fmt.Fprintf(&sb, "// %s is Java %s, sent as a VarInt ordinal.\ntype %s pk.VarInt\n\nconst (\n", name, e.Java, name)
		for i, v := range e.Values {
			if i == 0 {
				fmt.Fprintf(&sb, "\t%s%s %s = iota\n", name, goEnumConst(v), name)
			} else {
				fmt.Fprintf(&sb, "\t%s%s\n", name, goEnumConst(v))
			}
		}
		sb.WriteString(")\n\n")
		fmt.Fprintf(&sb, "func (e *%s) ReadFrom(r io.Reader) (int64, error) { return (*pk.VarInt)(e).ReadFrom(r) }\n", name)
		fmt.Fprintf(&sb, "func (e %s) WriteTo(w io.Writer) (int64, error)  { return pk.VarInt(e).WriteTo(w) }\n", name)
		fmt.Fprintf(&sb, "// Count is the number of constants (EnumSet[%s] needs it for its bit set size).\nfunc (%s) Count() int { return %d }\n\n", name, name, len(e.Values))
	}
	var sizes []int
	for bits := range gs.fixedBits {
		sizes = append(sizes, bits)
	}
	sort.Ints(sizes)
	for _, bits := range sizes {
		n := (bits + 7) / 8
		fmt.Fprintf(&sb, "// FixedBits%d is FriendlyByteBuf.readFixedBitSet(%d): %d byte(s).\ntype FixedBits%d [%d]byte\n\n", bits, bits, n, bits, n)
		fmt.Fprintf(&sb, "func (b *FixedBits%d) ReadFrom(r io.Reader) (int64, error) { n, err := io.ReadFull(r, b[:]); return int64(n), err }\n", bits)
		fmt.Fprintf(&sb, "func (b FixedBits%d) WriteTo(w io.Writer) (int64, error)  { n, err := w.Write(b[:]); return int64(n), err }\n\n", bits)
	}
	var lens []int
	for l := range gs.fixedBytes {
		lens = append(lens, l)
	}
	sort.Ints(lens)
	for _, l := range lens {
		fmt.Fprintf(&sb, "// FixedBytes%d is a fixed block of %d bytes (readBytes(%d)).\ntype FixedBytes%d [%d]byte\n\n", l, l, l, l, l)
		fmt.Fprintf(&sb, "func (b *FixedBytes%d) ReadFrom(r io.Reader) (int64, error) { n, err := io.ReadFull(r, b[:]); return int64(n), err }\n", l)
		fmt.Fprintf(&sb, "func (b FixedBytes%d) WriteTo(w io.Writer) (int64, error)  { n, err := w.Write(b[:]); return int64(n), err }\n\n", l)
	}
	return sb.String()
}

func goEnumConst(v string) string {
	var sb strings.Builder
	for _, w := range strings.Split(strings.ToLower(v), "_") {
		if w == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	s := sb.String()
	if s == "" || !unicode.IsLetter(rune(s[0])) {
		s = "V" + s
	}
	return s
}

func (gs *genState) renderStructs() string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_packets.go", "packet_schema.json"))
	sb.WriteString("package types\n\nimport (\n\t\"io\"\n\n\tpk \"github.com/mj41/go-mc26/net/packet\"\n)\n\n")
	for _, name := range gs.order {
		s := gs.structs[name]
		fmt.Fprintf(&sb, "// %s is Java %s.\ntype %s struct {\n", name, s.Java, name)
		for _, f := range s.Fields {
			t := strings.ReplaceAll(f.Type, "types.", "")
			if f.Comment != "" {
				fmt.Fprintf(&sb, "\t%s %s // %s\n", f.Name, t, f.Comment)
			} else {
				fmt.Fprintf(&sb, "\t%s %s\n", f.Name, t)
			}
		}
		sb.WriteString("}\n\n")
		fmt.Fprintf(&sb, "func (v *%s) ReadFrom(r io.Reader) (int64, error) {\n\treturn pk.Tuple{%s}.ReadFrom(r)\n}\n\n", name, fieldRefs(s.Fields, "&v."))
		fmt.Fprintf(&sb, "func (v %s) WriteTo(w io.Writer) (int64, error) {\n\treturn pk.Tuple{%s}.WriteTo(w)\n}\n\n", name, fieldRefs(s.Fields, "v."))
	}
	return sb.String()
}
