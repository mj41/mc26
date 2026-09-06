// gen_packets generates the protocol/<state> packages (one Go struct with
// ReadFrom/WriteTo per packet) and protocol/types/{enums,structs}_gen.go from
// packet_schema.json (format 2, GenPacketSchema) joined with packets.json.
package generate

import (
	"fmt"
	"go/format"
	"path/filepath"
	"regexp"
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
	Version    int                    `json:"version"`
	Packets    map[string]schemaEntry `json:"packets"`
	Structs    map[string]schemaEntry `json:"structs"`
	Components map[string]schemaEntry `json:"components"`
}

// packetsReport is packets.json: state -> flow -> name -> {protocol_id}.
type packetsReport map[string]map[string]map[string]struct {
	ProtocolID int `json:"protocol_id"`
}

// genState collects the enums and shared structs discovered while typing, for
// one target package (protocol/types for the packets, level/component for the
// data components).
type genState struct {
	enums      map[string]*pktEnumDef // Go name -> def
	structs    map[string]*structDef  // Go name -> def
	unions     map[string]*unionDef   // Go name -> def
	order      []string               // struct emission order (dependencies first)
	unionOrder []string
	fixedBits  map[int]bool           // sizes of fixed bit sets seen (readFixedBitSet(n))
	fixedBytes map[int]bool           // sizes of fixed byte blocks seen (readBytes(n))

	pkg      string            // package the enums and shared structs are written into
	q        string            // how other packages refer to them ("types." or "")
	wire     string            // how the wire generics are referred to ("types." or "wire.")
	prims    map[string]string // schema primitive -> Go type
	hand     map[string]string // Java short name of a struct kept by hand -> Go type
	reserved map[string]bool   // Go names taken by the package's own types (components); records get a suffix

	guardHook   func(elem schemaNode) (string, error) // set while typing a packet: the Go type of a guarded entry list
	fieldNames  map[string][]string                   // naming_overrides.json field_names: Java short class → field names
	typeNames   map[string]string                     // naming_overrides.json type_names: Java short class → Go name
	registryIDs registriesJSON                        // registries.json, for the numeric ids of a union's cases
}

// newPacketGenState targets protocol/types, referred to as types. from the
// packet packages.
func newPacketGenState() *genState {
	hand := map[string]string{}
	for name := range handWrittenTypes {
		hand[name] = "types." + name
	}
	for name, typ := range externalHandTypes {
		hand[name] = typ
	}
	return &genState{
		enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{}, fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
		pkg: "types", q: "types.", wire: "types.", prims: primTypes, hand: hand,
	}
}

