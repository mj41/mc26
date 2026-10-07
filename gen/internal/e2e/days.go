package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// scenarioDays lives the first days in the survival world on the game's own
// clock (no time set but the first morning), the robot keeping its own time
// of day from the server's clock, as a person does:
//
//   - day 1: wood, a pickaxe, a shelter dug into a hill (its room the first
//     stone), a sword, an axe, torches; more wood while it is light;
//   - night 1: down its stairs to the iron's level and a mine there, then on
//     down and another mine, level after level, till morning;
//   - day 2: back up and out, a wheat field, wood; in at dusk;
//   - night 2: down its stairs again, an iron pickaxe, on down into the
//     deepslate and a mine there for diamonds.
//
// It is never left waiting: day and night it has chores, round after round,
// as a person playing is on the move. The server judges the diamond, the
// field, the robot alive. Normal
// difficulty all along. Long — two days and nights, an hour — it runs only
// when named (e2e --only days).
func scenarioDays(o Options, bin string) error { return lives(o, bin, "days", 2) }

// scenarioWeek lives on past the second day as the robot will live in a
// world it plays in: each day up its stairs, its field harvested and sown
// again (bread: it hunts nothing), wood till dusk, in; each night down its
// stairs and mines. MC26_DAYS days (7: a week; two hours and more of the
// game's clock); only when named (e2e --only week).
func scenarioWeek(o Options, bin string) error {
	days := 7
	if v, err := strconv.Atoi(os.Getenv("MC26_DAYS")); err == nil && v >= 2 {
		days = v
	}
	return lives(o, bin, "week", days)
}

