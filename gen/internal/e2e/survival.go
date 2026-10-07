package e2e

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// survivalSeed is the seed of the ordinary world the survival scenario plays
// in (its spawn is on a treeless mountain; the robot starts in the nearest
// forest).
const survivalSeed = "26265"

// worldSeed is the seed of the ordinary world: MC26_SEED, to play another
// world (or the one of a kept run again, its meta.json's seed), else
// survivalSeed.
func worldSeed() string {
	if s := os.Getenv("MC26_SEED"); s != "" {
		return s
	}
	return survivalSeed
}

// scenarioSurvival starts a second server, of ordinary terrain (a fixed seed,
// generated anew every run): slopes, trees with leaves, tall grass and
// flowers, water, sand, caves — nothing built for the robot. It spawns there
// with nothing and must reach the flat world's goals: a wooden pickaxe,
// cobblestone, a hut around itself, without a complaint from the server.
// forestStart starts a server of the survival world (generated anew), the
// robot joined and put in the world's nearest forest, the clock at noon and
// stopped — or, with cycle, running from the morning as it does. done stops
// them both.
func forestStart(o Options, bin, name, logName string, cycle bool) (srv *smoke.Server, r *robotBot, rcon *rconClient, done func(), err error) {
	var closers []func()
	done = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	defer func() {
		if err != nil {
			done()
		}
	}()
	dir := filepath.Join(o.WorkDir, "server-survival")
	if err = os.RemoveAll(filepath.Join(dir, "world")); err != nil {
		return
	}
	// one version's world for all (worldFrom): a copy, its forest known
	var forest *[2]int
	if from := worldFrom(o.Version); from != "" {
		var shared string
		var at [2]int
		if shared, at, err = sharedWorld(o, from, worldSeed()); err != nil {
			err = fmt.Errorf("the world of %s: %w", from, err)
			return
		}
		if err = copyDir(filepath.Join(shared, "world"), filepath.Join(dir, "world")); err != nil {
			return
		}
		forest = &at
	}
	srv = &smoke.Server{
		Version: o.Version, JarPath: o.JarPath, WorkDir: dir,
		Port: o.Port, Runtime: o.Runtime, Log: o.Log,
		LevelType: "minecraft:normal", Seed: worldSeed(),
	}
	if err = servers.start(srv); err != nil {
		return
	}
	closers = append(closers, func() { servers.stop(srv) })
	if err = srv.WaitReady(6 * time.Minute); err != nil {
		return
	}
	_ = os.Remove(strings.TrimSuffix(filepath.Join(o.WorkDir, logName), ".log") + ".memory.json") // a new world: nothing remembered
	if r, err = startRobot(o, bin, filepath.Join(o.WorkDir, logName), srv, name); err != nil {
		return
	}
	closers = append(closers, r.close)
	if err = waitFor(r.out, 60*time.Second, "Login success", "robot: loaded"); err != nil {
		err = fmt.Errorf("%v\n%s", err, tail(r.out.String(), 15))
		return
	}
	if rcon, err = dialRCON(srv.RCONAddr(), srv.RCONPassword); err != nil {
		err = fmt.Errorf("rcon: %w", err)
		return
	}
	closers = append(closers, func() { rcon.close() })
	if os.Getenv("MC26_PLAY_ADDR") != "" {
		stop := watchVisitors(o, srv, name)
		closers = append(closers, stop)
	}
	clock := []string{"time set 0"} // the morning, the clock running
	if !cycle {
		// noon, the clock stopped: the rule is advance_time in 26.x (it was
		// doDaylightCycle before)
		clock = []string{"time set noon", "gamerule advance_time false"}
	}
	for _, c := range clock {
		var resp string
		if resp, err = rcon.command(c); err != nil || rconFailed(resp) {
			err = fmt.Errorf("rcon %q: %q %v", c, resp, err)
			return
		}
	}
	if _, err = r.ask("wait 40", 10*time.Second); err != nil {
		return
	}
	// the nearest forest of the world, the robot on its ground: a start a
	// person would choose, the same every run (the world is the seed's)
	var m []string
	if forest != nil {
		m = []string{"", strconv.Itoa(forest[0]), strconv.Itoa(forest[1])}
	} else {
		var resp string
		if resp, err = rcon.command("locate biome minecraft:forest"); err != nil {
			return
		}
		if m = regexp.MustCompile(`\[(-?\d+), [^,]+, (-?\d+)\]`).FindStringSubmatch(resp); m == nil {
			err = fmt.Errorf("no forest: %q", resp)
			return
		}
	}
	if forest != nil {
		// the same place every version: spreadplayers picks one at random
		err = toGround(rcon, name, forest[0], forest[1])
	} else if resp, e := rcon.command("spreadplayers " + m[1] + " " + m[2] + " 0 1 false " + name); e != nil || !strings.Contains(resp, "Spread") {
		err = fmt.Errorf("%q %v", resp, e)
	}
	if err != nil {
		err = fmt.Errorf("to the forest at %s %s: %w", m[1], m[2], err)
		return
	}
	if _, err = r.ask("wait 60", 10*time.Second); err != nil {
		return
	}
	if _, err = r.agree(rcon, name); err != nil {
		err = fmt.Errorf("in the forest: %w", err)
		return
	}
	return srv, r, rcon, done, nil
}

