// gen_registryid generates one .go file per registry from registries.json.
// Each file contains a var TypeName = []string{...} with all entries sorted
// by protocol ID.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func genRegistryID(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "registries.json")
	outDir := filepath.Join(goMCRoot, "data", "registryid")

	var registries registriesJSON
	if err := readJSON(jsonPath, &registries); err != nil {
		return fmt.Errorf("genRegistryID: %w", err)
	}

	tmpl, err := loadTemplate(goMCRoot, "registryid.go.tmpl")
	if err != nil {
		return fmt.Errorf("genRegistryID: %w", err)
	}

	// Sorted keys for deterministic output.
	keys := make([]string, 0, len(registries))
	for k := range registries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	totalEntries := 0
	for _, key := range keys {
		reg := registries[key]
		typeName := registryTypeName(key)
		fileName := registryFileName(key)

		// Build ordered entry list.
		entries := make([]string, len(reg.Entries))
		for name, v := range reg.Entries {
			entries[v.ProtocolID] = name
		}

		data := struct {
			Header   string
			TypeName string
			Entries  []string
		}{
			Header:   generatedHeader("gen_registryid.go", "registries.json"),
			TypeName: typeName,
			Entries:  entries,
		}

		out, err := executeTemplate(tmpl, data)
		if err != nil {
			return fmt.Errorf("genRegistryID: formatting %s: %w", fileName, err)
		}

		outPath := filepath.Join(outDir, fileName)
		if err := writeFile(outPath, out); err != nil {
			return fmt.Errorf("genRegistryID: %w", err)
		}
		totalEntries += len(entries)
	}

	// The glue that fills a registry container from those tables: both sides of
	// it are generated, so it is too.
	bootstrapTmpl, err := loadTemplate(goMCRoot, "registryid-bootstrap.go.tmpl")
	if err != nil {
		return fmt.Errorf("genRegistryID: %w", err)
	}
	out, err := executeTemplate(bootstrapTmpl, struct {
		Header     string
		Version    string
		BlockCount int
	}{
		Header:     generatedHeader("gen_registryid.go", "registries.json"),
		Version:    jsonVersion,
		BlockCount: len(registries["minecraft:block"].Entries),
	})
	if err != nil {
		return fmt.Errorf("genRegistryID: bootstrap: %w", err)
	}
	if err := writeFile(filepath.Join(outDir, "bootstrap", "builtinregistries_gen.go"), out); err != nil {
		return fmt.Errorf("genRegistryID: bootstrap: %w", err)
	}

	logf("genRegistryID: wrote %d registry files (%d total entries) and the bootstrap", len(keys), totalEntries)
	return nil
}

// registryFileName converts a registry key to a filename.
//
//	minecraft:block            → block.go
//	minecraft:entity_type      → entitytype.go
//	minecraft:worldgen/block_state_provider_type → worldgen_blockstateprovidertype.go
func registryFileName(key string) string {
	name := strings.TrimPrefix(key, "minecraft:")
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "/", "_")
	return name + ".go"
}

// registryTypeName converts a registry key to a Go exported type name.
//
//	block            → Block
//	entity_type      → EntityType
//	worldgen/block_state_provider_type → WorldgenBlockStateProviderType
func registryTypeName(key string) string {
	name := strings.TrimPrefix(key, "minecraft:")
	name = strings.ReplaceAll(name, "/", "_")
	parts := strings.Split(name, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}
