package e2e

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// The botflat scenario: the outside bot (botday.go) on a stable flat world —
// no day and night, no weather, no mobs at all, peaceful — in stages, each
// adding one thing to an empty world: nothing, a tree, a tree and stone, then
// all of them and water. Each stage is a bot of its own (a new mind) on the
// same server, the area made again before it; the server judges it — what
// it carries, whether it died, how far it went, how long it stood still —
// and the bot's plan lines say what it did. A stage's goal is what that world
// allows; botflat fails when a stage misses its goal.

// The flat world's layers (flatLayers): bedrock, stone up to 60, two dirt,
// grass at 63 — sea level — so a bot stands at 64, digs through two blocks of
// earth to stone, and the night's stairs go down through stone where the
// stages put their ores.
const flatGround = 64

// flatLayers is the flat world's generator: bedrock, 124 stone, 2 dirt, grass.
const flatLayers = `{"layers":[{"block":"minecraft:bedrock","height":1},{"block":"minecraft:stone","height":124},` +
	`{"block":"minecraft:dirt","height":2},{"block":"minecraft:grass_block","height":1}],"biome":"minecraft:plains",` +
	`"features":false,"lakes":false,"structure_overrides":[]}`

// fy is a height off the ground (0: where the bot stands), as text.
func fy(d int) string { return strconv.Itoa(flatGround + d) }

// flatStage is one stage: what the world gains, what the bot must carry
// (item: count), the time it should take (target) and the most it may
// (last: then the stage fails).
//
// A stage's fitness: "fit", the goal met within the target; "slow", within
// the limit; "fail", not met. Beside it the time the bot stood idle, by the
// task it was on — where it waits is what to make faster.
type flatStage struct {
	name   string
	setup  []string // RCON commands after the area was made again
	target time.Duration
	last   time.Duration
	goal   map[string]int
	world  map[string]int // blocks of a kind there must be in its area at the end (a field's farmland, its wheat)
	steps  []string       // the plan steps it must say done, each once (a done task does not come back)
	swim   bool           // it must have been in the water (a way across, not round)
	clock  string         // an RCON command that sets the clock back after the stage ("": none)
	do     string         // a single body skill run on its own (the bot's -do), the stage ends with it
	start  [3]int         // where in its area the bot is put (off the spawn's 0, ground, 0)
	round  int            // botprobe's round, from 1 (0: no probe), in its conditions
}

