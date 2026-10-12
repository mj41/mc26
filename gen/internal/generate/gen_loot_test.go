package generate

import (
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
