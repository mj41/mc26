package recipe

import (
	"slices"
	"testing"
)

func unlockOf(t *testing.T, result string) [][]string {
	t.Helper()
	rs := For(result)
	if len(rs) == 0 {
		t.Fatalf("no recipe makes %s", result)
	}
	return rs[0].Unlock
}

// What unlocks a recipe is what its advancement asks the player to hold: a
// stick for the wooden pickaxe, not the planks it is made of; the logs of a
// kind for its planks.
func TestUnlock(t *testing.T) {
	if u := unlockOf(t, "minecraft:wooden_pickaxe"); len(u) != 1 || !slices.Equal(u[0], []string{"minecraft:stick"}) {
		t.Errorf("wooden pickaxe: %v, want a stick", u)
	}
	if u := unlockOf(t, "minecraft:oak_planks"); len(u) != 1 || !slices.Contains(u[0], "#minecraft:oak_logs") {
		t.Errorf("oak planks: %v, want the oak logs", u)
	}
	if u := unlockOf(t, "minecraft:crafting_table"); len(u) != 0 {
		t.Errorf("crafting table: %v, want none (unlocked at once)", u)
	}
}

// The iron ingot is smelted in 200 ticks (ten seconds), and a crafting recipe has no cooking time.
func TestCookingTime(t *testing.T) {
	var found bool
	for _, r := range For("minecraft:iron_ingot") {
		if r.Kind == "minecraft:smelting" && len(r.Ingredients) == 1 && slices.Contains(r.Ingredients[0], "minecraft:raw_iron") {
			found = true
			if r.Cooking != 200 {
				t.Errorf("iron ingot from raw iron: %d ticks, want 200", r.Cooking)
			}
		}
	}
	if !found {
		t.Errorf("no smelting recipe of raw iron")
	}
	if c := For("minecraft:oak_planks")[0].Cooking; c != 0 {
		t.Errorf("planks cook %d", c)
	}
}