// flatHill is a stepped hill of stone round the area's middle: twenty layers
// a block high, each a block smaller all round than the one under it.
func flatHill() []string {
	var out []string
	for k := 0; k < 20; k++ {
		h := 20 - k
		out = append(out, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:stone", -h, flatGround+k, -h, h, flatGround+k, h))
	}
	return out
}

// flatRings is how far the bot explores on the flat world (-explore-rings):
// a stage with nothing to do ends once it has seen it all, not at its limit.
// The server sends 6 rings round the player (smoke: view-distance 6): 8
// rings round the spawn are a loop two chunks out, 256 blocks (10 were 512,
// longer than the stage).
const flatRings = 8

var flatStages = []flatStage{
	{name: "empty", target: 90 * time.Second, last: 3 * time.Minute},
	{name: "a tree", target: 45 * time.Second, last: 3 * time.Minute,
		setup: []string{flatTree("oak", 6, 2)},
		goal:  map[string]int{"minecraft:wooden_pickaxe": 1},
		steps: []string{"logs: 5", "crafting table, wooden pickaxe"}},
	{name: "a tree and stone", target: 75 * time.Second, last: 4 * time.Minute,
		setup: []string{flatTree("oak", 6, 2), "fill -8 " + fy(-3) + " -3 -4 " + fy(-1) + " 1 minecraft:stone"},
		goal:  map[string]int{"minecraft:stone_pickaxe": 1},
		steps: []string{"logs: 5", "crafting table, wooden pickaxe", "cobblestone for the stone tools"}},
	// trees enough for the plan's 16 more logs after the tools (an oak gives
	// 4 to 6: six of them, a birch; with two the bot went exploring for the
	// rest)
	{name: "trees, stone, water", target: 3 * time.Minute, last: 7 * time.Minute,
		setup: []string{flatTree("oak", 6, 2), flatTree("birch", -2, 9),
			flatTree("oak", 10, -4), flatTree("oak", 12, 6), flatTree("oak", -6, 12),
			flatTree("oak", 4, 14), flatTree("oak", -12, 6),
			"fill -8 " + fy(-3) + " -3 -4 " + fy(-1) + " 1 minecraft:stone", "fill 3 " + fy(-1) + " -9 6 " + fy(-1) + " -6 minecraft:water"},
		goal: map[string]int{"minecraft:stone_pickaxe": 1, "minecraft:stone_axe": 1, "minecraft:stone_sword": 1, "minecraft:stone_shovel": 1,
			"logs": 16},
		steps: []string{"logs: 5", "crafting table, wooden pickaxe", "cobblestone for the stone tools", "stone pickaxe, axe, sword, shovel",
			"beaten tools thrown away", "logs: 16 more"}},
	// a tree across a canal (6 wide, 2 deep, 41 long): swum across, not
	// walked round — the stage counts the seconds in water
	{name: "a tree across water", target: 60 * time.Second, last: 3 * time.Minute,
		setup: []string{"fill 4 " + fy(-2) + " -20 9 " + fy(-1) + " 20 minecraft:water", flatTree("oak", 14, 0)},
		goal:  map[string]int{"minecraft:wooden_pickaxe": 1},
		steps: []string{"logs: 5", "crafting table, wooden pickaxe"}, swim: true},
	// dusk near (the clock held there), the day's tools and wood given: a
	// shelter dug where it stands — three stair steps down, closed — before
	// the night (the stage ends when it is said built)
	{name: "dusk, tools given", target: 30 * time.Second, last: 2 * time.Minute,
		setup: []string{"time set 11800", "give Builder minecraft:stone_pickaxe", "give Builder minecraft:stone_axe",
			"give Builder minecraft:stone_sword", "give Builder minecraft:stone_shovel", "give Builder minecraft:oak_log 16",
			"give Builder minecraft:dirt 8"},
		steps: []string{"no night out under the sky: night coming, out under the sky"}, clock: "time set noon"},
	// the night's mine: dusk, the day's tools and wood given — no resting:
	// the shelter, then its stairs down (east from the spawn, a block down a
	// block on) through the stone, where coal and iron lie beside the steps
	// at feet and head height: taken, not walked past
	{name: "a night's mine", target: 3 * time.Minute, last: 5 * time.Minute,
		setup: append([]string{"time set 12200"}, append(flatOres(),
			"give Builder minecraft:stone_pickaxe", "give Builder minecraft:stone_axe", "give Builder minecraft:stone_sword",
			"give Builder minecraft:stone_shovel", "give Builder minecraft:oak_log 16", "give Builder minecraft:dirt 8")...),
		// the iron smelted in the night: the furnace loaded in the stairs, a
		// timer, the stairs on, the ingots collected when it is up
		goal: map[string]int{"minecraft:coal": 1, "minecraft:iron_ingot": 3}, clock: "time set noon"},
}

// flatOres are the night's mine's ores: beside the stairs it digs east from
// the spawn (at x, its feet are at the ground less x), on both sides, at feet
// and head height — coal 6 and 7 on, iron 10 and 11 on.
func flatOres() []string {
	var out []string
	for _, o := range []struct {
		x   int
		ore string
	}{{6, "coal_ore"}, {7, "coal_ore"}, {10, "iron_ore"}, {11, "iron_ore"}} {
		for _, z := range []int{-1, 1} {
			out = append(out, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:%s", o.x, flatGround-o.x, z, o.x, flatGround-o.x+1, z, o.ore))
		}
	}
	return out
}

// flatTree builds a tree the same every time (the game's own oaks grow 4 to
// 6 logs, at random: one with 4 failed "logs: 5"): a trunk of 6 logs on the
// ground at x, z, a crown of leaves that do not decay round its top three.
func flatTree(wood string, x, z int) string {
	g := flatGround
	return fmt.Sprintf("fill %d %d %d %d %d %d minecraft:%s_leaves[persistent=true] replace minecraft:air\n"+
		"fill %d %d %d %d %d %d minecraft:%s_log", x-2, g+3, z-2, x+2, g+6, z+2, wood, x, g, z, x, g+5, z, wood)
}

// flatItems are what a stage's report counts in the bot's inventory.
var flatItems = []string{"minecraft:coal", "minecraft:raw_iron", "minecraft:iron_ingot", "minecraft:furnace", "minecraft:oak_log", "minecraft:birch_log", "minecraft:crafting_table",
	"minecraft:wooden_pickaxe", "minecraft:cobblestone", "minecraft:stone_pickaxe", "minecraft:stone_axe",
	"minecraft:stone_sword", "minecraft:stone_shovel"}

var foundRe = regexp.MustCompile(`Found (\d+) matching item`)

var exploreRe = regexp.MustCompile(`explore: chunk (\[[-0-9 ]+\]) not yet seen \(ring (\d+)\)`)

// The bricks: each a single skill of the body on its own (the bot's -do), no
// brain, in the smallest world that shows it — the bottom tier. Numbered from 8.
var flatBricks = []flatStage{
	{name: "brick: fell a tree", do: "gather:minecraft:oak_log:6", target: 30 * time.Second, last: 90 * time.Second,
		setup: []string{flatTree("oak", 6, 2)}, goal: map[string]int{"logs": 6}},
	{name: "brick: craft planks and a pickaxe", do: "craft:minecraft:wooden_pickaxe", target: 15 * time.Second, last: 60 * time.Second,
		setup: []string{"give Builder minecraft:oak_log 3"}, goal: map[string]int{"minecraft:wooden_pickaxe": 1, "minecraft:crafting_table": 1}}, // the table taken up again, carried on
	// (the cobblestone it gives counted, as the goal: "stone" was the skill's
	// count of stone items, never got — the skill went on to its limit)
	{name: "brick: mine stone", do: "gather:minecraft:cobblestone", target: 20 * time.Second, last: 90 * time.Second,
		setup: []string{"fill -8 " + fy(-3) + " -3 -4 " + fy(-1) + " 1 minecraft:stone", "give Builder minecraft:wooden_pickaxe"},
		goal:  map[string]int{"minecraft:cobblestone": 1}},
	{name: "brick: stairs down", do: "downstairs:8", target: 30 * time.Second, last: 90 * time.Second, // three stone a step
		setup: []string{"give Builder minecraft:stone_pickaxe"}}, // the night's stairs are dug with a stone one
	{name: "brick: a shelter", do: "shelter", target: 15 * time.Second, last: 90 * time.Second,
		setup: []string{"give Builder minecraft:stone_pickaxe", "give Builder minecraft:dirt 8"}},
	{name: "brick: smelt iron", do: "smelt:minecraft:raw_iron", target: 50 * time.Second, last: 2 * time.Minute,
		setup: []string{"give Builder minecraft:stone_pickaxe", "give Builder minecraft:cobblestone 8", "give Builder minecraft:raw_iron 3",
			"give Builder minecraft:coal 2", "give Builder minecraft:oak_log 2"},
		goal: map[string]int{"minecraft:iron_ingot": 3}},
	// the construction plans (design §5e): the room dug into a hill (a hill
	// of stone east of the spawn), the hut built on open ground
	{name: "brick: a room in a hill", do: "build:room", target: 90 * time.Second, last: 3 * time.Minute,
		setup: []string{"fill 3 " + fy(0) + " -6 14 " + fy(6) + " 6 minecraft:stone", "give Builder minecraft:stone_pickaxe",
			"give Builder minecraft:oak_log 8", "give Builder minecraft:coal 2"}},
	{name: "brick: a hut", do: "build:hut", target: 90 * time.Second, last: 3 * time.Minute,
		setup: []string{"give Builder minecraft:stone_pickaxe", "give Builder minecraft:cobblestone 64",
			"give Builder minecraft:oak_log 8", "give Builder minecraft:coal 2"}},
	// stone as a person finds it: none in sight — down through the earth
	// (two dirt, then stone); a hill of stone 36 off — walked to
	{name: "brick: stone under the earth", do: "gather:minecraft:cobblestone:3", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"give Builder minecraft:wooden_pickaxe"}, goal: map[string]int{"minecraft:cobblestone": 3}},
	{name: "brick: stone on a hill", do: "gather:minecraft:cobblestone:3", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill 32 " + fy(0) + " -4 40 " + fy(5) + " 4 minecraft:stone", "give Builder minecraft:wooden_pickaxe"},
		goal:  map[string]int{"minecraft:cobblestone": 3}},
	// the field: a pool, earth round it, a hoe and seeds given
	{name: "brick: sow a field", do: "sow:4", target: 30 * time.Second, last: 90 * time.Second,
		setup: []string{"fill 5 " + fy(-1) + " -1 6 " + fy(-1) + " 0 minecraft:water", "give Builder minecraft:wooden_hoe", "give Builder minecraft:wheat_seeds 6"},
		world: map[string]int{"minecraft:wheat": 4, "minecraft:farmland": 4}},
	// a stepped hill of stone (one block a step, twenty high, its top above
	// the low ground's 80): the bot put on top, walks down
	{name: "brick: down from the hills", do: "golower", target: 30 * time.Second, last: 90 * time.Second,
		setup: flatHill(), start: [3]int{0, 20, 0}},
	// the farm (design §5e, the old robot's field): a bucket filled at a
	// pond; seeds from the grass; the field plan built — its pool, channels,
	// rows — with one bucket of water given and a pond 14 off for the second;
	// a ripe field harvested
	{name: "brick: fill a bucket", do: "fillbucket", target: 20 * time.Second, last: 60 * time.Second,
		setup: []string{"fill 6 " + fy(-1) + " -1 8 " + fy(-1) + " 1 minecraft:water", "give Builder minecraft:bucket"},
		goal:  map[string]int{"minecraft:water_bucket": 1}},
	{name: "brick: seeds from the grass", do: "gather:minecraft:wheat_seeds:4", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill -6 " + fy(0) + " -6 6 " + fy(0) + " 6 minecraft:short_grass"},
		goal:  map[string]int{"minecraft:wheat_seeds": 4}},
	{name: "brick: build the field", do: "field", target: 2 * time.Minute, last: 4 * time.Minute,
		setup: []string{"fill -16 " + fy(-1) + " -12 -14 " + fy(-1) + " -10 minecraft:water", "give Builder minecraft:water_bucket",
			"give Builder minecraft:stone_hoe", "give Builder minecraft:stone_shovel", "give Builder minecraft:wheat_seeds 24",
			"give Builder minecraft:dirt 16"},
		world: map[string]int{"minecraft:farmland": 16, "minecraft:wheat": 16, "minecraft:water": 9 + 14}},
	{name: "brick: a harvest", do: "harvest", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill 1 " + fy(-1) + " -1 8 " + fy(-1) + " -1 minecraft:water", "fill 1 " + fy(-1) + " 0 8 " + fy(-1) + " 1 minecraft:farmland[moisture=7]",
			"fill 1 " + fy(0) + " 0 8 " + fy(0) + " 1 minecraft:wheat[age=7]", "give Builder minecraft:stone_hoe",
			"damage Builder 1 minecraft:sweet_berry_bush"}, // a hit it tells the cause of
		goal: map[string]int{"minecraft:wheat": 12}},
	// the night's mine at its level: put in a room in the stone two over 16,
	// the stairs down to it, then lanes 16 long, 3 apart — the iron beside the
	// first taken
	{name: "brick: a mine at its level", do: "mine:40", target: 90 * time.Second, last: 3 * time.Minute,
		setup: []string{"fill -1 18 -1 1 19 1 minecraft:air", "fill 8 16 1 8 17 1 minecraft:iron_ore", "fill 12 16 -1 12 16 -1 minecraft:iron_ore",
			"give Builder minecraft:stone_pickaxe", "give Builder minecraft:coal 2", "give Builder minecraft:oak_log 2"},
		start: [3]int{0, 18 - flatGround, 0}, goal: map[string]int{"minecraft:raw_iron": 3},
		world: map[string]int{"minecraft:torch|minecraft:wall_torch@16": 2}}, // its lanes lit
	// the stairs into a cave: a pocket of air beside the stairs' way — the
	// openings closed with blocks, never left for a mob to come in by
	{name: "brick: stairs into a cave", do: "downstairs:8", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill 4 57 1 7 60 3 minecraft:air", "give Builder minecraft:stone_pickaxe", "give Builder minecraft:cobblestone 32"},
		world: map[string]int{"minecraft:cobblestone@58": 4}},
	// sand over the stairs' way: turned from, not dug under (it fell on its
	// head: suffocated in twelve seconds)
	{name: "brick: stairs under sand", do: "downstairs:8", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill 2 60 -3 8 67 3 minecraft:sand", "give Builder minecraft:stone_pickaxe", "give Builder minecraft:stone_shovel"}},
	// water beside the stairs' way (an underground lake): closed with blocks
	// before the step is dug — no water flows in
	{name: "brick: stairs by water", do: "downstairs:8", target: 40 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill 3 56 1 8 61 3 minecraft:water", "give Builder minecraft:stone_pickaxe", "give Builder minecraft:cobblestone 32"},
		world: map[string]int{"minecraft:cobblestone@58": 4}},
	// shut in under gravel: a pocket at 30 in the stone, gravel over its head
	// and over a way beside — climbed out by a way with nothing falling over it
	{name: "brick: out from under gravel", do: "climb:30", target: 90 * time.Second, last: 3 * time.Minute,
		setup: []string{"fill 0 30 0 0 31 0 minecraft:air", "fill 0 32 0 0 32 0 minecraft:gravel", "fill 1 32 0 1 33 0 minecraft:gravel",
			"give Builder minecraft:stone_pickaxe", "give Builder minecraft:stone_shovel", "give Builder minecraft:cobblestone 32"},
		start: [3]int{0, 30 - flatGround, 0}},
	// shut in its shelter, then the harvest: out by its door first (the walk
	// to the field went for its own closing blocks, stuck before them)
	{name: "brick: from the shelter to the harvest", do: "shelter+harvest", target: 50 * time.Second, last: 2 * time.Minute,
		setup: []string{"fill 4 " + fy(-1) + " -1 11 " + fy(-1) + " -1 minecraft:water", "fill 4 " + fy(-1) + " 0 11 " + fy(-1) + " 1 minecraft:farmland[moisture=7]",
			"fill 4 " + fy(0) + " 0 11 " + fy(0) + " 1 minecraft:wheat[age=7]", "give Builder minecraft:stone_pickaxe", "give Builder minecraft:dirt 8"},
		goal: map[string]int{"minecraft:wheat": 12}},
	// a lake under the stairs' way, every way round: the stairs end over it
	// and the mine goes on at that height, its floor put beside its own (at
	// y 35 the stairs once turned and turned over water for eight minutes)
	{name: "brick: stairs over a lake", do: "mine:24", target: 90 * time.Second, last: 3 * time.Minute,
		setup: []string{"fill -6 50 -6 14 55 6 minecraft:water", "give Builder minecraft:stone_pickaxe", "give Builder minecraft:cobblestone 64",
			"give Builder minecraft:coal 2", "give Builder minecraft:oak_log 2"},
		world: map[string]int{"minecraft:torch|minecraft:wall_torch@57": 1}}, // its tunnel at 57, lit
	// the plan, not a brick: day 1's tools, wood and stone given, a bucket of
	// water, grass about and a pond — its morning's field: a hoe, seeds, the
	// field built and sown (the shelter first: day 1's plan before it)
	{name: "plan: a morning's field", target: 4 * time.Minute, last: 7 * time.Minute,
		setup: []string{"fill -12 " + fy(0) + " -12 12 " + fy(0) + " -8 minecraft:short_grass", "fill 14 " + fy(-1) + " -14 16 " + fy(-1) + " -12 minecraft:water",
			"give Builder minecraft:stone_pickaxe", "give Builder minecraft:stone_axe", "give Builder minecraft:stone_sword",
			"give Builder minecraft:stone_shovel", "give Builder minecraft:oak_log 20", "give Builder minecraft:cobblestone 64",
			"give Builder minecraft:dirt 16", "give Builder minecraft:water_bucket"},
		steps: []string{"a hoe", "seeds: 4", "the field by home: built and sown"},
		world: map[string]int{"minecraft:farmland": 4, "minecraft:wheat": 4, "minecraft:water": 9 + 14}}, // as many sown as seeds got: four
	// out of a pond onto its bank, a block higher than the water (still
	// water a block deep at 62, its surface 62.89, the bank's top at 64):
	// the game's own answer, a cow in the pond tempted by the wheat the bot
	// holds on the bank — the server moves it by the game's physics (a
	// player's moves are the client's: the server takes them as sent). Its
	// own way stops short of the bank (its path ends in the water, 0.4 off
	// it), so a command block sets its speed toward the bank every tick
	// (0.1, as the bot swam; its rise left to the game): its way in the
	// bot's ticks (ents) — out on the bank, or bobbing against it as the bot
	// did (63.91 at most, the stuck walks of the days)
	{name: "brick: a cow out of the water onto a bank one up", do: "tempt:minecraft:wheat:30", target: 30 * time.Second, last: time.Minute,
		setup: []string{"fill -3 " + fy(-2) + " -2 0 " + fy(-2) + " 2 minecraft:water", "fill -3 " + fy(-1) + " -2 0 " + fy(-1) + " 2 minecraft:air",
			"summon minecraft:cow -1.5 " + fy(-2) + " 0.5 {Tags:[\"waterexit\"]}", "give Builder minecraft:wheat",
			"setblock 8 " + fy(-3) + " 8 minecraft:repeating_command_block{auto:1b,Command:\"execute as @e[type=minecraft:cow,tag=waterexit] run data modify entity @s Motion[0] set value 0.1d\"}"},
		start: [3]int{5, 0, 0}},
	// its control: the bank's top level with the water's block (63) — the
	// cow comes out (the wheat tempts it)
	{name: "brick: a cow out of the water onto a level bank", do: "tempt:minecraft:wheat:30", target: 30 * time.Second, last: time.Minute,
		setup: []string{"fill -3 " + fy(-2) + " -2 0 " + fy(-2) + " 2 minecraft:water", "fill -3 " + fy(-1) + " -2 8 " + fy(-1) + " 2 minecraft:air",
			"summon minecraft:cow -1.5 " + fy(-2) + " 0.5", "give Builder minecraft:wheat"},
		start: [3]int{5, -1, 0}},
	// the bot's control: out of the same pond onto the level bank, a single
	// walk, nothing built or dug
	{name: "brick: out of the water onto a level bank", do: "walkto:3,1,0", target: 10 * time.Second, last: time.Minute,
		setup: []string{"fill -3 " + fy(-2) + " -2 0 " + fy(-2) + " 2 minecraft:water", "fill -3 " + fy(-1) + " -2 4 " + fy(-1) + " 2 minecraft:air"},
		start: [3]int{-1, -2, 0}},
}

// flatGroups are the small groups of stages a change is tested with (the
// whole botflat after a set of changes): their stage numbers.
var flatGroups = map[string][]int{
	"botflatwood":  {2, 3, 4, 5},
	"botflatnight": {6, 7, 24, 25, 26, 27, 28, 30},         // the night, its mine at its level, its stairs closed to caves and water, not under sand, out from under gravel, over a lake
	"botbricks":    {8, 9, 10, 11, 12, 13, 16, 17, 18, 19}, // 14, 15: botbuild
	"botbuild":     {14, 15},                               // the construction plans
	"botfarm":      {18, 20, 21, 22, 23, 29, 31},           // the field: sow, a bucket, seeds, the plan built, a harvest; the plan's morning field
	"botwaterexit": {32, 33, 34},                           // out of a pond onto a bank one up: a cow (the game's answer), its control, the bot's control
}

// botprobe runs every brick (one skill each, 8 to 30: not the plan's
// stages) probeRounds times, mobs off on the flat world: the controlled
// measurements of the body's skills, their job lines in the kept runs (the
// bot's events), for the outcome tables a simulator is made of.
const probeRounds = 5

func probeBricks() []int {
	var out []int
	for n := 8; n <= 30; n++ {
		out = append(out, n)
	}
	return out
}

// probeTraceBricks are the bricks the small game's conformance tests are
// held to first (fell a tree, craft planks and a pickaxe, mine stone, out
// from under gravel): botprobetrace runs them alone, probeRounds times, after
// a change of the body's traces.
var probeTraceBricks = []int{8, 9, 10, 28}

// flatWave is how many stages run side by side at most: more bots at once
// slowed the server's ticks (a probe's two dozen bricks go in waves).
const flatWave = 10

// scenarioBotFlat runs the stages picked (their numbers, from 1), all when
// none are.
func scenarioBotFlat(o Options, pick ...int) error {
	return scenarioBotFlatRounds(o, "botflat", 1, pick...)
}

// scenarioBotFlatRounds runs the stages picked rounds times on one server
// (a round's stages named <scenario>-<n>-r<round> when there are more).
func scenarioBotFlatRounds(o Options, scenario string, rounds int, pick ...int) error {
	flatStages := append(append([]flatStage{}, flatStages...), flatBricks...)
	const name = "Builder"
	dir := filepath.Join(o.WorkDir, "server-"+scenario)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	bin, err := buildBot(o, scenario)
	if err != nil {
		return err
	}
	srv := &smoke.Server{Version: o.Version, JarPath: o.JarPath, WorkDir: dir,
		Port: o.Port, Runtime: o.Runtime, Log: o.Log, Generator: flatLayers}
	if err := servers.start(srv); err != nil {
		return err
	}
	defer servers.stop(srv)
	if err := srv.WaitReady(6 * time.Minute); err != nil {
		return err
	}
	rcon, err := dialRCON(srv.RCONAddr(), srv.RCONPassword)
	if err != nil {
		return fmt.Errorf("rcon: %w", err)
	}
	defer rcon.close()
	// stable: no clock, no weather, no monsters, no hunger's harm
	for _, c := range []string{"difficulty peaceful", "time set noon", "gamerule advance_time false",
		"weather clear 1000000", "gamerule spawn_monsters false",
		"setworldspawn 0 " + fy(0) + " 0", "forceload add -32 -32 32 32", "scoreboard objectives add deaths deathCount"} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	// no mob spawns of itself (the game rule's name changed in 1.21.11:
	// the new one first, else the old)
	if resp, err := rcon.command("gamerule spawn_mobs false"); err != nil || rconFailed(resp) {
		if resp, err := rcon.command("gamerule doMobSpawning false"); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon: no mob spawning: %q %v", resp, err)
		}
	}
	var failed, card []string
	for round := 1; round <= rounds; round++ {
		f, c := flatRound(o, rcon, srv, bin, scenario, name, round, rounds, flatStages, pick)
		failed, card = append(failed, f...), append(card, c...)
	}
	o.Log("%s: fitness — %s", scenario, strings.Join(card, " | "))
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d stage runs: %s", len(failed), len(card), strings.Join(failed, "; "))
	}
	return nil
}

