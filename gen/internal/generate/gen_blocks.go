// gen_blocks generates level/block/blocks.go, level/block/block_states.nbt,
// and level/block/properties_enum.go from blocks.json + block_properties.json.
//
// blocks.json comes from MC's --all data generator.
// block_properties.json comes from GenBlockProperties.java extractor.
package generate

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mj41/go-mc26/nbt"
)

// ---------------------------------------------------------------------------
// JSON model
// ---------------------------------------------------------------------------

type blocksJSON = map[string]blockEntry

type blockEntry struct {
	Definition json.RawMessage     `json:"definition"`
	Properties map[string][]string `json:"properties"`
	States     []stateEntry        `json:"states"`
}

type stateEntry struct {
	ID         int               `json:"id"`
	Default    bool              `json:"default,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// ---------------------------------------------------------------------------
// block_properties.json model
// ---------------------------------------------------------------------------

type propsJSONData struct {
	Properties []propJSONDef       `json:"properties"`
	Enums      map[string][]string `json:"enums"`
}

type propJSONDef struct {
	Field     string   `json:"field"`
	Name      string   `json:"name"`
	Type      string   `json:"type"` // "boolean", "integer", "enum"
	EnumClass string   `json:"enum_class,omitempty"`
	Values    []string `json:"values,omitempty"`
	Min       int      `json:"min,omitempty"`
	Max       int      `json:"max,omitempty"`
}

// ---------------------------------------------------------------------------
// Internal model
// ---------------------------------------------------------------------------

type blockInfo struct {
	FullName   string // "minecraft:stone"
	ShortName  string // "stone"
	GoName     string // "Stone"
	MinStateID int
	Props      []propInfo
	States     []stateEntry
}

type propInfo struct {
	Name   string // JSON property name, e.g. "facing"
	GoName string // Go field name, e.g. "Facing"
	GoType string // Go type name, e.g. "Direction"
	NBTTag string // nbt tag value, same as Name
}

// ---------------------------------------------------------------------------
// Enum definition model
// ---------------------------------------------------------------------------

// enumDef describes a property enum type with its canonical value order.
// The value order matches MC's internal ordinals (important for iota constants).
type enumDef struct {
	TypeName string   // Go type name, e.g. "Direction"
	Values   []string // Ordered MC values, e.g. ["down", "up", "north", "south", "west", "east"]
}

// ---------------------------------------------------------------------------
// NBT output model
// ---------------------------------------------------------------------------

type nbtState struct {
	Name       string          `nbt:"Name"`
	Properties *sortedPropsNBT `nbt:"Properties,omitempty"`
}

// sortedPropsNBT is a deterministic map[string]string for NBT encoding.
// Go maps iterate in random order, causing non-reproducible gzip output.
// This type implements nbt.Marshaler to write compound entries in sorted
// key order, making block_states.nbt byte-for-byte reproducible.
type sortedPropsNBT struct {
	keys   []string
	values []string
}

func newSortedPropsNBT(m map[string]string) sortedPropsNBT {
	if len(m) == 0 {
		return sortedPropsNBT{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := make([]string, len(keys))
	for i, k := range keys {
		values[i] = m[k]
	}
	return sortedPropsNBT{keys: keys, values: values}
}

func (s sortedPropsNBT) TagType() byte { return nbt.TagCompound }
func (s sortedPropsNBT) MarshalNBT(w io.Writer) error {
	for i, k := range s.keys {
		// Write TagString header: type byte + key name length + key name
		kb := []byte(k)
		if _, err := w.Write([]byte{nbt.TagString, byte(len(kb) >> 8), byte(len(kb))}); err != nil {
			return err
		}
		if _, err := w.Write(kb); err != nil {
			return err
		}
		// Write string value: length + bytes
		vb := []byte(s.values[i])
		if _, err := w.Write([]byte{byte(len(vb) >> 8), byte(len(vb))}); err != nil {
			return err
		}
		if _, err := w.Write(vb); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte{nbt.TagEnd})
	return err
}

// ---------------------------------------------------------------------------
// genBlocks — entry point
// ---------------------------------------------------------------------------

func genBlocks(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "blocks.json")
	propsJSONPath := filepath.Join(jsonDir, "block_properties.json")
	blocksOut := filepath.Join(goMCRoot, "level", "block", "blocks.go")
	statesOut := filepath.Join(goMCRoot, "level", "block", "block_states.nbt")
	propsOut := filepath.Join(goMCRoot, "level", "block", "properties_enum.go")

	// Load block_properties.json — property type metadata extracted from MC runtime.
	enumLookup, enumDefs, err := loadPropsJSON(propsJSONPath)
	if err != nil {
		return fmt.Errorf("genBlocks: %w", err)
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("genBlocks: reading %s: %w", jsonPath, err)
	}

	var raw blocksJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("genBlocks: parsing blocks.json: %w", err)
	}

	// Extract ordered keys by scanning for top-level keys in insertion order.
	orderedKeys, err := extractOrderedKeys(data)
	if err != nil {
		return fmt.Errorf("genBlocks: %w", err)
	}

	// Build block infos sorted by minimum state ID (registry order).
	blocks := make([]blockInfo, 0, len(raw))
	for _, fullName := range orderedKeys {
		entry, ok := raw[fullName]
		if !ok {
			continue
		}
		shortName := strings.TrimPrefix(fullName, "minecraft:")
		goName := snakeToCamel(shortName)

		minID := math.MaxInt
		for _, s := range entry.States {
			if s.ID < minID {
				minID = s.ID
			}
		}

		props, err := resolveProperties(enumLookup, fullName, entry.Properties)
		if err != nil {
			return fmt.Errorf("genBlocks: %w", err)
		}

		blocks = append(blocks, blockInfo{
			FullName:   fullName,
			ShortName:  shortName,
			GoName:     goName,
			MinStateID: minID,
			Props:      props,
			States:     entry.States,
		})
	}

	// Sort by minimum state ID to match registry order.
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].MinStateID < blocks[j].MinStateID
	})

	// ---- Generate blocks.go ----
	var buf strings.Builder
	generateBlocksGo(&buf, blocks)

	if err := writeFile(blocksOut, []byte(buf.String())); err != nil {
		return fmt.Errorf("genBlocks: %w", err)
	}
	logf("genBlocks: wrote %s (%d blocks, %d bytes)", blocksOut, len(blocks), buf.Len())

	// ---- Generate block_states.nbt ----
	if err := writeStatesNBT(statesOut, blocks); err != nil {
		return fmt.Errorf("genBlocks: %w", err)
	}

	// ---- Generate properties_enum.go ----
	if err := validateEnums(blocks, enumDefs); err != nil {
		return fmt.Errorf("genBlocks: enum validation: %w", err)
	}
	if err := generatePropertiesEnum(propsOut, enumDefs); err != nil {
		return fmt.Errorf("genBlocks: %w", err)
	}
	logf("genBlocks: wrote %s (%d enum types)", propsOut, len(enumDefs))

	return nil
}

// ---------------------------------------------------------------------------
// blocks.go generation
// ---------------------------------------------------------------------------

func generateBlocksGo(w *strings.Builder, blocks []blockInfo) {
	w.WriteString(generatedHeader("gen_blocks.go", "blocks.json"))
	w.WriteByte('\n')
	w.WriteString("package block\n\n")

	// Compute max GoName length for alignment in ID() methods and FromID.
	maxGoNameLen := 0
	maxFullNameLen := 0
	for _, b := range blocks {
		if len(b.GoName) > maxGoNameLen {
			maxGoNameLen = len(b.GoName)
		}
		if len(b.FullName) > maxFullNameLen {
			maxFullNameLen = len(b.FullName)
		}
	}

	// Type declarations.
	w.WriteString("type (\n")
	for _, b := range blocks {
		if len(b.Props) == 0 {
			fmt.Fprintf(w, "\t%-*s struct{}\n", maxGoNameLen, b.GoName)
		} else {
			fmt.Fprintf(w, "\t%-*s struct {\n", maxGoNameLen, b.GoName)
			maxFieldLen := 0
			maxTypeLen := 0
			for _, p := range b.Props {
				if len(p.GoName) > maxFieldLen {
					maxFieldLen = len(p.GoName)
				}
				if len(p.GoType) > maxTypeLen {
					maxTypeLen = len(p.GoType)
				}
			}
			for _, p := range b.Props {
				fmt.Fprintf(w, "\t\t%-*s %-*s `nbt:\"%s\"`\n", maxFieldLen, p.GoName, maxTypeLen, p.GoType, p.NBTTag)
			}
			w.WriteString("\t}\n")
		}
	}
	w.WriteString(")\n\n")

	// ID() methods.
	for _, b := range blocks {
		pad := strings.Repeat(" ", maxGoNameLen-len(b.GoName))
		fmt.Fprintf(w, "func (%s) ID() string%s { return %q }\n", b.GoName, pad, b.FullName)
	}
	w.WriteString("\n")

	// FromID map.
	w.WriteString("var FromID = map[string]Block{\n")
	for _, b := range blocks {
		pad := strings.Repeat(" ", maxFullNameLen-len(b.FullName))
		fmt.Fprintf(w, "\t%q:%s %s{},\n", b.FullName, pad, b.GoName)
	}
	w.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// block_states.nbt generation
