package generate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeLoot(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The loot tables are generated for a version whose data has named predicates (data/minecraft/predicate, 26.3) and for
// one whose has none (26.1 and 26.2: their tables hold their conditions inline): the second has no predicates, and no
// error. The all-versions check found the second: the generator had read a directory only 26.3 has.
func TestLootTablesWithAndWithoutPredicates(t *testing.T) {
	files := map[string]string{
		"data/minecraft/loot_table/blocks/stone.json":    `{"type":"minecraft:block","pools":[]}`,
		"data/minecraft/loot_table/entities/zombie.json": `{"type":"minecraft:entity","pools":[]}`,
	}
	read := func(out string) map[string]map[string]json.RawMessage {
		b, err := os.ReadFile(filepath.Join(out, "data", "loot", "tables.json"))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]map[string]json.RawMessage
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	// no predicate directory
	in, out := t.TempDir(), t.TempDir()
	writeLoot(t, in, files)
	if err := genLootTables(in, out, filepath.Join(in, "data", "minecraft", "loot_table", "blocks")); err != nil {
		t.Fatalf("no predicates: %v", err)
	}
	if got := read(out); len(got["predicates"]) != 0 || len(got["tables"]) != 1 || len(got["entities"]) != 1 {
		t.Errorf("tables %d entities %d predicates %d", len(got["tables"]), len(got["entities"]), len(got["predicates"]))
	}
	// with one
	in2, out2 := t.TempDir(), t.TempDir()
	writeLoot(t, in2, files)
	writeLoot(t, in2, map[string]string{"data/minecraft/predicate/tool/can_silk_touch.json": `{ "condition": "x" }`})
	if err := genLootTables(in2, out2, filepath.Join(in2, "data", "minecraft", "loot_table", "blocks")); err != nil {
		t.Fatalf("with predicates: %v", err)
	}
	if got := read(out2); len(got["predicates"]) != 1 || string(got["predicates"]["minecraft:tool/can_silk_touch"]) != `{"condition":"x"}` {
		t.Errorf("predicates: %v", got["predicates"])
	}
}

// The tables of a version with the old shape (26.1, 26.2: "condition" and "function" keys, block_state_property) are
// written in the evaluator's: "type" keys, match_block with blocks and state; and 26.3's own are left as they are.
func TestOldLootTablesAreWrittenInTheEvaluatorsShape(t *testing.T) {
	old := `{"type":"minecraft:block","pools":[{"rolls":1.0,"entries":[{"type":"minecraft:item","name":"minecraft:wheat",
	  "conditions":[{"condition":"minecraft:block_state_property","block":"minecraft:wheat","properties":{"age":"7"}},
	                {"condition":"minecraft:inverted","term":{"condition":"minecraft:killed_by_player"}}],
	  "functions":[{"function":"minecraft:set_count","count":{"type":"minecraft:uniform","min":1.0,"max":3.0}}]}]}]}`
	in, out := t.TempDir(), t.TempDir()
	writeLoot(t, in, map[string]string{
		"data/minecraft/loot_table/blocks/wheat.json":    old,
		"data/minecraft/loot_table/entities/zombie.json": `{"type":"minecraft:entity","pools":[]}`,
	})
	if err := genLootTables(in, out, filepath.Join(in, "data", "minecraft", "loot_table", "blocks")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(out, "data", "loot", "tables.json"))
	var all struct{ Tables map[string]json.RawMessage }
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Pools []struct {
			Entries []struct {
				Conditions []map[string]any
				Functions  []map[string]any
			}
		}
	}
	if err := json.Unmarshal(all.Tables["minecraft:wheat"], &got); err != nil {
		t.Fatal(err)
	}
	e := got.Pools[0].Entries[0]
	if e.Conditions[0]["type"] != "minecraft:match_block" || e.Conditions[0]["blocks"] != "minecraft:wheat" || e.Conditions[0]["state"].(map[string]any)["age"] != "7" ||
		e.Conditions[0]["condition"] != nil || e.Conditions[0]["block"] != nil {
		t.Errorf("block_state_property: %v", e.Conditions[0])
	}
	if e.Conditions[1]["type"] != "minecraft:inverted" || e.Conditions[1]["term"].(map[string]any)["type"] != "minecraft:killed_by_player" {
		t.Errorf("inverted: %v", e.Conditions[1])
	}
	if e.Functions[0]["type"] != "minecraft:set_count" || e.Functions[0]["function"] != nil || e.Functions[0]["count"].(map[string]any)["max"].(float64) != 3 {
		t.Errorf("set_count: %v", e.Functions[0])
	}
	// the same table with a predicate directory (26.3's shape) is left alone: its "condition" is a named predicate
	in2, out2 := t.TempDir(), t.TempDir()
	writeLoot(t, in2, map[string]string{
		"data/minecraft/loot_table/blocks/stone.json":       `{"type":"minecraft:block","pools":[{"entries":[{"type":"minecraft:item","condition":"minecraft:tool/can_silk_touch","name":"minecraft:stone"}]}]}`,
		"data/minecraft/loot_table/entities/zombie.json":    `{"type":"minecraft:entity","pools":[]}`,
		"data/minecraft/predicate/tool/can_silk_touch.json": `{"type":"minecraft:match_tool"}`,
	})
	if err := genLootTables(in2, out2, filepath.Join(in2, "data", "minecraft", "loot_table", "blocks")); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(out2, "data", "loot", "tables.json"))
	if !bytes.Contains(b2, []byte(`"condition":"minecraft:tool/can_silk_touch"`)) {
		t.Errorf("26.3's named predicate was rewritten: %s", b2)
	}
}

// 26.1's entity predicates are written as 26.2 and 26.3 write them, a tag entry names its tag in items, a key already
// so named is left alone.
func TestOldEntityPredicates(t *testing.T) {
	raw := `{"type":"minecraft:entity","pools":[{"entries":[{"type":"minecraft:tag","expand":true,"name":"minecraft:creeper_drop_music_discs"}],
	 "conditions":[{"condition":"minecraft:entity_properties","entity":"this","predicate":{"flags":{"is_on_fire":true},"vehicle":{"type":"minecraft:zombie_horse"},
	   "type":"#minecraft:skeletons","type_specific":{"type":"minecraft:slime","size":{"min":2}},"minecraft:equipment":{"mainhand":{}}}},
	  {"condition":"minecraft:damage_source_properties","predicate":{"source_entity":{"type":"#minecraft:skeletons"}}}]}]}`
	got, err := normalizeOldLoot([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"minecraft:flags":{"is_on_fire":true}`, `"minecraft:vehicle":{"minecraft:entity_type":"minecraft:zombie_horse"}`,
		`"minecraft:entity_type":"#minecraft:skeletons"`, `"minecraft:type_specific/cube_mob":{"size":{"min":2}}`, `"minecraft:equipment":{"mainhand":{}}`,
		`"items":"#minecraft:creeper_drop_music_discs"`, `"source_entity":{"minecraft:entity_type":"#minecraft:skeletons"}`} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("lacks %s:\n%s", want, got)
		}
	}
}