type flatResult struct {
	report string
	err    error
}

// flatRound runs the stages picked once. The stages side by side: each
// without a clock of its own in an area of its own (a thousand blocks east of
// the last, its own bot), in waves of flatWave, each wave's areas made before
// its bots join (no fill while a bot works); those that set the clock (shared
// by the world) one after another after them, at the spawn. It answers the
// failures and the scorecards.
func flatRound(o Options, rcon *rconClient, srv *smoke.Server, bin, scenario, name string, round, rounds int, flatStages []flatStage, pick []int) (failed, card []string) {
	var par, ser []int
	for i, st := range flatStages {
		if len(pick) > 0 && !slices.Contains(pick, i+1) {
			continue
		}
		if st.clock == "" && !strings.HasPrefix(strings.Join(st.setup, " "), "time set") {
			par = append(par, i)
		} else {
			ser = append(ser, i)
		}
	}
	stageOf := func(i int) (string, flatStage) {
		st := flatStages[i]
		if rounds == 1 {
			return fmt.Sprintf("%s-%d", scenario, i+1), st
		}
		st.round = round
		return fmt.Sprintf("%s-%d-r%d", scenario, i+1, round), st
	}
	results := make([]flatResult, len(flatStages))
	for w := 0; w < len(par); w += flatWave {
		wave := par[w:min(w+flatWave, len(par))]
		gives := make([][]string, len(flatStages))
		for k, i := range wave {
			ox := (k + 1) * flatApart
			who := fmt.Sprintf("%s%d", name, k+2)
			if resp, err := rcon.command(fmt.Sprintf("forceload add %d -32 %d 32", ox-32, ox+32)); err != nil || rconFailed(resp) {
				results[i] = flatResult{"", fmt.Errorf("rcon forceload: %q %v", resp, err)}
				continue
			}
			_, st := stageOf(i)
			g, err := flatStageSetup(rcon, who, ox, st)
			if err != nil {
				results[i] = flatResult{"", err}
				continue
			}
			gives[i] = g
		}
		var wg sync.WaitGroup
		for k, i := range wave {
			if results[i].err != nil {
				continue
			}
			ox := (k + 1) * flatApart
			who := fmt.Sprintf("%s%d", name, k+2)
			wg.Add(1)
			go func(i, ox int, who string) {
				defer wg.Done()
				stage, st := stageOf(i)
				r, err := flatStageRun(o, rcon, srv, bin, stage, who, ox, st, gives[i])
				results[i] = flatResult{r, err}
			}(i, ox, who)
			time.Sleep(3 * time.Second) // the logins one after another
		}
		wg.Wait()
	}
	for _, i := range ser {
		stage, st := stageOf(i)
		g, err := flatStageSetup(rcon, name, 0, st)
		if err != nil {
			results[i] = flatResult{"", err}
			continue
		}
		r, err := flatStageRun(o, rcon, srv, bin, stage, name, 0, st, g)
		results[i] = flatResult{r, err}
	}
	for _, i := range append(append([]int{}, par...), ser...) {
		stage, st := stageOf(i)
		res := results[i]
		o.Log("%s (%s): %s", stage, st.name, res.report)
		f, _, _ := strings.Cut(res.report, ";")
		label := st.name
		if rounds > 1 {
			label = fmt.Sprintf("%s (r%d)", st.name, round)
		}
		card = append(card, fmt.Sprintf("%s: %s", label, strings.TrimPrefix(f, "fitness ")))
		if res.err != nil {
			o.Log("%s (%s): FAIL: %v", stage, st.name, res.err)
			failed = append(failed, fmt.Sprintf("%s: %v", label, res.err))
		}
	}
	return failed, card
}

