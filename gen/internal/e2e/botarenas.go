package e2e

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// The botarenas3 scenario: three outside bots (botday.go's) on one server, each in a walled arena of its own
// (arenas.go) on the flat world, side by side along x. The arenas share the world's clock and weather: the
// clock runs on from the world's start and no command here sets the time or the weather. No monsters spawn.
// Each bot has a run of its own (its own events file), all of them the same job (MC26_JOB), so a viewer can
// list them together.
//
// A botday run is judged by the advancements of a first day, which need trees and stone an arena does not
// have; a bot here is judged by a plain run of arenaLength: it ends ok when the bot is alive (no death) and
// has not stood still as long as botDayStillLimit.

const (
	arenaBots   = 3
	arenaLength = 20 * time.Minute
	arenaScen   = "botarenas3"
)

// arenaBotNames are the bots' names on the server, by slot.
func arenaBotNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("Bot%d", i+1)
	}
	return out
}

// arenaRunNames are the robot names of their runs (they match the store's run directory names).
func arenaRunNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("ipmbot-a%d", i+1)
	}
	return out
}

// arenaJoinDelay is how long after the first bot's login bot i logs in: the logins one after another.
func arenaJoinDelay(i int) time.Duration { return time.Duration(i) * 3 * time.Second }

// arenaServerSetup is the server's setup, once: peaceful, no monsters. The clock and the weather are the
// world's own.
func arenaServerSetup() []string {
	return []string{"difficulty peaceful", "gamerule spawn_monsters false", "scoreboard objectives add deaths deathCount"}
}

// arenaBotSetup puts a bot at the start of its arena (centred on ox) and keeps it there as its spawn, with
// nothing carried and full health and food.
func arenaBotSetup(name string, ox int) []string {
	at := fmt.Sprintf("%d %s 0", ox, fy(0))
	return []string{
		"tp " + name + " " + at,
		"spawnpoint " + name + " " + at,
		"clear " + name,
		"effect give " + name + " minecraft:instant_health 1 10",
		"effect give " + name + " minecraft:saturation 1 10",
		"scoreboard players set " + name + " deaths 0",
	}
}

// arenaJob is the job the wave's runs share: the bench's (MC26_JOB), else one made from the time.
func arenaJob(env string, at time.Time) string {
	if env != "" {
		return env
	}
	return arenaScen + "-" + at.Format("20060102-150405")
}

// arenaVerdict judges a bot at the end of its run: alive, and not stood still as long as the limit.
func arenaVerdict(deaths string, longestStill time.Duration) error {
	if deaths != "0" {
		return fmt.Errorf("died (deaths %s)", deaths)
	}
	if longestStill >= botDayStillLimit {
		return fmt.Errorf("stalled: still %s (a hard limit)", longestStill.Round(time.Second))
	}
	return nil
}

// scenarioBotArenas runs the three bots on one server in the cached world of three walled arenas.
func scenarioBotArenas(o Options) error {
	scenario := arenaScen
	from := worldFrom(o.Version)
	if from == "" {
		from = o.Version
	}
	shared, err := sharedArenaWorld(o, from, arenaRecipeHash(arenaBots), arenaBots)
	if err != nil {
		return fmt.Errorf("the arenas' world of %s: %w", from, err)
	}
	dir := filepath.Join(o.WorkDir, "server-"+scenario)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := copyDir(filepath.Join(shared, "world"), filepath.Join(dir, "world")); err != nil {
		return err
	}
	bin, err := buildBot(o, scenario)
	if err != nil {
		return err
	}
	if os.Getenv("MC26_JOB") == "" { // newRun reads it: the runs of this wave are one job
		job := arenaJob("", time.Now())
		os.Setenv("MC26_JOB", job)
		defer os.Unsetenv("MC26_JOB")
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
	for _, c := range arenaServerSetup() {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	names, runNames := arenaBotNames(arenaBots), arenaRunNames(arenaBots)
	errs := make([]error, arenaBots)
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := range names {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(arenaJoinDelay(i))
			errs[i] = arenaBotRun(o, rcon, srv, bin, names[i], runNames[i], (i+1)*flatApart)
		}(i)
	}
	wg.Wait()
	var failed []string
	for i, err := range errs {
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", names[i], err))
		}
	}
	o.Log("%s: %d of %d bots passed after %s", scenario, arenaBots-len(failed), arenaBots, time.Since(t0).Round(time.Second))
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "\n"))
	}
	return nil
}

// arenaBotRun is one bot's run in its arena (centred on ox): the process, its run, its setup after the
// login, and the watch to the end of the length. It mirrors runBotDay's loop; the pieces they share are
// small and tied to its locals, so they are copied, not extracted.
func arenaBotRun(o Options, rcon *rconClient, srv *smoke.Server, bin, name, runName string, ox int) error {
	scenario := arenaScen + "/" + name
	lf, err := os.Create(filepath.Join(o.WorkDir, arenaScen+"-"+name+".log"))
	if err != nil {
		return err
	}
	defer lf.Close()
	out := &syncBuffer{}
	args := []string{"-address", srv.Addr(), "-name", name}
	run, err := newRun(o, arenaScen, runName, srv)
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
	proc := watchProc(cmd)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := readBotLines(pr, func(line string) {
			fmt.Fprintln(lf, line)
			out.Write([]byte(line + "\n"))
			if _, own := botSays(line); own || strings.Contains(line, "panic") {
				o.Log("%s: bot: %s", scenario, line)
			}
		}); err != nil {
			o.Log("%s: the bot's output: %v", scenario, err)
		}
	}()
	defer func() {
		proc.stop(cmd)
		pw.Close()
		wg.Wait()
	}()
	if err := waitFor(out, 90*time.Second, "loaded"); err != nil {
		return fmt.Errorf("the bot did not join: %v\n%s", err, tail(out.String(), 20))
	}
	if err := waitBotPrefix(out, 30*time.Second); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 20))
	}
	for _, c := range arenaBotSetup(name, ox) {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	o.Log("%s: the bot in its arena at x %d", scenario, ox)

	start := time.Now()
	var lastPos string
	stillSince, longestStill := time.Now(), time.Duration(0)
	deaths := "0"
	defer func() {
		d, _ := strconv.Atoi(deaths)
		run.score(map[string]any{"arena": ox, "mobs": mobsOff, "difficulty": "peaceful"},
			map[string]any{"deaths": d, "longestStill": int(longestStill.Seconds()), "length": int(time.Since(start).Seconds())})
	}()
	for time.Since(start) < arenaLength {
		time.Sleep(10 * time.Second)
		if proc.ended() {
			time.Sleep(200 * time.Millisecond)
			return fmt.Errorf("%s", proc.said(scenario, out.String()))
		}
		if resp, err := rcon.command("scoreboard players get " + name + " deaths"); err == nil {
			if f := strings.Fields(resp); len(f) >= 3 && f[1] == "has" {
				if f[2] != deaths {
					o.Log("%s: DIED (%s) after %s", scenario, f[2], time.Since(start).Round(time.Second))
				}
				deaths = f[2]
			}
		}
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
		if still := time.Since(stillSince); still >= botDayStillLimit {
			return fmt.Errorf("stalled: still %s at %s (a hard limit)\n%s", still.Round(time.Second), lastPos, tail(out.String(), 30))
		}
	}
	if err := arenaVerdict(deaths, longestStill); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 25))
	}
	o.Log("%s: ok after %s, deaths %s, longest still %s", scenario, time.Since(start).Round(time.Second), deaths, longestStill.Round(time.Second))
	return nil
}
