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
	whiles     map[string]*whileListDef // Go name -> def
	whileOrder []string
	order      []string // struct emission order (dependencies first)
	unionOrder []string
	fixedBits  map[int]bool // sizes of fixed bit sets seen (readFixedBitSet(n))
	fixedBytes map[int]bool // sizes of fixed byte blocks seen (readBytes(n))

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
	// the names protocol/types already has by hand: a generated struct may not take one
	reserved := map[string]bool{}
	for name := range handWrittenTypes {
		reserved[name] = true
	}
	return &genState{
		enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{}, whiles: map[string]*whileListDef{},
		fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
		pkg: "types", q: "types.", wire: "types.", prims: primTypes, hand: hand, reserved: reserved,
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
		enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{}, whiles: map[string]*whileListDef{},
		fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
		pkg: "component", q: "", wire: "wire.", prims: prims, hand: hand,
	}
}

type pktEnumDef struct {
	GoName string
	Java   string
	Values []string
	// Names is set for an enum that travels as its serialized name instead of
	// its ordinal (StringRepresentable): one name per constant.
	Names []string
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
	Registry string // the registry, or the enum, the key names
	ByEnum   bool   // the key is an enum ordinal, not a registry id
	Cases    []unionCase
	Fields   []goField // the union of every case's fields
}