// flatApart is how far apart the side by side stages' areas lie (far beyond
// any bot's sight and exploring).
const flatApart = 1000

// flatAreaAt makes a stage's area again, round ox along x: air above the
// ground, the ground as the flat world's (grass on dirt, stone under it where
// the stages dug), no items or mobs lying in it.
func flatAreaAt(ox int) []string {
	out := append([]string{}, flatArea...) // shifted with the stage's own commands
	return append(out, fmt.Sprintf("kill @e[type=!minecraft:player,x=%d,y=%d,z=-24,dx=48,dy=60,dz=48]", ox-24, flatGround-40))
}

// flatShift moves a stage's command into its area (ox along x) and gives it
// to its bot: a fill's x, a give's or a clear's player.
func flatShift(c string, ox int, name string) string {
	f := strings.Fields(c)
	if len(f) == 0 {
		return c
	}
	switch f[0] {
	case "fill":
		if len(f) >= 8 && ox != 0 {
			for _, k := range []int{1, 4} {
				if v, err := strconv.Atoi(f[k]); err == nil {
					f[k] = strconv.Itoa(v + ox)
				}
			}
		}
	case "setblock":
		if len(f) >= 5 && ox != 0 {
			if v, err := strconv.Atoi(f[1]); err == nil {
				f[1] = strconv.Itoa(v + ox)
			}
		}
	case "summon": // summon <entity> x y z: its x
		if len(f) >= 5 && ox != 0 {
			if v, err := strconv.ParseFloat(f[2], 64); err == nil {
				f[2] = strconv.FormatFloat(v+float64(ox), 'f', -1, 64)
			}
		}
	case "give", "clear", "effect", "damage":
		for k := range f {
			if f[k] == "Builder" {
				f[k] = name
			}
		}
	}
	return strings.Join(f, " ")
}

