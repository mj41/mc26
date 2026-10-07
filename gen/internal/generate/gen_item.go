// gen_item generates data/item/item.go from items.json + registries.json.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// --- JSON models ---

// itemsJSONData: map of "minecraft:<name>" -> { components: { ... } }
type itemsJSONData map[string]struct {
	Components itemComponents `json:"components"`
}

type itemComponents struct {
	MaxStackSize int      `json:"minecraft:max_stack_size"`
	ItemName     itemName `json:"minecraft:item_name"`
}

type itemName struct {
	Translate string `json:"translate"`
}

type itemEntry struct {
	VarName     string // Go identifier, e.g. "PolishedGranite"
	ID          int    // protocol_id from registries.json
	DisplayName string // Title Case, e.g. "Polished Granite"
	Name        string // short name, e.g. "polished_granite"
	StackSize   int    // max_stack_size, defaults to 64
}

func genItem(jsonDir, goMCRoot string) error {
	itemsPath := filepath.Join(jsonDir, "items.json")
	registriesPath := filepath.Join(jsonDir, "registries.json")
	outPath := filepath.Join(goMCRoot, "data", "item", "item.go")

	// --- Parse items.json ---
	var items itemsJSONData
	if err := readJSON(itemsPath, &items); err != nil {
		return fmt.Errorf("genItem: %w", err)
	}

	// --- Parse registries.json ---
	var regs registriesJSON
	if err := readJSON(registriesPath, &regs); err != nil {
		return fmt.Errorf("genItem: %w", err)
	}

	itemReg, ok := regs["minecraft:item"]
	if !ok {
		return fmt.Errorf("genItem: registry 'minecraft:item' not found in registries.json")
	}

	// --- Merge data ---
	var entries []itemEntry
	for fullName, regEntry := range itemReg.Entries {
		shortName := stripMinecraftPrefix(fullName)

		stackSize := 64 // default
		if itemData, ok := items[fullName]; ok {
			if itemData.Components.MaxStackSize > 0 {
				stackSize = itemData.Components.MaxStackSize
			}
		}

		entries = append(entries, itemEntry{
			VarName:     snakeToCamel(shortName),
			ID:          regEntry.ProtocolID,
			DisplayName: snakeToTitle(shortName),
			Name:        shortName,
			StackSize:   stackSize,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	tmpl, err := loadTemplate(goMCRoot, "item.go.tmpl")
	if err != nil {
		return fmt.Errorf("genItem: %w", err)
	}

	data := struct {
		Header string
		Items  []itemEntry
	}{
		Header: generatedHeader("gen_item.go", "items.json", "registries.json"),
		Items:  entries,
	}

	out, err := executeTemplate(tmpl, data)
	if err != nil {
		return fmt.Errorf("genItem: %w", err)
	}

	if err := writeFile(outPath, out); err != nil {
		return fmt.Errorf("genItem: %w", err)
	}
	logf("genItem: wrote %s (%d items, %d bytes)", outPath, len(entries), len(out))
	return nil
}

// genItemDefaults generates data/item/defaults_gen.go: every item's default
// components as items.json's "wire" has them (a DataComponentPatch in its
// network encoding, base64), by item id; data/item/defaults.go decodes them
// with level/component.
func genItemDefaults(jsonDir, outRoot string) error {
	var items map[string]struct {
		Wire    string   `json:"wire"`
		Skipped []string `json:"wire_skipped"`
	}
	if err := readJSON(filepath.Join(jsonDir, "items.json"), &items); err != nil {
		return fmt.Errorf("genItemDefaults: %w", err)
	}
	var regs registriesJSON
	if err := readJSON(filepath.Join(jsonDir, "registries.json"), &regs); err != nil {
		return fmt.Errorf("genItemDefaults: %w", err)
	}
	reg := regs["minecraft:item"]
	wire := make([]string, len(reg.Entries))
	for name, e := range reg.Entries {
		it, ok := items[name]
		if !ok || it.Wire == "" {
			return fmt.Errorf("genItemDefaults: %s has no wire components (re-run extraction; GenItems writes them)", name)
		}
		wire[e.ProtocolID] = it.Wire
	}
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_item.go", "items.json", "registries.json"))
	sb.WriteString("\npackage item\n\n")
	sb.WriteString("// defaultWire holds every item's default components by item id: a\n")
	sb.WriteString("// DataComponentPatch in its network encoding, base64.\n")
	sb.WriteString("var defaultWire = [...]string{\n")
	for _, w := range wire {
		fmt.Fprintf(&sb, "\t%q,\n", w)
	}
	sb.WriteString("}\n")
	if err := writeGo(filepath.Join(outRoot, "data", "item", "defaults_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genItemDefaults: %w", err)
	}
	logf("genItemDefaults: %d items", len(wire))
	return nil
}