// lives is days and week: the robot's first days, then the days after.
func lives(o Options, bin, scenario string, days int) error {
	const name = "Settler"
	srv, r, rcon, done, err := forestStart(o, bin, name, scenario+".log", true)
	if err != nil {
		return err
	}
	defer done()
	if resp, err := rcon.command("difficulty normal"); err != nil || rconFailed(resp) {
		return fmt.Errorf("difficulty: %q %v", resp, err)
	}
	logFrom := serverLogSize(srv)
	// where the nearest village is, for the log (the robot is not told: it
	// finds one by looking)
	if resp, err := rcon.command("locate structure #minecraft:village"); err == nil {
		o.Log("%s: %s", scenario, strings.TrimSpace(resp))
	}
	r0 := r
	defer func() {
		if r != r0 {
			r.close() // a robot started again (held, resumed): its own close
		}
	}()
	held := os.Getenv("MC26_HOLD") != ""
	// the clock moved on while the robot only waits (dusk at home, a night
	// it stays in, a hideout till morning): time add, as time set would
	// start the day count again. MC26_REALTIME=1 keeps the game's time.
	// Crops and furnaces do not go on in time skipped.
	fast := os.Getenv("MC26_REALTIME") == ""
	var skipMu sync.Mutex
	skipTo := func(to int, why string) {
		if !fast {
			return
		}
		skipMu.Lock()
		defer skipMu.Unlock()
		// the time of day is a timeline in 26.x (no "time query daytime"):
		// "Timeline minecraft:day is at 504 tick(s)"
		resp, err := rcon.command("time query minecraft:day")
		if err != nil {
			o.Log("%s: the clock not moved (%s): %v", scenario, why, err)
			return
		}
		m := regexp.MustCompile(`(\d+) tick`).FindStringSubmatch(resp)
		t := 0
		if m != nil {
			t, err = strconv.Atoi(m[1])
		}
		if m == nil || err != nil {
			o.Log("%s: the clock not moved (%s): time query answered %q", scenario, why, resp)
			return
		}
		d := ((to-t)%24000 + 24000) % 24000
		if d < 200 || d > 12000 {
			return // there already, or the wait is not a night's
		}
		if resp, err := rcon.command(fmt.Sprintf("time add %d", d)); err != nil || rconFailed(resp) {
			o.Log("%s: time add %d: %q %v", scenario, d, resp, err)
			return
		}
		o.Log("%s: the clock moved on %d ticks, to %d (%s)", scenario, d, to, why)
	}
	once := func(cmd string, limit time.Duration) (string, error) {
		from := len(r.out.String())
		// standing still three minutes on one command is stuck: held, the
		// robot is stopped, which ends the command (MC26_HOLD only)
		quit := make(chan struct{})
		defer close(quit)
		// walled in till morning: the night skipped
		go func(b *robotBot) {
			for {
				select {
				case <-quit:
					return
				case <-time.After(3 * time.Second):
				}
				if strings.Contains(b.out.String()[from:], "robot: waiting for the morning") {
					skipTo(23000, "walled in till morning")
					return
				}
			}
		}(r)
		if held {
			go func(b *robotBot) {
				for {
					select {
					case <-quit:
						return
					case <-time.After(5 * time.Second):
					}
					for _, m := range stillRe.FindAllStringSubmatch(b.out.String()[from:], -1) {
						if n, _ := strconv.Atoi(m[1]); n >= 180 {
							o.Log("%s: %s: the robot has stood still %ds: stopped", scenario, cmd, n)
							b.stop()
							return
						}
					}
				}
			}(r)
		}
		ans, err := r.ask(cmd, limit)
		// a death is the end: it respawns with nothing, far from all it built
		if strings.Contains(r.out.String()[from:], "Died") {
			return "", fmt.Errorf("%s: the robot died\n%s", cmd, tail(r.out.String(), 15))
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w\n%s", cmd, err, tail(r.out.String(), 10))
		}
		o.Log(scenario+": %s: %s", cmd, ans)
		return ans, nil
	}
	// do is a robot command; with MC26_HOLD a failure (stuck, an error, a
	// death) holds the run instead of ending it: the server and its world
	// stay, the robot is fixed and rebuilt, then <scenario>.resume in the
	// work directory starts it again in the same world, from its memory, and
	// the command is tried again (<scenario>.abort ends the run)
	deaths := 0
	do := func(cmd string, limit time.Duration) (string, error) {
		relogs := 0 // started again at once on this command (maxRelogs)
		for {
			ans, err := once(cmd, limit)
			// a death: counted (the run is not survived), and the robot goes
			// back for its things and home, as a player does; then the
			// command again
			if err != nil && strings.Contains(err.Error(), "the robot died") && deaths < 5 {
				deaths++
				o.Log("%s: DIED (%d): %s", scenario, deaths, firstLine(err.Error()))
				// the robot's process gone too (stopped while it lay dead):
				// held, started again, then it goes back for its things
				select {
				case <-r.done:
					if held {
						nr, herr := holdAndResume(o, scenario, name, srv, r, err, &relogs)
						if herr != nil {
							return "", herr
						}
						r = nr
					}
				default:
				}
				if ans, rerr := once("recover", 10*time.Minute); rerr != nil {
					o.Log("%s: recover: %v", scenario, firstLine(rerr.Error()))
				} else {
					o.Log("%s: recover: %s", scenario, ans)
				}
				continue
			}
			if err == nil || !held {
				return ans, err
			}
			nr, herr := holdAndResume(o, scenario, name, srv, r, err, &relogs)
			if herr != nil {
				return "", herr
			}
			r = nr
		}
	}
	// pause waits a little; a robot stopped meanwhile (MC26_HOLD) is held and
	// resumed as in do (its relogs counted till a pause goes through)
	pauseRelogs := 0
	pause := func() (string, error) {
		ans, err := r.ask("wait 100", 10*time.Second)
		if err != nil && held {
			nr, herr := holdAndResume(o, scenario, name, srv, r, err, &pauseRelogs)
			if herr != nil {
				return "", herr
			}
			r = nr
			return "", nil
		}
		if err == nil {
			pauseRelogs = 0
		}
		return ans, err
	}
	phase := func() (string, error) {
		ans, err := r.ask("time", 5*time.Second)
		if err != nil {
			return "", err
		}
		f := strings.Fields(ans) // day N tick T phase; …
		if len(f) < 5 {
			return "", fmt.Errorf("time answered %q", ans)
		}
		return strings.TrimSuffix(f[4], ";"), nil
	}
	// busy keeps the robot at the chores of each round while the phase is
	// while — a person playing is on the move day and night, not waiting
	busy := func(while string, round func(i int) ([]string, error)) error {
		for i := 0; i < 20; i++ {
			chores, err := round(i)
			if err != nil {
				return err
			}
			idle := true // every chore of the round answered that it stays in
			for _, c := range chores {
				if p, err := phase(); err != nil || p != while {
					return err
				}
				// "!": a chore the rest stands on — its failing is the end
				must := strings.HasPrefix(c, "!")
				c = strings.TrimPrefix(c, "!")
				ans, err := do(c, 30*time.Minute) // a mine takes long
				if err != nil {
					if must || strings.Contains(err.Error(), "the robot died") {
						return err
					}
					o.Log(scenario+": %v (going on)", err) // a chore not done is not the end of it
				}
				if err != nil || !strings.HasPrefix(ans, "staying in") && !strings.HasPrefix(ans, "stopped:") {
					idle = false
				}
			}
			// it stays in (no pickaxe, hurt with nothing to eat): the phase
			// waited out, not twenty rounds of the same answer
			for idle {
				p, err := phase()
				if err != nil || p != while {
					return err
				}
				if while == "night" {
					skipTo(23000, "staying in for the night")
				}
				if _, err := pause(); err != nil {
					return err
				}
			}
		}
		return fmt.Errorf("still %s after 20 rounds of chores", while)
	}
	// y is the robot's height, as it says
	y := func() (int, error) {
		ans, err := r.ask("pos", 5*time.Second)
		if err != nil {
			return 0, err
		}
		var x, h float64
		if _, err := fmt.Sscanf(ans, "%f %f", &x, &h); err != nil {
			return 0, fmt.Errorf("pos answered %q", ans)
		}
		return int(h), nil
	}
	// a night's mining: on down the stairs and another mine, to the deepest
	// level (the diamonds'), its tunnels longer each round (at the bottom,
	// the same mine dug on further)
	mining := func(i int) ([]string, error) {
		h, err := y()
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("descend %d", max(h-12, -54)), fmt.Sprintf("mine %d", 24+16*i)}, nil
	}
	// smelt: what it mined into ingots, in as many furnaces as it takes — at
	// the night's end where the mine is, at home in the evening
	smelt := func() {
		if _, err := do("smeltall", 20*time.Minute); err != nil {
			o.Log("%s: %v", scenario, err)
		}
	}
	// waitFor waits for the phase want — the day not waited out idle: in
	// the day (a run behind the clock, held and resumed) it does the day's
	// chores and comes in at dusk; only dusk and dawn are waited through
	waitFor := func(want string, limit time.Duration) error {
		deadline := time.Now().Add(limit)
		for {
			p, err := phase()
			if err != nil {
				return err
			}
			if p == want {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("no %s after %s (it is %s)", want, limit, p)
			}
			if p == "day" && want != "day" {
				for _, c := range []string{"out", "harvest", "get logs +16", "in"} {
					if _, err := do(c, 10*time.Minute); err != nil {
						o.Log("%s: %v (going on)", scenario, err)
					}
				}
				deadline = time.Now().Add(limit)
				continue
			}
			if want == "night" && p == "dusk" {
				skipTo(13000, "home at dusk")
			}
			if _, err := pause(); err != nil {
				return err
			}
		}
	}
	have := func(item string, n int) error {
		resp, err := rcon.command("data get entity " + name + " Inventory")
		if err != nil {
			return err
		}
		if got := itemCounts(resp)[item]; got < n {
			return fmt.Errorf("the server has %d %s, not %d: %v", got, item, n, itemCounts(resp))
		}
		return nil
	}

	// day 1: wood and a wooden pickaxe, then the shelter dug into a hill —
	// its room the first stone (the night's stairs give the rest) — a sword,
	// an axe (the logs cut faster), torches (the room lit coming in). The
	// animals are left alone: it eats the apples the leaves drop, and the
	// field's bread
	for _, c := range []string{"get wooden_pickaxe", "get logs 16", "shelter", "get stone_sword", "get stone_axe", "get torch 16", "get stick 8", "in"} {
		if _, err := do(c, 6*time.Minute); err != nil {
			return err
		}
	}
	if err := busy("day", func(i int) ([]string, error) {
		if i == 0 { // the first afternoon: water found and remembered (the field's)
			return []string{"out", "water", fmt.Sprintf("get logs %d", 32+16*i), "in"}, nil
		}
		return []string{"out", fmt.Sprintf("get logs %d", 32+16*i), "in"}, nil
	}); err != nil {
		return err
	}
	if _, err := do("in", 4*time.Minute); err != nil { // wherever the day ended
		o.Log(scenario+": %v", err)
	}
	smelt() // at home, the evening
	if err := waitFor("night", 15*time.Minute); err != nil {
		return err
	}
	// night 1: down to the iron, a mine, on down — till the robot comes up
	// at morning to the day's work
	if err := busy("night", func(i int) ([]string, error) {
		if i == 0 {
			return []string{"!get stone_pickaxe 4", "!descend 16", "mine"}, nil
		}
		return mining(i)
	}); err != nil {
		return err
	}
	smelt() // the night's ore, before it goes up
	// day 2
	if _, err := do("in", 10*time.Minute); err != nil { // up the stairs home
		return err
	}
	if _, err := do("out", 4*time.Minute); err != nil {
		return err
	}
	field, err := do("farm", 10*time.Minute)
	if err != nil {
		return err
	}
	// to know the land round home: where the woods are, water, lava
	if _, err := do("scout 48", 10*time.Minute); err != nil {
		o.Log("%s: %v", scenario, err)
	}
	if err := busy("day", func(i int) ([]string, error) {
		return []string{fmt.Sprintf("get logs %d", 16+16*i)}, nil
	}); err != nil {
		return err
	}
	if _, err := do("in", 6*time.Minute); err != nil {
		return err
	}
	smelt() // at home, the evening
	if err := waitFor("night", 15*time.Minute); err != nil {
		return err
	}
	// night 2: down its stairs, the iron pickaxe (if the mines have not made
	// it one yet), down to the diamonds' level and mines there
	if err := busy("night", func(i int) ([]string, error) {
		if i == 0 {
			return []string{"!down", "!get iron_pickaxe", "!descend -54", "mine"}, nil
		}
		return mining(i)
	}); err != nil {
		return err
	}
	smelt() // the night's ore, before it goes up
	// days 3, 4, …: up its stairs, the field, wood till dusk, in; down and
	// mines by night
	for d := 3; d <= days; d++ {
		o.Log("%s: day %d", scenario, d)
		if _, err := do("in", 10*time.Minute); err != nil { // up the stairs home
			o.Log("%s: %v", scenario, err)
		}
		if _, err := do("out", 4*time.Minute); err != nil {
			o.Log("%s: %v", scenario, err)
		}
		if err := busy("day", func(i int) ([]string, error) {
			if i == 0 && d >= 5 { // from day five: a village found, traded with
				return []string{"harvest", "village 50", "tradeall", "in"}, nil
			}
			if i == 0 { // and each day a little further round home
				return []string{"harvest", fmt.Sprintf("scout %d", 48+16*(d-2)), "get logs +16"}, nil
			}
			return []string{"harvest", "get logs +16"}, nil
		}); err != nil {
			return err
		}
		if _, err := do("in", 6*time.Minute); err != nil {
			o.Log("%s: %v", scenario, err)
		}
		smelt() // at home, the evening
		if err := waitFor("night", 15*time.Minute); err != nil {
			return err
		}
		if err := busy("night", func(i int) ([]string, error) {
			if i == 0 {
				return []string{"down", "mine"}, nil
			}
			return mining(i)
		}); err != nil {
			return err
		}
		smelt()
	}
	if err := have("minecraft:iron_pickaxe", 1); err != nil {
		return err
	}
	if err := have("minecraft:diamond", 1); err != nil {
		if _, err := do("get diamond", 20*time.Minute); err != nil {
			return err
		}
		if err := have("minecraft:diamond", 1); err != nil {
			return err
		}
	}
	// the field: wheat growing where it was sown
	var fx, fy, fz int
	if _, err := fmt.Sscanf(field, "field at %d %d %d", &fx, &fy, &fz); err == nil {
		resp, _ := rcon.command(fmt.Sprintf("execute if block %d %d %d minecraft:wheat", fx+1, fy+1, fz))
		o.Log(scenario+": wheat by the water: %q", resp)
	}
	if ans, err := r.ask("time", 5*time.Second); err == nil {
		o.Log(scenario+": %s", ans)
	}
	if resp, err := rcon.command("data get entity " + name + " Health"); err != nil || strings.Contains(resp, " 0.0f") {
		return fmt.Errorf("the robot's health: %q %v", resp, err)
	} else {
		o.Log(scenario+": %s", resp)
	}
	if bad := complaints(serverLogFrom(srv, logFrom), name); len(bad) > 0 {
		return fmt.Errorf("the server objected:\n%s", strings.Join(bad, "\n"))
	}
	if deaths > 0 {
		return fmt.Errorf("lived the %d days, but died %d times on the way", days, deaths)
	}
	return nil
}