func scenarioSurvival(o Options, bin string) error {
	const name = "Settler"
	srv, r, rcon, done, err := forestStart(o, bin, name, "survival.log", false)
	if err != nil {
		return err
	}
	defer done()
	defer func() { r.close() }() // the robot as it is at the end (started again after a relog)
	ask := func(line string, timeout time.Duration) (string, error) {
		return relogAsk(o, bin, filepath.Join(o.WorkDir, "survival.log"), srv, name, &r, line, timeout)
	}
	p, _ := r.pos()
	o.Log("survival: the robot starts at %.1f %.1f %.1f", p[0], p[1], p[2])
	// real terrain: chunk sections with big palettes, many block states; the
	// robot's view of a cube around it, judged block by block
	var wrong []string
	checked := 0
	cx, cy, cz := int(math.Floor(p[0])), int(math.Floor(p[1])), int(math.Floor(p[2]))
	for dy := -20; dy <= 12; dy += 4 {
		for dx := -24; dx <= 24; dx += 6 {
			for dz := -24; dz <= 24; dz += 6 {
				at := fmt.Sprintf("%d %d %d", cx+dx, cy+dy, cz+dz)
				got, err := ask("block "+at, 5*time.Second)
				for try := 0; err != nil && strings.Contains(err.Error(), "not loaded") && try < 10; try++ {
					ask("wait 20", 5*time.Second) // the chunks come in batches
					got, err = ask("block "+at, 5*time.Second)
				}
				if err != nil {
					return err
				}
				checked++
				if resp, err := rcon.command("execute if block " + at + " " + got); err != nil || !strings.Contains(resp, "passed") {
					wrong = append(wrong, fmt.Sprintf("%s: the robot sees %s", at, got))
				}
			}
		}
	}
	if len(wrong) > 0 {
		return fmt.Errorf("the robot sees %d of %d blocks wrong:\n%s", len(wrong), checked, strings.Join(wrong[:min(len(wrong), 20)], "\n"))
	}
	if f, err := ask("find oak_log birch_log", 10*time.Second); err == nil {
		o.Log("survival: the nearest log: %s", f)
	}
	if _, err := ask("get wooden_pickaxe", 6*time.Minute); err != nil {
		return fmt.Errorf("get a wooden pickaxe: %w\n%s", err, tail(r.out.String(), 8))
	}
	if resp, err := rcon.command("data get entity " + name + " Inventory"); err != nil || itemCounts(resp)["minecraft:wooden_pickaxe"] < 1 {
		return fmt.Errorf("after get wooden_pickaxe the server has %q (%v)", resp, err)
	}
	if _, err := ask("get cobblestone 25", 8*time.Minute); err != nil {
		return fmt.Errorf("get cobblestone: %w\n%s", err, tail(r.out.String(), 8))
	}
	// torches: coal ore if some is in sight, charcoal from a furnace else
	if _, err := ask("get torch 4", 6*time.Minute); err != nil {
		return fmt.Errorf("get torches: %w\n%s", err, tail(r.out.String(), 8))
	}
	if resp, err := rcon.command("data get entity " + name + " Inventory"); err != nil || itemCounts(resp)["minecraft:torch"] < 4 {
		return fmt.Errorf("after get torch the server has %q (%v)", resp, err)
	}
	// wood for the night's crafting (pickaxes, a crafting table down below),
	// got while there are trees
	for _, g := range []string{"get stick 8", "get oak_log 16"} {
		if _, err := ask(g, 4*time.Minute); err != nil {
			return fmt.Errorf("%s: %w\n%s", g, err, tail(r.out.String(), 8))
		}
	}
	// the shelter for the night: a room three by three dug into a hill,
	// lit, its way in shut (a hut built around it on open ground)
	ans, err := ask("shelter", 5*time.Minute)
	if err != nil {
		return fmt.Errorf("shelter: %w\n%s", err, tail(r.out.String(), 8))
	}
	o.Log("survival: shelter %s", ans)
	var how string
	var x, y, z, fdx, fdz int
	if _, err := fmt.Sscanf(ans, "%s at %d %d %d facing %d %d", &how, &x, &y, &z, &fdx, &fdz); err != nil {
		return fmt.Errorf("shelter answered %q", ans)
	}
	// going in: right is (-fdz, fdx); a dug room's tunnel leaves the far wall
	tunnel := func(dx, dz, dy int) bool {
		return how == "dug" && dx == 2*fdx && dz == 2*fdz && dy >= 0 && dy <= 1
	}
	if _, err := ask("wait 10", 5*time.Second); err != nil { // its last steps reach the server
		return err
	}
	if _, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("in the shelter: %w", err)
	}
	// closed: the room three by three and two high open, everything round it
	// (walls, the way in, floor, roof) something a monster does not pass
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			for dy := -1; dy <= 2; dy++ {
				inside := dx >= -1 && dx <= 1 && dz >= -1 && dz <= 1 && dy >= 0 && dy <= 1
				corner := (dx == -2 || dx == 2) && (dz == -2 || dz == 2)
				if inside || corner || tunnel(dx, dz, dy) {
					continue
				}
				at := fmt.Sprintf("%d %d %d", x+dx, y+dy, z+dz)
				if resp, err := rcon.command("execute if block " + at + " #minecraft:replaceable"); err != nil || !strings.Contains(resp, "failed") {
					return fmt.Errorf("the shelter at %d %d %d is open at %s (%q %v)", x, y, z, at, resp, err)
				}
			}
		}
	}
	// furnished along the right wall, going in: crafting table, chest, furnace
	rx, rz := -fdz, fdx
	for k, kind := range []string{"crafting_table", "chest", "furnace"} {
		along := k - 1
		at := fmt.Sprintf("%d %d %d", x+rx+along*fdx, y, z+rz+along*fdz)
		if resp, err := rcon.command("execute if block " + at + " minecraft:" + kind); err != nil || !strings.Contains(resp, "passed") {
			return fmt.Errorf("the shelter at %d %d %d has no %s at %s: %q %v", x, y, z, kind, at, resp, err)
		}
		if kind == "chest" {
			resp, _ := rcon.command("data get block " + at + " Items")
			o.Log("survival: the chest holds %v", itemCounts(resp))
		}
	}
	// lit by its torches: light enough that nothing spawns inside (a
	// monster needs block light 0)
	// the inline predicate's kind is "type" from 26.3, "condition" before
	var dark string
	for _, key := range []string{"type", "condition"} {
		pred := `{` + key + `:"minecraft:location_check",predicate:{light:{light:{min:8}}}}`
		if dark, err = rcon.command(fmt.Sprintf("execute positioned %d %d %d if predicate %s", x, y, z, pred)); err != nil {
			return err
		}
		if !strings.Contains(dark, "Failed to parse") {
			break
		}
	}
	if !strings.Contains(dark, "passed") {
		return fmt.Errorf("the shelter is dark at %d %d %d: %q", x, y, z, dark)
	}
	// stone pickaxes for the way down (one wears out after 131 blocks), made
	// at the shelter's crafting table
	if _, err := ask("get stone_pickaxe 3", 4*time.Minute); err != nil {
		return fmt.Errorf("get stone pickaxes: %w\n%s", err, tail(r.out.String(), 8))
	}
	if resp, err := rcon.command("data get entity " + name + " Inventory"); err != nil || itemCounts(resp)["minecraft:stone_pickaxe"] < 3 {
		return fmt.Errorf("after get stone_pickaxe 3 the server has %q (%v)", resp, err)
	}
	// the night, on normal difficulty: the robot goes down for the ores,
	// a staircase from the shelter, lit as it goes — a night that passes
	// (the clock running again): one held at dusk keeps its mobs for good
	for _, c := range []string{"difficulty normal", "time set 13000", "gamerule advance_time true"} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	// on from the tunnel into the hill, or from a built hut's floor (a step
	// at a time, ahead: the robot digs nothing under itself)
	down, err := ask("descend 16", 12*time.Minute)
	if err != nil {
		return fmt.Errorf("descend: %w\n%s", err, tail(r.out.String(), 10))
	}
	o.Log("survival: %s", down)
	at, err := r.agree(rcon, name)
	if err != nil {
		return fmt.Errorf("down the stairs: %w", err)
	}
	// down to 16 — or, the night over first (the robot goes up to the day's
	// work at morning; a night is ten real minutes, a hill full of ore
	// slower), well under the shelter
	// (or within six of it: every way on down dangerous, it stops near
	// enough — descend's own rule); the morning come first, a night's
	// stairs at least 60 under the shelter (a shelter high on a mountain has
	// further to go than a night)
	if at[1] > 22.5 && !(strings.Contains(down, "(morning)") && at[1] <= float64(y)-60+0.5) {
		return fmt.Errorf("descend answered %q, the robot is at %v", down, at)
	}
	// the mines below, the iron pickaxe, the diamonds and the days after
	// are the days scenario's (e2e --only days)
	if resp, err := rcon.command("data get entity " + name + " Health"); err != nil || strings.Contains(resp, " 0.0f") {
		return fmt.Errorf("the robot's health: %q %v", resp, err)
	} else {
		o.Log("survival: %s", resp)
	}
	if bad := complaints(serverLogFrom(srv, 0), name); len(bad) > 0 {
		return fmt.Errorf("the server objected:\n%s", strings.Join(bad, "\n"))
	}
	return nil
}

