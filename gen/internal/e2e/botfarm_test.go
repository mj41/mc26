package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestFarmTally: the counters read from the server's answers, a score never
// set read as 0, and what is sown after the first wheat counted as sown again.
func TestFarmTally(t *testing.T) {
	score := map[string]int{}
	cmd := func(c string) (string, error) {
		var name, obj string
		fmt.Sscanf(c, "scoreboard players get %s %s", &name, &obj)
		if n, ok := score[obj]; ok {
			return fmt.Sprintf("%s has %d [%s]", name, n, obj), nil
		}
		return "Can't get value of " + obj + " for " + name + "; none is set", nil
	}
	tl := newFarmTally()
	tl.read(cmd, "Builder", time.Minute)
	if tl.n["sown"] != 0 || tl.sownAtHarvest != -1 {
		t.Fatalf("nothing set: %v %d", tl.n, tl.sownAtHarvest)
	}
	score["ftilled"], score["fsown"] = 4, 4
	tl.read(cmd, "Builder", 2*time.Minute)
	if tl.n["tilled"] != 4 || tl.n["sown"] != 4 || tl.sownAgain() != 0 {
		t.Fatalf("sown: %v, again %d", tl.n, tl.sownAgain())
	}
	score["fcut"], score["fwheat"] = 4, 4
	tl.read(cmd, "Builder", 12*time.Minute)
	score["fsown"] = 7
	tl.read(cmd, "Builder", 13*time.Minute)
	if tl.sownAgain() != 3 || tl.fitness()["firstWheat"] != 720 {
		t.Fatalf("again %d, fitness %v", tl.sownAgain(), tl.fitness())
	}
	f := botFarmScenarios["botfarmdays"]
	if !tl.farmPassed(&f) {
		t.Fatalf("4 wheat, 3 sown again: passes %d", f.harvest)
	}
	if fast := botFarmScenarios["botfarmfast"]; tl.farmPassed(&fast) {
		t.Fatalf("4 wheat passed botfarmfast's %d", fast.harvest)
	}
}

// TestFarmScenarioNames: the bench takes names of letters and digits only.
func TestFarmScenarioNames(t *testing.T) {
	for name, f := range botFarmScenarios {
		for _, r := range name {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				t.Errorf("%q: not letters and digits", name)
			}
		}
		if botFarmWhat[name] == "" || f.length == 0 || f.harvest == 0 {
			t.Errorf("%q: not described or no length/harvest", name)
		}
	}
	if botFarmScenarios["botfarmnobucket"].give[len(botFarmScenarios["botfarmnobucket"].give)-1] == "minecraft:bucket 1" {
		t.Errorf("botfarmnobucket hands a bucket")
	}
}
