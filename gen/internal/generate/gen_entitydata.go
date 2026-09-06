// gen_entitydata generates the entity metadata layout from entity_data.json
// (GenEntityData: the serializers of SynchedEntityData with their wire form,
// and the synched fields of every entity type):
//
//   - protocol/types/entitydata_gen.go — the serializer names and
//     NewEntityDataValue, which returns an empty value of the right Go type
//     for a serializer id (the hand-written types.EntityData reads the list
//     of the set_entity_data packet with it);
//   - data/entitydata/entitydata_gen.go — index constants per declaring
//     class (EntitySharedFlags, LivingEntityHealth, …) and, per entity type,
//     the fields it carries with their index and serializer.
//
// It runs inside genPackets, on the packet generator's state, so the enums and
// records the serializers use (Pose, Rotations, VillagerData, …) land in
// protocol/types like any other.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type entityDataFile struct {
	Version     int `json:"version"`
	Serializers []struct {
		ID       int        `json:"id"`
		Name     string     `json:"name"`
		Coverage string     `json:"coverage"`
		Type     schemaNode `json:"type"`
	} `json:"serializers"`
	Entities map[string]struct {
		ID     int    `json:"id"`
		Class  string `json:"class"`
		Fields []struct {
			Index      int    `json:"index"`
			Name       string `json:"name"`
			Class      string `json:"class"`
			Serializer int    `json:"serializer"`
		} `json:"fields"`
	} `json:"entities"`
}

