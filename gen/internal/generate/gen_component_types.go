// gen_component_types generates individual _gen.go files for each component type.
// The schema is built by merging an auto-extracted component_schema.json
// (from GenComponentSchema.java) with hand-crafted overrides.
package generate

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// componentSchema represents one entry in the merged component schema.
type componentSchema struct {
	Name         string         `json:"name"`
	Pattern      string         `json:"pattern"`
	EmbedType    string         `json:"embedType,omitempty"`
	FieldName    string         `json:"fieldName,omitempty"`
	FieldType    string         `json:"fieldType,omitempty"`
	SerMethod    string         `json:"serMethod,omitempty"`
	BaseType     string         `json:"baseType,omitempty"`
	ElemType     string         `json:"elementType,omitempty"`
	Fields       []tupleField   `json:"fields,omitempty"`
	InlineType   string         `json:"inlineType,omitempty"`
	InlineFields []tupleField   `json:"inlineFields,omitempty"`
	Holders      []holderConfig `json:"holders,omitempty"`
	Comment      string         `json:"_comment,omitempty"` // ignored, for JSON readability
}

type tupleField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type holderConfig struct {
	TypeName   string `json:"typeName"`
	DataName   string `json:"dataName"`
	InlineType string `json:"inlineType"`
}

func genComponentTypes(jsonDir, goMCRoot string) error {
	compDir := filepath.Join(goMCRoot, "level", "component")

	schema, err := loadMergedSchema(jsonDir, goMCRoot)
	if err != nil {
		return fmt.Errorf("genComponentTypes: %w", err)
	}

	generatedTypes := make(map[string]bool)
	generated := 0
	for _, s := range schema {
		if s.Pattern == "custom" || s.Pattern == "named_int" {
			continue
		}
		goName := componentGoName(s.Name)
		code, err := generateComponentCode(s, goName)
		if err != nil {
			return fmt.Errorf("genComponentTypes: %s: %w", s.Name, err)
		}

		formatted, err := format.Source([]byte(code))
		if err != nil {
			return fmt.Errorf("genComponentTypes: %s: gofmt: %w\n---\n%s", s.Name, err, code)
		}

		fileName := strings.ToLower(goName) + "_gen.go"
		outPath := filepath.Join(compDir, fileName)
		if err := writeFile(outPath, formatted); err != nil {
			return fmt.Errorf("genComponentTypes: %w", err)
		}
		generatedTypes[goName] = true
		generated++
	}

	logf("genComponentTypes: generated %d component type files in %s", generated, compDir)
	logReplaceableFiles(compDir, generatedTypes)
	return nil
}