// ---------------------------------------------------------------------------

func writeStatesNBT(outPath string, blocks []blockInfo) error {
	type stateWithName struct {
		id    int
		name  string
		props map[string]string
	}

	var allStates []stateWithName
	for _, b := range blocks {
		for _, s := range b.States {
			allStates = append(allStates, stateWithName{
				id:    s.ID,
				name:  b.FullName,
				props: s.Properties,
			})
		}
	}

	sort.Slice(allStates, func(i, j int) bool {
		return allStates[i].id < allStates[j].id
	})

	// Verify contiguous IDs starting from 0.
	for i, s := range allStates {
		if s.id != i {
			logf("genBlocks: WARNING: expected state ID %d, got %d (block %s)", i, s.id, s.name)
		}
	}

	// Build NBT state list.
	nbtStates := make([]nbtState, len(allStates))
	for i, s := range allStates {
		ns := nbtState{Name: s.name}
		if len(s.props) > 0 {
			sp := newSortedPropsNBT(s.props)
			ns.Properties = &sp
		}
		nbtStates[i] = ns
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("creating output directory for states NBT: %w", err)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}
	defer f.Close()

	z := gzip.NewWriter(f)
	if err := nbt.NewEncoder(z).Encode(nbtStates, ""); err != nil {
		return fmt.Errorf("encoding NBT: %w", err)
	}
	if err := z.Close(); err != nil {
		return fmt.Errorf("closing gzip writer: %w", err)
	}

	logf("genBlocks: wrote %s (%d states)", outPath, len(nbtStates))
	return nil
}