// watchVisitors puts every player who joins but the robot into spectator mode,
// as soon as it sees them (every five seconds), over an RCON connection of its
// own: a person come to watch (MC26_PLAY_ADDR) neither gets in the robot's way
// nor is fought. It returns its stop.
func watchVisitors(o Options, srv *smoke.Server, robot string) func() {
	rc, err := dialRCON(srv.RCONAddr(), srv.RCONPassword)
	if err != nil {
		o.Log("visitors: rcon: %v", err)
		return func() {}
	}
	quit := make(chan struct{})
	go func() {
		defer rc.close()
		seen := map[string]bool{}
		for {
			select {
			case <-quit:
				return
			case <-time.After(5 * time.Second):
			}
			resp, err := rc.command("list")
			if err != nil {
				continue
			}
			// "There are N of a max of M players online: a, b"
			i := strings.LastIndex(resp, ":")
			if i < 0 {
				continue
			}
			for _, n := range strings.Split(resp[i+1:], ",") {
				n = strings.TrimSpace(n)
				if n == "" || n == robot || seen[n] {
					continue
				}
				seen[n] = true
				if _, err := rc.command("gamemode spectator " + n); err == nil {
					o.Log("visitors: %s joined, a spectator", n)
				}
			}
		}
	}()
	return func() { close(quit) }
}

