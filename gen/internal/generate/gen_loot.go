// gen_loot generates data/loot/blocks_gen.go from the block loot tables of
// the vanilla data pack (data/minecraft/loot_table/blocks/*.json, which the
// extraction copies from the data generator's output): for every block, the
// items its loot table can give and what each needs — nothing, silk touch,
// shears, luck, or a condition the generator does not name. data/loot/loot.go
// looks it up both ways (BlockDrops, DroppedBy).
package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type lootDrop struct {
	Item  string
	Needs string
}

func genLoot(jsonDir, outRoot string) error {
	dir := filepath.Join(jsonDir, "data", "minecraft", "loot_table", "blocks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("genLoot: %w (re-run extraction: the data must include the data packs)", err)
	}
	drops := map[string][]lootDrop{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var table struct {
			Pools []json.RawMessage `json:"pools"`
		}
		if err := readJSON(filepath.Join(dir, e.Name()), &table); err != nil {
			return fmt.Errorf("genLoot: %w", err)
		}
		block := "minecraft:" + strings.TrimSuffix(e.Name(), ".json")
		seen := map[lootDrop]bool{}
		for _, raw := range table.Pools {
			var pool struct {
				Condition  json.RawMessage   `json:"condition"`
				Conditions []json.RawMessage `json:"conditions"`
				Entries    []json.RawMessage `json:"entries"`
			}
			if err := json.Unmarshal(raw, &pool); err != nil {
				return fmt.Errorf("genLoot: %s: %w", block, err)
			}
			needs := needsOf(append(pool.Conditions, pool.Condition))
			for _, en := range pool.Entries {
				walkLootEntry(en, needs, func(d lootDrop) {
					if !seen[d] {
						seen[d] = true
						drops[block] = append(drops[block], d)
					}
				})
			}
		}
	}
	blocks := make([]string, 0, len(drops))
	for b := range drops {
		blocks = append(blocks, b)
	}
	sort.Strings(blocks)
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_loot.go", "data/minecraft/loot_table/blocks/*.json"))
	sb.WriteString("\npackage loot\n\n")
	sb.WriteString("// blockDrops lists, per block, the items its loot table can give and what each needs.\n")
	sb.WriteString("var blockDrops = map[string][]Drop{\n")
	n := 0
	for _, b := range blocks {
		fmt.Fprintf(&sb, "\t%q: {", b)
		for i, d := range drops[b] {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "{%q, %q}", d.Item, d.Needs)
			n++
		}
		sb.WriteString("},\n")
	}
	sb.WriteString("}\n")
	if err := writeGo(filepath.Join(outRoot, "data", "loot", "blocks_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genLoot: %w", err)
	}
	logf("genLoot: %d blocks, %d drops", len(blocks), n)
	return nil
}

// walkLootEntry visits the item entries under a loot entry: an item, or the
// children of alternatives, a group, a sequence. The needs of an entry are
// its own conditions on top of its parent's.
func walkLootEntry(raw json.RawMessage, needs string, visit func(lootDrop)) {
	var e struct {
		Type       string            `json:"type"`
		Name       string            `json:"name"`
		Condition  json.RawMessage   `json:"condition"`
		Conditions []json.RawMessage `json:"conditions"`
		Children   []json.RawMessage `json:"children"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	if own := needsOf(append(e.Conditions, e.Condition)); own != "" {
		needs = own
	}
	switch strings.TrimPrefix(e.Type, "minecraft:") {
	case "item":
		visit(lootDrop{Item: e.Name, Needs: needs})
	case "alternatives", "group", "sequence":
		for _, c := range e.Children {
			walkLootEntry(c, needs, visit)
		}
	}
}

// needsOf names what a list of loot conditions asks of the player: a named
// predicate ("minecraft:tool/can_silk_touch") or an inline condition. The
// conditions every drop has (survives_explosion) need nothing.
func needsOf(conds []json.RawMessage) string {
	for _, raw := range conds {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var ref string
		if json.Unmarshal(raw, &ref) == nil {
			if n := needsOfName(ref); n != "" {
				return n
			}
			continue
		}
		var c struct {
			Condition string            `json:"condition"`
			Type      string            `json:"type"`
			Term      json.RawMessage   `json:"term"`
			Terms     []json.RawMessage `json:"terms"`
			Predicate json.RawMessage   `json:"predicate"`
		}
		if json.Unmarshal(raw, &c) != nil {
			continue
		}
		kind := c.Type
		if kind == "" {
			kind = c.Condition
		}
		switch strings.TrimPrefix(kind, "minecraft:") {
		case "survives_explosion":
		case "random_chance", "random_chance_with_enchanted_bonus", "table_bonus":
			return "chance"
		case "match_tool":
			p := string(c.Predicate)
			switch {
			case strings.Contains(p, "silk_touch"):
				return "silk_touch"
			case strings.Contains(p, "shears"):
				return "shears"
			}
			return "tool"
		case "inverted":
			// "not silk touch" is what a plain hand has
			if n := needsOf([]json.RawMessage{c.Term}); n == "silk_touch" || n == "shears" {
				continue
			}
			return "other"
		case "any_of", "all_of":
			if n := needsOf(c.Terms); n != "" {
				return n
			}
		case "reference":
			return "other"
		default:
			return "other"
		}
	}
	return ""
}

func needsOfName(ref string) string {
	switch {
	case strings.Contains(ref, "silk_touch"):
		return "silk_touch"
	case strings.Contains(ref, "shear"):
		return "shears"
	}
	return "other"
}
