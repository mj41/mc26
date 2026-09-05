// gen_component generates level/component/components.go from components.json.
// It discovers implemented types by scanning Go source files in the component
// directory for types with an ID() method.
package generate

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type componentJSON struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Networkable bool   `json:"networkable"`
}

func genComponent(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "components.json")
	outPath := filepath.Join(goMCRoot, "level", "component", "components.go")
	compDir := filepath.Join(goMCRoot, "level", "component")

	if componentNameOverrides == nil {
		var no namingOverrides
		if err := readHandCrafted(goMCRoot, "naming_overrides.json", &no); err != nil {
			return fmt.Errorf("genComponent: %w", err)
		}
		componentNameOverrides = no.ComponentNames
	}

	var components []componentJSON
	if err := readJSON(jsonPath, &components); err != nil {
		return fmt.Errorf("genComponent: %w", err)
	}

	// Generate individual type files first (so discoverImplementedTypes finds them).
	if err := genComponentTypes(jsonDir, goMCRoot); err != nil {
		return err
	}

	// Discover implemented types by scanning Go source files.
	implemented := discoverImplementedTypes(compDir)

	// Generate the switch statement.
	var buf strings.Builder

	buf.WriteString(generatedHeader("gen_component.go", "components.json"))
	buf.WriteString(`package component

import pk "github.com/mj41/go-mc26/net/packet"

type DataComponent interface {
	pk.Field
	ID() string
}

// NewComponent returns a new DataComponent for the given wire ID (registry protocol_id).
func NewComponent(id int32) DataComponent {
	switch id {
`)

	var unimplemented []string
	for _, c := range components {
		if !c.Networkable {
			continue
		}
		goName := componentGoName(c.Name)
		if _, ok := implemented[goName]; ok {
			fmt.Fprintf(&buf, "\tcase %d:\n\t\treturn new(%s)\n", c.ID, goName)
		} else {
			fmt.Fprintf(&buf, "\tcase %d:\n\t\treturn nil // TODO: %s\n", c.ID, c.Name)
			unimplemented = append(unimplemented, fmt.Sprintf("  %d: %s → %s", c.ID, c.Name, goName))
		}
	}

	buf.WriteString("\t}\n\treturn nil\n}\n")

	// Format with gofmt.
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genComponent: gofmt: %w", err)
	}

	if err := writeFile(outPath, formatted); err != nil {
		return fmt.Errorf("genComponent: %w", err)
	}

	logf("genComponent: wrote %s (%d components, %d unimplemented)", outPath, len(components), len(unimplemented))

	if len(unimplemented) > 0 {
		logf("Unimplemented component types (returning nil):")
		for _, u := range unimplemented {
			logf("%s", u)
		}
	}

	return nil
}

// discoverImplementedTypes scans Go files in the component directory for
// types that implement the DataComponent interface (have an ID() method).
func discoverImplementedTypes(dir string) map[string]bool {
	result := make(map[string]bool)

	// Match: func (TypeName) ID() string   OR   func (t *TypeName) ID() string
	re := regexp.MustCompile(`func\s+\([^)]*?\*?\s*(\w+)\)\s+ID\(\)\s+string`)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if e.Name() == "components.go" || e.Name() == "components_test.go" {
			continue // Skip the file we're generating
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		matches := re.FindAllSubmatch(data, -1)
		for _, m := range matches {
			result[string(m[1])] = true
		}
	}

	return result
}

// componentNameOverrides is loaded from hand-crafted/naming_overrides.json.
// Maps snake_case component names (without minecraft: prefix) to Go type names.
var componentNameOverrides map[string]string

// componentGoName converts a Minecraft registry name to a Go type name.
// e.g., "minecraft:custom_data" → "CustomData"
//
//	"minecraft:map_id" → "MapID"
func componentGoName(name string) string {
	name = stripMinecraftPrefix(name)

	if v, ok := componentNameOverrides[name]; ok {
		return v
	}

	// Split on _ and / then PascalCase each part.
	name = strings.ReplaceAll(name, "/", "_")
	return snakeToCamel(name)
}