// relogAsk is ask on the robot *rp in a world of its own: a robot that ended
// itself to relog (its world and the server's disagreed) is started again at
// once, from its memory, and the command tried again — maxRelogs times at
// most, as the days do.
func relogAsk(o Options, bin, logPath string, srv *smoke.Server, name string, rp **robotBot, line string, timeout time.Duration) (string, error) {
	for relogs := 0; ; relogs++ {
		r := *rp
		ans, err := r.ask(line, timeout)
		if err == nil || relogs >= maxRelogs || !strings.Contains(r.out.String(), "robot: relog:") {
			return ans, err
		}
		o.Log("%s: a relog on %q (%d of %d): started again", strings.TrimSuffix(filepath.Base(logPath), ".log"), line, relogs+1, maxRelogs)
		r.close()
		nr, serr := startRobot(o, bin, logPath, srv, name)
		if serr != nil {
			return "", fmt.Errorf("%w; started again: %v", err, serr)
		}
		if werr := waitFor(nr.out, 60*time.Second, "Login success", "robot: loaded"); werr != nil {
			nr.close()
			return "", fmt.Errorf("%w; started again: %v", err, werr)
		}
		*rp = nr
	}
}

// toGround puts name on the ground nearest x, z — grass or dirt under the
// sky's heightmap (leaves not counted: under them), looked for round x, z in
// a fixed order — facing south: the same place in every version's copy of
// the world.
func toGround(rcon *rconClient, name string, x, z int) error {
	// the chunks looked in loaded for the look (a player far off has not)
	area := fmt.Sprintf("%d %d %d %d", x-6, z-6, x+6, z+6)
	if resp, err := rcon.command("forceload add " + area); err != nil || rconFailed(resp) {
		return fmt.Errorf("forceload: %q %v", resp, err)
	}
	defer rcon.command("forceload remove " + area)
	for _, c := range [][2]int{{x - 6, z - 6}, {x + 6, z + 6}, {x - 6, z + 6}, {x + 6, z - 6}} {
		for try := 0; ; try++ {
			resp, err := rcon.command(fmt.Sprintf("execute if loaded %d 64 %d", c[0], c[1]))
			if err != nil {
				return err
			}
			if strings.Contains(resp, "passed") {
				break
			}
			if try == 60 {
				return fmt.Errorf("%d %d not loaded after 30s: %q", c[0], c[1], resp)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	first := ""
	for ring := 0; ring <= 6; ring++ {
		for dx := -ring; dx <= ring; dx++ {
			for dz := -ring; dz <= ring; dz++ {
				if max(dx, -dx, dz, -dz) != ring {
					continue
				}
				// whole x and z: positioned is the block's centre
				at := fmt.Sprintf("execute positioned %d 0 %d positioned over motion_blocking_no_leaves ", x+dx, z+dz)
				// grass, podzol, mycelium or dirt (#dirt is dirt alone in 26.x)
				var resp string
				for _, tag := range []string{"#minecraft:grass_blocks", "#minecraft:dirt"} {
					var err error
					if resp, err = rcon.command(at + "if block ~ ~-1 ~ " + tag); err != nil {
						return err
					}
					if strings.Contains(resp, "passed") {
						break
					}
				}
				if !strings.Contains(resp, "passed") {
					if first == "" {
						first = resp
					}
					continue
				}
				if resp, err := rcon.command(at + "run tp " + name + " ~ ~ ~ 0 0"); err != nil || rconFailed(resp) {
					return fmt.Errorf("tp: %q %v", resp, err)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("no grass or dirt within 6 of %d %d (at it: %q)", x, z, first)
}