// loadMergedSchema builds the component schema by merging the auto-extracted
// schema (from GenComponentSchema.java) with hand-crafted overrides.
//
// Merge logic: start with extracted entries, then replace any entry whose
// name matches an override. Override entries not present in extracted (new
// components added manually) are appended. The result is sorted by name.
//
// Fallback: if the extracted schema doesn't exist (e.g., running without
// --extract, or using an older json-dir), fall back to the hand-crafted
// component_schema.json.
func loadMergedSchema(jsonDir, goMCRoot string) ([]componentSchema, error) {
	extractedPath := filepath.Join(jsonDir, "component_schema.json")
	overridesPath := filepath.Join(assetsDir, "hand-crafted", "component_schema_overrides.json")
	fallbackPath := filepath.Join(assetsDir, "hand-crafted", "component_schema.json")

	// Try to load extracted schema.
	var extracted []componentSchema
	if err := readJSON(extractedPath, &extracted); err != nil {
		// Fallback to the hand-crafted schema.
		logf("genComponentTypes: no extracted schema at %s, using hand-crafted fallback", extractedPath)
		var fallback []componentSchema
		if err := readJSON(fallbackPath, &fallback); err != nil {
			return nil, fmt.Errorf("reading fallback schema: %w", err)
		}
		return fallback, nil
	}

	// Load overrides (optional — if missing, just use extracted as-is).
	var overrides []componentSchema
	if err := readJSON(overridesPath, &overrides); err != nil {
		logf("genComponentTypes: no overrides file, using extracted schema as-is")
		return extracted, nil
	}

	// Build override map (name → entry), skipping comment-only entries.
	overrideMap := make(map[string]componentSchema, len(overrides))
	for _, o := range overrides {
		if o.Name == "" {
			continue // skip _comment entries
		}
		overrideMap[o.Name] = o
	}

	// Merge: extracted entries, replacing with overrides where present.
	seen := make(map[string]bool, len(extracted))
	merged := make([]componentSchema, 0, len(extracted)+len(overrides))
	overridden := 0
	for _, e := range extracted {
		if o, ok := overrideMap[e.Name]; ok {
			merged = append(merged, o)
			overridden++
		} else {
			merged = append(merged, e)
		}
		seen[e.Name] = true
	}

	// Append override entries not present in extracted (manually added components).
	added := 0
	for _, o := range overrides {
		if o.Name == "" {
			continue // skip _comment entries
		}
		if !seen[o.Name] {
			merged = append(merged, o)
			added++
		}
	}

	// Sort by name for stable output.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Name < merged[j].Name
	})

	logf("genComponentTypes: merged schema: %d extracted + %d overrides applied (%d overridden, %d added) = %d total",
		len(extracted), len(overrides), overridden, added, len(merged))

	return merged, nil
}

func generateComponentCode(s componentSchema, goName string) (string, error) {
	switch s.Pattern {
	case "embed":
		return genEmbed(s, goName), nil
	case "embed_nbt":
		return genEmbedNBT(s, goName), nil
	case "empty":
		return genEmpty(s, goName), nil
	case "delegate":
		return genDelegate(s, goName), nil
	case "array":
		return genArray(s, goName), nil
	case "tuple":
		return genTuple(s, goName), nil
	case "holder_data":
		return genHolderData(s, goName), nil
	case "composite_holder":
		return genCompositeHolder(s, goName), nil
	default:
		return "", fmt.Errorf("unknown pattern %q", s.Pattern)
	}
}

const compGenHeader = "// Code generated by gen/gen_component_types.go from merged component schema; DO NOT EDIT.\n"

// compReceiver returns a lowercase receiver variable for the given Go type name,
// avoiding collision with 'r' (reader) and 'w' (writer) params.
func compReceiver(goName string) string {
	ch := strings.ToLower(goName[:1])
	if ch == "r" || ch == "w" {
		if len(goName) > 1 {
			ch2 := strings.ToLower(goName[1:2])
			if ch2 != "r" && ch2 != "w" {
				return ch2
			}
		}
		return "x"
	}
	return ch
}

// ---------------------------------------------------------------------------
// Import set
// ---------------------------------------------------------------------------

type importSet struct {
	entries map[string]string // path → alias ("" for no alias)
}

func newImportSet() *importSet {
	return &importSet{entries: make(map[string]string)}
}

func (is *importSet) addIO()    { is.entries["io"] = "" }
func (is *importSet) addPk()    { is.entries["github.com/mj41/go-mc26/net/packet"] = "pk" }
func (is *importSet) addDynbt() { is.entries["github.com/mj41/go-mc26/nbt/dynbt"] = "" }
func (is *importSet) addChat()  { is.entries["github.com/mj41/go-mc26/chat"] = "" }
func (is *importSet) addBlock() { is.entries["github.com/mj41/go-mc26/level/block"] = "" }

// addForType adds imports required for a given type string.
func (is *importSet) addForType(typ string) {
	if strings.Contains(typ, "pk.") {
		is.addPk()
	}
	if strings.Contains(typ, "chat.") {
		is.addChat()
	}
	if strings.Contains(typ, "dynbt.") {
		is.addDynbt()
	}
	if strings.Contains(typ, "block.") {
		is.addBlock()
	}
}