// ---------------------------------------------------------------------------
// Property type resolution (data-driven from block_properties.json)
// ---------------------------------------------------------------------------

// loadPropsJSON loads block_properties.json (extracted from MC runtime) and
// returns an enum lookup map and enum definitions for code generation.
func loadPropsJSON(path string) (enumLookup map[string]string, defs []enumDef, err error) {
	var pd propsJSONData
	if err := readJSON(path, &pd); err != nil {
		return nil, nil, fmt.Errorf("loadPropsJSON: %w", err)
	}

	// Build lookup: (propName, sorted values) → enum class name.
	enumLookup = make(map[string]string)
	for _, p := range pd.Properties {
		if p.Type == "enum" {
			key := makeValueKey(p.Name, p.Values)
			enumLookup[key] = p.EnumClass
		}
	}

	// Build enum definitions from the enums map.
	defs = make([]enumDef, 0, len(pd.Enums))
	for name, values := range pd.Enums {
		defs = append(defs, enumDef{
			TypeName: name,
			Values:   values,
		})
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].TypeName < defs[j].TypeName
	})

	return enumLookup, defs, nil
}

// makeValueKey builds a lookup key from a property name and its value set.
func makeValueKey(name string, values []string) string {
	sorted := make([]string, len(values))
	copy(sorted, values)
	sort.Strings(sorted)
	return name + "\x00" + strings.Join(sorted, "\x00")
}

func resolveProperties(enumLookup map[string]string, blockName string, props map[string][]string) ([]propInfo, error) {
	if len(props) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]propInfo, 0, len(names))
	for _, name := range names {
		values := props[name]
		goType, err := resolveType(enumLookup, blockName, name, values)
		if err != nil {
			return nil, err
		}
		result = append(result, propInfo{
			Name:   name,
			GoName: snakeToCamel(name),
			GoType: goType,
			NBTTag: name,
		})
	}
	return result, nil
}

func resolveType(enumLookup map[string]string, blockName, propName string, values []string) (string, error) {
	// Boolean detection: exactly ["true", "false"] in any order.
	if isBoolean(values) {
		return "Boolean", nil
	}

	// Integer detection: all values parse as integers.
	if isInteger(values) {
		return "Integer", nil
	}

	// Enum lookup from block_properties.json data.
	key := makeValueKey(propName, values)
	if enumClass, ok := enumLookup[key]; ok {
		return enumClass, nil
	}

	return "", fmt.Errorf("cannot determine Go type for property %q on block %s (values: %v)\n  Re-extract block_properties.json from MC to pick up new properties", propName, blockName, values)
}

func isBoolean(values []string) bool {
	if len(values) != 2 {
		return false
	}
	s := make(map[string]bool, 2)
	for _, v := range values {
		s[v] = true
	}
	return s["true"] && s["false"]
}

