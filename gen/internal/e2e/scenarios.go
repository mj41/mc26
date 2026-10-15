package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/mj41/mc26/gen/internal/build"
	"github.com/mj41/mc26/gen/internal/smoke"
)

// scenario is one thing the harness runs: its name (what --only takes), a line saying what it is, whether it runs
// only when named, and the run.
type scenario struct {
	name string
	what string // one line, plain English (mc26 e2e --list)
	long bool   // run only when named (--only)
	fn   func() error
}

// scenarioTable is every scenario, in the order they run: those on the flat world's server, then those after it
// stopped. bin and srv are read when a scenario runs (the examples built, the server started), not now: the table
// is built before them, for the checks of --only and for --list.
func scenarioTable(o Options, v *build.Info, bin *string, srv **smoke.Server) (scenarios, after []scenario) {
	// on the flat world's server
	scenarios = []scenario{
		{name: "mcping", what: "server list ping: the status reply of the vanilla server, read by the kit", long: false, fn: func() error { return scenarioPing(o, *bin, *srv, v) }},
		{name: "daze", what: "a bot joins, stands, looks and answers chat", long: false, fn: func() error { return scenarioDaze(o, *bin, *srv) }},
		{name: "twobots", what: "two bots on one server see and talk to each other", long: false, fn: func() error { return scenarioTwoBots(o, *bin, *srv) }},
		{name: "minimal", what: "the smallest bot joins the game", long: false, fn: func() error {
			return scenarioLogin(o, *bin, *srv, "minimal", "Minimal", 20*time.Second, "joined")
		}},
		{name: "autofish", what: "the fishing bot logs in and starts the game", long: false, fn: func() error {
			return scenarioLogin(o, *bin, *srv, "autofish", "Fisher", 25*time.Second, "Login success", "Game start")
		}},
		{name: "autofishbite", what: "the fishing bot reels on a real bite (a pond, a Lure III rod): the server gives the catch", long: true, fn: func() error { return scenarioAutofishBite(o, *bin, *srv) }},
		{name: "pressure", what: "three bots log in and reach game start", long: false, fn: func() error { return scenarioPressure(o, *bin, *srv) }},
		{name: "robot", what: "the kit's robot: movement, digging, crafting, fights, dimensions, each judged by the server", long: false, fn: func() error { return scenarioRobot(o, *bin, *srv) }},
	}
	// after it stopped: the world it wrote, then servers of their own
	after = []scenario{
		{name: "mcadump", what: "the saved world's chunks dumped and read back", long: false, fn: func() error { return scenarioMcadump(o, *bin, *srv) }},
		{name: "savechunk", what: "a chunk written by the library and loaded by the server", long: false, fn: func() error { return scenarioSaveChunk(o, *srv) }},
		{name: "saveschema", what: "the save format's schema checked against the server's world", long: false, fn: func() error { return scenarioSaveSchema(o, *srv) }},
		{name: "fixtures", what: "the save tests' small world cut from the server's world", long: false, fn: func() error { return scenarioFixtures(o, *srv) }},
		{name: "survival", what: "the robot on a survival world: it plays on its own for a while", long: false, fn: func() error { return scenarioSurvival(o, *bin) }},
		{name: "days", what: "the robot plays its first two days", long: true, fn: func() error { return scenarioDays(o, *bin) }},
		{name: "week", what: "the robot plays a week", long: true, fn: func() error { return scenarioWeek(o, *bin) }},
		{name: "livedays", what: "two days of the robot living by itself, no orders", long: true, fn: func() error { return scenarioLiveDays(o, *bin) }},
		{name: "liveweek", what: "a week of the robot living by itself, no orders", long: true, fn: func() error { return scenarioLiveWeek(o, *bin) }},
		{name: "village", what: "the robot in a village: trades and houses", long: true, fn: func() error { return scenarioVillage(o, *bin) }},
		{name: "field", what: "the robot builds and tends a field", long: true, fn: func() error { return scenarioField(o, *bin) }},
		{name: "fieldwild", what: "a field in the wild: sowing and harvest from nothing", long: true, fn: func() error { return scenarioFieldWild(o, *bin) }},
		{name: "farmstart", what: "the robot starts a farm from nothing", long: true, fn: func() error { return scenarioFarmStart(o, *bin) }},
		{name: "fish", what: "the robot fishes", long: true, fn: func() error { return scenarioFish(o, *bin) }},
		{name: "botdayx", what: "one day from nothing on a new random world, natural mobs", long: true, fn: func() error { return scenarioBotDay(o, 0, 1, mobsNatural) }},
		{name: "botdaysx", what: "two days from nothing on a new random world, natural mobs", long: true, fn: func() error { return scenarioBotDay(o, 0, 2, mobsNatural) }},
		{name: "botfarmfast", what: botFarmWhat["botfarmfast"], long: true, fn: func() error { return scenarioBotFarm(o, "botfarmfast") }},
		{name: "botfarmnobucket", what: botFarmWhat["botfarmnobucket"], long: true, fn: func() error { return scenarioBotFarm(o, "botfarmnobucket") }},
		{name: "botfarmempty", what: botFarmWhat["botfarmempty"], long: true, fn: func() error { return scenarioBotFarm(o, "botfarmempty") }},
		{name: "botfarmdays", what: botFarmWhat["botfarmdays"], long: true, fn: func() error { return scenarioBotFarm(o, "botfarmdays") }},
		{name: "botflat", what: "every stage and brick of the flat world, in turn", long: true, fn: func() error { return scenarioBotFlat(o) }},
		{name: "botflat1", what: "flat world stage: " + flatStages[0].name, long: true, fn: func() error { return scenarioBotFlat(o, 1) }},
		{name: "botflat2", what: "flat world stage: " + flatStages[1].name, long: true, fn: func() error { return scenarioBotFlat(o, 2) }},
		{name: "botflat3", what: "flat world stage: " + flatStages[2].name, long: true, fn: func() error { return scenarioBotFlat(o, 3) }},
		{name: "botflat4", what: "flat world stage: " + flatStages[3].name, long: true, fn: func() error { return scenarioBotFlat(o, 4) }},
		{name: "botflat5", what: "flat world stage: " + flatStages[4].name, long: true, fn: func() error { return scenarioBotFlat(o, 5) }},
		{name: "botflat6", what: "flat world stage: " + flatStages[5].name, long: true, fn: func() error { return scenarioBotFlat(o, 6) }},
		{name: "botflat7", what: "flat world stage: " + flatStages[6].name, long: true, fn: func() error { return scenarioBotFlat(o, 7) }},
		{name: "botflatwood", what: "the flat world: the stages that need wood", long: true, fn: func() error { return scenarioBotFlat(o, flatGroups["botflatwood"]...) }},
		{name: "botflatnight", what: "the flat world: the night, its mine, stairs and the bricks that go with them", long: true, fn: func() error { return scenarioBotFlat(o, flatGroups["botflatnight"]...) }},
		{name: "botbricks", what: "the single-skill bricks of the flat world", long: true, fn: func() error { return scenarioBotFlat(o, flatGroups["botbricks"]...) }},
		{name: "botbuild", what: "the construction plans: a room in a hill, a hut", long: true, fn: func() error { return scenarioBotFlat(o, flatGroups["botbuild"]...) }},
		{name: "botfarm", what: "the field bricks: sow, bucket, seeds, plan built, harvest", long: true, fn: func() error { return scenarioBotFlat(o, flatGroups["botfarm"]...) }},
		{name: "botwaterexit", what: "out of a pond onto a bank: a cow, its control, the bot", long: true, fn: func() error { return scenarioBotFlat(o, flatGroups["botwaterexit"]...) }},
		{name: "botprobe", what: "every brick, five rounds, for measurement", long: true, fn: func() error { return scenarioBotFlatRounds(o, "botprobe", probeRounds, probeBricks()...) }},
		{name: "botprobetrace", what: "the four bricks the small game is held to, five rounds", long: true, fn: func() error { return scenarioBotFlatRounds(o, "botprobetrace", probeRounds, probeTraceBricks...) }},
	}
	// the botday worlds of a fixed seed under each condition of the mobs
	// (botday1, botday1off, botday1script; botdays1… two days): runs of the
	// same seed and condition are what fitness compares
	for n := 1; n <= len(botDaySeeds); n++ {
		for _, days := range []int{1, 2} {
			for _, mobs := range []string{mobsNatural, mobsOff, mobsScript} {
				name := fmt.Sprintf("botday%d", n)
				if days == 2 {
					name = fmt.Sprintf("botdays%d", n)
				}
				if mobs != mobsNatural {
					name += mobs
				}
				n, days, mobs := n, days, mobs
				after = append(after, scenario{name: name, what: botDayWhat(n, days, mobs), long: true, fn: func() error { return scenarioBotDay(o, n, days, mobs) }})
			}
		}
	}
	// each brick on its own (botbrick8 … botbrick23): one skill, alone on the
	// server, to look at a failing one
	for i := len(flatStages) + 1; i <= len(flatStages)+len(flatBricks); i++ {
		n := i
		after = append(after, scenario{name: fmt.Sprintf("botbrick%d", n), what: "one brick alone on the server: " + strings.TrimPrefix(flatBricks[n-len(flatStages)-1].name, "brick: "), long: true, fn: func() error { return scenarioBotFlat(o, n) }})
	}
	return scenarios, after
}