// stillRe finds the robot's still lines: "robot: still 180s at …".
var stillRe = regexp.MustCompile(`robot: still (\d+)s`)

// maxRelogs is how many times a robot that ended itself to relog is started
// again at once on one command; the next relog is held as a failure is.
const maxRelogs = 5

// holdAndResume holds a run on err (MC26_HOLD): the robot stopped and its run
// marked held, then it waits for <scenario>.resume (or .abort) in the work
// directory — three hours at most — and starts the robot again, built anew
// from the kit, in the same world, from its memory. relogs counts the
// relogs of the command it is held on.
func holdAndResume(o Options, scenario, name string, srv *smoke.Server, r *robotBot, cause error, relogs *int) (*robotBot, error) {
	// a relog (the robot ended itself: its world and the server's
	// disagreed) is no fault to fix: started again at once — maxRelogs
	// times on one command; a robot that keeps relogging is one to fix
	relog := strings.Contains(r.out.String(), "robot: relog:")
	if relog && *relogs >= maxRelogs {
		relog = false
		cause = fmt.Errorf("relogged %d times on one command: %w", *relogs, cause)
	} else if relog {
		*relogs++
	}
	r.close()
	r.run.hold(cause)
	resume := filepath.Join(o.WorkDir, scenario+".resume")
	abort := filepath.Join(o.WorkDir, scenario+".abort")
	_ = os.Remove(resume)
	_ = os.Remove(abort)
	o.Log("%s: HELD: %v", scenario, firstLine(cause.Error()))
	if relog {
		o.Log("%s: a relog (%d of %d on this command): started again at once", scenario, *relogs, maxRelogs)
	} else {
		o.Log("%s: fix the robot, re-assemble the kit (mc26 kit), then: touch %s (or %s to end)", scenario, resume, abort)
	}
	deadline := time.Now().Add(3 * time.Hour)
	for !relog {
		if _, err := os.Stat(abort); err == nil {
			_ = os.Remove(abort)
			return nil, fmt.Errorf("held and aborted: %w", cause)
		}
		if _, err := os.Stat(resume); err == nil {
			_ = os.Remove(resume)
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("held three hours, no resume: %w", cause)
		}
		time.Sleep(2 * time.Second)
	}
	bin, err := buildExamples(o)
	if err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}
	nr, err := startRobot(o, bin, filepath.Join(o.WorkDir, scenario+".log"), srv, name)
	if err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}
	if err := waitFor(nr.out, 60*time.Second, "Login success", "robot: loaded"); err != nil {
		nr.close()
		return nil, fmt.Errorf("resume: %v\n%s", err, tail(nr.out.String(), 15))
	}
	if _, err := nr.ask("wait 20", 10*time.Second); err != nil {
		nr.close()
		return nil, fmt.Errorf("resume: %w", err)
	}
	o.Log("%s: RESUMED, the robot started again from its memory", scenario)
	return nr, nil
}

// firstLine is s up to its first line break.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