func (is *importSet) render() string {
	if len(is.entries) == 0 {
		return ""
	}

	type imp struct {
		path, alias string
	}
	var stdlib, external []imp
	for path, alias := range is.entries {
		if isStdlibImport(path) {
			stdlib = append(stdlib, imp{path, alias})
		} else {
			external = append(external, imp{path, alias})
		}
	}
	sort.Slice(stdlib, func(i, j int) bool { return stdlib[i].path < stdlib[j].path })
	sort.Slice(external, func(i, j int) bool { return external[i].path < external[j].path })

	var buf strings.Builder
	buf.WriteString("import (\n")
	for _, im := range stdlib {
		if im.alias != "" {
			fmt.Fprintf(&buf, "\t%s %q\n", im.alias, im.path)
		} else {
			fmt.Fprintf(&buf, "\t%q\n", im.path)
		}
	}
	if len(stdlib) > 0 && len(external) > 0 {
		buf.WriteString("\n")
	}
	for _, im := range external {
		if im.alias != "" {
			fmt.Fprintf(&buf, "\t%s %q\n", im.alias, im.path)
		} else {
			fmt.Fprintf(&buf, "\t%q\n", im.path)
		}
	}
	buf.WriteString(")\n")
	return buf.String()
}

func isStdlibImport(path string) bool {
	return !strings.Contains(path, ".")
}

// ---------------------------------------------------------------------------
// Pattern generators
// ---------------------------------------------------------------------------

func genEmbed(s componentSchema, goName string) string {
	is := newImportSet()
	is.addForType(s.EmbedType)

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	if imports := is.render(); imports != "" {
		buf.WriteString(imports)
		buf.WriteString("\n")
	}
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)
	fmt.Fprintf(&buf, "type %s struct{ %s }\n\n", goName, s.EmbedType)
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n", goName, s.Name)
	return buf.String()
}

func genEmbedNBT(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()
	is.addDynbt()
	is.addPk()

	rcv := compReceiver(goName)

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)
	fmt.Fprintf(&buf, "type %s struct{ dynbt.Value }\n\n", goName)
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (int64, error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\treturn pk.NBT(&%s.Value).ReadFrom(r)\n", rcv)
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (int64, error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\treturn pk.NBT(&%s.Value).WriteTo(w)\n", rcv)
	buf.WriteString("}\n")
	return buf.String()
}

func genEmpty(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()

	rcv := compReceiver(goName)

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)
	fmt.Fprintf(&buf, "type %s struct{}\n\n", goName)
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (int64, error) { return 0, nil }\n\n", rcv, goName)
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (int64, error) { return 0, nil }\n", rcv, goName)
	return buf.String()
}

