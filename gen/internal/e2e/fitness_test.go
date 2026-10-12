package e2e

import (
	"strings"
	"testing"
)

func TestMilestones(t *testing.T) {
	if got := milestonesFor("not measured"); len(got) == 0 || got[0].What != anyWorld[0].What {
		t.Fatalf("an unmeasured seed: %v", got)
	}
	if got := milestonesFor(survivalSeed); len(got) == 0 || &got[0] == &anyWorld[0] {
		t.Fatalf("the scenario's seed has no milestones of its own")
	}
	for seed, ms := range map[string][]milestone{"": anyWorld, survivalSeed: fitnessBySeed[survivalSeed]} {
		last := 0
		for _, m := range ms {
			if m.Morning < 2 || m.Morning < last || m.What == "" || len(m.Any) == 0 {
				t.Errorf("seed %q: %+v (mornings from 2, in order, each named, an advancement)", seed, m)
			}
			for _, a := range m.Any {
				if !strings.HasPrefix(a, "minecraft:") || !strings.Contains(a, "/") {
					t.Errorf("seed %q: %q is no advancement id", seed, a)
				}
			}
			last = m.Morning
		}
	}
}
