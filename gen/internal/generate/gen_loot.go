// gen_loot generates data/loot/blocks_gen.go from the block loot tables of
// the vanilla data pack (data/minecraft/loot_table/blocks/*.json, which the
// extraction copies from the data generator's output): for every block, the
// items its loot table can give and what each needs — nothing, silk touch,
// shears, luck, or a condition the generator does not name. data/loot/loot.go
// looks it up both ways (BlockDrops, DroppedBy) — and data/loot/tables.json, the
// tables whole (pools, rolls, entries, conditions, functions) and the named
// predicates they refer to (data/minecraft/predicate/**), which data/loot/eval.go
// plays: what a block gives, with the tool, the enchantments and a chance. The
// entities' tables (loot_table/entities/**) are there too, played the same way:
// what a mob killed leaves, by whom and how it was killed.
package generate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	return genLootTables(jsonDir, outRoot, dir)
}

// genLootTables writes the block loot tables whole, the entity ones
// (loot_table/entities/**: "minecraft:zombie", "minecraft:sheep/white"), the
// other tables an entry of theirs names (minecraft:gameplay/fishing/fish, by
// its full name) and the predicates, minified.
func genLootTables(jsonDir, outRoot, tablesDir string) error {
	all := struct {
		Tables     map[string]json.RawMessage `json:"tables"`
		Entities   map[string]json.RawMessage `json:"entities"`
		Others     map[string]json.RawMessage `json:"others"`
		Predicates map[string]json.RawMessage `json:"predicates"`
	}{map[string]json.RawMessage{}, map[string]json.RawMessage{}, map[string]json.RawMessage{}, map[string]json.RawMessage{}}
	tablesRoot := filepath.Join(jsonDir, "data", "minecraft", "loot_table")
	readTable := func(path string) (json.RawMessage, error) {
		var v map[string]json.RawMessage
		if err := readJSON(path, &v); err != nil {
			return nil, fmt.Errorf("genLoot: %w", err)
		}
		delete(v, "random_sequence") // (the game's seed of its randomness: ours is the caller's)
		return json.Marshal(v)
	}
	readDir := func(dir string, into map[string]json.RawMessage) error {
		return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
				return err
			}
			b, err := readTable(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			into["minecraft:"+strings.TrimSuffix(filepath.ToSlash(rel), ".json")] = b
			return nil
		})
	}
	if err := readDir(tablesDir, all.Tables); err != nil {
		return err
	}
	if err := readDir(filepath.Join(tablesRoot, "entities"), all.Entities); err != nil {
		return fmt.Errorf("genLoot: %w (re-run extraction: the data must include the data packs)", err)
	}
	// the tables named by a loot_table entry that are neither a block's nor an entity's, and the ones they name
	todo := []json.RawMessage{}
	for _, m := range []map[string]json.RawMessage{all.Tables, all.Entities} {
		for _, b := range m {
			todo = append(todo, b)
		}
	}
	for len(todo) > 0 {
		b := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, ref := range lootTableRefs(b) {
			name, ok := strings.CutPrefix(ref, "minecraft:")
			if !ok || strings.HasPrefix(name, "blocks/") || strings.HasPrefix(name, "entities/") || all.Others[ref] != nil {
				continue
			}
			t, err := readTable(filepath.Join(tablesRoot, filepath.FromSlash(name)+".json"))
			if err != nil {
				return err
			}
			all.Others[ref] = t
			todo = append(todo, t)
		}
	}
	predDir := filepath.Join(jsonDir, "data", "minecraft", "predicate")
	if _, serr := os.Stat(predDir); errors.Is(serr, fs.ErrNotExist) {
		// a version before the game had named predicates (26.1, 26.2): its tables hold their conditions inline
		predDir = ""
	}
	err := error(nil)
	if predDir != "" {
		err = filepath.WalkDir(predDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
				return err
			}
			var v json.RawMessage
			if err := readJSON(path, &v); err != nil {
				return err
			}
			rel, _ := filepath.Rel(predDir, path)
			var compact bytes.Buffer
			if err := json.Compact(&compact, v); err != nil {
				return err
			}
			all.Predicates["minecraft:"+strings.TrimSuffix(filepath.ToSlash(rel), ".json")] = compact.Bytes()
			return nil
		})
	}
	if err != nil {
		return fmt.Errorf("genLoot: %w", err)
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	logf("genLoot: %d block tables, %d entity tables, %d others, %d predicates, %d bytes",
		len(all.Tables), len(all.Entities), len(all.Others), len(all.Predicates), len(b))
	return writeFile(filepath.Join(outRoot, "data", "loot", "tables.json"), b)
}

// lootTableRefs are the tables a table's loot_table entries name ("value": "minecraft:entities/sheep/white").
func lootTableRefs(raw json.RawMessage) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if t, _ := v["type"].(string); t == "minecraft:loot_table" {
				if ref, ok := v["value"].(string); ok {
					out = append(out, ref)
				}
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v)
	}
	return out
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