func genDelegate(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()
	is.addForType(s.FieldType)

	if s.SerMethod == "nbt" || s.SerMethod == "nbtfield" {
		is.addPk()
	}

	rcv := compReceiver(goName)

	var readExpr, writeExpr string
	switch s.SerMethod {
	case "direct":
		readExpr = fmt.Sprintf("%s.%s.ReadFrom(r)", rcv, s.FieldName)
		writeExpr = fmt.Sprintf("%s.%s.WriteTo(w)", rcv, s.FieldName)
	case "nbt":
		readExpr = fmt.Sprintf("pk.NBT(&%s.%s).ReadFrom(r)", rcv, s.FieldName)
		writeExpr = fmt.Sprintf("pk.NBT(&%s.%s).WriteTo(w)", rcv, s.FieldName)
	case "nbtfield":
		readExpr = fmt.Sprintf("pk.NBTField{V: &%s.%s, AllowUnknownFields: true}.ReadFrom(r)", rcv, s.FieldName)
		writeExpr = fmt.Sprintf("pk.NBTField{V: &%s.%s, AllowUnknownFields: true}.WriteTo(w)", rcv, s.FieldName)
	}

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)
	fmt.Fprintf(&buf, "type %s struct {\n\t%s %s\n}\n\n", goName, s.FieldName, s.FieldType)
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (int64, error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\treturn %s\n", readExpr)
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (int64, error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\treturn %s\n", writeExpr)
	buf.WriteString("}\n")
	return buf.String()
}

func genArray(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()
	is.addPk()
	is.addForType(s.ElemType)

	rcv := compReceiver(goName)

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)
	fmt.Fprintf(&buf, "type %s struct {\n\t%s []%s\n}\n\n", goName, s.FieldName, s.ElemType)
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (int64, error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\treturn pk.Array(&%s.%s).ReadFrom(r)\n", rcv, s.FieldName)
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (int64, error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\treturn pk.Array(&%s.%s).WriteTo(w)\n", rcv, s.FieldName)
	buf.WriteString("}\n")
	return buf.String()
}

func genTuple(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()
	is.addPk()

	rcv := compReceiver(goName)

	for _, f := range s.Fields {
		is.addForType(f.Type)
		if inner := extractInnerType(f.Type); inner != "" {
			is.addForType(inner)
		}
	}

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)

	// Struct definition.
	fmt.Fprintf(&buf, "type %s struct {\n", goName)
	for _, f := range s.Fields {
		fmt.Fprintf(&buf, "\t%s %s\n", f.Name, fieldStructType(f.Type))
	}
	buf.WriteString("}\n\n")

	// ID.
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)

	// ReadFrom.
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (int64, error) {\n", rcv, goName)
	buf.WriteString("\treturn pk.Tuple{\n")
	for _, f := range s.Fields {
		fmt.Fprintf(&buf, "\t\t%s,\n", fieldTupleArg(rcv, f))
	}
	buf.WriteString("\t}.ReadFrom(r)\n")
	buf.WriteString("}\n\n")

	// WriteTo.
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (int64, error) {\n", rcv, goName)
	buf.WriteString("\treturn pk.Tuple{\n")
	for _, f := range s.Fields {
		fmt.Fprintf(&buf, "\t\t%s,\n", fieldTupleArg(rcv, f))
	}
	buf.WriteString("\t}.WriteTo(w)\n")
	buf.WriteString("}\n")
	return buf.String()
}

// ---------------------------------------------------------------------------
// Holder pattern generators
// ---------------------------------------------------------------------------

// genHolderData generates a component with the registryEntryHolder pattern:
// Type:VarInt. If 0, read inline data. If >0, registry ref (value-1).
// If inlineFields are provided, the inline data type is also generated.
func genHolderData(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()
	is.addPk()

	rcv := compReceiver(goName)

	// Collect imports from inline fields.
	for _, f := range s.InlineFields {
		is.addForType(f.Type)
		if inner := extractInnerType(f.Type); inner != "" {
			is.addForType(inner)
		}
	}

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)

	// Generate inline data type if fields are specified.
	if len(s.InlineFields) > 0 {
		fmt.Fprintf(&buf, "type %s struct {\n", s.InlineType)
		for _, f := range s.InlineFields {
			fmt.Fprintf(&buf, "\t%s %s\n", f.Name, fieldStructType(f.Type))
		}
		buf.WriteString("}\n\n")

		drcv := strings.ToLower(s.InlineType[:1])
		if drcv == "r" || drcv == "w" {
			drcv = "d"
		}
		fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (int64, error) {\n", drcv, s.InlineType)
		fmt.Fprintf(&buf, "\treturn pk.Tuple{")
		for i, f := range s.InlineFields {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "&%s.%s", drcv, f.Name)
		}
		buf.WriteString("}.ReadFrom(r)\n")
		buf.WriteString("}\n\n")

		fmt.Fprintf(&buf, "func (%s %s) WriteTo(w io.Writer) (int64, error) {\n", drcv, s.InlineType)
		fmt.Fprintf(&buf, "\treturn pk.Tuple{")
		for i, f := range s.InlineFields {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "&%s.%s", drcv, f.Name)
		}
		buf.WriteString("}.WriteTo(w)\n")
		buf.WriteString("}\n\n")
	}

	// Generate main component struct.
	fmt.Fprintf(&buf, "type %s struct {\n", goName)
	buf.WriteString("\tType       pk.VarInt\n")
	fmt.Fprintf(&buf, "\tInlineData %s\n", s.InlineType)
	buf.WriteString("}\n\n")

	// ID.
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)

	// ReadFrom.
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (n int64, err error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\tn, err = %s.Type.ReadFrom(r)\n", rcv)
	buf.WriteString("\tif err != nil {\n\t\treturn\n\t}\n")
	fmt.Fprintf(&buf, "\tif %s.Type == 0 {\n", rcv)
	fmt.Fprintf(&buf, "\t\tn2, err := %s.InlineData.ReadFrom(r)\n", rcv)
	buf.WriteString("\t\tn += n2\n")
	buf.WriteString("\t\treturn n, err\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn\n")
	buf.WriteString("}\n\n")

	// WriteTo.
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (n int64, err error) {\n", rcv, goName)
	fmt.Fprintf(&buf, "\tn, err = %s.Type.WriteTo(w)\n", rcv)
	buf.WriteString("\tif err != nil {\n\t\treturn\n\t}\n")
	fmt.Fprintf(&buf, "\tif %s.Type == 0 {\n", rcv)
	fmt.Fprintf(&buf, "\t\tn2, err := %s.InlineData.WriteTo(w)\n", rcv)
	buf.WriteString("\t\tn += n2\n")
	buf.WriteString("\t\treturn n, err\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn\n")
	buf.WriteString("}\n")
	return buf.String()
}

// genCompositeHolder generates a component with multiple registryEntryHolder fields,
// each conditional on its type being 0 (inline). Uses pk.Tuple with pk.Opt.
func genCompositeHolder(s componentSchema, goName string) string {
	is := newImportSet()
	is.addIO()
	is.addPk()

	rcv := compReceiver(goName)

	var buf strings.Builder
	buf.WriteString(compGenHeader)
	buf.WriteString("package component\n\n")
	buf.WriteString(is.render())
	buf.WriteString("\n")
	fmt.Fprintf(&buf, "var _ DataComponent = (*%s)(nil)\n\n", goName)

	// Struct definition.
	fmt.Fprintf(&buf, "type %s struct {\n", goName)
	for _, h := range s.Holders {
		fmt.Fprintf(&buf, "\t%s pk.VarInt\n", h.TypeName)
		fmt.Fprintf(&buf, "\t%s %s\n", h.DataName, h.InlineType)
	}
	buf.WriteString("}\n\n")

	// ID.
	fmt.Fprintf(&buf, "func (%s) ID() string { return %q }\n\n", goName, s.Name)

	// ReadFrom.
	fmt.Fprintf(&buf, "func (%s *%s) ReadFrom(r io.Reader) (n int64, err error) {\n", rcv, goName)
	buf.WriteString("\treturn pk.Tuple{\n")
	for _, h := range s.Holders {
		fmt.Fprintf(&buf, "\t\t&%s.%s,\n", rcv, h.TypeName)
		fmt.Fprintf(&buf, "\t\tpk.Opt{\n")
		fmt.Fprintf(&buf, "\t\t\tHas:   func() bool { return %s.%s == 0 },\n", rcv, h.TypeName)
		fmt.Fprintf(&buf, "\t\t\tField: &%s.%s,\n", rcv, h.DataName)
		buf.WriteString("\t\t},\n")
	}
	buf.WriteString("\t}.ReadFrom(r)\n")
	buf.WriteString("}\n\n")

	// WriteTo.
	fmt.Fprintf(&buf, "func (%s *%s) WriteTo(w io.Writer) (n int64, err error) {\n", rcv, goName)
	buf.WriteString("\treturn pk.Tuple{\n")
	for _, h := range s.Holders {
		fmt.Fprintf(&buf, "\t\t&%s.%s,\n", rcv, h.TypeName)
		fmt.Fprintf(&buf, "\t\tpk.Opt{\n")
		fmt.Fprintf(&buf, "\t\t\tHas:   func() bool { return %s.%s == 0 },\n", rcv, h.TypeName)
		fmt.Fprintf(&buf, "\t\t\tField: &%s.%s,\n", rcv, h.DataName)
		buf.WriteString("\t\t},\n")
	}
	buf.WriteString("\t}.WriteTo(w)\n")
	buf.WriteString("}\n")
	return buf.String()
}

// ---------------------------------------------------------------------------
// Type helpers
// ---------------------------------------------------------------------------

// extractInnerType extracts the inner type from pk.Array[T], pk.Option[T], pk.NBTField[T].
func extractInnerType(typ string) string {
	for _, prefix := range []string{"pk.Array[", "pk.Option[", "pk.NBTField["} {
		if strings.HasPrefix(typ, prefix) && strings.HasSuffix(typ, "]") {
			return typ[len(prefix) : len(typ)-1]
		}
	}
	return ""
}

// fieldStructType converts a schema type to a Go struct field type.
//
//	pk.Array[T]    → []T
//	pk.Option[T]   → pk.Option[T, *T]
//	pk.NBTField[T] → T
//	everything else → as-is
func fieldStructType(typ string) string {
	if strings.HasPrefix(typ, "pk.Array[") && strings.HasSuffix(typ, "]") {
		inner := typ[len("pk.Array[") : len(typ)-1]
		return "[]" + inner
	}
	if strings.HasPrefix(typ, "pk.Option[") && strings.HasSuffix(typ, "]") {
		inner := typ[len("pk.Option[") : len(typ)-1]
		return fmt.Sprintf("pk.Option[%s, *%s]", inner, inner)
	}
	if strings.HasPrefix(typ, "pk.NBTField[") && strings.HasSuffix(typ, "]") {
		inner := typ[len("pk.NBTField[") : len(typ)-1]
		return inner
	}
	return typ
}

// fieldTupleArg returns the expression used in pk.Tuple{...} for a field.
//
//	pk.Array[T]    → pk.Array(&rcv.Name)
//	pk.Option[T]   → &rcv.Name
//	pk.NBTField[T] → pk.NBTField{V: &rcv.Name, AllowUnknownFields: true}
//	everything else → &rcv.Name
func fieldTupleArg(rcv string, f tupleField) string {
	if strings.HasPrefix(f.Type, "pk.Array[") {
		return fmt.Sprintf("pk.Array(&%s.%s)", rcv, f.Name)
	}
	if strings.HasPrefix(f.Type, "pk.NBTField[") {
		return fmt.Sprintf("pk.NBTField{V: &%s.%s, AllowUnknownFields: true}", rcv, f.Name)
	}
	return fmt.Sprintf("&%s.%s", rcv, f.Name)
}

// ---------------------------------------------------------------------------
// Replaceable file logging
// ---------------------------------------------------------------------------

// logReplaceableFiles scans existing (non-generated) .go files in compDir for
// struct type definitions that match generated types, and logs them so the user
// knows which hand-written files can be deleted.
func logReplaceableFiles(compDir string, generatedTypes map[string]bool) {
	entries, err := os.ReadDir(compDir)
	if err != nil {
		return
	}
	re := regexp.MustCompile(`^type\s+(\w+)\s+struct`)
	var replaceable []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_gen.go") || e.Name() == "components.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(compDir, e.Name()))
		if err != nil {
			continue
		}
		matches := re.FindAllSubmatch(data, -1)
		var found []string
		for _, m := range matches {
			name := string(m[1])
			if generatedTypes[name] {
				found = append(found, name)
			}
		}
		if len(found) > 0 {
			replaceable = append(replaceable, fmt.Sprintf("  %s (types: %s)", e.Name(), strings.Join(found, ", ")))
		}
	}
	if len(replaceable) > 0 {
		logf("genComponentTypes: hand-written files replaceable by _gen.go:")
		for _, r := range replaceable {
			logf("%s", r)
		}
	}
}