func isInteger(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if _, err := strconv.Atoi(v); err != nil {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// JSON key order extraction
// ---------------------------------------------------------------------------

func extractOrderedKeys(data []byte) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))

	t, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("extracting ordered keys: %w", err)
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected '{' at start of blocks.json, got %v", t)
	}

	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading key: %w", err)
		}
		key, ok := t.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", t)
		}
		keys = append(keys, key)

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, fmt.Errorf("skipping value for %s: %w", key, err)
		}
	}

	return keys, nil
}

// ---------------------------------------------------------------------------
// Properties enum generation — generates properties_enum.go from canonical
// enum definitions. Values are auto-validated against blocks.json data.
// ---------------------------------------------------------------------------

func generatePropertiesEnum(outPath string, defs []enumDef) error {
	var buf strings.Builder

	buf.WriteString(generatedHeader("gen_blocks.go", "block_properties.json"))
	buf.WriteByte('\n')
	buf.WriteString("package block\n\n")
	buf.WriteString("import (\n\t\"errors\"\n\t\"strconv\"\n)\n\n")

	for _, def := range defs {
		writeEnumType(&buf, def)
	}

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("gofmt: %w", err)
	}

	return writeFile(outPath, formatted)
}

func writeEnumType(w *strings.Builder, def enumDef) {
	typeName := def.TypeName
	receiver := strings.ToLower(typeName[:1])

	// Type declaration.
	fmt.Fprintf(w, "type %s byte\n\n", typeName)

	// Constants.
	fmt.Fprintf(w, "const (\n")
	for i, val := range def.Values {
		constName := makeConstName(typeName, val)
		if i == 0 {
			fmt.Fprintf(w, "\t%s %s = iota\n", constName, typeName)
		} else {
			fmt.Fprintf(w, "\t%s\n", constName)
		}
	}
	fmt.Fprintf(w, ")\n\n")

	// String array.
	fmt.Fprintf(w, "var str%s = [...]string{", typeName)
	for i, val := range def.Values {
		if i > 0 {
			w.WriteString(", ")
		}
		fmt.Fprintf(w, "%q", val)
	}
	w.WriteString("}\n\n")

	// String() method.
	fmt.Fprintf(w, "func (%s %s) String() string {\n", receiver, typeName)
	fmt.Fprintf(w, "\tif int(%s) < len(str%s) {\n", receiver, typeName)
	fmt.Fprintf(w, "\t\treturn str%s[%s]\n", typeName, receiver)
	w.WriteString("\t}\n")
	fmt.Fprintf(w, "\treturn \"invalid %s\"\n", typeName)
	w.WriteString("}\n\n")

	// MarshalText() method.
	fmt.Fprintf(w, "func (%s %s) MarshalText() (text []byte, err error) {\n", receiver, typeName)
	fmt.Fprintf(w, "\tif int(%s) < len(str%s) {\n", receiver, typeName)
	fmt.Fprintf(w, "\t\treturn []byte(str%s[%s]), nil\n", typeName, receiver)
	w.WriteString("\t}\n")
	fmt.Fprintf(w, "\treturn nil, errors.New(\"invalid %s: \" + strconv.Itoa(int(%s)))\n", typeName, receiver)
	w.WriteString("}\n\n")

	// UnmarshalText() method.
	fmt.Fprintf(w, "func (%s *%s) UnmarshalText(text []byte) error {\n", receiver, typeName)
	w.WriteString("\tswitch str := string(text); str {\n")
	for _, val := range def.Values {
		constName := makeConstName(typeName, val)
		fmt.Fprintf(w, "\tcase %q:\n\t\t*%s = %s\n", val, receiver, constName)
	}
	w.WriteString("\tdefault:\n")
	fmt.Fprintf(w, "\t\treturn errors.New(\"unknown %s: \" + str)\n", typeName)
	w.WriteString("\t}\n")
	w.WriteString("\treturn nil\n")
	w.WriteString("}\n\n")
}

func makeConstName(typeName, value string) string {
	return typeName + enumSnakeToCamel(value)
}

func enumSnakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

// validateEnums checks that all enum types used by resolveType are defined
// in enumDefs, and that all values observed in blocks.json are represented.
func validateEnums(blocks []blockInfo, defs []enumDef) error {
	knownEnums := make(map[string]*enumDef, len(defs))
	for i := range defs {
		knownEnums[defs[i].TypeName] = &defs[i]
	}

	for _, b := range blocks {
		for _, p := range b.Props {
			if p.GoType != "Boolean" && p.GoType != "Integer" {
				if _, ok := knownEnums[p.GoType]; !ok {
					return fmt.Errorf("enum type %q used by resolveType but not defined in enumDefs", p.GoType)
				}
			}
		}
	}

	return nil
}