// scenarioNames are the names of the scenarios, those on the flat world's server first.
func scenarioNames(scenarios, after []scenario) []string {
	var names []string
	for _, sc := range append(append([]scenario(nil), scenarios...), after...) {
		names = append(names, sc.name)
	}
	return names
}

// botDayWhat says what a generated botday scenario is: its seed, how many days, the mobs.
func botDayWhat(n, days int, mobs string) string {
	d := "one day"
	if days == 2 {
		d = "two days"
	}
	m := map[string]string{mobsNatural: "natural mobs", mobsOff: "mobs off", mobsScript: "the scripted mobs"}[mobs]
	return fmt.Sprintf("%s from nothing on seed %d (%s), %s", d, n, botDaySeeds[n-1], m)
}

// listEntry is a line of --list.
type listEntry struct {
	Name string `json:"name"`
	What string `json:"what"`
	Long bool   `json:"long"`
}

// scenarioRE is what a scenario's name must match: the bench takes no other (its scenarioRE).
var scenarioRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)

// List writes the scenarios to w, one JSON object a line in the harness's order: no server, no build — what a
// bench asks of a delivery to know which scenarios it has.
func List(w io.Writer) error {
	scenarios, after := scenarioTable(Options{}, nil, new(string), new(*smoke.Server))
	for _, sc := range append(scenarios, after...) {
		if !scenarioRE.MatchString(sc.name) {
			return fmt.Errorf("e2e: scenario %q is not a name a bench takes", sc.name)
		}
		if sc.what == "" || len(sc.what) > 120 || strings.ContainsAny(sc.what, "\n") {
			return fmt.Errorf("e2e: scenario %q has no one-line description", sc.name)
		}
		line, err := json.Marshal(listEntry{sc.name, sc.what, sc.long})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}
