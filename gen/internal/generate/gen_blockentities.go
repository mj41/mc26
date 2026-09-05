// gen_blockentities generates level/block/blockentity.go and
// level/block/blockentities.go from block_entities.json.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type blockEntityJSON struct {
	Name        string   `json:"name"`
	ValidBlocks []string `json:"valid_blocks"`
}

type blockEntityTmplEntry struct {
	GoName      string
	Name        string
	Receiver    string
	ValidBlocks []string
}

func genBlockEntities(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "block_entities.json")
	outDir := filepath.Join(goMCRoot, "level", "block")

	var entities []blockEntityJSON
	if err := readJSON(jsonPath, &entities); err != nil {
		return fmt.Errorf("genBlockEntities: %w", err)
	}

	// Sort by registry name for stable output.
	sort.Slice(entities, func(i, j int) bool {
		return entities[i].Name < entities[j].Name
	})

	// Build template entries.
	maxWidth := 0
	var tmplEntries []blockEntityTmplEntry
	for _, e := range entities {
		goName := blockEntityGoTypeName(e.Name)
		if len(goName) > maxWidth {
			maxWidth = len(goName)
		}
		blocks := make([]string, len(e.ValidBlocks))
		copy(blocks, e.ValidBlocks)
		sort.Strings(blocks)
		tmplEntries = append(tmplEntries, blockEntityTmplEntry{
			GoName:      goName,
			Name:        e.Name,
			Receiver:    strings.ToLower(goName[:1]),
			ValidBlocks: blocks,
		})
	}

	header := generatedHeader("gen_blockentities.go", "block_entities.json")

	// Generate blockentity.go
	tmpl1, err := loadTemplate(goMCRoot, "blockentity.go.tmpl")
	if err != nil {
		return fmt.Errorf("genBlockEntities: %w", err)
	}
	data1 := struct {
		Header   string
		Entities []blockEntityTmplEntry
		MaxWidth int
	}{header, tmplEntries, maxWidth}
	out1, err := executeTemplate(tmpl1, data1)
	if err != nil {
		return fmt.Errorf("genBlockEntities: blockentity.go: %w", err)
	}
	if err := writeFile(filepath.Join(outDir, "blockentity.go"), out1); err != nil {
		return fmt.Errorf("genBlockEntities: %w", err)
	}

	// Generate blockentities.go
	tmpl2, err := loadTemplate(goMCRoot, "blockentities.go.tmpl")
	if err != nil {
		return fmt.Errorf("genBlockEntities: %w", err)
	}
	data2 := struct {
		Header   string
		Entities []blockEntityTmplEntry
	}{header, tmplEntries}
	out2, err := executeTemplate(tmpl2, data2)
	if err != nil {
		return fmt.Errorf("genBlockEntities: blockentities.go: %w", err)
	}
	if err := writeFile(filepath.Join(outDir, "blockentities.go"), out2); err != nil {
		return fmt.Errorf("genBlockEntities: %w", err)
	}

	logf("genBlockEntities: wrote blockentity.go + blockentities.go (%d block entity types)", len(entities))
	return nil
}

// blockEntityGoTypeName converts a registry name like "minecraft:mob_spawner"
// to a Go type name like "MobSpawnerEntity".
func blockEntityGoTypeName(registryName string) string {
	name := stripMinecraftPrefix(registryName)

	// Convert snake_case to CamelCase.
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			r, size := utf8.DecodeRuneInString(p)
			parts[i] = strings.ToUpper(string(r)) + p[size:]
		}
	}

	return strings.Join(parts, "") + "Entity"
}