// flatArea makes the area round the spawn again: air above the ground, the
// ground as the flat world's (grass on dirt), no items lying, nothing carried.
var flatArea = []string{
	"fill -24 " + fy(0) + " -24 24 " + fy(12) + " 24 minecraft:air", // a fill takes 32768 blocks at most
	"fill -24 " + fy(13) + " -24 24 " + fy(24) + " 24 minecraft:air",
	"fill -24 " + fy(-3) + " -24 24 " + fy(-2) + " 24 minecraft:dirt",
	"fill -24 " + fy(-1) + " -24 24 " + fy(-1) + " 24 minecraft:grass_block",
	// the stone under it made again where the stages dug (the night's stairs
	// go deep): three fills of twelve layers
	"fill -24 " + fy(-15) + " -24 24 " + fy(-4) + " 24 minecraft:stone",
	"fill -24 " + fy(-27) + " -24 24 " + fy(-16) + " 24 minecraft:stone",
	"fill -24 " + fy(-39) + " -24 24 " + fy(-28) + " 24 minecraft:stone",
}

// flatStageSetup makes a stage's area and world (its fills, trees, ores) and
// answers its gives, given once its bot is in. The side by side stages are
// all set up before the first bot joins: a fill stalls the server's ticks,
// and the bricks running beside one saw their digs and places unanswered
// (the server did not change the block, a stair step walked into stone).
func flatStageSetup(rcon *rconClient, name string, ox int, st flatStage) ([]string, error) {
	var cmds []string
	var gives []string // after the bot is in and its inventory cleared
	for _, c := range append(flatAreaAt(ox), st.setup...) {
		for _, c := range strings.Split(c, "\n") { // a tree is two commands
			c = flatShift(c, ox, name)
			if strings.HasPrefix(c, "give ") || strings.HasPrefix(c, "damage ") { // to its bot: once it is in
				gives = append(gives, c)
				continue
			}
			cmds = append(cmds, c)
		}
	}
	for _, c := range cmds {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) || strings.Contains(resp, "Failed") || strings.Contains(resp, "Too many") {
			return nil, fmt.Errorf("setup %q: %q %v", c, resp, err)
		}
	}
	return gives, nil
}

// flatStageRun runs a stage and notes its result in its kept run (ok or
// fail, as a botday run's), so a reader of the runs counts its passes.
func flatStageRun(o Options, rcon *rconClient, srv *smoke.Server, bin, stage, name string, ox int, st flatStage, gives []string) (string, error) {
	began := time.Now()
	report, err := flatStageRunOnce(o, rcon, srv, bin, stage, name, ox, st, gives)
	runResult(flatRunName(stage, st), began, err)
	return report, err
}

// flatRunName is a stage's kept run's name, its world in it (botflat-2-a-tree):
// a watcher knows there is nothing to find on the empty one.
func flatRunName(stage string, st flatStage) string {
	return stage + "-" + strings.NewReplacer(" ", "-", ",", "").Replace(st.name)
}

