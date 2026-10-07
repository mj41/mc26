// gen_components generates level/component/<name>_gen.go — one Go type with
// ReadFrom/WriteTo and ID() per data component — plus the enums and shared
// records they use, from the "components" section of packet_schema.json (the
// stream codec of every DataComponents registration, read from the jar's
// bytecode).
package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// componentDef is one generated component type.
type componentDef struct {
	GoName string // Food
	MCName string // minecraft:food
	Java   string // FoodProperties (the record) or the codec's description
	Kind   string // struct, enum, unit, value
	Fields []goField
	Value  string // Go type of the single value (Kind value)
}

// genComponents writes the component types; it is called by genComponent
// before the registry (components.go) is written.
func genComponents(jsonDir, outRoot string) error {
	var schema packetSchemaFile
	if err := readJSON(filepath.Join(jsonDir, "packet_schema.json"), &schema); err != nil {
		return fmt.Errorf("genComponents: %w", err)
	}
	if len(schema.Components) == 0 {
		return fmt.Errorf("genComponents: packet_schema.json has no components section (re-run extraction)")
	}
	gs := newComponentGenState()
	// A component whose value is a union keyed by a registry needs the numeric ids of
	// that registry's entries, the same as a packet does.
	var regs registriesJSON
	if err := readJSON(filepath.Join(jsonDir, "registries.json"), &regs); err != nil {
		return fmt.Errorf("genComponents: %w", err)
	}
	gs.registryIDs = regs
	// Component type names win over record and enum names.
	gs.reserved = map[string]bool{}
	for name := range schema.Components {
		gs.reserved[componentGoName(name)] = true
	}

	compDir := filepath.Join(outRoot, "level", "component")
	old, _ := filepath.Glob(filepath.Join(compDir, "*_gen.go"))
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}

	var defs []componentDef
	var skipped []string
	for _, name := range sortedKeys(schema.Components) {
		e := schema.Components[name]
		if e.Coverage != "full" {
			skipped = append(skipped, name+" ("+holeSummary(e.Type)+")")
			continue
		}
		def, err := gs.componentDef(name, e)
		if err != nil {
			skipped = append(skipped, name+" ("+err.Error()+")")
			continue
		}
		defs = append(defs, def)
		if err := writeGo(filepath.Join(compDir, strings.ToLower(def.GoName)+"_gen.go"), renderComponent(def)); err != nil {
			return fmt.Errorf("genComponents: %w", err)
		}
	}
	if err := writeGo(filepath.Join(compDir, "enums_gen.go"), gs.renderEnums()); err != nil {
		return fmt.Errorf("genComponents: %w", err)
	}
	if err := writeGo(filepath.Join(compDir, "structs_gen.go"), gs.renderStructs()); err != nil {
		return fmt.Errorf("genComponents: %w", err)
	}
	if err := writeGo(filepath.Join(compDir, "components_gen_test.go"), renderComponentTest(defs)); err != nil {
		return fmt.Errorf("genComponents: %w", err)
	}
	if err := writeGo(filepath.Join(compDir, "skipped_gen.go"), renderComponentSkipped(skipped)); err != nil {
		return fmt.Errorf("genComponents: %w", err)
	}
	logf("genComponents: %d of %d components generated (%d enums, %d shared structs); %d hand-written or untyped",
		len(defs), len(schema.Components), len(gs.enums), len(gs.structs), len(skipped))
	return nil
}

func (gs *genState) componentDef(name string, e schemaEntry) (componentDef, error) {
	def := componentDef{GoName: componentGoName(name), MCName: name}
	n := e.Type
	switch n["k"] {
	case "struct":
		def.Java = str(n["name"])
		fields, err := gs.fieldsOf(n, def.Java)
		if err != nil {
			return def, err
		}
		if len(fields) == 0 {
			def.Kind = "unit"
			return def, nil
		}
		def.Kind = "struct"
		def.Fields = fields
	case "unit":
		def.Kind = "unit"
	case "enum":
		vals, _ := n["values"].([]any)
		if len(vals) == 0 {
			return def, fmt.Errorf("enum %v without values", n["name"])
		}
		ids, err := enumIDs(n)
		if err != nil {
			return def, err
		}
		def.Java = str(n["name"])
		// The component type is the enum itself, under the component's name.
		enumName := gs.enumNamed(def.GoName, def.Java, vals, ids)
		gs.serializedNames(enumName, n)
		if enumName != def.GoName {
			def.Kind = "value"
			def.Value = enumName
			return def, nil
		}
		def.Kind = "enum"
	default:
		typ, comment, err := gs.goType(n, name)
		if err != nil {
			return def, err
		}
		def.Kind = "value"
		def.Value = typ
		def.Java = comment
	}
	return def, nil
}

// enumNamed registers an enum under a chosen Go name (the component's), or
// returns the name it already has.
func (gs *genState) enumNamed(goName, java string, vals []any, ids []int) string {
	var values []string
	for _, v := range vals {
		values = append(values, v.(string))
	}
	for _, e := range gs.enums {
		if e.Java == java && strings.Join(e.Values, ",") == strings.Join(values, ",") {
			return e.GoName
		}
	}
	if e, ok := gs.enums[goName]; ok && e.Java != java {
		return gs.enum(java, vals, ids)
	}
	gs.enums[goName] = &pktEnumDef{GoName: goName, Java: java, Values: values, IDs: ids}
	return goName
}

