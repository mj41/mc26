package e2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// invSample is the server's answer to "data get entity Builder Inventory" for the items given as
// slot, id, count (the shape of 26.x: count before id, the slot as a byte; worn armor in slots 100-103).
func invSample(items ...string) string {
	var parts []string
	for i := 0; i+2 < len(items); i += 3 {
		parts = append(parts, fmt.Sprintf("{count: %s, Slot: %sb, id: \"minecraft:%s\"}", items[i+2], items[i], items[i+1]))
	}
	return "Builder has the following entity data: [" + strings.Join(parts, ", ") + "]"
}

func goalByWhat(t *testing.T, what string) goalMilestone {
	t.Helper()
	for _, g := range goalMilestones {
		if g.what == what {
			return g
		}
	}
	t.Fatalf("no goal milestone %q", what)
	return goalMilestone{}
}

// TestInventoryCounts: the items counted from the answer, worn pieces and nested components included.
func TestInventoryCounts(t *testing.T) {
	resp := invSample("0", "bread", "40", "1", "bread", "24", "100", "iron_boots", "1") +
		"" // slots 100-103 are the worn armor
	got, ok := inventoryCounts(resp)
	if !ok || got["minecraft:bread"] != 64 || got["minecraft:iron_boots"] != 1 {
		t.Fatalf("counts %v ok %v", got, ok)
	}
	nested := `Builder has the following entity data: [{components: {"minecraft:damage": 3, "minecraft:enchantments": {levels: {}}}, count: 1, Slot: 2b, id: "minecraft:iron_helmet"}, {Slot: 3b, id: "minecraft:bread"}]`
	got, ok = inventoryCounts(nested)
	if !ok || got["minecraft:iron_helmet"] != 1 || got["minecraft:bread"] != 1 {
		t.Fatalf("nested: %v (an item with no count is one)", got)
	}
	if _, ok := inventoryCounts("No entity was found"); ok {
		t.Fatalf("no entity read as an inventory")
	}
	if got, ok := inventoryCounts("Builder has the following entity data: []"); !ok || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, ok)
	}
}

// TestGoalMilestones: a stack of bread is 64 (in one slot or two), each piece of armor is itself, and
// full armor is all four.
func TestGoalMilestones(t *testing.T) {
	bread := goalByWhat(t, "a stack of bread")
	full := goalByWhat(t, "full iron armor")
	have := func(items ...string) map[string]int {
		m, _ := inventoryCounts(invSample(items...))
		return m
	}
	if !bread.reached(have("0", "bread", "64")) || !bread.reached(have("0", "bread", "40", "1", "bread", "24")) {
		t.Errorf("64 bread not a stack")
	}
	if bread.reached(have("0", "bread", "63")) {
		t.Errorf("63 bread reported a stack")
	}
	for what, id := range map[string]string{"an iron helmet": "iron_helmet", "an iron chestplate": "iron_chestplate",
		"iron leggings": "iron_leggings", "iron boots": "iron_boots"} {
		g := goalByWhat(t, what)
		if !g.reached(have("100", id, "1")) || g.reached(have("0", "iron_ingot", "9")) {
			t.Errorf("%s: worn counts, ingots do not", what)
		}
	}
	three := have("100", "iron_boots", "1", "101", "iron_leggings", "1", "102", "iron_chestplate", "1")
	if full.reached(three) {
		t.Errorf("3 pieces reported full armor")
	}
	four := have("100", "iron_boots", "1", "101", "iron_leggings", "1", "102", "iron_chestplate", "1", "103", "iron_helmet", "1")
	if !full.reached(four) {
		t.Errorf("4 pieces not full armor")
	}
}