func flatStageRunOnce(o Options, rcon *rconClient, srv *smoke.Server, bin, stage, name string, ox int, st flatStage, gives []string) (string, error) {
	out := &syncBuffer{}
	args := []string{"-address", srv.Addr(), "-name", name, "-debug", // a small world: every step logged
		"-explore-rings", strconv.Itoa(flatRings)}
	if ox != 0 { // it begins in its own area, not at the spawn
		args = append(args, "-wait-at", fmt.Sprintf("%d,%d", ox+st.start[0], st.start[2]))
	}
	runName := flatRunName(stage, st)
	kept, err := newRun(o, runName, name, srv)
	if err != nil {
		o.Log("runs: %v (no events kept)", err)
	} else if kept != nil {
		args = append(args, "-events", kept.events())
		defer func() { kept.end(out.String()) }()
	}
	lf, err := os.Create(filepath.Join(o.WorkDir, stage+".log"))
	if err != nil {
		return "", err
	}
	defer lf.Close()
	if st.do != "" {
		args = append(args, "-do", st.do)
	}
	cmd := exec.Command(bin, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return "", err
	}
	proc := watchProc(cmd) // its end is seen when it comes, not as a stall minutes later
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := readBotLines(pr, func(line string) {
			fmt.Fprintln(lf, line)
			out.Write([]byte(line + "\n"))
			if _, own := botSays(line); own && !strings.Contains(line, "status at") || strings.Contains(line, "panic") {
				o.Log("%s: bot: %s", stage, line)
			}
		})
		if err != nil {
			o.Log("%s: the bot's output: %v", stage, err)
		}
	}()
	defer func() {
		// back at the spawn before it goes: the next stage's bot logs in
		// there (once it logged in 48 blocks off, set out exploring before
		// the stage's tp, and never saw the stage's tree)
		// and nothing carried: the next stage's bot looks first, before the
		// stage's clear (once it saw the last stage's pickaxe, counted its
		// logs and pickaxe done, then had none)
		_, _ = rcon.command("tp " + name + " " + strconv.Itoa(ox) + " " + fy(0) + " 0")
		_, _ = rcon.command("clear " + name)
		proc.stop(cmd)
		pw.Close()
		wg.Wait()
	}()
	if err := waitFor(out, 90*time.Second, "loaded"); err != nil {
		return "", fmt.Errorf("the bot did not join: %v\n%s", err, tail(out.String(), 20))
	}
	if err := waitBotPrefix(out, 30*time.Second); err != nil {
		return "", fmt.Errorf("%v\n%s", err, tail(out.String(), 20))
	}
	// the last stage's bot left it where it was: back at the spawn, nothing
	// carried, health and food full
	for _, c := range []string{"tp " + name + " " + strconv.Itoa(ox+st.start[0]) + " " + fy(st.start[1]) + " " + strconv.Itoa(st.start[2]), "clear " + name, "effect give " + name + " minecraft:instant_health 1 10",
		"effect give " + name + " minecraft:saturation 1 10", "scoreboard players set " + name + " deaths 0"} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return "", fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	for _, c := range gives {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return "", fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if st.clock != "" {
		defer func() { _, _ = rcon.command(st.clock) }()
	}
	o.Log("%s (%s): the bot in, %s to live", stage, st.name, st.last)
	start := time.Now()
	var lastPos string
	var prev [3]float64
	walked := 0.0
	stillSince, longestStill := time.Now(), time.Duration(0)
	met, metAfter := false, time.Duration(0)
	idle, idleBy := time.Duration(0), map[string]time.Duration{}
	wet := time.Duration(0)
	poll := 2 * time.Second
	if st.swim {
		poll = 400 * time.Millisecond // a canal is crossed in a second or two
	}
	// the watcher: the stage's time plan (step i of n due by the target's
	// (i+1)/n, and half as much again), and no stand-still: a stage behind its
	// plan, or with the bot still and silent half a minute, ends at once —
	// not at its limit
	behind := ""
	var worldHas, worldShort []string // the area's blocks counted at a skill's end
	lastSaid, saidAt := 0, time.Now()
	for time.Since(start) < st.last && !met && behind == "" {
		time.Sleep(poll)
		if st.do != "" && botHas(out.String(), "do "+st.do+": ") {
			time.Sleep(time.Second) // the last lines, the inventory settled
			if botHas(out.String(), "do "+st.do+": failed") {
				return "", fmt.Errorf("the skill %q failed\n%s", st.do, flatLastLines(out.String(), 10))
			}
			met, metAfter = true, time.Since(start)
			for it, n := range st.goal {
				if flatCount(rcon, name, it) < n {
					met = false
				}
			}
			for bl, n := range st.world {
				if got, said := flatBlocksSaid(rcon, ox, bl); got < n {
					met = false
					worldShort = append(worldShort, fmt.Sprintf("%s %d of %d (%q)", strings.TrimPrefix(bl, "minecraft:"), got, n, said))
				} else {
					worldHas = append(worldHas, fmt.Sprintf("%s %d", strings.TrimPrefix(bl, "minecraft:"), got))
				}
			}
			break
		} else if proc.ended() {
			time.Sleep(200 * time.Millisecond) // (its last lines are read from the pipe)
			return "", fmt.Errorf("%s", proc.said(stage, out.String()))
		}
		if resp, err := rcon.command("data get entity " + name + " Pos"); err == nil {
			if m := posRe.FindStringSubmatch(resp); m != nil {
				p := [3]float64{atof(m[1]), atof(m[2]), atof(m[3])}
				if lastPos != "" {
					d := dist3(p, prev)
					walked += d
					if d < 0.3 { // idle this poll: on what
						idle += poll
						idleBy[flatLastTask(out.String())] += poll
					}
				}
				prev = p
				pos := fmt.Sprintf("%.0f %.0f %.0f", p[0], p[1], p[2])
				if pos != lastPos {
					lastPos, stillSince = pos, time.Now()
				} else if s := time.Since(stillSince); s > longestStill {
					longestStill = s
				}
			}
		}
		if resp, err := rcon.command("execute as " + name + " at @s if block ~ ~ ~ minecraft:water"); err == nil && strings.Contains(resp, "passed") {
			wet += poll
		}
		if n := strings.Count(out.String(), " done"); n != lastSaid {
			lastSaid, saidAt = n, time.Now() // a step said done: progress
		}
		for i, step := range st.steps {
			due := st.target * time.Duration(i+1) / time.Duration(len(st.steps)) * 3 / 2
			if time.Since(start) > due && !botHas(out.String(), step+": done") {
				behind = fmt.Sprintf("behind its plan: %q not done by %s", step, due.Round(time.Second))
				break
			}
		}
		// (a single skill has its own time limit: a furnace's wait is no stall)
		if still := time.Since(stillSince); st.do == "" && still > 30*time.Second && time.Since(saidAt) > 30*time.Second {
			behind = fmt.Sprintf("stalled: still and no step done for %s", still.Round(time.Second))
		}
		if len(st.goal) == 0 && len(st.steps) > 0 {
			if _, missing, _ := flatSteps(out.String(), st.steps); len(missing) == 0 {
				met, metAfter = true, time.Since(start) // its steps done: the stage ends
			}
		} else if len(st.goal) == 0 && strings.Contains(out.String(), "explore: every chunk within") {
			met, metAfter = true, time.Since(start) // seen all it may: the stage ends early
		}
		if len(st.goal) > 0 && (st.do == "" || len(st.world) == 0) { // a skill with a world to show ends at its own end
			met = true
			for it, n := range st.goal {
				if flatCount(rcon, name, it) < n {
					met = false
				}
			}
			if met {
				metAfter = time.Since(start)
				if st.do != "" {
					// the goal is in its pack, but the skill's end is the
					// bot's to say: its last job's line and trace come with
					// it (stopped at the count, brick 10 kept no trace)
					waitSkillEnd(out, st.do, skillEndWait)
				}
			}
		}
	}
	if len(worldHas)+len(worldShort) == 0 { // its area's blocks counted at its end (a skill's: at its end, above)
		for bl, n := range st.world {
			if got, said := flatBlocksSaid(rcon, ox, bl); got < n {
				worldShort = append(worldShort, fmt.Sprintf("%s %d of %d (%q)", strings.TrimPrefix(bl, "minecraft:"), got, n, said))
			} else {
				worldHas = append(worldHas, fmt.Sprintf("%s %d", strings.TrimPrefix(bl, "minecraft:"), got))
			}
		}
	}
	var held []string
	for _, it := range flatItems {
		if n := flatCount(rcon, name, it); n > 0 {
			held = append(held, fmt.Sprintf("%s %d", strings.TrimPrefix(it, "minecraft:"), n))
		}
	}
	deaths := "?"
	if resp, err := rcon.command("scoreboard players get " + name + " deaths"); err == nil {
		if f := strings.Fields(resp); len(f) >= 3 && f[1] == "has" {
			deaths = f[2]
		}
	}
	took := time.Since(start)
	// the scorecard in the kept run: its goal's seconds (null not met), its
	// deaths, standing still, idle, its length — under the flat world's
	// conditions (no mobs, the stage's own world)
	var goalAt any
	if met {
		goalAt = int(metAfter.Seconds())
	}
	d, _ := strconv.Atoi(deaths)
	conditions := map[string]any{"world": "flat", "stage": st.name, "mobs": mobsOff, "difficulty": "peaceful"}
	if st.round > 0 {
		conditions["probe"] = st.round // botprobe's round: the same brick, again
	}
	kept.score(conditions,
		map[string]any{"goal": goalAt, "target": int(st.target.Seconds()), "deaths": d, "longestStill": int(longestStill.Seconds()),
			"idle": int(idle.Seconds()), "length": int(took.Seconds())})
	fitness := "no goal"
	steps, missing, repeated := flatSteps(out.String(), st.steps)
	switch {
	case st.do != "" && len(st.goal) == 0 && met && metAfter <= st.target:
		fitness = fmt.Sprintf("fit: the skill done in %s (target %s)", metAfter.Round(time.Second), st.target)
	case st.do != "" && len(st.goal) == 0 && met:
		fitness = fmt.Sprintf("slow: the skill done in %s (target %s, limit %s)", metAfter.Round(time.Second), st.target, st.last)
	case st.do != "" && len(st.goal) == 0:
		fitness = fmt.Sprintf("fail: the skill not done in %s", st.last)
	case len(st.goal) == 0 && len(st.steps) > 0 && met && metAfter <= st.target:
		fitness = fmt.Sprintf("fit: its steps in %s (target %s)", metAfter.Round(time.Second), st.target)
	case len(st.goal) == 0 && len(st.steps) > 0 && met:
		fitness = fmt.Sprintf("slow: its steps in %s (target %s, limit %s)", metAfter.Round(time.Second), st.target, st.last)
	case len(st.goal) == 0 && len(st.steps) > 0:
		fitness = fmt.Sprintf("fail: its steps not done in %s", st.last)
	case len(st.goal) == 0 && met && metAfter <= st.target:
		fitness = fmt.Sprintf("fit: all within %d rings seen in %s (target %s)", flatRings, metAfter.Round(time.Second), st.target)
	case len(st.goal) == 0 && met:
		fitness = fmt.Sprintf("slow: all within %d rings seen in %s (target %s)", flatRings, metAfter.Round(time.Second), st.target)
	case len(st.goal) == 0:
		fitness = fmt.Sprintf("fail: not all within %d rings seen in %s", flatRings, st.last)
	case met && metAfter <= st.target:
		fitness = fmt.Sprintf("fit: the goal in %s (target %s)", metAfter.Round(time.Second), st.target)
	case met:
		fitness = fmt.Sprintf("slow: the goal in %s (target %s, limit %s)", metAfter.Round(time.Second), st.target, st.last)
	default:
		fitness = fmt.Sprintf("fail: no goal in %s", st.last)
	}
	fitness += steps
	if len(worldHas)+len(worldShort) > 0 {
		fitness += " — in its area " + strings.Join(append(worldHas, worldShort...), ", ")
	}
	if st.swim {
		fitness += fmt.Sprintf(" — in water %s", wet.Round(time.Second))
	}
	report := fmt.Sprintf("fitness %s; idle %s (%.0f%%)%s; carries [%s]; deaths %s; walked %.0f blocks, ended %.0f from the spawn; longest standing still %s; %s",
		fitness, idle.Round(time.Second), 100*idle.Seconds()/max(took.Seconds(), 1), flatIdleBy(idleBy),
		strings.Join(held, ", "), deaths, walked, dist3(prev, [3]float64{float64(ox), flatGround, 0}),
		longestStill.Round(time.Second), flatPlanSummary(out.String()))
	switch {
	case deaths != "0":
		return report, fmt.Errorf("died")
	case st.swim && wet == 0:
		return report, fmt.Errorf("never in the water: walked round it")
	case behind != "":
		return report, fmt.Errorf("%s\n%s", behind, flatLastLines(out.String(), 20))
	case len(worldShort) > 0:
		return report, fmt.Errorf("in its area only %s\n%s", strings.Join(worldShort, ", "), flatLastLines(out.String(), 20))
	case len(missing) > 0:
		return report, fmt.Errorf("steps not done: %s\n%s", strings.Join(missing, ", "), flatLastLines(out.String(), 20))
	case len(repeated) > 0:
		return report, fmt.Errorf("steps done again: %s\n%s", strings.Join(repeated, ", "), flatLastLines(out.String(), 20))
	case len(st.goal) == 0 && len(st.steps) == 0 && !met:
		return report, fmt.Errorf("not all within %d rings seen in %s", flatRings, st.last)
	case len(st.goal) > 0 && !met:
		var want []string
		for it, n := range st.goal {
			want = append(want, fmt.Sprintf("%s %d", strings.TrimPrefix(it, "minecraft:"), n))
		}
		return report, fmt.Errorf("goal not met in %s: %s", st.last, strings.Join(want, ", "))
	case strings.Contains(out.String(), "panic"):
		return report, fmt.Errorf("the bot panicked")
	}
	return report, nil
}