func renderComponent(d componentDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "// %s is the data component %s", d.GoName, d.MCName)
	if d.Java != "" && d.Kind != "value" {
		fmt.Fprintf(&sb, " (Java %s)", d.Java)
	}
	sb.WriteString(".\n")
	switch d.Kind {
	case "struct":
		fmt.Fprintf(&sb, "type %s struct {\n", d.GoName)
		for _, f := range d.Fields {
			if f.Comment != "" {
				fmt.Fprintf(&sb, "\t%s %s // %s\n", f.Name, f.Type, f.Comment)
			} else {
				fmt.Fprintf(&sb, "\t%s %s\n", f.Name, f.Type)
			}
		}
		sb.WriteString("}\n\n")
		fmt.Fprintf(&sb, "func (v *%s) ReadFrom(r io.Reader) (int64, error) {\n\treturn pk.Tuple{%s}.ReadFrom(r)\n}\n\n", d.GoName, fieldRefs(d.Fields, "&v."))
		fmt.Fprintf(&sb, "func (v %s) WriteTo(w io.Writer) (int64, error) {\n\treturn pk.Tuple{%s}.WriteTo(w)\n}\n\n", d.GoName, fieldRefs(d.Fields, "v."))
	case "unit":
		fmt.Fprintf(&sb, "type %s struct{}\n\n", d.GoName)
		fmt.Fprintf(&sb, "func (*%s) ReadFrom(io.Reader) (int64, error) { return 0, nil }\n", d.GoName)
		fmt.Fprintf(&sb, "func (%s) WriteTo(io.Writer) (int64, error)  { return 0, nil }\n\n", d.GoName)
	case "value":
		if d.Java != "" {
			sb.WriteString("// Value: " + d.Java + ".\n")
		}
		fmt.Fprintf(&sb, "type %s struct{ Value %s }\n\n", d.GoName, d.Value)
		fmt.Fprintf(&sb, "func (v *%s) ReadFrom(r io.Reader) (int64, error) { return v.Value.ReadFrom(r) }\n", d.GoName)
		fmt.Fprintf(&sb, "func (v %s) WriteTo(w io.Writer) (int64, error)  { return v.Value.WriteTo(w) }\n\n", d.GoName)
	case "enum":
		// the type and its codec are in enums_gen.go
	}
	fmt.Fprintf(&sb, "// ID returns the registry name of the component.\nfunc (%s) ID() string { return %q }\n\n", d.GoName, d.MCName)
	fmt.Fprintf(&sb, "var _ DataComponent = (*%s)(nil)\n", d.GoName)
	body := sb.String()
	var imports []string
	if strings.Contains(body, "io.") {
		imports = append(imports, "\t\"io\"")
	}
	if strings.Contains(body, "pk.") {
		imports = append(imports, "\tpk \"github.com/mj41/go-mc26/net/packet\"")
	}
	for _, p := range []struct{ prefix, path string }{
		{"wire.", "github.com/mj41/go-mc26/wire"}, {"chat.", "github.com/mj41/go-mc26/chat"},
		{"level.", "github.com/mj41/go-mc26/level"}, {"user.", "github.com/mj41/go-mc26/yggdrasil/user"},
	} {
		if strings.Contains(body, p.prefix) {
			imports = append(imports, "\t\""+p.path+"\"")
		}
	}
	header := generatedHeader("gen_components.go", "packet_schema.json") + "package component\n\n"
	if len(imports) > 0 {
		header += "import (\n" + strings.Join(imports, "\n") + "\n)\n\n"
	}
	return header + body
}

func renderComponentSkipped(skipped []string) string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_components.go", "packet_schema.json"))
	sb.WriteString("package component\n\n")
	sb.WriteString("// Components not generated, because the schema does not type them fully:\n")
	for _, s := range skipped {
		fmt.Fprintf(&sb, "//   - %s\n", s)
	}
	return sb.String()
}

func renderComponentTest(defs []componentDef) string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_components.go", "packet_schema.json"))
	sb.WriteString("package component\n\nimport (\n\t\"bytes\"\n\t\"testing\"\n)\n\n")
	sb.WriteString("// TestComponentRoundTrip encodes the zero value of every generated component,\n// decodes it and encodes it again; the two encodings must match.\n")
	sb.WriteString("func TestComponentRoundTrip(t *testing.T) {\n\tcases := []struct {\n\t\tname string\n\t\tzero DataComponent\n\t}{\n")
	sort.Slice(defs, func(i, j int) bool { return defs[i].MCName < defs[j].MCName })
	for _, d := range defs {
		fmt.Fprintf(&sb, "\t\t{%q, new(%s)},\n", d.MCName, d.GoName)
	}
	sb.WriteString("\t}\n\tfor _, c := range cases {\n\t\tt.Run(c.name, func(t *testing.T) {\n\t\t\tvar a bytes.Buffer\n\t\t\tif _, err := c.zero.WriteTo(&a); err != nil {\n\t\t\t\tt.Fatalf(\"encode: %v\", err)\n\t\t\t}\n\t\t\tif _, err := c.zero.ReadFrom(bytes.NewReader(a.Bytes())); err != nil {\n\t\t\t\tt.Fatalf(\"decode %x: %v\", a.Bytes(), err)\n\t\t\t}\n\t\t\tvar b bytes.Buffer\n\t\t\tif _, err := c.zero.WriteTo(&b); err != nil {\n\t\t\t\tt.Fatalf(\"re-encode: %v\", err)\n\t\t\t}\n\t\t\tif !bytes.Equal(a.Bytes(), b.Bytes()) {\n\t\t\t\tt.Fatalf(\"round trip differs: %x vs %x\", a.Bytes(), b.Bytes())\n\t\t\t}\n\t\t\tif c.zero.ID() != c.name {\n\t\t\t\tt.Fatalf(\"ID %q\", c.zero.ID())\n\t\t\t}\n\t\t})\n\t}\n}\n")
	return sb.String()
}