// genEntityData writes the two files; it returns quietly when the data has no
// entity_data.json (an extraction older than the extractor).
func (gs *genState) genEntityData(jsonDir, outRoot string) error {
	var data entityDataFile
	if err := readJSON(filepath.Join(jsonDir, "entity_data.json"), &data); err != nil {
		return fmt.Errorf("genEntityData: %w (re-run extraction; the data must include GenEntityData's output)", err)
	}

	// protocol/types/entitydata_gen.go
	var sb strings.Builder
	sb.WriteString("// EntityDataSerializers names the entity data serializer ids of Minecraft " + jsonVersion + "\n")
	sb.WriteString("// (EntityDataSerializers, in registration order).\nvar EntityDataSerializers = []string{")
	names := make([]string, len(data.Serializers))
	for i, s := range data.Serializers {
		names[i] = fmt.Sprintf("%q", s.Name)
	}
	sb.WriteString(strings.Join(names, ", ") + "}\n\n")
	sb.WriteString("// NewEntityDataValue returns an empty value of the Go type a serializer id\n")
	sb.WriteString("// carries, ready to ReadFrom, or an error for a serializer whose wire form the\n")
	sb.WriteString("// schema does not type.\nfunc NewEntityDataValue(serializer int32) (pk.Field, error) {\n\tswitch serializer {\n")
	typed := 0
	var untyped []string
	for _, s := range data.Serializers {
		holes := gs.untyped(s.Type)
		var typ string
		var err error
		if holes == "" {
			typ, _, err = gs.goType(s.Type, "EntityDataSerializers."+s.Name)
			if err != nil {
				holes = err.Error()
			}
		}
		if holes != "" {
			untyped = append(untyped, fmt.Sprintf("%d %s (%s)", s.ID, s.Name, holes))
			continue
		}
		typed++
		fmt.Fprintf(&sb, "\tcase %d: // %s\n\t\treturn new(%s), nil\n", s.ID, s.Name, gs.local(typ))
	}
	sb.WriteString("\t}\n\treturn nil, fmt.Errorf(\"entity data serializer %d: %s\", serializer, entityDataSerializerNote(serializer))\n}\n\n")
	sb.WriteString("func entityDataSerializerNote(serializer int32) string {\n\tif int(serializer) < len(EntityDataSerializers) {\n\t\treturn EntityDataSerializers[serializer] + \" is not typed by the schema\"\n\t}\n\treturn \"unknown\"\n}\n")
	body := sb.String()
	imports := "import (\n\t\"fmt\"\n\n\tpk \"github.com/mj41/go-mc26/net/packet\"\n"
	for _, p := range []struct{ prefix, path string }{
		{"chat.", "\t\"github.com/mj41/go-mc26/chat\""},
		{"level.", "\t\"github.com/mj41/go-mc26/level\""},
		{"user.", "\t\"github.com/mj41/go-mc26/yggdrasil/user\""},
		{"sign.", "\t\"github.com/mj41/go-mc26/chat/sign\""},
	} {
		if strings.Contains(body, p.prefix) {
			imports += p.path + "\n"
		}
	}
	imports += ")\n\n"
	var hdr strings.Builder
	hdr.WriteString(generatedHeader("gen_entitydata.go", "entity_data.json"))
	hdr.WriteString("\n// The entity metadata serializers: what a set_entity_data value is, by serializer id.\n")
	if len(untyped) > 0 {
		hdr.WriteString("//\n// Serializers the schema cannot type (their values cannot be decoded):\n")
		for _, u := range untyped {
			hdr.WriteString("//   " + u + "\n")
		}
	}
	hdr.WriteString("package types\n\n")
	if err := writeGo(filepath.Join(outRoot, "protocol", "types", "entitydata_gen.go"), hdr.String()+imports+body); err != nil {
		return fmt.Errorf("genEntityData: %w", err)
	}

	// data/entitydata/entitydata_gen.go
	type constDef struct {
		Name  string
		Index int
		Class string
		Ser   string
	}
	consts := map[string]constDef{}
	for _, e := range data.Entities {
		for _, f := range e.Fields {
			name := entityFieldConst(f.Class, f.Name)
			if c, ok := consts[name]; ok && c.Index != f.Index {
				name = fmt.Sprintf("%s%d", name, f.Index) // the same class name at two places in the hierarchy
			}
			consts[name] = constDef{Name: name, Index: f.Index, Class: f.Class, Ser: data.Serializers[f.Serializer].Name}
		}
	}
	var sc strings.Builder
	sc.WriteString(generatedHeader("gen_entitydata.go", "entity_data.json"))
	sc.WriteString("\n// Package entitydata is the entity metadata layout of Minecraft " + jsonVersion + ": which\n")
	sc.WriteString("// synched field sits at which index for every entity type, and its serializer.\n")
	sc.WriteString("// The indices are cumulative along the class hierarchy, so a field's constant\n")
	sc.WriteString("// (EntitySharedFlags, LivingEntityHealth, …) holds for every entity of that class.\n")
	sc.WriteString("package entitydata\n\n")
	sc.WriteString("// Field is one synched data field of an entity type.\ntype Field struct {\n\tIndex      int    // the index in set_entity_data\n\tName       string // the Java field (DATA_HEALTH_ID)\n\tClass      string // the declaring class (LivingEntity)\n\tSerializer int    // the serializer id (types.EntityDataSerializers)\n}\n\n")
	sc.WriteString("// Field indices by declaring class and field.\nconst (\n")
	var constNames []string
	for name := range consts {
		constNames = append(constNames, name)
	}
	sort.Slice(constNames, func(i, j int) bool {
		a, b := consts[constNames[i]], consts[constNames[j]]
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		return a.Index < b.Index
	})
	for _, name := range constNames {
		c := consts[name]
		fmt.Fprintf(&sc, "\t%s = %d // %s.%s, %s\n", name, c.Index, c.Class, c.Name, c.Ser)
	}
	sc.WriteString(")\n\n")
	sc.WriteString("// Fields lists, per entity type, every synched field it carries (its own and\n// its superclasses'), by index.\nvar Fields = map[string][]Field{\n")
	for _, key := range sortedKeys(data.Entities) {
		e := data.Entities[key]
		fmt.Fprintf(&sc, "\t%q: {\n", key)
		for _, f := range e.Fields {
			fmt.Fprintf(&sc, "\t\t{%d, %q, %q, %d},\n", f.Index, f.Name, f.Class, f.Serializer)
		}
		sc.WriteString("\t},\n")
	}
	sc.WriteString("}\n")
	if err := writeGo(filepath.Join(outRoot, "data", "entitydata", "entitydata_gen.go"), sc.String()); err != nil {
		return fmt.Errorf("genEntityData: %w", err)
	}
	logf("genEntityData: %d serializers (%d typed), %d entity types, %d field constants", len(data.Serializers), typed, len(data.Entities), len(consts))
	return nil
}

// entityFieldConst names an index constant: LivingEntity + DATA_HEALTH_ID → LivingEntityHealth.
func entityFieldConst(class, field string) string {
	f := strings.TrimPrefix(field, "DATA_")
	f = strings.TrimPrefix(f, "ID_")
	f = strings.TrimSuffix(f, "_ID")
	return goTypeName(class) + goEnumConst(f)
}
