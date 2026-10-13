package e2e

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// The farm scenarios: the botday judge on a world of a fixed seed with water
// by the spawn, the bot handed at its start what its first day's plan makes
// (a table, stone tools, logs, cobblestone, torches, bread) and the farm's
// tools (a hoe, seeds, a bucket), so its field comes in its first minutes,
// not after a day. They are judged by the server's own counts (farmCounters):
// the hoe used, the seeds sown, the wheat cut and picked up, and the
// seeds sown again after the first harvest.
//
//   - botfarmfast: the crops growing ten times as fast (random_tick_speed 30)
//     through the game's own days and nights: a field's whole cycle, sown,
//     ripe, harvested and sown again, in 30 minutes (the bot's own harvest
//     timer is ten minutes, by day only: sown in the first minutes, harvested
//     at the next dawn). The day is not held: with the day's plan done and no
//     night coming the bot stood by its field with nothing to do, and the
//     stall limit ended the run before its harvest (botfarmfast 1013-231946);
//   - botfarmnobucket: botfarmfast without the bucket: the field by water it
//     finds, or dry by home;
//   - botfarmdays: two days and their nights at the game's own pace: the
//     field tended through them, the wheat each day.
//
// Mobs are off in all three: they measure the farm, not the fights.

// farmSetup is what a farm scenario changes of a botday run.
type farmSetup struct {
	give    []string      // items handed at the start: "minecraft:bread 8"
	rules   []string      // game rules: "random_tick_speed 30"
	length  time.Duration // the run's length
	harvest int           // wheat picked up, at least, to pass
}

// botFarmSeed is the farm scenarios' world: botday2's (8675309), water a
// few dozen blocks from the spawn (its botday runs sowed by it).
const botFarmSeed = "8675309"

// botFarmKit is the first day's plan, made: the farm starts at once.
var botFarmKit = []string{
	"minecraft:crafting_table 1", "minecraft:stone_pickaxe 1", "minecraft:stone_axe 1",
	"minecraft:stone_shovel 1", "minecraft:stone_sword 1", "minecraft:oak_log 16",
	"minecraft:cobblestone 32", "minecraft:torch 16", "minecraft:bread 8",
	"minecraft:stone_hoe 1", "minecraft:wheat_seeds 8",
}

// botFarmScenarios are the farm scenarios by name.
var botFarmScenarios = map[string]farmSetup{
	"botfarmfast": {
		give:    append(append([]string{}, botFarmKit...), "minecraft:bucket 1"),
		rules:   []string{"random_tick_speed 30"},
		length:  30 * time.Minute,
		harvest: 6,
	},
	"botfarmnobucket": {
		give:    botFarmKit,
		rules:   []string{"random_tick_speed 30"},
		length:  30 * time.Minute,
		harvest: 6,
	},
	"botfarmdays": {
		give:    append(append([]string{}, botFarmKit...), "minecraft:bucket 1"),
		length:  2*20*time.Minute + time.Minute,
		harvest: 3,
	},
}

// botFarmWhat says what a farm scenario is.
var botFarmWhat = map[string]string{
	"botfarmfast":     "the field from minute one (hoe, seeds, bucket handed), crops 10x: sown, cut at dawn, sown again, 30 min",
	"botfarmnobucket": "botfarmfast without a bucket: the field by water it finds, or dry by home",
	"botfarmdays":     "the field from minute one (hoe, seeds, bucket handed), two days and nights: the wheat each day",
}

// scenarioBotFarm is a farm scenario: a botday run with its farmSetup.
func scenarioBotFarm(o Options, name string) error {
	f, ok := botFarmScenarios[name]
	if !ok {
		return fmt.Errorf("no farm scenario %q", name)
	}
	return runBotDay(o, botDayRun{scenario: name, seed: botFarmSeed, days: 1, mobs: mobsOff, farm: &f})
}

// farmCounter is one of the server's statistics a farm is judged by, kept as a
// scoreboard objective.
type farmCounter struct{ objective, criterion, what string }

// farmCounters are kept on every botday run, farm or not: the wheat a day is
// a fitness of its own.
var farmCounters = []farmCounter{
	// (the game has no count of the farmland tilled: a hoe is "used" for a
	// cell tilled and for a block broken with it — leaves, moss — alike)
	{"fhoe", "minecraft.used:minecraft.stone_hoe", "hoe"},
	{"fsown", "minecraft.used:minecraft.wheat_seeds", "sown"},
	{"fcut", "minecraft.mined:minecraft.wheat", "cut"},
	{"fwheat", "minecraft.picked_up:minecraft.wheat", "wheat"},
}

var scoreRe = regexp.MustCompile(` has (\d+) `)

// farmTally is the farm counters read, and the seeds sown when the first
// wheat was picked up (what is sown after it is sown again).
type farmTally struct {
	n             map[string]int
	sownAtHarvest int // -1: no wheat picked up yet
	firstWheat    time.Duration
}

func newFarmTally() *farmTally { return &farmTally{n: map[string]int{}, sownAtHarvest: -1} }

// read reads each counter of the player (a score never set is 0).
func (t *farmTally) read(cmd rconCmd, name string, since time.Duration) {
	for _, c := range farmCounters {
		resp, err := cmd("scoreboard players get " + name + " " + c.objective)
		if err != nil {
			continue
		}
		if m := scoreRe.FindStringSubmatch(resp); m != nil {
			t.n[c.what], _ = strconv.Atoi(m[1])
		}
	}
	if t.sownAtHarvest < 0 && t.n["wheat"] > 0 {
		t.sownAtHarvest, t.firstWheat = t.n["sown"], since
	}
}

// sownAgain is how many seeds were sown after the first harvest.
func (t *farmTally) sownAgain() int {
	if t.sownAtHarvest < 0 {
		return 0
	}
	return t.n["sown"] - t.sownAtHarvest
}

// fitness is the tally for the run's scorecard.
func (t *farmTally) fitness() map[string]any {
	f := map[string]any{"sownAgain": t.sownAgain()}
	for _, c := range farmCounters {
		f[c.what] = t.n[c.what]
	}
	if t.sownAtHarvest >= 0 {
		f["firstWheat"] = int(t.firstWheat.Seconds())
	}
	return f
}

// String says the tally in a line.
func (t *farmTally) String() string {
	return fmt.Sprintf("hoe used %d, sown %d, cut %d, wheat %d, sown again %d",
		t.n["hoe"], t.n["sown"], t.n["cut"], t.n["wheat"], t.sownAgain())
}

// farmPassed: wheat picked up, at least the setup's, and seeds sown again
// after it (the field kept, not taken once and left).
func (t *farmTally) farmPassed(f *farmSetup) bool {
	return t.n["wheat"] >= f.harvest && t.sownAgain() > 0
}