// flatSteps checks the plan steps the stage wants: each said done ("<step>:
// done"), once. It answers the card's words, the steps missing and those
// done more than once.
func flatSteps(log string, want []string) (string, []string, []string) {
	if len(want) == 0 {
		return "", nil, nil
	}
	var missing, repeated []string
	for _, st := range want {
		switch n := botCount(log, st+": done"); {
		case n == 0:
			missing = append(missing, st)
		case n > 1:
			repeated = append(repeated, fmt.Sprintf("%s (%d times)", st, n))
		}
	}
	card := fmt.Sprintf(" — steps %d of %d done", len(want)-len(missing), len(want))
	if len(missing) > 0 {
		card += ", missing: " + strings.Join(missing, ", ")
	}
	if len(repeated) > 0 {
		card += ", done again: " + strings.Join(repeated, ", ")
	}
	return card, missing, repeated
}

// flatLastLines are the bot's last n lines worth reading (its plan and its
// body's steps: no status lines, no job starts) — why a stage failed, in its
// report.
func flatLastLines(log string, n int) string {
	var keep []string
	for _, l := range strings.Split(log, "\n") {
		said, own := botSays(l)
		if !own || strings.Contains(l, "status at") || strings.HasPrefix(said, "job: ") {
			continue
		}
		keep = append(keep, "    "+said)
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, "\n")
}