// whileListDef is a list whose length is a bit of every entry: EquipmentEntryList.
type whileListDef struct {
	GoName string
	Elem   string // the Go type of one entry
	Field  string // the entry field carrying the bit
	Mask   int
	More   bool // the bit is set while another entry follows (it always is, so far)
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
	Guard               string // packet field read only when this holds ("$." stands for the receiver)
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
			enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{}, whiles: map[string]*whileListDef{},
			fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
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

// primGoTypes are the Go types a schema primitive maps to. A hand-written type
// in that set is the definition of its primitive, so a struct of the same name
// carrying fields is a different encoding of the same Java class and has to be
// generated: Mojang writes a chunk position as one packed long in most packets
// and as two var ints in a waypoint, both from ChunkPos, and substituting the
// packed type for the second read eight bytes where the wire has two.
var primGoTypes = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range primTypes {
		m[t] = true
	}
	return m
}()

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
			if err := writeGo(filepath.Join(goMCRoot, "protocol", state, flow+"_gen_test.go"), gs.renderPacketTest(state, flow, pkts)); err != nil {
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
	// naming_overrides.json names the fields of a structure whose reader gives
	// the walker nothing to name them by; applied here so guards that reference
	// a field see the final name.
	override := gs.fieldNames[shortJava(owner)]
	if len(override) != len(raw) {
		override = nil
	}
	// a guard names the field by its name in the schema, which an override renames
	byName := map[string]goField{}
	for i, f := range raw {
		fm := f.(map[string]any)
		ft := fm["type"].(map[string]any)
		when, _ := fm["when"].(string)
		jname, _ := fm["name"].(string)
		schemaName := jname
		if override != nil {
			jname = override[i]
		} else if strings.HasPrefix(jname, "lambda$") || strings.ContainsAny(jname, "$") {
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
		guard, err := guardExpr(fm["when"], byName)
		if err != nil {
			return nil, err
		}
		g := goField{Name: name, Type: typ, Comment: comment, When: when, Guard: guard}
		byName[schemaName] = g
		out = append(out, g)
	}
	return out, nil
}

// guardExpr renders a field's "when" (alternatives of tests on earlier fields,
// named as the schema names them) as a Go condition over the receiver "$.".
func guardExpr(when any, earlier map[string]goField) (string, error) {
	alts, _ := when.([]any)
	if len(alts) == 0 {
		return "", nil
	}
	var ands []string
	for _, a := range alts {
		var ors []string
		tests, _ := a.([]any)
		for _, ta := range tests {
			t, _ := ta.(map[string]any)
			f, ok := earlier[str(t["field"])]
			if !ok {
				return "", fmt.Errorf("guard on unknown field %v", t["field"])
			}
			not := t["not"] == true
			ref := "$." + f.Name
			var e string
			switch str(t["test"]) {
			case "bit":
				if not {
					e = fmt.Sprintf("%s&%v == 0", ref, t["value"])
				} else {
					e = fmt.Sprintf("%s&%v != 0", ref, t["value"])
				}
			case "true":
				if not {
					e = "!bool(" + ref + ")"
				} else {
					e = "bool(" + ref + ")"
				}
			case "eq":
				op := "=="
				if not {
					op = "!="
				}
				e = fmt.Sprintf("%s %s %s", ref, op, guardValue(f, t["value"]))
			case "maskeq":
				// some bits of an earlier field are a number: the two low bits of the
				// command tree's flags say whether a node is a literal or an argument
				op := "=="
				if not {
					op = "!="
				}
				e = fmt.Sprintf("%s&%v %s %s", ref, t["mask"], op, guardValue(f, t["value"]))
			case "cmp":
				op := str(t["op"])
				if not {
					op = map[string]string{">": "<=", "<=": ">", "<": ">=", ">=": "<"}[op]
				}
				e = fmt.Sprintf("%s %s %s", ref, op, guardValue(f, t["value"]))
			case "in":
				vals, _ := t["value"].([]any)
				// A predicate the extractor evaluated over a byte's whole domain often is
				// one bit of it (numberHasMin is `flags & 1`); say that instead of listing
				// the 128 values it holds for.
				if mask, ok := oneBitOf(f.Type, vals); ok {
					op := "!="
					if not {
						op = "=="
					}
					e = fmt.Sprintf("%s&%d %s 0", ref, mask, op)
					break
				}
				var eqs []string
				for _, v := range vals {
					eqs = append(eqs, fmt.Sprintf("%s == %s", ref, guardValue(f, v)))
				}
				e = "(" + strings.Join(eqs, " || ") + ")"
				if not {
					e = "!" + e
				}
			default:
				return "", fmt.Errorf("guard test %v", t["test"])
			}
			ors = append(ors, e)
		}
		if len(ors) == 1 {
			ands = append(ands, ors[0])
		} else {
			ands = append(ands, "("+strings.Join(ors, " || ")+")")
		}
	}
	// Nested regions can test the same thing twice — a read guarded by a branch
	// inside another branch on the same field. Saying it once is the same
	// condition, and `A && A` is not something to generate.
	seen := map[string]bool{}
	var kept []string
	for _, a := range ands {
		if seen[a] {
			continue
		}
		seen[a] = true
		kept = append(kept, a)
	}
	return strings.Join(kept, " && "), nil
}

// oneBitOf reports whether vals is exactly the set of byte values with one bit
// set — the shape an enumerated predicate on a flags byte takes — and returns
// that bit. Only pk.Byte is considered, since the set has to be the whole
// domain for the answer to hold.
func oneBitOf(goType string, vals []any) (int, bool) {
	if goType != "pk.Byte" || len(vals) != 128 {
		return 0, false
	}
	have := map[int]bool{}
	for _, v := range vals {
		x, ok := v.(float64)
		if !ok {
			return 0, false
		}
		have[int(x)] = true
	}
	for bit := 0; bit < 8; bit++ {
		mask := 1 << bit
		same := true
		for v := -128; v <= 127 && same; v++ {
			if (uint8(int8(v))&uint8(mask) != 0) != have[v] {
				same = false
			}
		}
		if same {
			return mask, true
		}
	}
	return 0, false
}

// guardValue renders a constant compared with field f: an enum constant name
// becomes the enum's Go constant, a number stays a number.
func guardValue(f goField, v any) string {
	if s, ok := v.(string); ok {
		return f.Type + goEnumConst(s)
	}
	if x, ok := v.(float64); ok {
		return fmt.Sprintf("%d", int64(x))
	}
	return fmt.Sprintf("%v", v)
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
		case "dispatch":
			holes = append(holes, str(n["k"]))
		case "enum", "enumset":
			if vals, _ := n["values"].([]any); len(vals) == 0 {
				holes = append(holes, "enum-without-values:"+str(n["name"]))
			}
		case "stringenum":
			if names, _ := n["names"].([]any); len(names) == 0 {
				holes = append(holes, "enum-without-names:"+str(n["name"]))
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
		// "?" is a registry the schema could not name: the id of an element of
		// whichever registry another field of the same value picks out.
		if str(n["registry"]) == "?" {
			return "pk.VarInt", "id in the registry the type names", nil
		}
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
		elem := schemaNode(n["elem"].(map[string]any))
		if elem["k"] == "ref" && elem["of"] == "dispatch" {
			// a list of the union being defined (composite slot displays): a slice recurses fine
			t := gs.q + gs.unionName(str(elem["name"]))
			return fmt.Sprintf("%sList[%s, *%s]", gs.wire, t, t), "", nil
		}
		et, _, err := gs.goType(elem, owner)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%sList[%s, *%s]", gs.wire, et, et), "", nil
	case "whilelist":
		// a list with no count: every entry but the last has a bit set that says another
		// follows, so it is read until that bit is clear
		elem := schemaNode(n["elem"].(map[string]any))
		et, _, err := gs.goType(elem, owner)
		if err != nil {
			return "", "", err
		}
		w, _ := n["while"].(map[string]any)
		if str(w["test"]) != "bit" {
			return "", "", fmt.Errorf("a list read until %v is not one this generator writes", w["test"])
		}
		mask, ok := w["value"].(float64)
		if !ok {
			return "", "", fmt.Errorf("a list read until a bit with no mask")
		}
		field, err := gs.entryField(elem, et, str(w["field"]))
		if err != nil {
			return "", "", err
		}
		return gs.q + gs.whileList(et, field, int(mask), w["not"] == true), "", nil
	case "ref":
		// the union being defined, inside itself (a slot display with a remainder): boxed
		if n["of"] != "dispatch" {
			return "", "", fmt.Errorf("recursive %v %v", n["of"], n["name"])
		}
		t := gs.q + gs.unionName(str(n["name"]))
		return fmt.Sprintf("%sBox[%s, *%s]", gs.wire, t, t), "", nil
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
	case "stringenum":
		vals, _ := n["values"].([]any)
		names, _ := n["names"].([]any)
		if len(names) == 0 || len(names) != len(vals) {
			return "", "", fmt.Errorf("string enum %v without its serialized names", n["name"])
		}
		return gs.q + gs.stringEnum(str(n["name"]), vals, names), "", nil
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
		// A hand-written type stands in for a shape the schema does not know. When the
		// schema does know the fields, they are what the wire carries: Mojang reuses
		// ChunkPos for a packed long and for two var ints, and substituting the packed
		// type for the second read eight bytes where the wire has two.
		if goType, ok := gs.hand[goTypeName(str(n["name"]))]; ok {
			fs, _ := n["fields"].([]any)
			if len(fs) == 0 || !primGoTypes[goType] {
				return goType, "", nil // shape kept by hand
			}
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

// stringEnum registers an enum that travels as its serialized name. It shares
// the naming of an ordinal enum but never merges with one: the same Java enum
// can appear both ways, and the two have different wire forms.
func (gs *genState) stringEnum(java string, vals, names []any) string {
	goName := goTypeName(java)
	if gs.reserved[goName] {
		goName += "Enum"
	}
	def := &pktEnumDef{GoName: goName, Java: java}
	for i, v := range vals {
		def.Values = append(def.Values, v.(string))
		def.Names = append(def.Names, names[i].(string))
	}
	for {
		e, ok := gs.enums[goName]
		if !ok {
			break
		}
		if e.Java == java && strings.Join(e.Names, ",") == strings.Join(def.Names, ",") {
			return goName
		}
		goName += "Name" // an ordinal enum, or another enum, already has the name
		def.GoName = goName
	}
	gs.enums[goName] = def
	return goName
}

// entryField is the Go field of an already generated entry type that a schema
// field name refers to. It goes through the struct the generator built rather
// than renaming the schema name again, so a naming override reaches here too.
func (gs *genState) entryField(elem schemaNode, goType, name string) (string, error) {
	fs, _ := elem["fields"].([]any)
	def := gs.structs[strings.TrimPrefix(goType, gs.q)]
	for i, f := range fs {
		fm, ok := f.(map[string]any)
		if !ok || str(fm["name"]) != name {
			continue
		}
		if def == nil || i >= len(def.Fields) {
			break
		}
		return def.Fields[i].Name, nil
	}
	return "", fmt.Errorf("%s has no field %q to read the list's length from", goType, name)
}

// whileList registers a list read until a bit of every entry is clear. It is
// named after the entry type, so two packets with the same entry share it.
func (gs *genState) whileList(elem, field string, mask int, more bool) string {
	base := elem[strings.LastIndex(elem, ".")+1:]
	goName := base + "List"
	def := &whileListDef{GoName: goName, Elem: elem, Field: field, Mask: mask, More: more}
	for {
		w, ok := gs.whiles[goName]
		if !ok {
			break
		}
		if w.Elem == elem && w.Field == field && w.Mask == mask && w.More == more {
			return goName
		}
		goName += "_"
		def.GoName = goName
	}
	gs.whiles[goName] = def
	gs.whileOrder = append(gs.whileOrder, goName)
	return goName
}

// renderWhileLists writes the list types: read until the bit is clear, written
// with the bit set on every entry but the last, so a caller never has to know
// the bit is there. An empty list has no wire form — the first entry is always
// present — so writing one is an error rather than a shorter packet.
func (gs *genState) renderWhileLists(sb *strings.Builder) {
	for _, name := range gs.whileOrder {
		w := gs.whiles[name]
		elem := gs.local(w.Elem)
		fmt.Fprintf(sb, "// %s is a list of %s with no count: every entry but the last has the %d bit of\n"+
			"// %s set to say another follows. ReadFrom clears it, WriteTo sets it, so %s holds\n"+
			"// only the value.\ntype %s []%s\n\n", name, elem, w.Mask, w.Field, w.Field, name, elem)
		// the bit is set while another entry follows, or clear while one does
		last, notLast := "&^=", "|="
		stop := "== 0"
		if !w.More {
			last, notLast = "|=", "&^="
			stop = "!= 0"
		}
		fmt.Fprintf(sb, "func (l *%s) ReadFrom(r io.Reader) (int64, error) {\n\t*l = (*l)[:0]\n\tvar n int64\n\tfor {\n"+
			"\t\tvar e %s\n\t\tm, err := e.ReadFrom(r)\n\t\tn += m\n\t\tif err != nil {\n\t\t\treturn n, err\n\t\t}\n"+
			"\t\tlast := e.%s&%d %s\n\t\te.%s &^= %d\n\t\t*l = append(*l, e)\n\t\tif last {\n\t\t\treturn n, nil\n\t\t}\n\t}\n}\n\n",
			name, elem, w.Field, w.Mask, stop, w.Field, w.Mask)
		fmt.Fprintf(sb, "func (l %s) WriteTo(w io.Writer) (int64, error) {\n\tif len(l) == 0 {\n"+
			"\t\treturn 0, fmt.Errorf(%q)\n\t}\n\tvar n int64\n\tfor i, e := range l {\n"+
			"\t\tif i == len(l)-1 {\n\t\t\te.%s %s %d\n\t\t} else {\n\t\t\te.%s %s %d\n\t\t}\n"+
			"\t\tm, err := e.WriteTo(w)\n\t\tn += m\n\t\tif err != nil {\n\t\t\treturn n, err\n\t\t}\n\t}\n\treturn n, nil\n}\n\n",
			name, name+": the wire form has at least one entry", w.Field, last, w.Mask, w.Field, notLast, w.Mask)
	}
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
// unionName is the Go name of the union built from Java class java.
func (gs *genState) unionName(java string) string {
	if goName := gs.typeNames[java]; goName != "" {
		return goName
	}
	return goTypeName(java)
}

func (gs *genState) unionType(n schemaNode, owner string) (string, error) {
	java := str(n["name"])
	goName := gs.unionName(java)
	if u, ok := gs.unions[goName]; ok && u.Java == java {
		return goName, nil
	}
	// reserve the name first: a case may refer back to the union (ref nodes)
	gs.unions[goName] = &unionDef{GoName: goName, Java: java}
	gs.unionOrder = append(gs.unionOrder, goName)
	key, _ := n["key"].(map[string]any)
	registry := str(key["registry"])
	// The key is a registry id, or the enum whose constants each hold their own reader; an
	// enum case carries the ordinal it dispatches on, so it needs no registry to be numbered.
	byEnum := key["k"] == "enum"
	keyType, keyDoc := "pk.VarInt", "id in "+registry
	var ids map[string]registryEntryData
	if byEnum {
		vals, _ := key["values"].([]any)
		if len(vals) == 0 {
			return "", fmt.Errorf("union %s: the enum it dispatches on has no values", java)
		}
		registry = str(key["name"])
		keyType = gs.q + gs.enum(registry, vals)
		keyDoc = "which case is on the wire"
	} else {
		if gs.registryIDs == nil {
			return "", fmt.Errorf("union %s: no registries.json to number the cases of %s", java, registry)
		}
		ids = gs.registryIDs["minecraft:"+registry].Entries
	}
	u := &unionDef{GoName: goName, Java: java, Registry: registry, ByEnum: byEnum}
	const keyField = "Type"
	u.Fields = append(u.Fields, goField{Name: keyField, Type: keyType, Comment: keyDoc})
	index := map[string]int{}
	cases, _ := n["cases"].([]any)
	for _, ca := range cases {
		c, _ := ca.(map[string]any)
		id := str(c["id"])
		uc := unionCase{ID: id, Num: -1}
		if num, ok := c["num"].(float64); ok {
			uc.Num = int(num)
		} else if e, ok := ids[id]; ok {
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
				// A case that reads some of its fields only under a condition on the
				// others cannot be flattened into the union: the tuple would read them
				// all. Keep it as a type of its own, which reads itself.
				if err == nil && hasGuards(fields) {
					typ, comment, e := gs.goType(schemaNode(ct), owner)
					err = e
					fields = []goField{{Name: goEnumConst(id[strings.LastIndex(id, ":")+1:]), Type: typ, Comment: comment}}
				}
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
					// a case field never shares the discriminator, and two cases share a
					// field only when it has the same Go type in both
					i, taken := index[name]
					if name == keyField || (taken && u.Fields[i].Type != f.Type) {
						name += goEnumConst(id[strings.LastIndex(id, ":")+1:])
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
		if u.ByEnum {
			fmt.Fprintf(sb, "// %s is Java %s: a dispatch on %s — the constant, then the fields the reader it\n// holds reads; only the fields of the case in Type are on the wire.\n", name, u.Java, u.Registry)
		} else {
			fmt.Fprintf(sb, "// %s is Java %s: a dispatch on the registry %s — the id, then the fields of\n// that element's own codec; only the fields of the case in Type are on the wire.\n", name, u.Java, u.Registry)
		}
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
		} else if hasGuards(p.Fields) {
			renderConditional(&sb, p)
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

func hasGuards(fs []goField) bool {
	for _, f := range fs {
		if f.Guard != "" {
			return true
		}
	}
	return false
}

// renderConditional writes ReadFrom/WriteTo for a packet whose fields are read
// only when a condition on earlier fields holds: the fields go in segments,
// each read or written after the previous ones, under its condition.
func renderConditional(sb *strings.Builder, p packetDef) {
	renderConditionalFields(sb, p.GoName, "p", p.Fields)
}

// renderConditionalFields writes ReadFrom/WriteTo of a type whose fields carry
// guards, with recv as the receiver name.
func renderConditionalFields(sb *strings.Builder, typeName, recv string, fields []goField) {
	type segment struct {
		guard  string
		fields []goField
	}
	var segs []segment
	for _, f := range fields {
		if len(segs) == 0 || segs[len(segs)-1].guard != f.Guard {
			segs = append(segs, segment{guard: f.Guard})
		}
		segs[len(segs)-1].fields = append(segs[len(segs)-1].fields, f)
	}
	for _, mode := range []string{"read", "write"} {
		if mode == "read" {
			fmt.Fprintf(sb, "func (%s *%s) ReadFrom(r io.Reader) (n int64, err error) {\n\tvar m int64\n", recv, typeName)
		} else {
			fmt.Fprintf(sb, "func (%s %s) WriteTo(w io.Writer) (n int64, err error) {\n\tvar m int64\n", recv, typeName)
		}
		for _, seg := range segs {
			indent := "\t"
			if seg.guard != "" {
				fmt.Fprintf(sb, "\tif %s {\n", strings.ReplaceAll(seg.guard, "$.", recv+"."))
				indent = "\t\t"
			}
			if mode == "read" {
				fmt.Fprintf(sb, "%sm, err = pk.Tuple{%s}.ReadFrom(r)\n", indent, fieldRefs(seg.fields, "&"+recv+"."))
			} else {
				fmt.Fprintf(sb, "%sm, err = pk.Tuple{%s}.WriteTo(w)\n", indent, fieldRefs(seg.fields, recv+"."))
			}
			fmt.Fprintf(sb, "%sn += m\n%sif err != nil {\n%s\treturn n, err\n%s}\n", indent, indent, indent, indent)
			if seg.guard != "" {
				sb.WriteString("\t}\n")
			}
		}
		sb.WriteString("\treturn n, nil\n}\n\n")
	}
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

// noZeroValue says why a packet has no zero value to round-trip: a list read
// until a bit is clear always has one entry on the wire, so an empty one has no
// encoding at all and the test would be asserting on an error.
func (gs *genState) noZeroValue(p packetDef) string {
	for _, f := range p.Fields {
		if w, ok := gs.whiles[strings.TrimPrefix(f.Type, gs.q)]; ok {
			return w.GoName + " is never empty on the wire"
		}
	}
	return ""
}

func (gs *genState) renderPacketTest(state, flow string, pkts []packetDef) string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_packets.go", "packet_schema.json + packets.json"))
	fmt.Fprintf(&sb, "package %s\n\nimport (\n\t\"bytes\"\n\t\"io\"\n\t\"testing\"\n)\n\n", state)
	fmt.Fprintf(&sb, "// Test%sRoundTrip encodes the zero value of every generated %s packet,\n// decodes it and encodes it again; the two encodings must match.\n", strings.ToUpper(flow[:1])+flow[1:], flow)
	fmt.Fprintf(&sb, "func Test%sRoundTrip(t *testing.T) {\n", strings.ToUpper(flow[:1])+flow[1:])
	sb.WriteString("\ttype codec interface {\n\t\tio.WriterTo\n\t}\n\tcases := []struct {\n\t\tname string\n\t\tzero codec\n\t\tread func(io.Reader) (codec, error)\n\t}{\n")
	var skipped []string
	for _, p := range pkts {
		if len(p.Fields) == 0 {
			continue
		}
		if why := gs.noZeroValue(p); why != "" {
			skipped = append(skipped, p.GoName+" ("+why+")")
			continue
		}
		fmt.Fprintf(&sb, "\t\t{%q, %s{}, func(r io.Reader) (codec, error) { var v %s; _, err := v.ReadFrom(r); return v, err }},\n", p.GoName, p.GoName, p.GoName)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&sb, "\t\t// no zero value to round-trip: %s\n", strings.Join(skipped, ", "))
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
		if e.Names != nil {
			renderStringEnum(&sb, e)
			continue
		}
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

// renderStringEnum writes an enum whose wire form is its serialized name, so
// the constants are the names themselves and a name this version does not know
// stays readable instead of failing to decode.
func renderStringEnum(sb *strings.Builder, e *pktEnumDef) {
	fmt.Fprintf(sb, "// %s is Java %s, sent as its serialized name.\ntype %s pk.String\n\nconst (\n", e.GoName, e.Java, e.GoName)
	for i, v := range e.Values {
		fmt.Fprintf(sb, "\t%s%s %s = %q\n", e.GoName, goEnumConst(v), e.GoName, e.Names[i])
	}
	sb.WriteString(")\n\n")
	fmt.Fprintf(sb, "func (e *%s) ReadFrom(r io.Reader) (int64, error) { return (*pk.String)(e).ReadFrom(r) }\n", e.GoName)
	fmt.Fprintf(sb, "func (e %s) WriteTo(w io.Writer) (int64, error)  { return pk.String(e).WriteTo(w) }\n\n", e.GoName)
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
	gs.renderWhileLists(&sb)
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
		if hasGuards(s.Fields) {
			renderConditionalFields(&sb, name, "v", s.Fields)
			continue
		}
		fmt.Fprintf(&sb, "func (v *%s) ReadFrom(r io.Reader) (int64, error) {\n\treturn pk.Tuple{%s}.ReadFrom(r)\n}\n\n", name, fieldRefs(s.Fields, "&v."))
		fmt.Fprintf(&sb, "func (v %s) WriteTo(w io.Writer) (int64, error) {\n\treturn pk.Tuple{%s}.WriteTo(w)\n}\n\n", name, fieldRefs(s.Fields, "v."))
	}
	body := sb.String()
	return generatedHeader("gen_packets.go", "packet_schema.json") + "package " + gs.pkg + "\n\n" + gs.packageImports(body) + "var _ = io.EOF\n\n" + body
}