// newComponentGenState targets level/component itself: generated names are
// local, the wire generics come from package wire, item stacks are the local
// SlotData and typed components the local Typed.
func newComponentGenState() *genState {
	prims := map[string]string{}
	for k, v := range primTypes {
		prims[k] = strings.ReplaceAll(v, "types.", "wire.")
	}
	prims["TEXT"] = "chat.Message"
	prims["OPTIONAL_TEXT"] = "pk.Option[chat.Message, *chat.Message]"
	prims["CHUNK_POS"] = "level.ChunkPos"
	prims["ITEM_STACK"] = "SlotData"
	prims["OPTIONAL_ITEM_STACK"] = "SlotData"
	prims["OPTIONAL_ITEM_STACK_LIST"] = "wire.List[SlotData, *SlotData]"
	prims["COMPONENT_PATCH"] = "Patch"
	prims["TYPED_DATA_COMPONENT"] = "Typed"
	prims["GAME_PROFILE_PROPERTIES"] = "wire.List[user.Property, *user.Property]"
	hand := map[string]string{
		"ItemStack": "SlotData", "SectionPos": "wire.SectionPos", "LpVec3": "wire.LpVec3",
		"MessageSignature": "wire.MessageSignature", "NBT": "wire.NBT",
		"OptionalNBT": "wire.OptionalNBT", "Empty": "wire.Empty", "IDSet": "wire.IDSet", "Text": "chat.Message",
		"ComponentPatch": "Patch", "TypedDataComponent": "Typed",
	}
	return &genState{
		enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{}, fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
		pkg: "component", q: "", wire: "wire.", prims: prims, hand: hand,
	}
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

// unionDef is a dispatch on a registry whose elements carry their own codec
// (particle types): the key, then the fields of the element's case.
type unionDef struct {
	GoName   string
	Java     string
	Registry string
	Cases    []unionCase
	Fields   []goField // the union of every case's fields
}

type unionCase struct {
	ID     string   // minecraft:dust
	Num    int      // the registry id
	Fields []string // Go field names, in wire order
	Hole   string   // why the case cannot be typed, if so
}

type goField struct {
	Name, Type, Comment string
	When                string // guarded entry field: the enum constant whose bit selects it
}

// guardedDef is a packet whose list entries carry only the parts selected by
// an EnumSet field of the packet (ClientboundPlayerInfoUpdatePacket).
type guardedDef struct {
	EntryType   string    // PlayerInfoUpdateEntry
	EntryJava   string    // ClientboundPlayerInfoUpdatePacket$EntryBuilder
	EntryFields []goField // with When
	GuardField  string    // Actions
	GuardType   string    // types.EnumSet[types.PlayerInfoUpdateAction]
	EnumType    string    // types.PlayerInfoUpdateAction
	ListField   string    // Entries
}

// handWrittenTypes are structures kept by hand in protocol/types/types.go; a
// schema struct with one of these names is not generated but mapped to it.
var handWrittenTypes = map[string]bool{
	"ChunkPos": true, "SectionPos": true, "LpVec3": true, "ItemStack": true, "Property": true,
	"MessageSignature": true, "NBT": true, "OptionalNBT": true,
	"ComponentPatch": true, "AddedComponent": true, "Empty": true, "Text": true, "IDSet": true,
	"ChatTypeBound": true, // chat.Type: holder or inline chat type with the decoration logic
}

// wireStructs are shared structures generated once into package wire
// (wire/structs_gen.go) from wherever the schema first shows them; the packets
// see them through aliases in protocol/types (wire_gen.go), the components as
// wire.X.
var wireStructs = map[string]bool{
	"Vec3": true, "Vector3f": true, "Quaternionf": true, "GlobalPos": true, "BlockHitResult": true, "GameProfile": true,
}

// wireGS collects the wire structs across the generators that run in one
// build (components first, then packets, which writes the file).
var wireGS *genState

func wireState() *genState {
	if wireGS == nil {
		prims := map[string]string{}
		for k, v := range primTypes {
			prims[k] = strings.ReplaceAll(v, "types.", "")
		}
		wireGS = &genState{
			enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{}, fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
			pkg: "wire", q: "", wire: "", prims: prims, hand: map[string]string{},
		}
	}
	return wireGS
}

// externalHandTypes are structures the schema describes but which other
// packages implement by hand with the same wire form (their Go name is the
// schema's struct name through goTypeName); a subtree under one of them is
// never a hole, whatever the walker could not type inside it.
var externalHandTypes = map[string]string{
	"SignedMessageBodyPacked":             "sign.PackedMessageBody",
	"MessageSignaturePacked":              "sign.PackedSignature",
	"FilterMask":                          "sign.FilterMask",
	"LevelChunkPacketDataBlockEntityInfo": "level.BlockEntity",
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
	"TYPED_DATA_COMPONENT": "types.AddedComponent", "GAME_PROFILE_PROPERTIES": "types.List[user.Property, *user.Property]",
	"ENTITY_DATA": "types.EntityData",
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

	gs := newPacketGenState()
	var overrides namingOverrides
	if err := readHandCrafted(goMCRoot, "naming_overrides.json", &overrides); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	gs.fieldNames = overrides.FieldNames
	gs.typeNames = overrides.TypeNames
	wireState().fieldNames = overrides.FieldNames
	var regs registriesJSON
	if err := readJSON(filepath.Join(jsonDir, "registries.json"), &regs); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	gs.registryIDs = regs
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
				holes := gs.untyped(entry.Type)
				if reason, hand := handPackets[state+"/"+flow+"/"+name]; hand {
					skipped = append(skipped, name+" (hand-written: "+reason+")")
					if holes == "" {
						logf("genPackets: %s/%s/%s is fully typed in the schema now; the hand-written version in hand.go could be retired", state, flow, name)
					}
					continue
				}
				if holes != "" {
					skipped = append(skipped, name+" ("+holes+")")
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
	// The entity metadata serializers: their Go types join protocol/types.
	if err := gs.genEntityData(jsonDir, goMCRoot); err != nil {
		return err
	}

	// An enum a wire struct uses (BlockHitResult.direction) lives in wire; the
	// packets see it through an alias rather than a second definition.
	ws := wireState()
	for name, we := range ws.enums {
		if pe, ok := gs.enums[name]; ok && strings.Join(pe.Values, ",") == strings.Join(we.Values, ",") {
			delete(gs.enums, name)
		}
	}
	if err := writeGo(filepath.Join(goMCRoot, "protocol", "types", "enums_gen.go"), gs.renderEnums()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	if err := writeGo(filepath.Join(goMCRoot, "protocol", "types", "structs_gen.go"), gs.renderStructs()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	if err := writeGo(filepath.Join(goMCRoot, "wire", "structs_gen.go"), ws.renderStructs()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	if err := writeGo(filepath.Join(goMCRoot, "wire", "enums_gen.go"), ws.renderEnums()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	var aliases strings.Builder
	aliases.WriteString(generatedHeader("gen_packets.go", "packet_schema.json"))
	aliases.WriteString("package types\n\nimport \"github.com/mj41/go-mc26/wire\"\n\n// The shared structures and enums generated into package wire, under the names the packets use.\ntype (\n")
	for _, name := range sortedKeys(ws.structs) {
		fmt.Fprintf(&aliases, "\t%s = wire.%s\n", name, name)
	}
	for _, name := range sortedKeys(ws.enums) {
		fmt.Fprintf(&aliases, "\t%s = wire.%s\n", name, name)
	}
	aliases.WriteString(")\n")
	if err := writeGo(filepath.Join(goMCRoot, "protocol", "types", "wire_gen.go"), aliases.String()); err != nil {
		return fmt.Errorf("genPackets: %w", err)
	}
	wireGS = nil
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
	Guarded  *guardedDef
	baseName string
}

func (gs *genState) packetDef(state, flow, name string, id int, e schemaEntry, abbrev string) (packetDef, error) {
	var fields []goField
	var guarded *guardedDef
	base := goPacketName(e.Class)
	switch e.Type["k"] {
	case "struct":
		// a list of guarded entries: rendered in the packet's package with the guard passed in
		gs.guardHook = func(elem schemaNode) (string, error) {
			if guarded != nil {
				return "", fmt.Errorf("two guarded lists in one packet")
			}
			ef, err := gs.fieldsOf(elem, str(elem["name"]))
			if err != nil {
				return "", err
			}
			guarded = &guardedDef{EntryType: base + "Entry", EntryJava: str(elem["name"]), EntryFields: ef}
			enumJava := str(elem["guard"])
			enumJava = enumJava[strings.LastIndex(enumJava, "/")+1:]
			for _, f := range e.Type["fields"].([]any) {
				fm := f.(map[string]any)
				ft := fm["type"].(map[string]any)
				if ft["k"] == "enumset" && str(ft["name"]) == enumJava {
					guarded.GuardField = goFieldName(str(fm["name"]))
					guarded.GuardType, _, err = gs.goType(schemaNode(ft), e.Class)
					if err != nil {
						return "", err
					}
					guarded.EnumType = strings.TrimSuffix(strings.TrimPrefix(guarded.GuardType, gs.wire+"EnumSet["), "]")
				}
			}
			if guarded.GuardField == "" {
				return "", fmt.Errorf("guarded entries without an EnumSet<%s> field", enumJava)
			}
			return "[]" + guarded.EntryType, nil
		}
		var err error
		fields, err = gs.fieldsOf(e.Type, e.Class)
		gs.guardHook = nil
		if err != nil {
			return packetDef{}, err
		}
		if guarded != nil {
			for _, f := range fields {
				if f.Type == "[]"+guarded.EntryType {
					guarded.ListField = f.Name
				}
			}
		}
		if names, ok := gs.fieldNames[e.Class[strings.LastIndex(e.Class, ".")+1:]]; ok && len(names) == len(fields) {
			for i := range fields {
				fields[i].Name = goFieldName(names[i])
			}
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
		Guarded:  guarded,
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

// fieldsOf types the fields of a struct node; a guarded field keeps the
// constant that selects it.
func (gs *genState) fieldsOf(n schemaNode, owner string) ([]goField, error) {
	raw, _ := n["fields"].([]any)
	var out []goField
	used := map[string]int{}
	for i, f := range raw {
		fm := f.(map[string]any)
		ft := fm["type"].(map[string]any)
		when, _ := fm["when"].(string)
		jname, _ := fm["name"].(string)
		if strings.HasPrefix(jname, "lambda$") || strings.ContainsAny(jname, "$") {
			jname = fmt.Sprintf("v%d", i) // synthetic getter: no field name in the bytecode
		}
		name := uniqueField(goFieldName(jname), used)
		var typ, comment string
		var err error
		if elem, ok := ft["elem"].(map[string]any); ok && ft["k"] == "list" && elem["guard"] != nil && gs.guardHook != nil {
			typ, err = gs.guardHook(schemaNode(elem))
			comment = "the parts of every entry are selected by the packet's action bits"
		} else {
			typ, comment, err = gs.goType(schemaNode(ft), owner)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, goField{Name: name, Type: typ, Comment: comment, When: when})
	}
	return out, nil
}

// uniqueField returns name, or name2, name3, … when it is taken in used.
func uniqueField(name string, used map[string]int) string {
	base := name
	for i := 2; used[name] > 0; i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	used[name]++
	return name
}

// untyped reports what keeps a packet from being generated: holes outside the
// subtrees that a hand-written type covers (externalHandTypes, the wire
// primitives of protocol/types) and outside guarded entries. Empty when the
// packet can be generated.
func (gs *genState) untyped(n schemaNode) string {
	var holes []string
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if n["k"] == "struct" || n["k"] == "dispatch" {
			if _, ok := gs.hand[goTypeName(str(n["name"]))]; ok {
				return
			}
		}
		if n["k"] == "dispatch" && n["cases"] != nil {
			return // a union: a case the schema cannot type is rejected when decoded
		}
		switch n["k"] {
		case "opaque":
			holes = append(holes, "opaque:"+str(n["java"]))
		case "dispatch", "either":
			holes = append(holes, str(n["k"]))
		case "enum", "enumset":
			if vals, _ := n["values"].([]any); len(vals) == 0 {
				holes = append(holes, "enum-without-values:"+str(n["name"]))
			}
		}
		if b, _ := n["conditional"].(bool); b && n["guard"] == nil {
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
			return fmt.Sprintf("%sFixedBits%d", gs.q, int(bits)), "", nil
		}
		if t == "FIXED_BYTES" {
			l, ok := n["len"].(float64)
			if !ok || l <= 0 {
				return "", "", fmt.Errorf("fixed byte block without size")
			}
			if int(l) == 256 {
				return gs.prims["MESSAGE_SIGNATURE"], "", nil
			}
			gs.fixedBytes[int(l)] = true
			return fmt.Sprintf("%sFixedBytes%d", gs.q, int(l)), "", nil
		}
		if gt, ok := gs.prims[t]; ok {
			return gt, "", nil
		}
		return "", "", fmt.Errorf("prim %s", t)
	case "string":
		return "pk.String", "", nil
	case "unit":
		return gs.wire + "Empty", "", nil
	case "nbt":
		return gs.wire + "NBT", "", nil
	case "registry":
		return "pk.VarInt", "registry " + str(n["registry"]), nil
	case "resourcekey":
		return "pk.Identifier", "resource key in " + str(n["registry"]), nil
	case "holderset":
		return gs.wire + "IDSet", "registry " + str(n["registry"]), nil
	case "holder":
		if d, ok := n["direct"].(map[string]any); ok {
			dt, _, err := gs.goType(schemaNode(d), owner)
			if err != nil {
				return "", "", err
			}
			return fmt.Sprintf("%sHolder[%s, *%s]", gs.wire, dt, dt), "registry " + str(n["registry"]), nil
		}
		return "pk.VarInt", "holder in " + str(n["registry"]), nil
	case "list":
		et, _, err := gs.goType(schemaNode(n["elem"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%sList[%s, *%s]", gs.wire, et, et), "", nil
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
		return fmt.Sprintf("%sMap[%s, *%s, %s, *%s]", gs.wire, kt, kt, vt, vt), "", nil
	case "either":
		lt, _, err := gs.goType(schemaNode(n["left"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		rt, _, err := gs.goType(schemaNode(n["right"].(map[string]any)), owner)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%sEither[%s, *%s, %s, *%s]", gs.wire, lt, lt, rt, rt), "", nil
	case "enum":
		vals, _ := n["values"].([]any)
		if len(vals) == 0 {
			return "", "", fmt.Errorf("enum %v without values", n["name"])
		}
		name := gs.enum(str(n["name"]), vals)
		return gs.q + name, "", nil
	case "enumset":
		vals, _ := n["values"].([]any)
		if len(vals) == 0 {
			return "", "", fmt.Errorf("enumset %v without values", n["name"])
		}
		name := gs.enum(str(n["name"]), vals)
		return gs.wire + "EnumSet[" + gs.q + name + "]", "", nil
	case "dispatch":
		if goType, ok := gs.hand[goTypeName(str(n["name"]))]; ok {
			return goType, "", nil // a type-keyed union kept by hand
		}
		if n["cases"] != nil {
			name, err := gs.unionType(n, owner)
			if err != nil {
				return "", "", err
			}
			return gs.q + name, "", nil
		}
		return "", "", fmt.Errorf("dispatch %v", n["name"])
	case "struct":
		if goType, ok := gs.hand[goTypeName(str(n["name"]))]; ok {
			return goType, "", nil // shape kept by hand
		}
		if wireStructs[goTypeName(str(n["name"]))] && gs.pkg != "wire" {
			name, err := wireState().structType(n, owner)
			if err != nil {
				return "", "", err
			}
			return gs.wire + name, "", nil
		}
		name, err := gs.structType(n, owner)
		if err != nil {
			return "", "", err
		}
		return gs.q + name, "", nil
	default:
		return "", "", fmt.Errorf("node %v", n["k"])
	}
}

func (gs *genState) enum(java string, vals []any) string {
	goName := goTypeName(java)
	if gs.reserved[goName] {
		if e, ok := gs.enums[goName]; !ok || e.Java != java {
			goName += "Enum"
		}
	}
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
	if gs.reserved[goName] {
		goName += "Record"
	}
	fields, err := gs.fieldsOf(n, java)
	if err != nil {
		return "", err
	}
	if names, ok := gs.fieldNames[java]; ok && len(names) == len(fields) {
		for i := range fields {
			fields[i].Name = goFieldName(names[i])
		}
	}
	sig := fieldsSig(fields)
	base := goName
	for {
		if s, ok := gs.structs[goName]; ok {
			if fieldsSig(s.Fields) == sig {
				return goName, nil
			}
			// The same record instantiated with other type arguments
			// (Filterable<String> vs Filterable<Component>): name it after the
			// first field whose type differs.
			suffix := "_"
			for i := range fields {
				if i < len(s.Fields) && fields[i].Type != s.Fields[i].Type {
					suffix = typeSuffix(fields[i].Type)
					break
				}
			}
			if goName == base+suffix || suffix == "_" {
				goName += "_"
			} else {
				goName = base + suffix
			}
			continue
		}
		break
	}
	gs.structs[goName] = &structDef{GoName: goName, Java: java, Fields: fields}
	gs.order = append(gs.order, goName)
	return goName, nil
}

// unionType registers a dispatch on a registry as one struct: the key, then
// the fields of every case (only those of the case named by the key are on
// the wire). Cases the schema cannot type are kept as ids that fail to decode.
func (gs *genState) unionType(n schemaNode, owner string) (string, error) {
	java := str(n["name"])
	goName := gs.typeNames[java]
	if goName == "" {
		goName = goTypeName(java)
	}
	if u, ok := gs.unions[goName]; ok && u.Java == java {
		return goName, nil
	}
	key, _ := n["key"].(map[string]any)
	registry := str(key["registry"])
	if gs.registryIDs == nil {
		return "", fmt.Errorf("union %s: no registries.json to number the cases of %s", java, registry)
	}
	ids := gs.registryIDs["minecraft:"+registry].Entries
	u := &unionDef{GoName: goName, Java: java, Registry: registry}
	u.Fields = append(u.Fields, goField{Name: "Type", Type: "pk.VarInt", Comment: "id in " + registry})
	index := map[string]int{}
	cases, _ := n["cases"].([]any)
	for _, ca := range cases {
		c, _ := ca.(map[string]any)
		id := str(c["id"])
		uc := unionCase{ID: id, Num: -1}
		if e, ok := ids[id]; ok {
			uc.Num = e.ProtocolID
		} else {
			uc.Hole = "not in registries.json"
		}
		ct, _ := c["type"].(map[string]any)
		if uc.Hole == "" {
			if h := gs.untyped(schemaNode(ct)); h != "" {
				uc.Hole = h
			}
		}
		if uc.Hole == "" {
			var fields []goField
			var err error
			switch ct["k"] {
			case "struct":
				fields, err = gs.fieldsOf(schemaNode(ct), str(ct["name"]))
			case "unit":
			default:
				typ, comment, e := gs.goType(schemaNode(ct), owner)
				err = e
				fields = []goField{{Name: goEnumConst(id[strings.LastIndex(id, ":")+1:]), Type: typ, Comment: comment}}
			}
			if err != nil {
				uc.Hole = err.Error()
			} else {
				for _, f := range fields {
					name := f.Name
					if i, ok := index[name]; ok && u.Fields[i].Type != f.Type {
						name += goEnumConst(id[strings.LastIndex(id, ":")+1:]) // the same name with another type in another case
					}
					if i, ok := index[name]; ok {
						u.Fields[i].Comment = strings.TrimSpace(u.Fields[i].Comment + ", " + id)
					} else {
						index[name] = len(u.Fields)
						u.Fields = append(u.Fields, goField{Name: name, Type: f.Type, Comment: strings.TrimSpace(id + " " + f.Comment)})
					}
					uc.Fields = append(uc.Fields, name)
				}
			}
		}
		u.Cases = append(u.Cases, uc)
	}
	gs.unions[goName] = u
	gs.unionOrder = append(gs.unionOrder, goName)
	return goName, nil
}

func (gs *genState) renderUnions(sb *strings.Builder) {
	for _, name := range gs.unionOrder {
		u := gs.unions[name]
		var holes []string
		for _, c := range u.Cases {
			if c.Hole != "" {
				holes = append(holes, c.ID+" ("+c.Hole+")")
			}
		}
		fmt.Fprintf(sb, "// %s is Java %s: a dispatch on the registry %s — the id, then the fields of\n// that element's own codec; only the fields of the case in Type are on the wire.\n", name, u.Java, u.Registry)
		if len(holes) > 0 {
			fmt.Fprintf(sb, "// Cases the schema cannot type, rejected when decoded: %s.\n", strings.Join(holes, ", "))
		}
		fmt.Fprintf(sb, "type %s struct {\n", name)
		for _, f := range u.Fields {
			if f.Comment != "" {
				fmt.Fprintf(sb, "\t%s %s // %s\n", f.Name, gs.local(f.Type), f.Comment)
			} else {
				fmt.Fprintf(sb, "\t%s %s\n", f.Name, gs.local(f.Type))
			}
		}
		sb.WriteString("}\n\n")
		// group the cases by their field list
		type group struct {
			nums   []string
			ids    []string
			fields []string
		}
		var groups []*group
		byKey := map[string]*group{}
		var unit []string
		for _, c := range u.Cases {
			if c.Hole != "" || c.Num < 0 {
				continue
			}
			if len(c.Fields) == 0 {
				unit = append(unit, fmt.Sprintf("%d", c.Num))
				continue
			}
			k := strings.Join(c.Fields, ",")
			g, ok := byKey[k]
			if !ok {
				g = &group{fields: c.Fields}
				byKey[k] = g
				groups = append(groups, g)
			}
			g.nums = append(g.nums, fmt.Sprintf("%d", c.Num))
			g.ids = append(g.ids, c.ID)
		}
		fmt.Fprintf(sb, "// fields returns the fields the case in Type carries, as one tuple to read or write.\nfunc (v *%s) fields() (pk.Tuple, error) {\n\tswitch v.Type {\n", name)
		for _, g := range groups {
			refs := make([]string, len(g.fields))
			for i, f := range g.fields {
				refs[i] = "&v." + f
			}
			fmt.Fprintf(sb, "\tcase %s: // %s\n\t\treturn pk.Tuple{%s}, nil\n", strings.Join(g.nums, ", "), strings.Join(g.ids, ", "), strings.Join(refs, ", "))
		}
		if len(unit) > 0 {
			fmt.Fprintf(sb, "\tcase %s:\n\t\treturn nil, nil // no data\n", strings.Join(unit, ", "))
		}
		for _, c := range u.Cases {
			if c.Hole != "" && c.Num >= 0 {
				fmt.Fprintf(sb, "\tcase %d: // %s\n\t\treturn nil, fmt.Errorf(%q)\n", c.Num, c.ID, u.Registry+" "+c.ID+": "+c.Hole)
			}
		}
		fmt.Fprintf(sb, "\t}\n\treturn nil, fmt.Errorf(\"%s %%d: unknown id\", v.Type)\n}\n\n", u.Registry)
		fmt.Fprintf(sb, "func (v *%s) ReadFrom(r io.Reader) (int64, error) {\n\tn, err := v.Type.ReadFrom(r)\n\tif err != nil {\n\t\treturn n, err\n\t}\n\tt, err := v.fields()\n\tif err != nil {\n\t\treturn n, err\n\t}\n\tm, err := t.ReadFrom(r)\n\treturn n + m, err\n}\n\n", name)
		fmt.Fprintf(sb, "func (v %s) WriteTo(w io.Writer) (int64, error) {\n\tn, err := v.Type.WriteTo(w)\n\tif err != nil {\n\t\treturn n, err\n\t}\n\tt, err := v.fields()\n\tif err != nil {\n\t\treturn n, err\n\t}\n\tm, err := t.WriteTo(w)\n\treturn n + m, err\n}\n\n", name)
	}
}

// typeSuffix turns a Go type expression into a name fragment:
// "pk.Option[chat.Message, *chat.Message]" → "Message", "pk.String" → "String".
func typeSuffix(t string) string {
	if i := strings.Index(t, "["); i >= 0 {
		inner := t[i+1:]
		if j := strings.IndexAny(inner, ",]"); j >= 0 {
			inner = inner[:j]
		}
		t = inner
	}
	t = strings.TrimPrefix(strings.TrimSpace(t), "*")
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	if t == "" {
		return "_"
	}
	return strings.ToUpper(t[:1]) + t[1:]
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
		if p.Guarded != nil {
			renderGuarded(&sb, p)
		} else if len(p.Fields) > 0 {
			fmt.Fprintf(&sb, "func (p *%s) ReadFrom(r io.Reader) (int64, error) {\n\treturn pk.Tuple{%s}.ReadFrom(r)\n}\n\n", p.GoName, fieldRefs(p.Fields, "&p."))
			fmt.Fprintf(&sb, "func (p %s) WriteTo(w io.Writer) (int64, error) {\n\treturn pk.Tuple{%s}.WriteTo(w)\n}\n\n", p.GoName, fieldRefs(p.Fields, "p."))
		}
	}
	body := sb.String()
	var out strings.Builder
	out.WriteString(generatedHeader("gen_packets.go", "packet_schema.json + packets.json"))
	fmt.Fprintf(&out, "// Package %s holds the generated %s packets of the %s state.\n", state, jsonVersion, state)
	fmt.Fprintf(&out, "package %s\n\n", state)
	imports := []string{"\t\"github.com/mj41/go-mc26/data/packetid\"", "\tpk \"github.com/mj41/go-mc26/net/packet\"", "\t\"github.com/mj41/go-mc26/protocol/types\""}
	for _, p := range []struct{ prefix, path string }{
		{"sign", "\t\"github.com/mj41/go-mc26/chat/sign\""},
		{"level", "\t\"github.com/mj41/go-mc26/level\""},
		{"chat", "\t\"github.com/mj41/go-mc26/chat\""},
		{"user", "\t\"github.com/mj41/go-mc26/yggdrasil/user\""},
	} {
		if usesPackage(body, p.prefix) {
			imports = append(imports, p.path)
		}
	}
	sort.Strings(imports)
	out.WriteString("import (\n\t\"io\"\n\n" + strings.Join(imports, "\n") + "\n)\n\n")
	out.WriteString("var (\n\t_ = io.EOF\n\t_ pk.Field\n\t_ types.Empty\n)\n\n")
	out.WriteString(body)
	return out.String()
}

// renderGuarded writes the entry type of a guarded packet, its fields(guard)
// selector and the packet's ReadFrom/WriteTo: the fields before the list, the
// entry count, every entry under the guard, the fields after.
func renderGuarded(sb *strings.Builder, p packetDef) {
	g := p.Guarded
	fmt.Fprintf(sb, "// %s is one entry of %s (Java %s); only the parts selected by the packet's %s are read and written.\n", g.EntryType, p.GoName, g.EntryJava, g.GuardField)
	fmt.Fprintf(sb, "type %s struct {\n", g.EntryType)
	for _, f := range g.EntryFields {
		c := f.Comment
		if f.When != "" {
			c = strings.TrimSpace("when " + goEnumConst(f.When) + " " + c)
		}
		if c != "" {
			fmt.Fprintf(sb, "\t%s %s // %s\n", f.Name, f.Type, c)
		} else {
			fmt.Fprintf(sb, "\t%s %s\n", f.Name, f.Type)
		}
	}
	sb.WriteString("}\n\n")
	fmt.Fprintf(sb, "// fields lists the parts of e selected by guard, as one tuple to read or write.\nfunc (e *%s) fields(guard %s) pk.Tuple {\n\tvar t pk.Tuple\n", g.EntryType, g.GuardType)
	i := 0
	for i < len(g.EntryFields) {
		f := g.EntryFields[i]
		if f.When == "" {
			fmt.Fprintf(sb, "\tt = append(t, &e.%s)\n", f.Name)
			i++
			continue
		}
		j := i
		var refs []string
		for j < len(g.EntryFields) && g.EntryFields[j].When == f.When {
			refs = append(refs, "&e."+g.EntryFields[j].Name)
			j++
		}
		fmt.Fprintf(sb, "\tif guard.Has(%s%s) {\n\t\tt = append(t, %s)\n\t}\n", g.EnumType, goEnumConst(f.When), strings.Join(refs, ", "))
		i = j
	}
	sb.WriteString("\treturn t\n}\n\n")
	var before, after []goField
	seenList := false
	for _, f := range p.Fields {
		if f.Name == g.ListField {
			seenList = true
			continue
		}
		if seenList {
			after = append(after, f)
		} else {
			before = append(before, f)
		}
	}
	fmt.Fprintf(sb, "func (p *%s) ReadFrom(r io.Reader) (n int64, err error) {\n\tvar m int64\n", p.GoName)
	if len(before) > 0 {
		fmt.Fprintf(sb, "\tif n, err = (pk.Tuple{%s}).ReadFrom(r); err != nil {\n\t\treturn n, err\n\t}\n", fieldRefs(before, "&p."))
	}
	fmt.Fprintf(sb, "\tvar count pk.VarInt\n\tif m, err = count.ReadFrom(r); err != nil {\n\t\treturn n + m, err\n\t}\n\tn += m\n")
	fmt.Fprintf(sb, "\tp.%s = make([]%s, int(count))\n\tfor i := range p.%s {\n\t\tif m, err = p.%s[i].fields(p.%s).ReadFrom(r); err != nil {\n\t\t\treturn n + m, err\n\t\t}\n\t\tn += m\n\t}\n", g.ListField, g.EntryType, g.ListField, g.ListField, g.GuardField)
	if len(after) > 0 {
		fmt.Fprintf(sb, "\tm, err = (pk.Tuple{%s}).ReadFrom(r)\n\treturn n + m, err\n}\n\n", fieldRefs(after, "&p."))
	} else {
		sb.WriteString("\treturn n, nil\n}\n\n")
	}
	fmt.Fprintf(sb, "func (p %s) WriteTo(w io.Writer) (n int64, err error) {\n\tvar m int64\n", p.GoName)
	if len(before) > 0 {
		fmt.Fprintf(sb, "\tif n, err = (pk.Tuple{%s}).WriteTo(w); err != nil {\n\t\treturn n, err\n\t}\n", fieldRefs(before, "p."))
	}
	fmt.Fprintf(sb, "\tif m, err = pk.VarInt(len(p.%s)).WriteTo(w); err != nil {\n\t\treturn n + m, err\n\t}\n\tn += m\n", g.ListField)
	fmt.Fprintf(sb, "\tfor i := range p.%s {\n\t\tif m, err = p.%s[i].fields(p.%s).WriteTo(w); err != nil {\n\t\t\treturn n + m, err\n\t\t}\n\t\tn += m\n\t}\n", g.ListField, g.ListField, g.GuardField)
	if len(after) > 0 {
		fmt.Fprintf(sb, "\tm, err = (pk.Tuple{%s}).WriteTo(w)\n\treturn n + m, err\n}\n\n", fieldRefs(after, "p."))
	} else {
		sb.WriteString("\treturn n, nil\n}\n\n")
	}
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

// packageImports returns the import block a rendered body of the target
// package needs, from the qualifiers it uses.
func (gs *genState) packageImports(body string) string {
	var imports []string
	std := "\t\"io\"\n"
	if strings.Contains(body, "fmt.") {
		std = "\t\"fmt\"\n\t\"io\"\n"
	}
	if usesPackage(body, "pk") {
		imports = append(imports, "\tpk \"github.com/mj41/go-mc26/net/packet\"")
	}
	for _, p := range []struct{ prefix, path string }{
		{"wire", "\t\"github.com/mj41/go-mc26/wire\""},
		{"chat", "\t\"github.com/mj41/go-mc26/chat\""},
		{"sign", "\t\"github.com/mj41/go-mc26/chat/sign\""},
		{"level", "\t\"github.com/mj41/go-mc26/level\""},
		{"user", "\t\"github.com/mj41/go-mc26/yggdrasil/user\""},
	} {
		if usesPackage(body, p.prefix) {
			imports = append(imports, p.path)
		}
	}
	sort.Strings(imports)
	return "import (\n" + std + "\n" + strings.Join(imports, "\n") + "\n)\n\n"
}

// usesPackage reports whether body refers to an identifier of pkg (pkg.Name),
// as opposed to the word in a comment ("on the wire.").
func usesPackage(body, pkg string) bool {
	return packageRef(pkg).MatchString(body)
}

var packageRefs = map[string]*regexp.Regexp{}

func packageRef(pkg string) *regexp.Regexp {
	re, ok := packageRefs[pkg]
	if !ok {
		re = regexp.MustCompile(`(^|[^A-Za-z0-9_])` + pkg + `\.[A-Z]`)
		packageRefs[pkg] = re
	}
	return re
}

// local strips the target package's own qualifier from a type expression, for
// code rendered inside that package.
func (gs *genState) local(t string) string {
	if gs.q == "" {
		return t
	}
	return strings.ReplaceAll(t, gs.q, "")
}

func (gs *genState) renderEnums() string {
	var sb strings.Builder
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
	body := sb.String()
	return generatedHeader("gen_packets.go", "packet_schema.json") + "package " + gs.pkg + "\n\n" + gs.packageImports(body) + "var _ = io.EOF\n\n" + body
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
	gs.renderUnions(&sb)
	for _, name := range gs.order {
		s := gs.structs[name]
		fmt.Fprintf(&sb, "// %s is Java %s.\ntype %s struct {\n", name, s.Java, name)
		for _, f := range s.Fields {
			t := gs.local(f.Type)
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
	body := sb.String()
	return generatedHeader("gen_packets.go", "packet_schema.json") + "package " + gs.pkg + "\n\n" + gs.packageImports(body) + "var _ = io.EOF\n\n" + body
}