// TestGoalMilestoneTimes: the time of a goal milestone is the poll that first saw it, kept after.
func TestGoalMilestoneTimes(t *testing.T) {
	inv := invSample("0", "bread", "10")
	cmd := func(c string) (string, error) {
		if strings.HasPrefix(c, "data get entity Builder Inventory") {
			return inv, nil
		}
		if strings.HasPrefix(c, "execute if entity") {
			if strings.Contains(c, "goal:") {
				t.Errorf("a goal milestone asked of the advancements: %s", c)
			}
			return "Test failed", nil
		}
		return "", fmt.Errorf("unknown %q", c)
	}
	start := time.Now()
	clock := start
	m := newMilestoneTimes(start, botRunMilestones(botDayRun{scenario: "botgoalfast", farm: &farmSetup{goal: true}}), "", "Builder", cmd, nil)
	m.now = func() time.Time { return clock }
	clock = start.Add(30 * time.Second)
	m.poll()
	if _, ok := m.Reached(goalByWhat(t, "a stack of bread").id); ok {
		t.Fatalf("10 bread reached the stack")
	}
	inv = invSample("0", "bread", "64")
	clock = start.Add(90 * time.Second)
	m.poll()
	clock = start.Add(120 * time.Second)
	inv = invSample("0", "bread", "20") // eaten since
	m.poll()
	if at, ok := m.Reached(goalByWhat(t, "a stack of bread").id); !ok || at != 90*time.Second {
		t.Fatalf("stack at %v %v, want 90s kept", at, ok)
	}
	if _, ok := m.Reached(goalByWhat(t, "full iron armor").id); ok {
		t.Fatalf("armor reached with none")
	}
}

// TestGoalOnlyInGoalRuns: the oracle for every other scenario is unchanged; only the goal runs add the
// goal milestones to the day-two list.
func TestGoalOnlyInGoalRuns(t *testing.T) {
	same := func(a, b []struct{ id, what string }) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if got := botRunMilestones(botDayRun{scenario: "botday1off", days: 1, mobs: mobsOff}); !same(got, botDayMilestones) {
		t.Errorf("botday1off milestones changed: %v", got)
	}
	if got := botRunMilestones(botDayRun{scenario: "botdays1off", days: 2, mobs: mobsOff}); !same(got, botDaysMilestones) {
		t.Errorf("botdays1off milestones changed: %v", got)
	}
	for _, name := range []string{"botfarmfast", "botfarmnobucket", "botfarmempty", "botfarmdays"} {
		f := botFarmScenarios[name]
		if got := botRunMilestones(botDayRun{scenario: name, days: 1, farm: &f}); !same(got, botDaysMilestones) {
			t.Errorf("%s milestones changed: %v", name, got)
		}
	}
	f := farmSetup{goal: true}
	got := botRunMilestones(botDayRun{scenario: "goal", days: 1, farm: &f})
	if len(got) != len(botDaysMilestones)+len(goalMilestones) || !same(got[:len(botDaysMilestones)], botDaysMilestones) {
		t.Errorf("goal run: %v", got)
	}
}

// TestGoalScenarios: botgoalfast and botgoal are botfarmempty's start (no bread, hoe, seeds or bucket), mobs off
// like the farm scenarios, three days with crops at 10x and four at the game's own speed; both are listed.
func TestGoalScenarios(t *testing.T) {
	empty := botFarmScenarios["botfarmempty"]
	for name, c := range map[string]struct {
		days  int
		rules []string
	}{"botgoalfast": {3, []string{"random_tick_speed 30"}}, "botgoal": {4, nil}} {
		f, ok := botFarmScenarios[name]
		if !ok {
			t.Fatalf("no scenario %s", name)
		}
		if !f.goal || f.days != c.days || !slices.Equal(f.rules, c.rules) || !slices.Equal(f.give, empty.give) {
			t.Errorf("%s: %+v", name, f)
		}
		if want := time.Duration(c.days)*20*time.Minute + time.Minute; f.length != want {
			t.Errorf("%s lasts %s, want %d days and a minute: %s", name, f.length, c.days, want)
		}
		if botFarmWhat[name] == "" {
			t.Errorf("%s: not described", name)
		}
		got := botRunMilestones(botDayRun{scenario: name, days: f.days, farm: &f})
		if len(got) != len(botDaysMilestones)+len(goalMilestones) {
			t.Errorf("%s: milestones %v", name, got)
		}
	}
	for _, name := range []string{"botfarmfast", "botfarmnobucket", "botfarmempty", "botfarmdays"} {
		if botFarmScenarios[name].goal {
			t.Errorf("%s is a goal run", name)
		}
	}
	scenarios, after := scenarioTable(Options{}, nil, new(string), new(*smoke.Server))
	known := scenarioNames(scenarios, after)
	for _, n := range []string{"botgoalfast", "botgoal"} {
		if !slices.Contains(known, n) {
			t.Errorf("%s is not in the scenario table", n)
		}
	}
}
