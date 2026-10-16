package e2e

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mj41/mc26/gen/internal/kit"
	"github.com/mj41/mc26/gen/internal/smoke"
)

// The botday scenarios: an outside bot — a Go package built in the kit's
// workspace, not one of the kit's examples — lives its first day from
// nothing in a new world (no shared copy: generated for the run), and is
// judged from the server alone, over RCON: the advancements it earns and
// when, its deaths, how long it stands still. botday1, botday2 and botday3
// are the same day in three worlds (their seeds), so three can run at once
// on three machines.
//
// A night's wait the bot announces ("waiting for dawn", a health event) is taken when the server confirms it
// (botwait.go): standing still then does not count toward the stall limit, and counts again 60 s after dawn.
//
// MC26_BOT is the bot's package, relative to the kit's directory
// (./bot/cmd/bot by default); it takes -address and -name. What the harness reads of its output, and the rest of
// what a downstream bot agrees with it, is in botlines.go.

// botDaySeeds are the worlds.
// (486922030: a desert spawn, no tree in sight — botdayx found it; the
// biome-aware explore is compared on it. 413413217: a snowy taiga by a
// frozen river, no table on its botdayx run — a cold start. 2666599: a
// badlands, terracotta over the stone and few trees: the stone tools took
// the day, the last log went to them, and the pickaxe wore out in the
// night's stairs with no wood left — its botdayx run stood still till it
// was called stalled. 960474411: a pillager outpost near the spawn — a
// botdayx run was shot at by day from the structure, which a random world
// cannot repeat)
var botDaySeeds = []string{"26265", "8675309", "424242", "486922030", "413413217", "2666599", "960474411"}

// botDayLength is how long a run lasts: a game day and its night (20
// minutes), to the next morning.
const botDayLength = 21 * time.Minute

// botDayMilestones are the advancements a first day earns, in their order.
var botDayMilestones = []struct{ id, what string }{
	{"minecraft:story/root", "a crafting table"},
	{"minecraft:story/mine_stone", "stone mined"},
	{"minecraft:story/upgrade_tools", "a stone pickaxe"},
	{"minecraft:story/smelt_iron", "iron smelted"},
}

// botDaysMilestones are the next days' too (botdaysx): the field, the iron.
var botDaysMilestones = append(append([]struct{ id, what string }{}, botDayMilestones...),
	struct{ id, what string }{"minecraft:husbandry/plant_seed", "a field sown"},
	struct{ id, what string }{"minecraft:story/iron_tools", "an iron pickaxe"},
)

// The hostile mobs are a condition of a run, so fitness is compared only
// between runs under the same: "natural" the game's own; "off" none spawn
// (spawn_monsters false; difficulty normal: hunger still counts, animals
// spawn); "script" none spawn but the script's, the same mobs at the same
// moments near the bot in every run.
const (
	mobsNatural = "natural"
	mobsOff     = "off"
	mobsScript  = "script"
)

// botDayScriptVersion names the script below: a change of it is a new
// condition (runs under another version are not compared).
const botDayScriptVersion = "1"

// botDayScript is when and where the scripted mobs come: at a minute of the
// run, or at a time of a day (its day, its daytime), a mob put on the ground
// dx, dz off the bot. Data, so it grows in one place.
var botDayScript = []struct {
	minute  float64 // minutes into the run (0: by the day's time)
	day     int     // the game's day (from 0), with daytime
	daytime int
	mob     string
	dx, dz  int
}{
	{minute: 5, mob: "minecraft:creeper", dx: 10},
	{day: 0, daytime: 12600, mob: "minecraft:zombie", dx: 12},    // dusk
	{day: 0, daytime: 14000, mob: "minecraft:skeleton", dz: -14}, // night
	{day: 0, daytime: 16000, mob: "minecraft:zombie", dz: 12},
	{day: 1, daytime: 12600, mob: "minecraft:zombie", dx: -12},
	{day: 1, daytime: 14000, mob: "minecraft:skeleton", dx: 14},
	{day: 1, daytime: 16000, mob: "minecraft:spider", dz: -12},
}

