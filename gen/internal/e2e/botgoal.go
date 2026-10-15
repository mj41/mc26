package e2e

import (
	"regexp"
	"strconv"
	"strings"
)

// The goal milestones: how far a run got toward "a stack of bread and full iron armor". They are read from the
// bot's inventory by RCON (the pack, the worn armor and the off hand; what is in a chest is not counted), and
// each takes the time of the first poll that saw it: a milestone kept, as the advancements are, not lost when the
// bread is eaten or the armor breaks. They are part of a goal run's list only (botRunMilestones); every other
// scenario judges what it always did.

// goalMilestone is a milestone read from the inventory: its id (it is no advancement; the "goal:" prefix keeps
// it from being asked of the server as one), what it is called in the scorecard, and what the counts must show.
type goalMilestone struct {
	id, what string
	reached  func(have map[string]int) bool
}

// goalIDPrefix starts the id of a milestone that is no advancement.
const goalIDPrefix = "goal:"

// breadStack is a full stack.
const breadStack = 64

// ironArmor are the four pieces of the iron armor.
var ironArmor = []struct{ item, what string }{
	{"minecraft:iron_helmet", "an iron helmet"},
	{"minecraft:iron_chestplate", "an iron chestplate"},
	{"minecraft:iron_leggings", "iron leggings"},
	{"minecraft:iron_boots", "iron boots"},
}

// goalMilestones are the goal run's milestones, in their order.
var goalMilestones = func() []goalMilestone {
	ms := []goalMilestone{{goalIDPrefix + "bread_stack", "a stack of bread",
		func(have map[string]int) bool { return have["minecraft:bread"] >= breadStack }}}
	for _, a := range ironArmor {
		item := a.item
		ms = append(ms, goalMilestone{goalIDPrefix + strings.TrimPrefix(item, "minecraft:"), a.what,
			func(have map[string]int) bool { return have[item] >= 1 }})
	}
	return append(ms, goalMilestone{goalIDPrefix + "iron_armor", "full iron armor", func(have map[string]int) bool {
		for _, a := range ironArmor {
			if have[a.item] < 1 {
				return false
			}
		}
		return true
	}})
}()

var (
	itemIDRe    = regexp.MustCompile(`\bid: "([^"]+)"`)
	itemCountRe = regexp.MustCompile(`\bcount: (\d+)`)
)

// inventoryCounts reads the answer of "data get entity <bot> Inventory" into the count of each item (a stack
// with no count is one item; the worn armor and the off hand are in the list). ok is false for an answer that
// is no inventory (no such entity).
func inventoryCounts(resp string) (counts map[string]int, ok bool) {
	i := strings.Index(resp, "entity data: ")
	if i < 0 {
		return nil, false
	}
	counts = map[string]int{}
	depth := 0
	var top strings.Builder // the text of the item's own compound, without what is nested in it (components)
	for j := i; j < len(resp); j++ {
		switch c := resp[j]; c {
		case '{':
			if depth == 0 {
				top.Reset()
			}
			depth++
			if depth > 1 {
				continue
			}
		case '}':
			depth--
			if depth == 0 {
				id := itemIDRe.FindStringSubmatch(top.String())
				if id != nil {
					n := 1
					if m := itemCountRe.FindStringSubmatch(top.String()); m != nil {
						n, _ = strconv.Atoi(m[1])
					}
					counts[id[1]] += n
				}
			}
			continue
		}
		if depth == 1 {
			top.WriteByte(resp[j])
		}
	}
	return counts, true
}

// isGoalID reports whether a goal milestone id belongs to the inventory goals.
func isGoalID(id string) bool { return strings.HasPrefix(id, goalIDPrefix) }

// botRunMilestones are the milestones a botday run keeps: the first day's, the next days' for two days or a
// farm scenario, and the goal's on top of them for a goal run only.
func botRunMilestones(r botDayRun) []struct{ id, what string } {
	ms := botDayMilestones
	if r.days > 1 || r.farm != nil {
		ms = botDaysMilestones
	}
	if r.farm != nil && r.farm.goal {
		ms = append([]struct{ id, what string }{}, ms...)
		for _, g := range goalMilestones {
			ms = append(ms, struct{ id, what string }{g.id, g.what})
		}
	}
	return ms
}

// goalByID is the goal milestone of an id.
func goalByID(id string) (goalMilestone, bool) {
	for _, g := range goalMilestones {
		if g.id == id {
			return g, true
		}
	}
	return goalMilestone{}, false
}
