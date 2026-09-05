// gen_biome generates level/biome/list.go from biomes.json.
package generate

import (
	"fmt"
	"path/filepath"
)

type biomeJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func genBiome(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "biomes.json")
	outPath := filepath.Join(goMCRoot, "level", "biome", "list.go")

	var biomes []biomeJSON
	if err := readJSON(jsonPath, &biomes); err != nil {
		return fmt.Errorf("genBiome: %w", err)
	}

	tmpl, err := loadTemplate(goMCRoot, "biome.go.tmpl")
	if err != nil {
		return fmt.Errorf("genBiome: %w", err)
	}

	data := struct {
		Header string
		Biomes []biomeJSON
	}{
		Header: generatedHeader("gen_biome.go", "biomes.json"),
		Biomes: biomes,
	}

	out, err := executeTemplate(tmpl, data)
	if err != nil {
		return fmt.Errorf("genBiome: %w", err)
	}

	if err := writeFile(outPath, out); err != nil {
		return fmt.Errorf("genBiome: %w", err)
	}
	logf("genBiome: wrote %s (%d biomes)", outPath, len(biomes))
	return nil
}
