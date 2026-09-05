// gen_soundid generates data/soundid/soundid.go from registries.json.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type soundEntry struct {
	Name string // e.g. "entity.allay.ambient_with_item"
	ID   int
}

func genSoundID(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "registries.json")
	outPath := filepath.Join(goMCRoot, "data", "soundid", "soundid.go")

	var regs registriesJSON
	if err := readJSON(jsonPath, &regs); err != nil {
		return fmt.Errorf("gen-soundid: %w", err)
	}

	soundEvent, ok := regs["minecraft:sound_event"]
	if !ok {
		return fmt.Errorf("gen-soundid: registry 'minecraft:sound_event' not found in registries.json")
	}

	var sounds []soundEntry
	for name, entry := range soundEvent.Entries {
		cleanName := strings.TrimPrefix(name, "minecraft:")
		cleanName = strings.ReplaceAll(cleanName, ":", ".")
		sounds = append(sounds, soundEntry{Name: cleanName, ID: entry.ProtocolID})
	}

	sort.Slice(sounds, func(i, j int) bool {
		return sounds[i].ID < sounds[j].ID
	})

	tmpl, err := loadTemplate(goMCRoot, "soundid.go.tmpl")
	if err != nil {
		return fmt.Errorf("gen-soundid: %w", err)
	}

	data := struct {
		Header string
		Sounds []soundEntry
	}{
		Header: generatedHeader("gen_soundid.go", "registries.json"),
		Sounds: sounds,
	}

	out, err := executeTemplate(tmpl, data)
	if err != nil {
		return fmt.Errorf("gen-soundid: %w", err)
	}

	if err := writeFile(outPath, out); err != nil {
		return fmt.Errorf("gen-soundid: %w", err)
	}
	logf("gen-soundid: wrote %s (%d sounds, %d bytes)", outPath, len(sounds), len(out))
	return nil
}