// flatLastTask is the task the bot said it took last ("" none yet).
func flatLastTask(log string) string {
	t, ok := botLast(log, "task: ")
	if !ok {
		return ""
	}
	if j := strings.IndexAny(t, "(\n"); j > 0 {
		t = t[:j]
	}
	return strings.TrimSpace(t)
}

// flatIdleBy is the idle time by task, the longest first: " — 20s crafting
// table, wooden pickaxe, 6s logs: 5".
func flatIdleBy(by map[string]time.Duration) string {
	type kv struct {
		task string
		d    time.Duration
	}
	var l []kv
	for t, d := range by {
		if t == "" {
			t = "(before a task)"
		}
		l = append(l, kv{t, d})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].d > l[j].d })
	var parts []string
	for _, e := range l {
		parts = append(parts, fmt.Sprintf("%s %s", e.d.Round(time.Second), e.task))
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, ", ")
}

// flatLogs are the logs a stage's "logs" goal counts, any tree's.
var flatLogs = []string{"minecraft:oak_log", "minecraft:birch_log"}

// flatBlocksSaid counts the blocks of a kind in a stage's area round ox, two
// layers under the ground to two over it: cloned, filtered, into the sky (the
// clone answers how many), then the copy cleared — and says the server's
// answer; "a|b" counts both kinds (a torch on the floor or on a wall).
func flatBlocksSaid(rcon *rconClient, ox int, bl string) (int, string) {
	at := ""
	if k, y, ok := strings.Cut(bl, "@"); ok {
		bl, at = k, "@"+y
	}
	if strings.Contains(bl, "|") {
		total, said := 0, ""
		for _, k := range strings.Split(bl, "|") {
			n, s := flatBlocksSaid(rcon, ox, k+at)
			total, said = total+n, said+s+"; "
		}
		return total, said
	}
	bl += at
	// "kind@y": round the height y instead (a mine's torches at 16)
	lo, hi := fy(-2), fy(1)
	if k, y, ok := strings.Cut(bl, "@"); ok {
		v, _ := strconv.Atoi(y)
		bl, lo, hi = k, strconv.Itoa(v-1), strconv.Itoa(v+2)
	}
	// strict: no block updates — wheat cloned with nothing under it would
	// break, its seeds falling into the area
	resp, err := rcon.command(fmt.Sprintf("clone %d %s -24 %d %s 24 %d %s -24 strict filtered %s force",
		ox-24, lo, ox+24, hi, ox-24, fy(60), bl))
	_, _ = rcon.command(fmt.Sprintf("fill %d %s -24 %d %s 24 minecraft:air strict", ox-24, fy(60), ox+24, fy(63)))
	if err != nil {
		return 0, err.Error()
	}
	if m := clonedRe.FindStringSubmatch(resp); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, resp
	}
	return 0, resp
}

var clonedRe = regexp.MustCompile(`(?i)cloned (\d+) block`)

// flatCount is how many of item the player carries ("clear … 0" counts and
// takes nothing); "logs": the logs of any tree in flatLogs.
func flatCount(rcon *rconClient, name, item string) int {
	if item == "logs" {
		n := 0
		for _, l := range flatLogs {
			n += flatCount(rcon, name, l)
		}
		return n
	}
	resp, err := rcon.command(fmt.Sprintf("clear %s %s 0", name, item))
	if err != nil {
		return 0
	}
	if m := foundRe.FindStringSubmatch(resp); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// flatPlanSummary is what the bot's plan lines say it did: the tasks it took
// in their order (each once), how many ended done, failed, stuck.
func flatPlanSummary(log string) string {
	var tasks []string
	seen := map[string]bool{}
	done, failed, stuck := 0, 0, 0
	targets, ring := map[string]bool{}, 0
	for _, line := range strings.Split(log, "\n") {
		l, own := botSays(line)
		if !own {
			continue
		}
		switch {
		case strings.HasPrefix(l, "task: "):
			t := strings.TrimPrefix(l, "task: ")
			if j := strings.Index(t, " ("); j > 0 {
				t = t[:j]
			}
			if !seen[t] {
				seen[t] = true
				tasks = append(tasks, t)
			}
		case strings.HasPrefix(l, "explore: chunk "): // "explore: chunk [x z] not yet seen (ring k), toward …"
			if m := exploreRe.FindStringSubmatch(l); m != nil {
				targets[m[1]] = true
				if k, _ := strconv.Atoi(m[2]); k > ring {
					ring = k
				}
			}
		case strings.HasPrefix(l, "stuck: "):
			stuck++
		case strings.HasSuffix(l, ": done"):
			done++
		case strings.HasSuffix(l, ": failed"):
			failed++
		}
	}
	ex := ""
	if len(targets) > 0 {
		ex = fmt.Sprintf("; explored %d chunks of the spiral, ring %d reached", len(targets), ring)
	}
	return fmt.Sprintf("tasks %s; %d done, %d failed, %d stuck%s", strings.Join(tasks, " > "), done, failed, stuck, ex)
}

func dist3(a, b [3]float64) float64 {
	d := 0.0
	for i := range a {
		d += (a[i] - b[i]) * (a[i] - b[i])
	}
	return math.Sqrt(d)
}

// skillEndWait is how long a stage whose goal is in the pack waits for its
// skill to say its end (the bot writes its last trace first, 20 ticks after
// the last job).
const skillEndWait = 10 * time.Second

// waitSkillEnd waits till the bot's output has the skill's end (its prefix and "do
// <do>: done" or "failed"), at most limit; it reports whether it came.
func waitSkillEnd(out interface{ String() string }, do string, limit time.Duration) bool {
	for t0 := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		if botHas(out.String(), "do "+do+": ") {
			return true
		}
		if time.Since(t0) >= limit {
			return false
		}
	}
}

// maxBotLine is the longest line of a bot's output kept whole in the logs.
const maxBotLine = 16 << 10

// readBotLines reads a bot's output line by line to its end, however long a
// line (one longer than maxBotLine cut, its length said): a bufio.Scanner
// stopped at a line over 64 KB, nothing read the pipe after it, and the bot,
// its writes blocked, stood still for the rest of its stage (botprobe's
// field, round 2: every event of the bot stopped at once). Should reading
// fail, the rest is drained all the same and the error answered.
func readBotLines(r io.Reader, line func(string)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		s, err := br.ReadString('\n')
		if len(s) > 0 {
			s = strings.TrimRight(s, "\r\n")
			if len(s) > maxBotLine {
				s = fmt.Sprintf("%s… (a line of %d bytes, cut)", s[:maxBotLine], len(s))
			}
			line(s)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			_, _ = io.Copy(io.Discard, r)
			return err
		}
	}
}