var posRe = regexp.MustCompile(`\[(-?[0-9.]+)d, (-?[0-9.]+)d, (-?[0-9.]+)d\]`)

// scenarioBotDay is botday n (its seed's world), or with n 0 botdayx: a new
// world from a random seed each run — new land to watch and to meet (the seed
// in its report, to keep a good one). days 2 is botdaysx: two days and their
// nights, the next days' plan and its growth too (the field, the iron).
func scenarioBotDay(o Options, n, days int, mobs string) error {
	var seed, scenario string
	if n == 0 {
		seed, scenario = strconv.FormatInt(time.Now().UnixNano()%1000000000, 10), "botdayx"
	} else {
		seed, scenario = botDaySeeds[n-1], fmt.Sprintf("botday%d", n)
	}
	if days > 1 {
		scenario = "botdaysx"
		if n > 0 {
			scenario = fmt.Sprintf("botdays%d", n)
		}
	}
	if mobs != mobsNatural {
		scenario += mobs // botday1off, botdays2script
	}
	return runBotDay(o, botDayRun{scenario: scenario, seed: seed, days: days, mobs: mobs})
}

// botDayRun is what a botday run is: its name, its world, its days, the mobs,
// and for a farm scenario (botfarm.go) what that changes.
type botDayRun struct {
	scenario, seed string
	days           int
	mobs           string
	farm           *farmSetup
}

