package e2e

import (
	"fmt"
	"os"
	"strings"
)

// Fitness: a run that goes on but gets nowhere is a failure too. A person new
// to a world has a stone pickaxe after the first night, iron by the third
// morning, a field sown…; each morning the days and the week check what the
// robot has done by then — its advancements, which the server keeps (a death
// or a relog loses none) — against the milestones due, and a milestone not
// met ends the run. The world decides what can be done how soon: the
// milestones are per seed (fitnessBySeed), anyWorld for the others.
// MC26_FITNESS=off leaves the check out (the scorecard is still logged).

// milestone is what the robot has done by the morning of day Morning (2: after
// the first night): one of the advancements Any.
type milestone struct {
	Morning int
	What    string
	Any     []string
}

// anyWorld are the milestones of a world not measured: a little behind what
// the scenario's own seed shows a robot can do.
var anyWorld = []milestone{
	{2, "a stone pickaxe", []string{"minecraft:story/upgrade_tools"}},
	{3, "a field sown", []string{"minecraft:husbandry/plant_seed"}},
	{4, "iron smelted", []string{"minecraft:story/smelt_iron"}},
	{5, "an iron pickaxe", []string{"minecraft:story/iron_tools"}},
}

// fitnessBySeed are the milestones of the worlds measured: what robots have
// done there by when, with a night to spare.
var fitnessBySeed = map[string][]milestone{
	// the survival scenarios' seed: iron in the first night's stairs (the
	// week runs of 2026-10-07 smelted it and made the pickaxe that night)
	survivalSeed: {
		{2, "a stone pickaxe", []string{"minecraft:story/upgrade_tools"}},
		{3, "iron smelted", []string{"minecraft:story/smelt_iron"}},
		// day 2 goes mostly up its stairs and on a forest plot's trees: the
		// field by day 3, a night to spare (the week runs of 2026-10-07 had
		// none on day 2)
		{4, "a field sown", []string{"minecraft:husbandry/plant_seed"}},
		{4, "an iron pickaxe", []string{"minecraft:story/iron_tools"}},
		{8, "diamonds", []string{"minecraft:story/mine_diamond"}},
	},
}

// milestonesFor are the milestones of a seed's world.
func milestonesFor(seed string) []milestone {
	if m, ok := fitnessBySeed[seed]; ok {
		return m
	}
	return anyWorld
}

// fitness checks the milestones due by the morning of day morning: the
// scorecard logged (every milestone so far, done or not), an error naming
// the ones due and not done (nil with MC26_FITNESS=off).
func fitness(o Options, rcon *rconClient, scenario, name, seed string, morning int) error {
	done := map[string]bool{}
	var card, missed []string
	for _, m := range milestonesFor(seed) {
		if m.Morning > morning {
			continue
		}
		ok := false
		for _, a := range m.Any {
			if _, seen := done[a]; !seen {
				resp, err := rcon.command(fmt.Sprintf("execute if entity @a[name=%s,advancements={%s=true}]", name, a))
				if err != nil {
					return fmt.Errorf("fitness: %w", err)
				}
				done[a] = strings.Contains(resp, "passed")
			}
			ok = ok || done[a]
		}
		mark := "done"
		if !ok {
			mark = "NOT done"
			if m.Morning == morning {
				missed = append(missed, fmt.Sprintf("%s (%s)", m.What, strings.Join(m.Any, " or ")))
			}
		}
		card = append(card, fmt.Sprintf("%s by morning %d: %s", m.What, m.Morning, mark))
	}
	if len(card) > 0 {
		o.Log("%s: fitness, morning %d: %s", scenario, morning, strings.Join(card, "; "))
	}
	if len(missed) == 0 || os.Getenv("MC26_FITNESS") == "off" {
		return nil
	}
	return fmt.Errorf("fitness: by the morning of day %d (seed %s) not done: %s", morning, seed, strings.Join(missed, ", "))
}