// runBotDay runs a botday run: the bot in a new world of the seed, judged.
func runBotDay(o Options, r botDayRun) error {
	scenario, seed, days, mobs := r.scenario, r.seed, r.days, r.mobs
	length, milestones := botDayLength, botRunMilestones(r)
	if days > 1 {
		length = time.Duration(days)*20*time.Minute + time.Minute
	}
	if r.farm != nil {
		length = r.farm.length
	}
	name := "Builder"
	dir := filepath.Join(o.WorkDir, "server-"+scenario)
	if err := os.RemoveAll(dir); err != nil { // always a new world
		return err
	}
	bin, err := buildBot(o, scenario)
	if err != nil {
		return err
	}
	srv := &smoke.Server{
		Version: o.Version, JarPath: o.JarPath, WorkDir: dir,
		Port: o.Port, Runtime: o.Runtime, Log: o.Log,
		LevelType: "minecraft:normal", Seed: seed,
	}
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
	setup := []string{"difficulty normal", "time set 0", "scoreboard objectives add deaths deathCount"}
	if mobs != mobsNatural {
		setup = append(setup, "gamerule spawn_monsters false")
	}
	for _, c := range farmCounters { // the wheat a day, on every run (botfarm.go)
		setup = append(setup, "scoreboard objectives add "+c.objective+" "+c.criterion)
	}
	if r.farm != nil {
		for _, rule := range r.farm.rules {
			setup = append(setup, "gamerule "+rule)
		}
	}
	for _, c := range setup {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	conditions := map[string]any{"seed": seed, "difficulty": "normal", "mobs": mobs, "days": max(days, 1)}
	if mobs == mobsScript {
		conditions["script"] = botDayScriptVersion
	}
	if r.farm != nil {
		conditions["farm"] = r.scenario
		if len(r.farm.rules) > 0 {
			conditions["rules"] = r.farm.rules
		}
	}

	logPath := filepath.Join(o.WorkDir, scenario+".log")
	lf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer lf.Close()
	out := &syncBuffer{}
	args := []string{"-address", srv.Addr(), "-name", name}
	// its events kept in a run of their own (runs.go), as the robot's: a
	// watcher (go-mc26-robotview) follows it there
	run, err := newRun(o, scenario, name, srv)
	if err != nil {
		o.Log("runs: %v (no events kept)", err)
	} else if run != nil {
		args = append(args, "-events", run.events())
		defer func() { run.end(out.String()) }()
	}
	cmd := exec.Command(bin, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	proc := watchProc(cmd) // its end is seen when it comes, not as a stall minutes later
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the bot's own story, its decisions, into the run's log
		defer wg.Done()
		err := readBotLines(pr, func(line string) {
			fmt.Fprintln(lf, line)
			out.Write([]byte(line + "\n"))
			if _, own := botSays(line); own || strings.Contains(line, "panic") {
				o.Log("%s: bot: %s", scenario, line)
			}
		})
		if err != nil {
			o.Log("%s: the bot's output: %v", scenario, err)
		}
	}()
	defer func() {
		proc.stop(cmd)
		pw.Close()
		wg.Wait()
	}()
	if err := waitFor(out, 90*time.Second, "loaded"); err != nil {
		return fmt.Errorf("%s: the bot did not join: %v\n%s", scenario, err, tail(out.String(), 20))
	}
	if err := waitBotPrefix(out, 30*time.Second); err != nil {
		return fmt.Errorf("%s: %v\n%s", scenario, err, tail(out.String(), 20))
	}
	if r.farm != nil { // handed at its start: the farm from its first minute
		for _, g := range r.farm.give {
			if resp, err := rcon.command("give " + name + " " + g); err != nil || rconFailed(resp) {
				return fmt.Errorf("%s: give %s: %q %v", scenario, g, resp, err)
			}
		}
		o.Log("%s: handed %v; rules %v", scenario, r.farm.give, r.farm.rules)
	}
	o.Log("%s: seed %s, the bot in, %d day(s) and their nights to live", scenario, seed, max(days, 1))

	start := time.Now()
	tally := newFarmTally()
	var lastPos string
	stillSince, longestStill := time.Now(), time.Duration(0)
	deaths := "0"
	var skySince time.Time // out under the sky at night since
	var skyLeakLogged time.Time
	var waits *waitTail // the bot's announced waits (botwait.go)
	if run != nil {
		waits = &waitTail{path: run.events()}
	}
	judge := newWaitJudge(rcon.command, name, func(l string) { o.Log("%s: %s", scenario, l) })
	// each milestone's time from the bot's own event, or the server's answer within a second (botmilestones.go)
	eventsPath := ""
	if run != nil {
		eventsPath = run.events()
	}
	reached := newMilestoneTimes(start, milestones, eventsPath, name, rcon.command, func(l string) { o.Log("%s: %s", scenario, l) })
	stopMilestones := make(chan struct{})
	defer close(stopMilestones)
	go reached.run(time.Second, stopMilestones)
	// the scorecard in the run's meta.json, however the run ends (an ok run
	// kept none: only a failure's error held it)
	defer func() {
		ms, msMs := map[string]any{}, map[string]any{}
		for _, m := range milestones {
			if t, ok := reached.Reached(m.id); ok {
				ms[m.what], msMs[m.what] = int(t.Seconds()), t.Milliseconds()
			} else {
				ms[m.what], msMs[m.what] = nil, nil
			}
		}
		d, _ := strconv.Atoi(deaths)
		run.score(conditions, map[string]any{"seed": seed, "milestones": ms, "milestonesMs": msMs, "deaths": d,
			"longestStill": int(longestStill.Seconds()), "length": int(time.Since(start).Seconds()), "farm": tally.fitness()})
	}()
	scripted := make([]bool, len(botDayScript))
	for time.Since(start) < length {
		time.Sleep(10 * time.Second)
		if proc.ended() {
			time.Sleep(200 * time.Millisecond) // (its last lines are read from the pipe)
			o.Log("%s: the bot's process ended", scenario)
			return fmt.Errorf("%s", proc.said(scenario, out.String()))
		}
		// a night out under the sky ends the run at once: a bot without a
		// shelter at night is no bot to go on watching
		if kind := nightOutside(rcon, name); kind != "" {
			if kind != "sky" {
				// a space open to the outside, not under the open sky: told, not failed (its flood past a
				// cave system's size ended botday2off 1015-140706 with a bot in its own mine); the bot's
				// own check has the sky and the open land
				if skyLeakLogged.IsZero() || time.Since(skyLeakLogged) > 30*time.Second {
					skyLeakLogged = time.Now()
					o.Log("%s: open space at night at %s (the flood passed %d cells): told, not failed", scenario, lastPos, watcherSpaceCells)
				}
			} else {
				if skySince.IsZero() {
					skySince = time.Now()
				}
				if s := time.Since(skySince); runFailsAtNight(kind, s) {
					o.Log("%s: OUT AT NIGHT under the sky for %s at %s, after %s", scenario, s.Round(time.Second), lastPos, time.Since(start).Round(time.Second))
					return fmt.Errorf("%s: out under the sky at night for %s at %s (deaths %s)\n%s", scenario,
						s.Round(time.Second), lastPos, deaths, tail(out.String(), 30))
				}
			}
		} else {
			skySince = time.Time{}
		}
		if mobs == mobsScript {
			botDayScripted(o, rcon, scenario, name, start, scripted)
		}
		if resp, err := rcon.command("scoreboard players get " + name + " deaths"); err == nil {
			if f := strings.Fields(resp); len(f) >= 3 && f[1] == "has" {
				if f[2] != deaths {
					o.Log("%s: DIED (%s) after %s", scenario, f[2], time.Since(start).Round(time.Second))
				}
				deaths = f[2]
			}
		}
		tally.read(rcon.command, name, time.Since(start))
		if resp, err := rcon.command("data get entity " + name + " Pos"); err == nil {
			if m := posRe.FindStringSubmatch(resp); m != nil {
				pos := fmt.Sprintf("%.0f %.0f %.0f", atof(m[1]), atof(m[2]), atof(m[3]))
				if pos != lastPos {
					lastPos, stillSince = pos, time.Now()
				} else if s := time.Since(stillSince); s > longestStill {
					longestStill = s
				}
			}
		}
		if waits != nil { // a wait the bot announced and the server confirms: standing still is no stall
			st := judge.step(waits.poll())
			stillSince = st.adjustStill(stillSince, time.Now())
			if st.Consumed {
				waits.consume()
			}
		}
		if still := time.Since(stillSince); still >= botDayStillLimit {
			o.Log("%s: STALLED: still %s at %s, after %s", scenario, still.Round(time.Second), lastPos, time.Since(start).Round(time.Second))
			return fmt.Errorf("%s: stalled: still %s at %s (a hard limit)\n%s", scenario, still.Round(time.Second), lastPos, tail(out.String(), 30))
		}
		for _, dl := range botDayDeadlines {
			if _, ok := reached.Reached(dl.id); !ok && time.Since(start) > dl.by {
				o.Log("%s: TOO SLOW: no %s by %s", scenario, dl.what, dl.by)
				return fmt.Errorf("%s: no %s by %s (a hard limit)\n%s", scenario, dl.what, dl.by, tail(out.String(), 30))
			}
		}
		if int(time.Since(start).Seconds())%60 < 10 {
			o.Log("%s: minute %d at %s, still %s, deaths %s, milestones %d of %d; farm: %s", scenario,
				int(time.Since(start).Minutes()), lastPos, time.Since(stillSince).Round(time.Second), deaths, reached.Count(), len(milestones), tally)
		}
	}
	reached.poll() // (the last second)
	tally.read(rcon.command, name, time.Since(start))
	var card []string
	for _, ms := range milestones {
		if t, ok := reached.Reached(ms.id); ok {
			card = append(card, fmt.Sprintf("%s after %s", ms.what, t.Round(100*time.Millisecond)))
		} else {
			card = append(card, ms.what+": not")
		}
	}
	summary := fmt.Sprintf("seed %s: %s; deaths %s; longest standing still %s; farm: %s", seed, strings.Join(card, ", "), deaths, longestStill.Round(time.Second), tally)
	o.Log("%s: %s", scenario, summary)
	if r.farm != nil && r.farm.goal { // a goal run is judged by its scorecard: it lives (the summary says how far)
		if _, ok := reached.Reached("minecraft:story/upgrade_tools"); !ok || deaths != "0" {
			return fmt.Errorf("%s\n%s", summary, tail(out.String(), 25))
		}
		return nil
	}
	if r.farm != nil { // a farm run passes by its field: the wheat, and sown again
		if !tally.farmPassed(r.farm) || deaths != "0" {
			return fmt.Errorf("%s (to pass: wheat %d or more, sown again, no death)\n%s", summary, r.farm.harvest, tail(out.String(), 25))
		}
		return nil
	}
	if _, ok := reached.Reached("minecraft:story/upgrade_tools"); !ok || deaths != "0" {
		return fmt.Errorf("%s\n%s", summary, tail(out.String(), 25))
	}
	return nil
}

// botDayScripted puts the script's mobs that are due near the bot, each
// once: on the ground (the surface over where it is) dx, dz off it.
func botDayScripted(o Options, rcon *rconClient, scenario, name string, start time.Time, done []bool) {
	day, daytime := -1, -1
	if t, ok := serverClock(rcon.command); ok {
		day, daytime = t/dayTicks, t%dayTicks
	}
	for i, ev := range botDayScript {
		if done[i] {
			continue
		}
		due := ev.minute > 0 && time.Since(start).Minutes() >= ev.minute ||
			ev.minute == 0 && day >= 0 && (day > ev.day || day == ev.day && daytime >= ev.daytime)
		if !due {
			continue
		}
		done[i] = true
		c := fmt.Sprintf("execute at %s positioned ~%d ~ ~%d positioned over motion_blocking_no_leaves run summon %s ~ ~ ~", name, ev.dx, ev.dz, ev.mob)
		resp, err := rcon.command(c)
		o.Log("%s: script: %s %d,%d off the bot after %s (%q %v)", scenario, ev.mob, ev.dx, ev.dz, time.Since(start).Round(time.Second), resp, err)
	}
}

// The hard limits: a run that cannot be useful any more ends at once,
// failed, not played out to its length (the owner: a run stuck at minute 26 of
// 41 told nothing more) — standing still that long (its night's work mines,
// it does not wait), or a milestone not reached by its time.
const botDayStillLimit = 4 * time.Minute

var botDayDeadlines = []struct {
	id, what string
	by       time.Duration
}{
	{"minecraft:story/root", "a crafting table", 5 * time.Minute},
	{"minecraft:story/upgrade_tools", "a stone pickaxe", 12 * time.Minute},
}

// botDayNightGrace is how long a bot may be out under the sky at night (two
// polls: a bot just walking into its shelter as night falls).
const botDayNightGrace = 20 * time.Second

// nightOutside: the night (13000 to 23000 of the day, when mobs spawn) and
// the player under the open sky — nothing over its head but air (leaves are
// no roof: the world surface without leaves is at its feet) — "sky"; or in a
// space open to the outside (openSpaceLeaks) — "leak"; "" else.
func nightOutside(rcon *rconClient, name string) string {
	if night, ok := serverNight(rcon.command); !ok || !night {
		return ""
	}
	if underOpenSky(rcon.command, name) {
		return "sky"
	}
	// a roof is not a shelter (the owner, 2026-10-15, bot-48): the space it stands in open to the outside
	if leaks, ok := openSpaceLeaks(rcon.command, name); ok && leaks {
		return "leak"
	}
	return ""
}

// runFailsAtNight says whether a night spent so long in the state ends the run: only the open sky ("sky") does,
// after botDayNightGrace; an open space ("leak") is told, never failed. With d 0 it answers whether the state
// can fail a run at all.
func runFailsAtNight(kind string, d time.Duration) bool {
	return kind == "sky" && d >= botDayNightGrace
}

// botDayLeakGrace is how long a bot may stand in a space open to the outside at night: it dug into a cave and
// digs itself a shelter (half a minute), longer than under the open sky (botDayNightGrace).
const botDayLeakGrace = 60 * time.Second

// buildBot builds the outside bot (MC26_BOT) in the kit's workspace.
func buildBot(o Options, scenario string) (string, error) {
	pkg := botPackage()
	kitDir, err := filepath.Abs(o.KitDir)
	if err != nil {
		return "", err
	}
	bin := filepath.Join(o.WorkDir, "bin", scenario+"-bot")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", bin, pkg)
	cmd.Dir = kitDir
	cmd.Env = kit.Env(kitDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the bot %s: %v\n%s", pkg, err, out)
	}
	return bin, nil
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%g", &f)
	return f
}
