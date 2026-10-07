package e2e

import (
	"context"
	"encoding/json"
	"errors"
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
	"syscall"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// robotBot is a running examples/robot: commands go to its stdin, and each
// answer is a log line "robot: <command> <answer>" (or "robot: error <command> …").
type robotBot struct {
	log   string // where close writes what the robot printed
	cmd   *exec.Cmd
	out   *syncBuffer
	stdin io.WriteCloser
	stop  context.CancelFunc
	done  chan struct{} // closed when the robot's process ended
	run   *run          // where its events are kept, nil if not
	once  sync.Once     // close's: a held robot is closed when held and at the end
}

func startRobot(o Options, bin, logPath string, srv *smoke.Server, name string) (*robotBot, error) {
	// twelve hours at most: the week scenario lives two weeks if asked
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	// its events beside its log, for go-mc26-robotview to follow — kept,
	// with what was run, in a run of their own (runs.go), the file beside the
	// log a link to it
	events := strings.TrimSuffix(logPath, ".log") + ".events.jsonl"
	_ = os.Remove(events)
	run, err := newRun(o, strings.TrimSuffix(filepath.Base(logPath), ".log"), name, srv)
	if err != nil {
		o.Log("runs: %v (events beside the log only)", err)
	} else if run != nil {
		if err := os.Symlink(run.events(), events); err != nil {
			o.Log("runs: %v", err)
		}
		events = run.events()
	}
	// its memory beside its log too: a robot started again in the same run
	// (held and resumed) goes on from it; a run's first starts without one
	memory := strings.TrimSuffix(logPath, ".log") + ".memory.json"
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "robot"), "-address", srv.Addr(), "-name", name, "-events", events, "-memory", memory)
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	b := &robotBot{log: logPath, cmd: cmd, out: out, stdin: stdin, stop: cancel, done: make(chan struct{}), run: run}
	go func() { _ = cmd.Wait(); close(b.done) }()
	running.add(b)
	return b, nil
}

// running is the robots started and not yet closed: when the harness is
// stopped (SIGTERM, a test bench's pod going; Ctrl-C) each is closed as a
// scenario's end would, so its run keeps robot.log and says when it ended.
var running = &robotSet{bots: map[*robotBot]bool{}}

type robotSet struct {
	mu   sync.Mutex
	bots map[*robotBot]bool
}

func (s *robotSet) add(b *robotBot)    { s.mu.Lock(); s.bots[b] = true; s.mu.Unlock() }
func (s *robotSet) remove(b *robotBot) { s.mu.Lock(); delete(s.bots, b); s.mu.Unlock() }

// closeAll closes every robot still running.
func (s *robotSet) closeAll() {
	s.mu.Lock()
	bots := make([]*robotBot, 0, len(s.bots))
	for b := range s.bots {
		bots = append(bots, b)
	}
	s.mu.Unlock()
	for _, b := range bots {
		b.close()
	}
}

// ask types a command on the robot's console and returns its answer: the rest
// of the first "robot: <name> " line printed after the command was typed.
func (b *robotBot) ask(line string, timeout time.Duration) (string, error) {
	name := strings.Fields(line)[0]
	from := len(b.out.String())
	if _, err := io.WriteString(b.stdin, line+"\n"); err != nil {
		return "", err
	}
	answer := regexp.MustCompile(`robot: (error )?` + regexp.QuoteMeta(name) + `:?( .*)?\n`)
	deadline := time.Now().Add(timeout)
	for {
		if m := answer.FindStringSubmatch(b.out.String()[from:]); m != nil {
			if m[1] != "" {
				return "", fmt.Errorf("robot %q: %s", line, strings.TrimSpace(m[2]))
			}
			return strings.TrimSpace(m[2]), nil
		}
		select {
		case <-b.done:
			return "", fmt.Errorf("robot %q: the robot exited (%v)\n%s", line, b.cmd.ProcessState, tail(b.out.String()[from:], 10))
		default:
		}
		if time.Now().After(deadline) {
			// what it was doing: its last lines, and where in its own code it
			// is (the robot logs every goroutine's stack on SIGUSR1)
			before := b.out.String()[from:]
			if b.cmd.Process != nil {
				b.cmd.Process.Signal(syscall.SIGUSR1)
				time.Sleep(2 * time.Second)
			}
			dump := b.out.String()[from+len(before):]
			return "", fmt.Errorf("robot %q: no answer in %s\n%s\nwhere, in the robot's code:\n%s", line, timeout, tail(before, 15), robotFrames(dump, 20))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (b *robotBot) close() { b.once.Do(b.closeNow) }

func (b *robotBot) closeNow() {
	_, _ = io.WriteString(b.stdin, "quit\n")
	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
		b.stop()
		<-b.done
	}
	_ = b.stdin.Close()
	_ = os.WriteFile(b.log, []byte(b.out.String()), 0o644)
	b.run.end(b.out.String())
	running.remove(b)
}

// robotPos asks the robot where it is: x, y, z.
func (b *robotBot) pos() ([3]float64, error) {
	ans, err := b.ask("pos", 5*time.Second)
	if err != nil {
		return [3]float64{}, err
	}
	f := strings.Fields(ans)
	if len(f) < 3 {
		return [3]float64{}, fmt.Errorf("pos answered %q", ans)
	}
	var p [3]float64
	for i := range p {
		if p[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return p, fmt.Errorf("pos answered %q: %v", ans, err)
		}
	}
	return p, nil
}

// serverLogFrom returns what the server logged after offset bytes.
func serverLogFrom(srv *smoke.Server, offset int64) string {
	b, err := os.ReadFile(srv.LogPath())
	if err != nil || int64(len(b)) < offset {
		return ""
	}
	return string(b[offset:])
}

func serverLogSize(srv *smoke.Server) int64 {
	fi, err := os.Stat(srv.LogPath())
	if err != nil {
		return 0
	}
	return fi.Size()
}

// complaints returns the lines of a server log in which the server objected
// to what the named player's client did.
func complaints(log, name string) []string {
	var out []string
	for line := range strings.SplitSeq(log, "\n") {
		// (a vehicle it steers: "Oak Boat (vehicle of Robot) moved wrongly!")
		for _, bad := range []string{name + " moved wrongly", name + " moved too quickly", name + ") moved wrongly", name + ") moved too quickly", name + " was kicked", "Disconnecting " + name, name + " lost connection: Flying"} {
			if strings.Contains(line, bad) {
				out = append(out, line)
			}
		}
	}
	return out
}

// samePos reports whether the robot's and the server's positions agree to
// within what the answers print.
func samePos(a, b [3]float64) bool {
	for k := range a {
		if math.Abs(a[k]-b[k]) > 1e-3 {
			return false
		}
	}
	return true
}

// scenarioRobot drives examples/robot: it joins and tells the server it
// loaded the level, ticks like the vanilla client, agrees with the server
// about where it is before and after a teleport, and stays online without a
// complaint from the server.
func scenarioRobot(o Options, bin string, srv *smoke.Server) error {
	const name = "Robot"
	var (
		r    *robotBot
		rcon *rconClient
	)
	agree := func(when string) error {
		if _, err := r.agree(rcon, name); err != nil {
			return fmt.Errorf("%s: %w", when, err)
		}
		return nil
	}

	steps := []struct {
		name string
		fn   func() error
	}{
		{"teleport", func() error { return robotTeleport(r, rcon, name, agree) }},
		{"world", func() error { return robotWorld(r, rcon) }},
		{"moves", func() error { return robotMoves(r, rcon, name) }},
		{"state", func() error { return robotState(r, rcon, name) }},
		{"paths", func() error { return robotPaths(r, rcon, name, bin, srv) }},
		{"dig", func() error { return robotDig(r, rcon, name) }},
		{"place", func() error { return robotPlaceUse(r, rcon, name) }},
		{"craft", func() error { return robotCraft(r, rcon, name) }},
		{"fight", func() error { return robotFight(r, rcon, name) }},
		{"goals", func() error { return robotGoals(o, r, rcon, name) }},
		{"hash", func() error { return robotHash(r, rcon, name) }},
		{"creatures", func() error { return robotCreatures(r, rcon, name) }},
		{"current", func() error { return robotCurrent(o, r, rcon, name) }},
		{"items", func() error { return robotItems(o, r, rcon, name) }},
		{"entities", func() error { return robotEntities(o, r, rcon, name) }},
		{"dimensions", func() error { return robotDimensions(r, rcon, name) }},
		{"chat", func() error { return robotChat(r, rcon, name, bin, srv) }},
		{"farm", func() error { return robotFarm(r, rcon, name) }},
		{"sleep", func() error { return robotSleep(r, rcon, name) }},
		{"minecart", func() error { return robotMinecart(r, rcon, name) }},
		{"boat", func() error { return robotBoat(r, rcon, name, srv) }},
	}
	// MC26_ROBOT_STEPS=fight,craft runs just those steps: for work on one (a
	// name not known would run none and pass)
	only := map[string]bool{}
	var known []string
	for _, st := range steps {
		known = append(known, st.name)
	}
	for _, s := range strings.Split(os.Getenv("MC26_ROBOT_STEPS"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if !slices.Contains(known, s) {
			return fmt.Errorf("MC26_ROBOT_STEPS: no step %q (known: %s)", s, strings.Join(known, ", "))
		}
		only[s] = true
	}

	logStart := serverLogSize(srv)
	joined := time.Now()
	var err error
	// nothing remembered from an earlier run: its search, its map (the course
	// is laid out afresh each time; a search left at ring 4 went on looking
	// far away for the logs beside it)
	_ = os.Remove(filepath.Join(o.WorkDir, "robot.memory.json"))
	if r, err = startRobot(o, bin, filepath.Join(o.WorkDir, "robot.log"), srv, name); err != nil {
		return err
	}
	defer r.close()
	if err := waitFor(r.out, 40*time.Second, "Login success", "robot: loaded"); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(r.out.String(), 15))
	}
	if rcon, err = dialRCON(srv.RCONAddr(), srv.RCONPassword); err != nil {
		return fmt.Errorf("rcon: %w", err)
	}
	defer rcon.close()
	// the player is saved with the world: start every run from a plain one
	// (one that left riding a vehicle joins in it again)
	for _, c := range []string{"ride " + name + " dismount", "effect clear " + name, "clear " + name, "xp set " + name + " 0 levels", "gamemode survival " + name} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	// the course by day, the clock stopped: it is not about the night (the
	// sleep step runs the clock for its night), and the robot walls itself
	// in under a night sky — a long step reached the dusk mid-maze
	// and its mobs only those its steps summon: what earlier runs left in
	// the world taken away (zombie horses whose riders burned, slimes, which
	// a flat world spawns in any light), no monster spawning of itself
	for _, c := range []string{"time set noon", "gamerule advance_time false", "gamerule spawn_monsters false",
		"kill @e[type=!minecraft:player]"} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	defer rcon.command("gamerule spawn_monsters true")
	defer rcon.command("gamerule advance_time true")
	for _, st := range steps {
		if len(only) > 0 && !only[st.name] {
			continue
		}
		if err := st.fn(); err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
	}
	if len(only) > 0 {
		return nil
	}

	// a minute online: keep-alives, position reminders, tick ends
	time.Sleep(time.Until(joined.Add(60 * time.Second)))
	state, err := r.ask("state", 5*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(state, "loaded=true") {
		return fmt.Errorf("state after a minute: %s", state)
	}
	if resp, err := rcon.command("list"); err != nil || !strings.Contains(resp, name) {
		return fmt.Errorf("after a minute the robot is not online: %q %v", resp, err)
	}
	if err := agree("after a minute"); err != nil {
		return err
	}
	if bad := complaints(serverLogFrom(srv, logStart), name); len(bad) > 0 {
		return fmt.Errorf("the server objected:\n%s", strings.Join(bad, "\n"))
	}
	return nil
}

// robotWorld checks the robot's view of the blocks: what the chunks brought,
// a single block changed (ClientboundBlockUpdate) and a filled area
// (ClientboundSectionBlocksUpdate). The robot answers `block x y z` with the
// full state, and the server judges the answer with `execute if block`.
func robotWorld(r *robotBot, rcon *rconClient) error {
	check := func(x, y, z int, want string) error {
		at := fmt.Sprintf("%d %d %d", x, y, z)
		got, err := r.ask("block "+at, 5*time.Second)
		if err != nil {
			return err
		}
		if want != "" && !strings.HasPrefix(got, want) {
			return fmt.Errorf("block %s: the robot sees %q, want %s", at, got, want)
		}
		resp, err := rcon.command("execute if block " + at + " " + got)
		if err != nil {
			return err
		}
		if !strings.Contains(resp, "Test passed") {
			return fmt.Errorf("block %s: the robot sees %q, the server says %q", at, got, resp)
		}
		return nil
	}
	// the flat world's layers, from the chunk packets
	for _, c := range []struct {
		y    int
		want string
	}{{-64, "minecraft:bedrock"}, {-62, "minecraft:dirt"}, {-61, "minecraft:grass_block"}, {-60, "minecraft:air"}} {
		if err := check(8, c.y, 8, c.want); err != nil {
			return err
		}
	}
	commands := []string{
		"fill 8 -60 8 10 -58 10 minecraft:stone",                        // 27 blocks in one section: a section update
		"setblock 12 -60 8 minecraft:oak_stairs[facing=east,half=top]",  // one block: a block update
		"setblock 12 -60 9 minecraft:redstone_wire[north=side,power=7]", // a state with many properties
		"setblock 9 -59 9 minecraft:air",                                // a hole in the filled cube
	}
	for _, c := range commands {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	time.Sleep(500 * time.Millisecond) // the updates travel
	for _, c := range []struct {
		x, y, z int
		want    string
	}{
		{8, -60, 8, "minecraft:stone"}, {10, -58, 10, "minecraft:stone"}, {9, -59, 9, "minecraft:air"},
		{12, -60, 8, "minecraft:oak_stairs[facing=east,half=top"}, {12, -60, 9, "minecraft:redstone_wire"},
	} {
		if err := check(c.x, c.y, c.z, c.want); err != nil {
			return err
		}
	}
	// and back, so a later step finds the ground as the world made it
	if _, err := rcon.command("fill 8 -60 8 12 -58 10 minecraft:air"); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return check(12, -60, 8, "minecraft:air")
}

// robotMoves runs the robot through a course built over RCON, one station at
// a time: it is teleported to the start, presses keys as a person would, and
// must end where the station says — on the slab, on the block it jumped onto,
// out of the pool, up the ladder — while the server neither complains nor
// sends it back (a teleport the scenario did not ask for is the server
// correcting a move it did not accept).
func robotMoves(r *robotBot, rcon *rconClient, name string) error {
	build := []string{
		"fill 20 -60 30 34 -50 56 minecraft:air",
		"fill 20 -63 30 34 -61 56 minecraft:grass_block",
		"fill 24 -60 33 30 -60 35 minecraft:oak_slab",
		"fill 24 -60 38 30 -60 40 minecraft:stone",
		"fill 24 -60 42 26 -58 44 minecraft:stone",
		"fill 24 -62 47 30 -61 49 minecraft:water",
		"fill 35 -60 46 35 -58 50 minecraft:stone", // the swim ends here, on the course's grass
		"fill 24 -60 52 24 -54 52 minecraft:stone",
		"fill 23 -60 52 23 -54 52 minecraft:ladder[facing=west]",
	}
	for _, c := range build {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	type station struct {
		name string
		tp   string // x y z yaw pitch
		keys []string
		// check sees where the last keys left the robot and where it settled
		check func(end, p [3]float64) error
	}
	near := func(what string, got, want, tol float64) error {
		if math.Abs(got-want) > tol {
			return fmt.Errorf("%s %.4f, want %.4f±%g", what, got, want, tol)
		}
		return nil
	}
	stations := []station{
		{"walk", "20.5 -60 30.5 -90 0", []string{"keys forward 20"}, func(_, p [3]float64) error {
			if err := near("y", p[1], -60, 1e-9); err != nil {
				return err
			}
			// 20 ticks of 0.1 × 0.98 a tick, ground friction 0.6 × 0.91, then the slide
			return near("x after 20 ticks walking", p[0], 20.5+4.3134, 0.001)
		}},
		{"sprint-jump", "20.5 -60 31.5 -90 0", []string{"keys forward,sprint 10", "keys forward,sprint,jump 14", "wait 20"}, func(_, p [3]float64) error {
			if err := near("y", p[1], -60, 1e-9); err != nil {
				return err
			}
			if p[0] < 20.5+6 {
				return fmt.Errorf("x %.4f: a sprint and a jump went less than 6 blocks", p[0])
			}
			return nil
		}},
		{"slab", "21.5 -60 34.5 -90 0", []string{"keys forward 30"}, func(_, p [3]float64) error {
			return near("y on the slabs", p[1], -59.5, 1e-9)
		}},
		{"jump-up", "21.5 -60 39.5 -90 0", []string{"keys forward 8", "keys forward,jump 12", "keys forward 5"}, func(_, p [3]float64) error {
			return near("y on the block", p[1], -59, 1e-9)
		}},
		{"drop", "25.5 -57 43.5 -90 0", []string{"keys forward 10", "wait 30"}, func(_, p [3]float64) error {
			return near("y after the drop", p[1], -60, 1e-9)
		}},
		// shift is held until the robot stopped: let go while still moving
		// and it slides off, in vanilla too
		{"sneak-edge", "25.5 -57 43.5 -90 0", []string{"keys forward,shift 40 shift", "keys shift 10"}, func(_, p [3]float64) error {
			if err := near("y sneaking at the edge", p[1], -57, 1e-9); err != nil {
				return err
			}
			if p[0] > 27.31 {
				return fmt.Errorf("x %.4f: sneaking walked off the edge at 27", p[0])
			}
			return nil
		}},
		{"swim", "21.5 -60 48.5 -90 0", []string{"keys forward 15", "keys forward,jump 80", "wait 20"}, func(_, p [3]float64) error {
			if err := near("y out of the pool", p[1], -60, 1e-9); err != nil {
				return err
			}
			if p[0] < 31 {
				return fmt.Errorf("x %.4f: still in the pool (it ends at 31)", p[0])
			}
			return nil
		}},
		{"ladder", "22.5 -60 52.5 -90 0", []string{"keys forward 40"}, func(end, _ [3]float64) error {
			// 0.1176 a tick once on it (0.2, less gravity and drag): more than
			// three blocks in 40 ticks with the walk to it
			if end[1] < -57 {
				return fmt.Errorf("y %.4f after 40 ticks: did not climb the ladder", end[1])
			}
			return nil
		}},
	}
	teleports := func() (int, error) {
		st, err := r.ask("state", 5*time.Second)
		if err != nil {
			return 0, err
		}
		i := strings.Index(st, "teleports=")
		if i < 0 {
			return 0, fmt.Errorf("state %q has no teleports", st)
		}
		return strconv.Atoi(strings.Fields(st[i+len("teleports="):])[0])
	}
	for _, st := range stations {
		before, err := teleports()
		if err != nil {
			return err
		}
		if resp, err := rcon.command("tp " + name + " " + st.tp); err != nil || !strings.Contains(resp, "Teleported") {
			return fmt.Errorf("%s: rcon tp: %q %v", st.name, resp, err)
		}
		if _, err := r.ask("wait 10", 5*time.Second); err != nil {
			return err
		}
		var end [3]float64
		for _, k := range st.keys {
			ans, err := r.ask(k, 30*time.Second)
			if err != nil {
				return fmt.Errorf("%s: %w", st.name, err)
			}
			if strings.HasPrefix(k, "keys ") {
				if end, err = parseXYZ(ans); err != nil {
					return fmt.Errorf("%s: %w", st.name, err)
				}
			}
		}
		// until the slide after the keys is over (a player stops once it moves
		// less than 0.003 a tick) and the server has the last position
		if _, err := r.ask("wait 20", 5*time.Second); err != nil {
			return err
		}
		mine, err := r.agree(rcon, name)
		if err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
		if after, err := teleports(); err != nil {
			return err
		} else if after != before+1 {
			return fmt.Errorf("%s: %d teleports, the scenario sent one: the server corrected the robot (at %v)", st.name, after-before, mine)
		}
		if err := st.check(end, mine); err != nil {
			return fmt.Errorf("%s: %w (the robot ended at %v)", st.name, err, mine)
		}
	}
	return nil
}

// agree compares the robot's position with the server's. The robot may still
// be moving (sliding down a ladder), so the server's answer is taken between
// two of the robot's: it must equal one of them or lie between them.
func (b *robotBot) agree(rcon *rconClient, name string) ([3]float64, error) {
	// a robot come to rest sends its last small move within a second (the
	// client sends it at least every 20 ticks): tried again a few times
	var p [3]float64
	var err error
	for try := 0; try < 4; try++ {
		if p, err = b.agreeOnce(rcon, name); err == nil {
			return p, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return p, err
}

func (b *robotBot) agreeOnce(rcon *rconClient, name string) ([3]float64, error) {
	before, err := b.pos()
	if err != nil {
		return before, err
	}
	resp, err := rcon.command("data get entity " + name + " Pos")
	if err != nil {
		return before, err
	}
	theirs, err := parsePos(resp)
	if err != nil {
		return before, err
	}
	after, err := b.pos()
	if err != nil {
		return before, err
	}
	for k := range theirs {
		lo, hi := math.Min(before[k], after[k])-1e-3, math.Max(before[k], after[k])+1e-3
		if theirs[k] < lo || theirs[k] > hi {
			return after, fmt.Errorf("the robot was at %v and then %v, the server says %v", before, after, theirs)
		}
	}
	return after, nil
}

// parseXYZ reads the first three numbers of an answer.
func parseXYZ(ans string) ([3]float64, error) {
	var p [3]float64
	f := strings.Fields(ans)
	if len(f) < 3 {
		return p, fmt.Errorf("answer %q has no position", ans)
	}
	for i := range p {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return p, fmt.Errorf("answer %q: %v", ans, err)
		}
		p[i] = v
	}
	return p, nil
}

// robotState checks what the robot knows of itself and of the entities
// around it against the server: the inventory slot by slot (the server's
// Inventory and equipment NBT), the held slot, an effect and the experience
// level; cows, a villager and items summoned, one moved, all removed.
func robotState(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"tp " + name + " 3.5 -60 18.5 0 0",
		"fill -2 -60 15 9 -55 30 minecraft:air",
		"clear " + name,
		"give " + name + " minecraft:stone 3",
		"give " + name + " minecraft:oak_log 70",
		"item replace entity " + name + " hotbar.4 with minecraft:diamond_pickaxe",
		"item replace entity " + name + " armor.head with minecraft:iron_helmet",
		"item replace entity " + name + " weapon.offhand with minecraft:shield",
		"effect give " + name + " minecraft:speed 120 1",
		"xp set " + name + " 5 levels",
	} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	inv, err := r.ask("inv", 5*time.Second)
	if err != nil {
		return err
	}
	got := map[string]bool{}
	for _, f := range strings.Fields(inv) {
		if !strings.HasPrefix(f, "held=") {
			got[f] = true
		}
	}
	// the server's view: the inventory list (slot, id, count) and the equipment
	want := map[string]bool{"39=minecraft:iron_helmet*1": true, "40=minecraft:shield*1": true}
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	entry := regexp.MustCompile(`\{[^{}]*\}`)
	field := func(s, re string) string {
		if m := regexp.MustCompile(re).FindStringSubmatch(s); m != nil {
			return m[1]
		}
		return ""
	}
	for _, e := range entry.FindAllString(resp, -1) {
		slot, id, count := field(e, `Slot: (\d+)b`), field(e, `id: "([^"]+)"`), field(e, `count: (\d+)`)
		if count == "" {
			count = "1"
		}
		want[slot+"="+id+"*"+count] = true
	}
	if len(want) < 6 {
		return fmt.Errorf("the server's inventory %q is not what was given", resp)
	}
	if !sameSet(got, want) {
		return fmt.Errorf("inventory: the robot has %v, the server %v", keys(got), keys(want))
	}
	if _, err := r.ask("hold 4", 5*time.Second); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	if resp, err := rcon.command("data get entity " + name + " SelectedItemSlot"); err != nil || !strings.HasSuffix(strings.TrimSpace(resp), " 4") {
		return fmt.Errorf("after hold 4 the server's selected slot: %q %v", resp, err)
	}
	if inv, err := r.ask("inv", 5*time.Second); err != nil || !strings.HasPrefix(inv, "held=4 ") {
		return fmt.Errorf("after hold 4 the robot: %q %v", inv, err)
	}
	status, err := r.ask("status", 5*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(status, "xp=5") || !strings.Contains(status, "minecraft:speed:1:") || !strings.Contains(status, "health=20") {
		return fmt.Errorf("status %q: want health 20, xp 5 and speed II", status)
	}

	// entities: summoned still (NoAI), compared with the server's positions
	summons := map[string]string{
		"t6cow":      `summon minecraft:cow 6.5 -60 21.5 {NoAI:1b,Tags:["t6","t6cow"]}`,
		"t6villager": `summon minecraft:villager 1.5 -60 23.25 {NoAI:1b,Tags:["t6","t6villager"]}`,
		"t6item":     `summon minecraft:item 3.5 -60 25.5 {Item:{id:"minecraft:diamond",count:2},PickupDelay:32767,Tags:["t6","t6item"]}`,
	}
	for _, c := range []string{"kill @e[tag=t6]", summons["t6cow"], summons["t6villager"], summons["t6item"]} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	ids := map[string]bool{} // the robot's ids of the summoned entities
	check := func(when string) error {
		time.Sleep(time.Second) // the item falls and settles
		near, err := r.ask("nearby 20", 5*time.Second)
		if err != nil {
			return err
		}
		for tag, typ := range map[string]string{"t6cow": "minecraft:cow", "t6villager": "minecraft:villager", "t6item": "minecraft:item"} {
			resp, err := rcon.command("data get entity @e[tag=" + tag + ",limit=1] Pos")
			if err != nil {
				return err
			}
			theirs, err := parsePos(resp)
			if err != nil {
				return fmt.Errorf("%s: %s: %v", when, tag, err)
			}
			found := false
			for _, e := range strings.Split(near, "; ") {
				f := strings.Fields(e)
				if len(f) == 5 && f[1] == typ {
					p, _ := parseXYZ(strings.Join(f[2:], " "))
					if samePos(p, theirs) {
						found = true
						ids[f[0]] = true
					}
				}
			}
			if !found {
				return fmt.Errorf("%s: no %s at %v among the robot's entities: %s", when, typ, theirs, near)
			}
		}
		return nil
	}
	if err := check("summoned"); err != nil {
		return err
	}
	if _, err := rcon.command("tp @e[tag=t6cow] 5.25 -60 26.75"); err != nil {
		return err
	}
	if err := check("after the cow's teleport"); err != nil {
		return err
	}
	if _, err := rcon.command("kill @e[tag=t6]"); err != nil {
		return err
	}
	// a mob is removed after its 20-tick death animation (its drops are new entities)
	time.Sleep(2500 * time.Millisecond)
	near, err := r.ask("nearby 20", 5*time.Second)
	if err != nil {
		return err
	}
	for _, e := range strings.Split(near, "; ") {
		if f := strings.Fields(e); len(f) > 0 && ids[f[0]] {
			return fmt.Errorf("after the kill the robot still sees %s", e)
		}
	}
	if _, err := rcon.command("kill @e[type=minecraft:item,x=3,y=-60,z=24,distance=..10]"); err != nil {
		return err
	}
	return nil
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// robotPaths sends the robot through a maze with goto — a wall with one
// opening, a raised floor to jump onto, a trench to jump over, a ladder up to
// the exit — then across a field where a wall is put in its way while it
// walks, then after the daze bot, which the scenario teleports twice.
func robotPaths(r *robotBot, rcon *rconClient, name, bin string, srv *smoke.Server) error {
	for _, c := range []string{
		"fill 40 -63 0 60 -50 30 minecraft:air",
		"fill 40 -63 0 60 -61 30 minecraft:grass_block",
		"fill 40 -60 0 56 -57 0 minecraft:stone", // the maze's walls, four high
		"fill 40 -60 12 56 -57 12 minecraft:stone",
		"fill 40 -60 0 40 -57 12 minecraft:stone",
		"fill 45 -60 0 45 -57 12 minecraft:stone", // the wall with an opening at z 10, three high:
		"fill 45 -60 10 45 -58 10 minecraft:air",  // the raised floor right behind it needs a jump
		"fill 46 -60 1 50 -60 11 minecraft:stone", // a raised floor behind it
		"fill 49 -63 1 49 -60 11 minecraft:air",   // a trench across it
		"fill 51 -60 1 53 -56 11 minecraft:stone", // a block five high, the exit on top
		"fill 50 -59 6 50 -56 6 minecraft:ladder[facing=west]",
		"tp " + name + " 42.5 -60 2.5 0 0",
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	ans, err := r.ask("goto 52 -55 6", 2*time.Minute)
	if err != nil {
		return fmt.Errorf("maze: %w", err)
	}
	if p, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("maze: %w", err)
	} else if math.Floor(p[0]) != 52 || p[1] != -55 || math.Floor(p[2]) != 6 {
		return fmt.Errorf("maze: arrived (%s) but at %v", ans, p)
	}

	// a wall put across the way while the robot walks: it plans again
	if _, err := rcon.command("tp " + name + " 42.5 -60 20.5 0 0"); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	from := len(r.out.String())
	if _, err := io.WriteString(r.stdin, "goto 58 -60 20\n"); err != nil {
		return err
	}
	time.Sleep(time.Second)
	if _, err := rcon.command("fill 52 -60 14 52 -58 26 minecraft:stone"); err != nil {
		return err
	}
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(200 * time.Millisecond) {
		out := r.out.String()[from:]
		if strings.Contains(out, "robot: error goto") {
			return fmt.Errorf("around the wall: %s", tail(out, 3))
		}
		if strings.Contains(out, "robot: goto ") {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("around the wall: no answer\n%s", tail(out, 5))
		}
	}
	if p, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("around the wall: %w", err)
	} else if math.Floor(p[0]) != 58 || math.Floor(p[2]) != 20 {
		return fmt.Errorf("around the wall: ended at %v\n%s", p, tail(r.out.String()[from:], 3))
	}

	// follow the daze bot through two teleports
	d, err := startDaze(bin, srv, "Leader")
	if err != nil {
		return err
	}
	defer d.close()
	if err := waitFor(d.out, 40*time.Second, "Game start"); err != nil {
		return fmt.Errorf("Leader: %v", err)
	}
	if _, err := rcon.command("tp Leader 50.5 -60 24.5"); err != nil {
		return err
	}
	time.Sleep(time.Second)
	if _, err := r.ask("follow Leader", 5*time.Second); err != nil {
		return err
	}
	defer r.ask("stop", 5*time.Second)
	for _, to := range [][3]float64{{50.5, -60, 24.5}, {44.5, -60, 28.5}, {58.5, -60, 16.5}} {
		if _, err := rcon.command(fmt.Sprintf("tp Leader %g %g %g", to[0], to[1], to[2])); err != nil {
			return err
		}
		deadline := time.Now().Add(40 * time.Second)
		for {
			p, err := r.pos()
			if err != nil {
				return err
			}
			if math.Hypot(p[0]-to[0], p[2]-to[2]) <= 3 && math.Abs(p[1]-to[1]) < 1 {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("follow: the robot is at %v, the leader at %v", p, to)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

// digTicks is how many ticks of digging a block takes after the start: the
// progress of a tick, speed / hardness / (30 with the right tool, 100
// without), summed in float32 as the client sums it until it reaches one.
func digTicks(speed, hardness float32, correct bool) int {
	mod := float32(100)
	if correct {
		mod = 30
	}
	step := speed / hardness / mod
	var p float32
	n := 0
	for p < 1 {
		p += step
		n++
	}
	return n
}

// robotDig has the robot break blocks with its hands and tools: dirt by hand,
// stone with a wooden pickaxe (cobblestone drops), stone by hand (nothing
// drops), obsidian with a diamond pickaxe. Each must be gone on the server,
// take the vanilla number of ticks, and leave the right drop, which the robot
// walks over to pick up.
func robotDig(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"fill 60 -63 0 72 -50 10 minecraft:air",
		"fill 60 -63 0 72 -61 10 minecraft:grass_block",
		"setblock 64 -60 4 minecraft:dirt",
		"setblock 65 -60 4 minecraft:stone",
		"setblock 66 -60 4 minecraft:stone",
		"setblock 67 -60 4 minecraft:obsidian",
		"kill @e[type=minecraft:item,x=66,y=-60,z=5,distance=..16]", // the world keeps strays between runs
		"clear " + name,
		"item replace entity " + name + " hotbar.1 with minecraft:wooden_pickaxe",
		"item replace entity " + name + " hotbar.2 with minecraft:diamond_pickaxe",
		"tp " + name + " 65.5 -60 2.5 0 0",
	} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	digs := []struct {
		x, slot int
		ticks   int
		drop    string // what the inventory gains, "" for nothing
	}{
		{64, 0, digTicks(1, 0.5, true), "minecraft:dirt"},
		{65, 1, digTicks(2, 1.5, true), "minecraft:cobblestone"},
		{66, 0, digTicks(1, 1.5, false), ""},
		{67, 2, digTicks(8, 50, true), "minecraft:obsidian"},
	}
	for _, d := range digs {
		at := fmt.Sprintf("%d -60 4", d.x)
		if _, err := r.ask(fmt.Sprintf("hold %d", d.slot), 5*time.Second); err != nil {
			return err
		}
		ans, err := r.ask("dig "+at, time.Minute)
		if err != nil {
			return fmt.Errorf("dig %s: %w", at, err)
		}
		var ticks, airborne int
		var was string
		if _, err := fmt.Sscanf(ans, "ticks=%d was=%s airborne=%d", &ticks, &was, &airborne); err != nil {
			return fmt.Errorf("dig %s answered %q", at, ans)
		}
		if ticks != d.ticks {
			return fmt.Errorf("dig %s took %d ticks (%d of them off the ground), the game's arithmetic says %d", at, ticks, airborne, d.ticks)
		}
		time.Sleep(300 * time.Millisecond)
		if resp, err := rcon.command("execute if block " + at + " minecraft:air"); err != nil || !strings.Contains(resp, "Test passed") {
			return fmt.Errorf("dig %s: the server still has the block (%q %v)", at, resp, err)
		}
		if d.drop == "" {
			near, err := r.ask("nearby 6", 5*time.Second)
			if err != nil {
				return err
			}
			if strings.Contains(near, "minecraft:item") {
				return fmt.Errorf("dig %s by hand dropped something: %s", at, near)
			}
			continue
		}
		if _, err := r.ask("collect 6", time.Minute); err != nil {
			return fmt.Errorf("after dig %s: %w", at, err)
		}
		inv, err := r.ask("inv", 5*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(inv, "="+d.drop+"*1") {
			return fmt.Errorf("after dig %s the inventory has no single %s: %s", at, d.drop, inv)
		}
		if _, err := rcon.command("tp " + name + " 65.5 -60 2.5 0 0"); err != nil {
			return err
		}
		if _, err := r.ask("wait 10", 5*time.Second); err != nil {
			return err
		}
	}
	return nil
}

// robotPlaceUse has the robot build with the block in its hand — a pillar
// three high, each block placed against the one below, and a wall three wide
// — open and close a door, press a button that lights the lamp it is on, and
// eat bread after a moment of hunger. The server is the judge of every block
// and of the food level.
func robotPlaceUse(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"fill 74 -63 0 88 -50 10 minecraft:air",
		"fill 74 -63 0 88 -61 10 minecraft:grass_block",
		"setblock 82 -60 4 minecraft:oak_door[half=lower,facing=north]",
		"setblock 82 -59 4 minecraft:oak_door[half=upper,facing=north]",
		"setblock 83 -60 4 minecraft:redstone_lamp",
		"setblock 83 -60 3 minecraft:stone_button[face=wall,facing=north]",
		"clear " + name,
		"item replace entity " + name + " hotbar.0 with minecraft:cobblestone 16",
		"item replace entity " + name + " hotbar.5 with minecraft:bread 4",
		"tp " + name + " 79.5 -60 2.5 0 0", // the pillar in front, the wall within reach
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("hold 0", 5*time.Second); err != nil {
		return err
	}
	isBlock := func(at, state string) error {
		resp, err := rcon.command("execute if block " + at + " " + state)
		if err != nil {
			return err
		}
		if !strings.Contains(resp, "Test passed") {
			return fmt.Errorf("%s is not %s on the server (%q)", at, state, resp)
		}
		return nil
	}
	for _, at := range []string{"79 -60 4", "79 -59 4", "79 -58 4", "78 -60 6", "79 -60 6", "80 -60 6"} {
		got, err := r.ask("place "+at, 30*time.Second)
		if err != nil {
			return fmt.Errorf("place %s: %w", at, err)
		}
		if got != "minecraft:cobblestone" {
			return fmt.Errorf("place %s: the robot sees %s", at, got)
		}
		if err := isBlock(at, "minecraft:cobblestone"); err != nil {
			return fmt.Errorf("place: %w", err)
		}
	}
	if inv, err := r.ask("inv", 5*time.Second); err != nil || !strings.Contains(inv, "0=minecraft:cobblestone*10") {
		return fmt.Errorf("after six placed the robot has %q (%v), want 10 cobblestone left", inv, err)
	}

	if _, err := r.ask("use 82 -60 4", 30*time.Second); err != nil {
		return fmt.Errorf("open the door: %w", err)
	}
	if err := isBlock("82 -60 4", "minecraft:oak_door[open=true]"); err != nil {
		return err
	}
	if _, err := r.ask("use 82 -60 4", 30*time.Second); err != nil {
		return fmt.Errorf("close the door: %w", err)
	}
	if err := isBlock("82 -60 4", "minecraft:oak_door[open=false]"); err != nil {
		return err
	}
	if _, err := r.ask("use 83 -60 3", 30*time.Second); err != nil {
		return fmt.Errorf("press the button: %w", err)
	}
	if err := isBlock("83 -60 4", "minecraft:redstone_lamp[lit=true]"); err != nil {
		return fmt.Errorf("after the button: %w", err)
	}

	// hunger (food does not drop in peaceful), then bread
	// hunger 201 is about 1 exhaustion a tick: 80 take the saturation, 20 a
	// further 5 food
	for _, c := range []string{"difficulty easy", "effect give " + name + " minecraft:hunger 6 200"} {
		if _, err := rcon.command(c); err != nil {
			return err
		}
	}
	defer rcon.command("difficulty peaceful")
	time.Sleep(7 * time.Second)
	food := func() (int, error) {
		st, err := r.ask("status", 5*time.Second)
		if err != nil {
			return 0, err
		}
		var f int
		i := strings.Index(st, "food=")
		if i < 0 {
			return 0, fmt.Errorf("status %q", st)
		}
		_, err = fmt.Sscanf(st[i:], "food=%d", &f)
		return f, err
	}
	before, err := food()
	if err != nil {
		return err
	}
	if before > 15 {
		return fmt.Errorf("food %d after the hunger: not hungry enough to eat bread", before)
	}
	if _, err := r.ask("hold 5", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("eat", 30*time.Second); err != nil {
		return fmt.Errorf("eat: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	after, err := food()
	if err != nil {
		return err
	}
	if after < before+5 {
		return fmt.Errorf("food %d before the bread, %d after: bread gives 5", before, after)
	}
	if inv, err := r.ask("inv", 5*time.Second); err != nil || !strings.Contains(inv, "5=minecraft:bread*3") {
		return fmt.Errorf("after eating the robot has %q (%v), want 3 bread left", inv, err)
	}
	return nil
}

// itemCounts sums the counts of a list of item NBT compounds by id, from a
// `data get` answer.
func itemCounts(resp string) map[string]int {
	// each compound's own id and count, not those of the stacks nested in its
	// components (they are counted as compounds of their own)
	type frame struct {
		id string
		n  int
	}
	out := map[string]int{}
	var stack []frame
	idRe := regexp.MustCompile(`^id: "([^"]+)"`)
	countRe := regexp.MustCompile(`^count: (\d+)`)
	for i := 0; i < len(resp); i++ {
		switch c := resp[i]; c {
		case '"', '\'':
			for i++; i < len(resp) && resp[i] != c; i++ {
				if resp[i] == '\\' {
					i++
				}
			}
		case '{':
			stack = append(stack, frame{n: 1})
		case '}':
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.id != "" {
				out[f.id] += f.n
			}
		default:
			if len(stack) == 0 || i > 0 && resp[i-1] != '{' && resp[i-1] != ' ' {
				continue
			}
			f := &stack[len(stack)-1]
			if m := idRe.FindStringSubmatch(resp[i:min(len(resp), i+200)]); m != nil {
				f.id = m[1]
			} else if m := countRe.FindStringSubmatch(resp[i:min(len(resp), i+20)]); m != nil {
				f.n, _ = strconv.Atoi(m[1])
			}
		}
	}
	return out
}

// robotCraft has the robot make things from logs with the recipe book, as a
// person clicking it does: planks, sticks and a crafting table in its own two
// by two, then the table placed, opened and a wooden pickaxe made in it; then
// cobblestone stored in a chest and taken back, raw iron smelted in a
// furnace. The server's NBT of the player, the chest and the inventory judges.
func robotCraft(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"fill 89 -63 0 100 -50 10 minecraft:air",
		"fill 89 -63 0 100 -61 10 minecraft:grass_block",
		"kill @e[type=minecraft:item,x=95,y=-60,z=5,distance=..12]",
		"clear " + name,
		"recipe give " + name + " *",
		"give " + name + " minecraft:oak_log 4",
		"tp " + name + " 91.5 -60 2.5 0 0",
	} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	for _, c := range []struct {
		item string
		n    string
	}{{"oak_planks", "16"}, {"stick", "4"}, {"crafting_table", "1"}} {
		got, err := r.ask("craft "+c.item+" "+c.n, 30*time.Second)
		if err != nil {
			return fmt.Errorf("craft %s: %w", c.item, err)
		}
		if got != c.n {
			return fmt.Errorf("craft %s %s made %s", c.item, c.n, got)
		}
	}
	inv, err := r.ask("inv", 5*time.Second)
	if err != nil {
		return err
	}
	m := regexp.MustCompile(`\b([0-8])=minecraft:crafting_table\*1`).FindStringSubmatch(inv)
	if m == nil {
		return fmt.Errorf("the crafting table is not in the hotbar: %s", inv)
	}
	if _, err := r.ask("hold "+m[1], 5*time.Second); err != nil {
		return err
	}
	if got, err := r.ask("place 93 -60 4", 30*time.Second); err != nil || got != "minecraft:crafting_table" {
		return fmt.Errorf("place the crafting table: %q %v", got, err)
	}
	if got, err := r.ask("open 93 -60 4", 30*time.Second); err != nil || !strings.Contains(got, "minecraft:crafting") {
		return fmt.Errorf("open the crafting table: %q %v", got, err)
	}
	if got, err := r.ask("craft wooden_pickaxe", 30*time.Second); err != nil || got != "1" {
		return fmt.Errorf("craft a wooden pickaxe: %q %v", got, err)
	}
	if _, err := r.ask("close", 5*time.Second); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	have := itemCounts(resp)
	for item, want := range map[string]int{"minecraft:wooden_pickaxe": 1, "minecraft:oak_planks": 7, "minecraft:stick": 2} {
		if have[item] != want {
			return fmt.Errorf("after crafting the server has %d %s, want %d (all: %v)", have[item], item, want, have)
		}
	}

	// a chest: store, check, take back
	for _, c := range []string{"setblock 95 -60 4 minecraft:chest", "setblock 97 -60 4 minecraft:furnace",
		"give " + name + " minecraft:cobblestone 20", "give " + name + " minecraft:raw_iron 2", "give " + name + " minecraft:coal 1",
		"tp " + name + " 95.5 -60 2.5 0 0"} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	if got, err := r.ask("open 95 -60 4", 30*time.Second); err != nil || !strings.Contains(got, "minecraft:generic_9x3") {
		return fmt.Errorf("open the chest: %q %v", got, err)
	}
	if got, err := r.ask("store cobblestone", 30*time.Second); err != nil || got != "20" {
		return fmt.Errorf("store cobblestone: %q %v", got, err)
	}
	if _, err := r.ask("close", 5*time.Second); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	if resp, err := rcon.command("data get block 95 -60 4 Items"); err != nil || itemCounts(resp)["minecraft:cobblestone"] != 20 {
		return fmt.Errorf("the chest holds %q (%v), want 20 cobblestone", resp, err)
	}
	if _, err := r.ask("open 95 -60 4", 30*time.Second); err != nil {
		return err
	}
	if got, err := r.ask("take cobblestone", 30*time.Second); err != nil || got != "20" {
		return fmt.Errorf("take cobblestone: %q %v", got, err)
	}
	if _, err := r.ask("close", 5*time.Second); err != nil {
		return err
	}

	// a furnace: two raw iron, one coal
	if got, err := r.ask("open 97 -60 4", 30*time.Second); err != nil || !strings.Contains(got, "minecraft:furnace") {
		return fmt.Errorf("open the furnace: %q %v", got, err)
	}
	if got, err := r.ask("smelt raw_iron coal 2", 60*time.Second); err != nil || got != "2" {
		return fmt.Errorf("smelt raw iron: %q %v", got, err)
	}
	if _, err := r.ask("close", 5*time.Second); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	resp, err = rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	if have := itemCounts(resp); have["minecraft:iron_ingot"] != 2 || have["minecraft:cobblestone"] != 20 {
		return fmt.Errorf("after the furnace the server has %v, want 2 iron ingots and 20 cobblestone back", have)
	}
	return nil
}

// robotFight puts the robot in a fenced arena with a stone sword and a husk
// (difficulty easy for it): the husk must die and the robot live. Then the
// robot is killed and must respawn and play on.
func robotFight(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"fill 102 -63 0 114 -50 12 minecraft:air",
		"fill 102 -63 0 114 -61 12 minecraft:grass_block",
		"fill 102 -60 0 112 -60 0 minecraft:oak_fence", // four fence walls (a one-high hollow fill is all wall)
		"fill 102 -60 10 112 -60 10 minecraft:oak_fence",
		"fill 102 -60 0 102 -60 10 minecraft:oak_fence",
		"fill 112 -60 0 112 -60 10 minecraft:oak_fence",
		"kill @e[tag=t11]",
		"kill @e[type=!minecraft:player,type=!minecraft:item,x=107,y=-60,z=5,distance=..24]", // strays of other runs
		"clear " + name,
		"effect clear " + name,
		"give " + name + " minecraft:stone_sword",
		"tp " + name + " 104.5 -60 2.5 0 0",
		"time set noon", // no zombies spawning at night; a husk does not burn
		"difficulty easy",
		`summon minecraft:husk 109.5 -60 7.5 {Tags:["t11"]}`,
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	defer rcon.command("difficulty peaceful")
	got, err := r.ask("guard 9 60", 90*time.Second)
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	if got != "killed=1" {
		return fmt.Errorf("guard answered %q, want killed=1", got)
	}
	if resp, err := rcon.command("execute if entity @e[tag=t11]"); err != nil || !strings.Contains(resp, "Test failed") {
		return fmt.Errorf("the husk is still there: %q %v", resp, err)
	}
	st, err := r.ask("status", 5*time.Second)
	if err != nil {
		return err
	}
	if strings.Contains(st, "health=0 ") {
		return fmt.Errorf("the robot died fighting: %s", st)
	}

	// killed: it respawns and plays on
	from := len(r.out.String())
	if _, err := rcon.command("kill " + name); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(r.out.String()[from:], "robot: loaded") {
		if time.Now().After(deadline) {
			return fmt.Errorf("no respawn after the kill:\n%s", tail(r.out.String()[from:], 8))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("after the respawn: %w", err)
	}
	if st, err := r.ask("status", 5*time.Second); err != nil || !strings.Contains(st, "health=20") {
		return fmt.Errorf("after the respawn: %q %v", st, err)
	}
	return nil
}

// robotTeleport: the robot agrees with the server after the spawn and a
// teleport, and a look shows up in the server's rotation.
func robotTeleport(r *robotBot, rcon *rconClient, name string, agree func(string) error) error {
	if err := agree("after the spawn"); err != nil {
		return err
	}
	if resp, err := rcon.command("tp " + name + " 5 -60 5"); err != nil || !strings.Contains(resp, "Teleported") {
		return fmt.Errorf("rcon tp: %q %v", resp, err)
	}
	time.Sleep(time.Second) // the teleport, its confirmation and a few position packets
	if err := agree("after a teleport"); err != nil {
		return err
	}
	if p, _ := r.pos(); math.Abs(p[0]-5.5) > 1e-3 || math.Abs(p[2]-5.5) > 1e-3 {
		return fmt.Errorf("after tp 5 -60 5 the robot is at %v", p)
	}
	if _, err := r.ask("look 90 30", 5*time.Second); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if resp, err := rcon.command("data get entity " + name + " Rotation"); err != nil || !strings.Contains(resp, "90.0f, 30.0f") {
		return fmt.Errorf("after look 90 30 the server has rotation %q %v", resp, err)
	}
	return nil
}

// robotGoals gives the robot goals instead of steps, in a patch with two
// trunks of oak logs and a boulder of stone, and nothing in its hands: get a
// wooden pickaxe (logs, planks, sticks, a crafting table placed and used),
// get 25 cobblestone (stone dug with the pickaxe), build a hut around itself.
// The server judges the pickaxe and every block of the hut.
func robotGoals(o Options, r *robotBot, rcon *rconClient, name string) error {
	// the robot there first, so the place is loaded (filled from afar it was
	// not, the fills refused, and the step lived on trunks earlier runs left)
	if err := r.prepare(rcon, name, 121.5, 2.5,
		"fill 118 -63 0 140 -50 22 minecraft:air",
		"fill 118 -63 0 140 -61 22 minecraft:grass_block",
		"fill 124 -60 6 124 -56 6 minecraft:oak_log",
		"fill 128 -60 10 128 -56 10 minecraft:oak_log",
		"fill 132 -60 4 135 -59 7 minecraft:stone",
		"kill @e[type=minecraft:item,x=129,y=-60,z=10,distance=..20]",
		"clear "+name,
		"time set noon",
		"tp "+name+" 121.5 -60 2.5 0 0",
	); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if rec, err := r.ask("recipe wooden_pickaxe", 5*time.Second); err == nil {
		o.Log("robot goals: %s", rec)
	}
	if _, err := r.ask("get wooden_pickaxe", 4*time.Minute); err != nil {
		return fmt.Errorf("get a wooden pickaxe: %w", err)
	}
	if resp, err := rcon.command("data get entity " + name + " Inventory"); err != nil || itemCounts(resp)["minecraft:wooden_pickaxe"] < 1 {
		return fmt.Errorf("after get wooden_pickaxe the server has %q (%v)", resp, err)
	}
	for _, q := range []string{"pos", "block 133 -60 5", "block 133 -59 5"} {
		if a, err := r.ask(q, 5*time.Second); err == nil {
			o.Log("robot goals: %s → %s", q, a)
		} else {
			o.Log("robot goals: %s → %v", q, err)
		}
	}
	if _, err := r.ask("get cobblestone 25", 4*time.Minute); err != nil {
		return fmt.Errorf("get cobblestone: %w", err)
	}
	if _, err := r.ask("goto 128 -60 17", 2*time.Minute); err != nil {
		return fmt.Errorf("go to the hut's place: %w", err)
	}
	ans, err := r.ask("build hut", 3*time.Minute)
	if err != nil {
		return fmt.Errorf("build a hut: %w", err)
	}
	var placed, x, y, z int
	if _, err := fmt.Sscanf(ans, "placed=%d at %d %d %d", &placed, &x, &y, &z); err != nil {
		return fmt.Errorf("build answered %q", ans)
	}
	for dy := 0; dy <= 2; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				if dy < 2 && dx == 0 && dz == 0 {
					continue // the robot's own place
				}
				at := fmt.Sprintf("%d %d %d", x+dx, y+dy, z+dz)
				if resp, err := rcon.command("execute if block " + at + " minecraft:cobblestone"); err != nil || !strings.Contains(resp, "Test passed") {
					return fmt.Errorf("the hut at %d %d %d has no cobblestone at %s (%q %v)", x, y, z, at, resp, err)
				}
			}
		}
	}
	return nil
}

// robotHash moves an enchanted pickaxe with damage on it from the hotbar to the
// inventory with two clicks. A click carries the hash of every component of
// the stack's patch; a wrong one is not refused, the server sends the slot
// back instead — so a right hash is no slot sent back, and the pickaxe where
// the clicks put it.
func robotHash(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"clear " + name,
		`item replace entity ` + name + ` hotbar.0 with minecraft:wooden_pickaxe[damage=10,repair_cost=3,enchantments={"minecraft:efficiency":3,"minecraft:unbreaking":1}]`,
	} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	before, err := r.ask("updates", 5*time.Second)
	if err != nil {
		return err
	}
	for _, slot := range []string{"36", "20"} {
		if _, err := r.ask("click "+slot, 5*time.Second); err != nil {
			return err
		}
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	after, err := r.ask("updates", 5*time.Second)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("the server sent %s slots back after two clicks with a damaged pickaxe (from %s): its component hashes were wrong", after, before)
	}
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	if !strings.Contains(resp, "Slot: 20b") || !strings.Contains(resp, "wooden_pickaxe") {
		return fmt.Errorf("the pickaxe is not in slot 20 on the server: %s", resp)
	}
	return nil
}

// entityID finds the robot's id of the nearest entity of a type around (x, z).
func (b *robotBot) entityID(typ string, x, z float64) (string, error) {
	near, err := b.ask("nearby 24", 5*time.Second)
	if err != nil {
		return "", err
	}
	best, bestD := "", math.MaxFloat64
	for _, e := range strings.Split(near, "; ") {
		f := strings.Fields(e)
		if len(f) != 5 || f[1] != typ {
			continue
		}
		p, err := parseXYZ(strings.Join(f[2:], " "))
		if err != nil {
			continue
		}
		if d := math.Hypot(p[0]-x, p[2]-z); d < bestD {
			best, bestD = f[0], d
		}
	}
	if best == "" {
		return "", fmt.Errorf("the robot sees no %s: %s", typ, near)
	}
	return best, nil
}

// robotCreatures: the robot writes a sign, shears a sheep, feeds two cows and
// trades with a villager — each judged by the server's NBT of the block, the
// animals and the player.
func robotCreatures(r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"fill 142 -63 0 156 -50 12 minecraft:air",
		"fill 142 -63 0 156 -61 12 minecraft:grass_block",
		"kill @e[type=!minecraft:player,x=149,y=-60,z=6,distance=..14]",
		"clear " + name,
		"time set noon",
		"tp " + name + " 145.5 -60 2.5 0 0",
		"give " + name + " minecraft:oak_sign",
		`summon minecraft:sheep 148.5 -60 4.5 {NoAI:1b,Color:0b,Tags:["t13"]}`,
		`summon minecraft:cow 150.5 -60 2.5 {NoAI:1b,Tags:["t13","t13cow"]}`,
		`summon minecraft:cow 151.5 -60 3.5 {NoAI:1b,Tags:["t13","t13cow"]}`,
		`summon minecraft:villager 147.5 -60 6.5 {NoAI:1b,Tags:["t13"],VillagerData:{profession:"minecraft:farmer",level:2,type:"minecraft:plains"},Offers:{Recipes:[{buy:{id:"minecraft:emerald",count:1},sell:{id:"minecraft:bread",count:2},maxUses:10}]}}`,
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	// a sign
	if _, err := r.ask("hold 0", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("sign 146 -60 4 Hello|from the|robot|26.x", 20*time.Second); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	if resp, err := rcon.command("data get block 146 -60 4 front_text.messages"); err != nil || !strings.Contains(resp, "Hello") || !strings.Contains(resp, "26.x") {
		return fmt.Errorf("the sign says %q (%v)", resp, err)
	}
	// shears on the sheep
	if _, err := rcon.command("item replace entity " + name + " hotbar.1 with minecraft:shears"); err != nil {
		return err
	}
	if _, err := r.ask("hold 1", 5*time.Second); err != nil {
		return err
	}
	id, err := r.entityID("minecraft:sheep", 148.5, 4.5)
	if err != nil {
		return err
	}
	if _, err := r.ask("interact "+id, 10*time.Second); err != nil {
		return fmt.Errorf("shear: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	if resp, err := rcon.command("data get entity @e[type=minecraft:sheep,tag=t13,limit=1] Sheared"); err != nil || !strings.Contains(resp, "1b") {
		return fmt.Errorf("the sheep is not sheared: %q (%v)", resp, err)
	}
	if _, err := r.ask("collect 6", 30*time.Second); err != nil {
		return fmt.Errorf("collect the wool: %w", err)
	}
	if inv, err := r.ask("inv", 5*time.Second); err != nil || !strings.Contains(inv, "minecraft:white_wool") {
		return fmt.Errorf("no wool after shearing: %q %v", inv, err)
	}
	// wheat to two cows
	if _, err := rcon.command("item replace entity " + name + " hotbar.2 with minecraft:wheat 4"); err != nil {
		return err
	}
	if _, err := r.ask("hold 2", 5*time.Second); err != nil {
		return err
	}
	for _, at := range [][2]float64{{150.5, 2.5}, {151.5, 3.5}} {
		if _, err := r.ask("goto 149 -60 3", 30*time.Second); err != nil {
			return err
		}
		id, err := r.entityID("minecraft:cow", at[0], at[1])
		if err != nil {
			return err
		}
		if _, err := r.ask("interact "+id, 10*time.Second); err != nil {
			return fmt.Errorf("feed a cow: %w", err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	// InLove counts down from 600: both cows above zero
	for _, sel := range []string{"@e[tag=t13cow,limit=1,sort=nearest,x=150.5,y=-60,z=2.5]", "@e[tag=t13cow,limit=1,sort=nearest,x=151.5,y=-60,z=3.5]"} {
		resp, err := rcon.command("data get entity " + sel + " InLove")
		var love int
		if i := strings.LastIndex(resp, ": "); err == nil && i >= 0 {
			love, _ = strconv.Atoi(strings.TrimSpace(resp[i+2:]))
		}
		if love <= 0 {
			return fmt.Errorf("a cow is not in love after the wheat: %q (%v)", resp, err)
		}
	}
	// two trades with the villager
	if _, err := rcon.command("give " + name + " minecraft:emerald 3"); err != nil {
		return err
	}
	if _, err := r.ask("goto 146 -60 5", 30*time.Second); err != nil {
		return err
	}
	vid, err := r.entityID("minecraft:villager", 147.5, 6.5)
	if err != nil {
		return err
	}
	if got, err := r.ask("trade "+vid+" 0 2", 30*time.Second); err != nil || got != "2" {
		return fmt.Errorf("trade: %q %v", got, err)
	}
	time.Sleep(300 * time.Millisecond)
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	if have := itemCounts(resp); have["minecraft:bread"] != 4 || have["minecraft:emerald"] != 1 {
		return fmt.Errorf("after two trades the server has %v, want 4 bread and 1 emerald", have)
	}
	return nil
}

// robotCurrent stands the robot in a channel of flowing water, holding no
// key: the current carries it downstream, as it does a person, and the
// server has no complaint about any of its moves.
func robotCurrent(o Options, r *robotBot, rcon *rconClient, name string) error {
	for _, c := range []string{
		"fill 160 -63 0 176 -50 6 minecraft:air",
		"fill 160 -63 0 176 -61 6 minecraft:grass_block",
		"fill 160 -60 2 176 -60 2 minecraft:stone", // the channel's walls
		"fill 160 -60 4 176 -60 4 minecraft:stone",
		"tp " + name + " 164.5 -60 3.5 -90 0",
	} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	// a chunk no player is near does not tick, and water placed there does
	// not flow: the robot first, then the source; the flow runs toward +x, a
	// block every five ticks
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := rcon.command("setblock 161 -60 3 minecraft:water"); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	for _, q := range []string{"block 164 -60 3", "block 164 -61 3", "pos"} {
		if a, err := r.ask(q, 5*time.Second); err == nil {
			o.Log("robot current: %s → %s", q, a)
		}
	}
	start, err := r.agree(rcon, name)
	if err != nil {
		return err
	}
	if _, err := r.ask("wait 60", 10*time.Second); err != nil {
		return err
	}
	end, err := r.agree(rcon, name)
	if err != nil {
		return err
	}
	if end[0]-start[0] < 0.3 {
		return fmt.Errorf("three seconds in the current moved the robot from %v to %v: not downstream", start, end)
	}
	return nil
}

// handlerErrors returns the packet handler errors the robot logged after offset.
func (b *robotBot) handlerErrors(from int) []string {
	var out []string
	for _, l := range strings.Split(b.out.String()[from:], "\n") {
		if strings.Contains(l, "handle packet") {
			out = append(out, l)
		}
	}
	return out
}

// robotItems gives the robot stacks with every kind of component — records,
// lists, texts, nested stacks — and checks it decodes them all, lists them as
// the server's Inventory has them, and clicks each out of its slot and back:
// the click carries the hash of every component, and a wrong one has the
// server send the slot back.
func robotItems(o Options, r *robotBot, rcon *rconClient, name string) error {
	items := []struct{ id, components string }{
		{"written_book", `[written_book_content={title:"T",author:"Robot",pages:["one","two"]}]`},
		{"writable_book", `[writable_book_content={pages:["draft"]}]`},
		{"firework_rocket", `[fireworks={flight_duration:2,explosions:[{shape:"star",colors:[I;16711680],has_trail:true}]}]`},
		{"potion", `[potion_contents={potion:"minecraft:strength"}]`},
		{"tipped_arrow", `[potion_contents={potion:"minecraft:swiftness"}]`},
		{"enchanted_book", `[stored_enchantments={"minecraft:sharpness":5}]`},
		{"leather_chestplate", `[dyed_color=16711680]`},
		{"iron_chestplate", `[trim={material:"minecraft:gold",pattern:"minecraft:coast"}]`},
		{"player_head", `[profile={name:"Robot"}]`},
		{"white_banner", `[banner_patterns=[{pattern:"minecraft:stripe_top",color:"red"}]]`},
		{"bundle", `[bundle_contents=[{id:"minecraft:stone",count:2}]]`},
		{"shulker_box", `[container=[{slot:0,item:{id:"minecraft:dirt",count:3}}]]`},
		{"diamond_sword", `[custom_name="Excalibur",lore=["first line"],enchantments={"minecraft:sharpness":2},unbreakable={}]`},
		{"crossbow", `[charged_projectiles=[{id:"minecraft:arrow"}]]`},
		{"goat_horn", `[instrument="minecraft:ponder_goat_horn"]`},
		{"compass", `[lodestone_tracker={target:{pos:[I;0,64,0],dimension:"minecraft:overworld"},tracked:false}]`},
		{"suspicious_stew", `[suspicious_stew_effects=[{id:"minecraft:speed",duration:100}]]`},
		{"shield", `[base_color="red"]`},
		{"golden_apple", `[rarity="epic"]`},
		{"bow", `[damage=5,repair_cost=2]`},
		{"diamond_pickaxe", `[enchantments={"minecraft:efficiency":4},damage=7]`},
		{"golden_sword", `[item_name={translate:"item.minecraft.stick",with:["x",{text:"y",underlined:true}]},tooltip_display={hidden_components:["minecraft:damage"]}]`},
		{"iron_sword", `[custom_data={a:1b,b:"s",c:[I;1,2],d:{e:2.5d}},attribute_modifiers=[{type:"minecraft:attack_damage",id:"minecraft:x",amount:3.0d,operation:"add_value",slot:"mainhand"}]]`},
		{"stick", `[custom_model_data={floats:[1.5f],flags:[true],strings:["a"],colors:[I;7]},custom_name={text:"Bold",bold:true,extra:[" and ",{translate:"item.minecraft.stick"}]}]`},
		{"filled_map", `[map_id=3]`},
	}
	if _, err := rcon.command("clear " + name); err != nil {
		return err
	}
	from := len(r.out.String())
	for _, it := range items {
		resp, err := rcon.command("give " + name + " minecraft:" + it.id + it.components)
		if err != nil || !strings.Contains(resp, "Gave") {
			return fmt.Errorf("give %s%s: %q %v", it.id, it.components, resp, err)
		}
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if bad := r.handlerErrors(from); len(bad) > 0 {
		return fmt.Errorf("the robot could not decode what it was given:\n%s", strings.Join(bad, "\n"))
	}
	inv, err := r.ask("inv", 5*time.Second)
	if err != nil {
		return err
	}
	slotOf := map[string]int{}
	for _, f := range strings.Fields(inv) {
		var slot int
		var rest string
		if n, _ := fmt.Sscanf(f, "%d=%s", &slot, &rest); n == 2 {
			slotOf[strings.TrimPrefix(strings.SplitN(rest, "*", 2)[0], "minecraft:")] = slot
		}
	}
	for _, it := range items {
		if _, ok := slotOf[it.id]; !ok {
			return fmt.Errorf("the robot does not list the %s it was given: %s", it.id, inv)
		}
	}
	var resent []string
	for _, it := range items {
		slot := slotOf[it.id]
		menu := slot
		if slot < 9 {
			menu = 36 + slot
		}
		before, err := r.ask("updates", 5*time.Second)
		if err != nil {
			return err
		}
		for range 2 {
			if _, err := r.ask(fmt.Sprintf("click %d", menu), 5*time.Second); err != nil {
				return err
			}
		}
		if _, err := r.ask("wait 4", 5*time.Second); err != nil {
			return err
		}
		after, err := r.ask("updates", 5*time.Second)
		if err != nil {
			return err
		}
		if after != before {
			resent = append(resent, it.id)
		}
	}
	if len(resent) > 0 {
		return fmt.Errorf("the server sent back the stacks whose hashes the robot got wrong: %v", resent)
	}
	if bad := r.handlerErrors(from); len(bad) > 0 {
		return fmt.Errorf("handler errors while clicking:\n%s", strings.Join(bad, "\n"))
	}
	// the inventory, as the server has it, still holds every stack where it was
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	have := itemCounts(resp)
	for _, it := range items {
		if have["minecraft:"+it.id] < 1 {
			return fmt.Errorf("after the clicks the server has no %s: %v", it.id, have)
		}
	}
	return nil
}

// robotEntities summons one of every entity type the registry has (still,
// silent, invulnerable; the few that cannot be summoned alone or would change
// the world left out) in a grid around the robot, and checks it was told about
// each, has the ones still there where the server put them, with the name
// their data carries and only data values the type's layout has, decodes the
// equipment and data the server sends later, all without a handler error.
func robotEntities(o Options, r *robotBot, rcon *rconClient, name string) error {
	var reg map[string]struct {
		Entries map[string]any `json:"entries"`
	}
	b, err := os.ReadFile(filepath.Join(o.DataDir, "registries.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		return err
	}
	var types []string
	for t := range reg["minecraft:entity_type"].Entries {
		types = append(types, t)
	}
	sort.Strings(types)
	skip := map[string]string{
		"minecraft:player":         "not summonable",
		"minecraft:fishing_bobber": "needs a player holding the rod",
		"minecraft:marker":         "never sent to clients",
		"minecraft:lightning_bolt": "converts the mobs it strikes",
		"minecraft:wither":         "explodes when it wakes",
	}
	extra := map[string]string{
		"minecraft:item":                 ",Item:{id:\"minecraft:stone\",count:1},PickupDelay:32767,Age:-32768",
		"minecraft:falling_block":        ",BlockState:{Name:\"minecraft:sand\"},Time:-100000,DropItem:0b",
		"minecraft:tnt":                  ",fuse:32767s",
		"minecraft:firework_rocket":      ",LifeTime:100000",
		"minecraft:area_effect_cloud":    ",Duration:100000,Radius:0.5f",
		"minecraft:item_frame":           ",Fixed:1b,Facing:1b",
		"minecraft:glow_item_frame":      ",Fixed:1b,Facing:1b",
		"minecraft:item_display":         ",item:{id:\"minecraft:diamond\",count:1}",
		"minecraft:block_display":        ",block_state:{Name:\"minecraft:stone\"}",
		"minecraft:text_display":         ",text:\"hello\"",
		"minecraft:piglin":               ",IsImmuneToZombification:1b",
		"minecraft:piglin_brute":         ",IsImmuneToZombification:1b",
		"minecraft:hoglin":               ",IsImmuneToZombification:1b",
		"minecraft:fireball":             ",acceleration_power:0d",
		"minecraft:small_fireball":       ",acceleration_power:0d",
		"minecraft:dragon_fireball":      ",acceleration_power:0d",
		"minecraft:wither_skull":         ",acceleration_power:0d",
		"minecraft:wind_charge":          ",acceleration_power:0d",
		"minecraft:breeze_wind_charge":   ",acceleration_power:0d",
		"minecraft:end_crystal":          ",ShowBottom:0b",
		"minecraft:experience_orb":       ",Value:1s",
		"minecraft:ominous_item_spawner": ",spawn_item_after_ticks:100000L",
		"minecraft:painting":             ",facing:2b,variant:\"minecraft:kebab\"",
	}
	// what a hanging entity hangs on, placed first at (x, z) of its spot
	support := map[string]func(x, z int) string{
		"minecraft:painting":   func(x, z int) string { return fmt.Sprintf("setblock %d -60 %d minecraft:stone", x, z+1) },
		"minecraft:leash_knot": func(x, z int) string { return fmt.Sprintf("setblock %d -60 %d minecraft:oak_fence", x, z) },
	}
	const x0, y0, z0, cols = 300, -60, -18, 13
	// the robot there first: a fill in chunks no one is near is refused
	if _, err := rcon.command("tp " + name + " 318.5 -50 0.5"); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	for _, c := range []string{
		"kill @e[type=!minecraft:player,x=318,y=-60,z=0,distance=..40]",
		"fill 296 -63 -22 342 -50 22 minecraft:air",
		"fill 296 -63 -22 342 -61 22 minecraft:stone",
		"setblock 318 -56 0 minecraft:glass",
		"tp " + name + " 318.5 -55 0.5",
		"time set noon",
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if _, err := r.ask("wait 40", 10*time.Second); err != nil {
		return err
	}
	// hostile mobs leave a peaceful world at once
	if _, err := rcon.command("difficulty easy"); err != nil {
		return err
	}
	defer rcon.command("difficulty peaceful")
	defer rcon.command("kill @e[type=!minecraft:player,x=318,y=-60,z=0,distance=..40]")
	if _, err := r.ask("seen reset", 5*time.Second); err != nil {
		return err
	}
	from := len(r.out.String())
	type placed struct {
		typ     string
		x, y, z float64
	}
	byName := map[string]placed{}
	var summoned, refused []string
	for i, t := range types {
		if why, ok := skip[t]; ok {
			o.Log("robot entities: %s left out: %s", t, why)
			continue
		}
		p := placed{t, float64(x0+3*(i%cols)) + 0.5, y0, float64(z0+3*(i/cols)) + 0.5}
		if f, ok := support[t]; ok {
			if _, err := rcon.command(f(int(p.x), int(p.z))); err != nil {
				return err
			}
		}
		n := fmt.Sprintf("e%d", i)
		cmd := fmt.Sprintf(`summon %s %.1f %d %.1f {NoAI:1b,Silent:1b,NoGravity:1b,Invulnerable:1b,PersistenceRequired:1b,CustomName:"%s",Tags:["t20"]%s}`,
			t, p.x, int(p.y), p.z, n, extra[t])
		resp, err := rcon.command(cmd)
		if err != nil {
			return err
		}
		if !strings.Contains(resp, "Summoned") {
			refused = append(refused, t+": "+resp)
			continue
		}
		byName[n] = p
		summoned = append(summoned, t)
	}
	if len(refused) > 0 {
		return fmt.Errorf("the server would not summon:\n%s", strings.Join(refused, "\n"))
	}
	if _, err := r.ask("wait 40", 10*time.Second); err != nil {
		return err
	}
	if bad := r.handlerErrors(from); len(bad) > 0 {
		return fmt.Errorf("handler errors with every entity around:\n%s", strings.Join(bad, "\n"))
	}
	seen, err := r.ask("seen", 5*time.Second)
	if err != nil {
		return err
	}
	var unseen []string
	for _, t := range summoned {
		if !strings.Contains(" "+seen+" ", " "+t+"=") {
			unseen = append(unseen, t)
		}
	}
	if len(unseen) > 0 {
		return fmt.Errorf("the robot was not told about %v (seen: %s)", unseen, seen)
	}
	list, err := r.ask("entities 48", 5*time.Second)
	if err != nil {
		return err
	}
	var problems []string
	present := map[string]bool{}
	for _, e := range strings.Split(list, "; ") {
		var id int
		var typ, nm, rest string
		var x, y, z float64
		if n, _ := fmt.Sscanf(e, "%d %s %f %f %f name=%q", &id, &typ, &x, &y, &z, &nm); n < 6 {
			continue
		}
		if i := strings.Index(e, " bad="); i >= 0 {
			rest = e[i+len(" bad="):]
		}
		p, ok := byName[nm]
		if !ok {
			continue // a passenger, a part, a drop
		}
		present[nm] = true
		if typ != p.typ {
			problems = append(problems, fmt.Sprintf("%s is a %s, summoned as a %s", nm, typ, p.typ))
		}
		// a rocket climbs whatever its NoGravity says
		if typ != "minecraft:firework_rocket" && (math.Abs(x-p.x) > 1.5 || math.Abs(y-p.y) > 1.5 || math.Abs(z-p.z) > 1.5) {
			problems = append(problems, fmt.Sprintf("%s %s at %.2f %.2f %.2f, summoned at %.1f %.1f %.1f", nm, typ, x, y, z, p.x, p.y, p.z))
		}
		if rest != "" {
			problems = append(problems, fmt.Sprintf("%s %s: data values its layout does not have (index:serializer): %s", nm, typ, rest))
		}
	}
	var gone []string
	for n, p := range byName {
		if !present[n] {
			gone = append(gone, p.typ)
		}
	}
	sort.Strings(gone)
	o.Log("robot entities: %d types summoned, %d still there; gone by now: %v", len(summoned), len(present), gone)
	if len(present) < len(summoned)*9/10 {
		problems = append(problems, fmt.Sprintf("only %d of %d entities listed by name (list: %s)", len(present), len(summoned), list))
	}
	if len(problems) > 0 {
		return fmt.Errorf("entities:\n%s", strings.Join(problems, "\n"))
	}
	// what the server sends later: equipment, a new name and colour
	for _, c := range []string{
		"item replace entity @e[type=minecraft:zombie,tag=t20,limit=1] armor.head with minecraft:diamond_helmet",
		"item replace entity @e[type=minecraft:zombie,tag=t20,limit=1] weapon.mainhand with minecraft:iron_sword[enchantments={\"minecraft:sharpness\":1}]",
		`data merge entity @e[type=minecraft:sheep,tag=t20,limit=1] {Color:14b,CustomName:"renamed"}`,
	} {
		if resp, err := rcon.command(c); err != nil || strings.Contains(resp, "No entity") || strings.Contains(resp, "nknown") {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	list, err = r.ask("entities 48", 5*time.Second)
	if err != nil {
		return err
	}
	var zombie, sheep string
	for _, e := range strings.Split(list, "; ") {
		f := strings.Fields(e)
		if len(f) > 1 && f[1] == "minecraft:zombie" && strings.Contains(e, `name="e`) {
			zombie = e
		}
		if len(f) > 1 && f[1] == "minecraft:sheep" && (sheep == "" || strings.Contains(e, `name="renamed"`)) {
			sheep = e // ours, renamed; a sheep that wandered in has no name
		}
	}
	if !strings.Contains(zombie, " equip=2 ") {
		return fmt.Errorf("the zombie's equipment did not arrive: %q", zombie)
	}
	if !strings.Contains(sheep, `name="renamed"`) {
		return fmt.Errorf("the sheep's new name did not arrive: %q", sheep)
	}
	if bad := r.handlerErrors(from); len(bad) > 0 {
		return fmt.Errorf("handler errors:\n%s", strings.Join(bad, "\n"))
	}
	return nil
}

// robotDimensions takes the robot into the Nether by teleport: a closed
// netherrack room, its floor dug, walked across; the bedrock at y 0 seen where
// the Nether's minimum height puts it. There it builds an obsidian frame,
// lights it and walks into the portal, which takes it back to the overworld.
// Then the End and back, by teleport. At every stop its dimension, position
// and blocks agree with the server's.
func robotDimensions(r *robotBot, rcon *rconClient, name string) error {
	run := func(cmds ...string) error {
		for _, c := range cmds {
			resp, err := rcon.command(c)
			if err != nil || rconFailed(resp) {
				return fmt.Errorf("rcon %q: %q %v", c, resp, err)
			}
		}
		return nil
	}
	// arrive waits for the robot to be in dim, loaded, on the ground, and
	// checks it is where the server says
	arrive := func(dim string) error {
		var st string
		for t := 0; t < 60; t++ {
			var err error
			if st, err = r.ask("state", 5*time.Second); err != nil {
				return err
			}
			if strings.Contains(st, "dim="+dim) && strings.Contains(st, "loaded=true") && strings.Contains(st, "onGround=true") {
				_, err := r.agree(rcon, name)
				return err
			}
			if _, err := r.ask("wait 5", 5*time.Second); err != nil {
				return err
			}
		}
		return fmt.Errorf("the robot did not arrive in %s: %s", dim, st)
	}
	block := func(x, y, z int, want string) error {
		got, err := r.ask(fmt.Sprintf("block %d %d %d", x, y, z), 5*time.Second)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(got, want) {
			return fmt.Errorf("block %d %d %d: the robot sees %s, not %s", x, y, z, got, want)
		}
		return nil
	}
	nether := "execute in minecraft:the_nether run "
	if err := run(
		"clear "+name,
		"item replace entity "+name+" hotbar.0 with minecraft:obsidian 14",
		"item replace entity "+name+" hotbar.1 with minecraft:flint_and_steel",
		"item replace entity "+name+" hotbar.2 with minecraft:diamond_pickaxe",
		"effect give "+name+" minecraft:fire_resistance infinite 0 true",
		nether+"forceload add 0 0 16 16",
		nether+"fill -2 63 -2 18 76 18 minecraft:netherrack",
		nether+"fill 0 65 0 16 75 16 minecraft:air",
		nether+"tp "+name+" 7.5 65 6.5 0 0",
	); err != nil {
		return err
	}
	defer rcon.command(nether + "forceload remove 0 0 16 16")
	defer rcon.command("effect clear " + name)
	if err := arrive("minecraft:the_nether"); err != nil {
		return fmt.Errorf("nether by teleport: %w", err)
	}
	for _, b := range []struct {
		x, y, z int
		want    string
	}{{7, 64, 6, "minecraft:netherrack"}, {7, 0, 6, "minecraft:bedrock"}, {7, 127, 6, "minecraft:bedrock"}, {7, 70, 6, "minecraft:air"}} {
		if err := block(b.x, b.y, b.z, b.want); err != nil {
			return fmt.Errorf("nether: %w", err)
		}
	}
	// dig the floor beside it, walk across the room
	if _, err := r.ask("hold 2", 5*time.Second); err != nil {
		return err
	}
	if ans, err := r.ask("dig 5 64 6", 20*time.Second); err != nil {
		return fmt.Errorf("nether dig: %w", err)
	} else if !strings.Contains(ans, "was=minecraft:netherrack") {
		return fmt.Errorf("nether dig: %s", ans)
	}
	if _, err := r.ask("wait 4", 5*time.Second); err != nil {
		return err
	}
	if resp, err := rcon.command(nether + "execute if block 5 64 6 minecraft:air"); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("the server still has the netherrack the robot dug: %q %v", resp, err)
	}
	for _, g := range []string{"goto 13 65 13", "goto 7 65 8"} {
		if ans, err := r.ask(g, 2*time.Minute); err != nil || strings.Contains(ans, "stuck") {
			return fmt.Errorf("nether %s: %q %v", g, ans, err)
		}
		if _, err := r.agree(rcon, name); err != nil {
			return fmt.Errorf("nether %s: %w", g, err)
		}
	}
	// the frame, from the bottom up, each block against one placed before
	if _, err := r.ask("hold 0", 5*time.Second); err != nil {
		return err
	}
	// its bottom sunk into the floor, so the robot walks in at floor level
	if err := run(nether + "fill 6 64 10 9 64 10 minecraft:air"); err != nil {
		return err
	}
	if _, err := r.ask("wait 4", 5*time.Second); err != nil {
		return err
	}
	if err := block(6, 64, 10, "minecraft:air"); err != nil {
		return fmt.Errorf("the floor the server took away: %w", err)
	}
	frame := [][3]int{
		{6, 64, 10}, {7, 64, 10}, {8, 64, 10}, {9, 64, 10},
		{6, 65, 10}, {6, 66, 10}, {6, 67, 10}, {9, 65, 10}, {9, 66, 10}, {9, 67, 10},
		{6, 68, 10}, {7, 68, 10}, {8, 68, 10}, {9, 68, 10},
	}
	for _, f := range frame {
		ans, err := r.ask(fmt.Sprintf("place %d %d %d", f[0], f[1], f[2]), 10*time.Second)
		if err != nil || !strings.HasPrefix(ans, "minecraft:obsidian") {
			return fmt.Errorf("frame %v: %q %v", f, ans, err)
		}
	}
	if _, err := r.ask("hold 1", 5*time.Second); err != nil {
		return err
	}
	if ans, err := r.ask("place 7 65 10", 10*time.Second); err != nil || !strings.HasPrefix(ans, "minecraft:nether_portal") {
		return fmt.Errorf("lighting the portal: %q %v", ans, err)
	}
	if resp, err := rcon.command(nether + "execute if block 8 67 10 minecraft:nether_portal"); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("the server has no portal in the frame: %q %v", resp, err)
	}
	// into the portal, and wait in it
	if ans, err := r.ask("goto 7 65 10", 30*time.Second); err != nil || strings.Contains(ans, "stuck") {
		return fmt.Errorf("into the portal: %q %v", ans, err)
	}
	if err := arrive("minecraft:overworld"); err != nil {
		return fmt.Errorf("through the portal: %w", err)
	}
	p, err := r.pos()
	if err != nil {
		return err
	}
	if resp, err := rcon.command(fmt.Sprintf("execute if block %d %d %d minecraft:nether_portal", int(math.Floor(p[0])), int(math.Floor(p[1])), int(math.Floor(p[2])))); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("the robot came out at %v, not in a portal: %q %v", p, resp, err)
	}
	if err := block(int(math.Floor(p[0])), int(math.Floor(p[1])), int(math.Floor(p[2])), "minecraft:nether_portal"); err != nil {
		return fmt.Errorf("overworld: %w", err)
	}
	// the End and back, by teleport
	end := "execute in minecraft:the_end run "
	if err := run(end+"forceload add 96 -4 104 4", end+"fill 98 48 -2 102 48 2 minecraft:obsidian", end+"fill 98 49 -2 102 52 2 minecraft:air", end+"tp "+name+" 100.5 49 0.5"); err != nil {
		return err
	}
	defer rcon.command(end + "forceload remove 96 -4 104 4")
	if err := arrive("minecraft:the_end"); err != nil {
		return fmt.Errorf("the end: %w", err)
	}
	if err := block(100, 48, 0, "minecraft:obsidian"); err != nil {
		return fmt.Errorf("the end: %w", err)
	}
	if err := run("execute in minecraft:overworld run tp " + name + " 0.5 -60 0.5"); err != nil {
		return err
	}
	if err := arrive("minecraft:overworld"); err != nil {
		return fmt.Errorf("back from the end: %w", err)
	}
	return block(0, -61, 0, "minecraft:")
}

// robotChat has a player — the daze bot — command the robot in chat, as
// people command bots: "robot pos" and "robot come" in public chat, a dig
// whispered with /msg, "robot follow" through a teleport, an order it does
// not know. The robot reads the player chat (its chat type and decoration),
// does it, and answers the way it was asked; the daze bot must receive each
// answer, decorated by its chat type, and the server must show the effect.
func robotChat(r *robotBot, rcon *rconClient, name, bin string, srv *smoke.Server) error {
	// the robot there first: a fill in chunks no one is near is refused
	if _, err := rcon.command("tp " + name + " 402.5 -50 2.5 0 0"); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	for _, c := range []string{
		"fill 400 -63 -4 424 -50 12 minecraft:air",
		"fill 400 -63 -4 424 -61 12 minecraft:grass_block",
		"tp " + name + " 402.5 -60 2.5 0 0",
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	d, err := startDaze(bin, srv, "Boss")
	if err != nil {
		return err
	}
	defer d.close()
	if err := waitFor(d.out, 40*time.Second, "Game start"); err != nil {
		return fmt.Errorf("Boss: %v", err)
	}
	if _, err := rcon.command("tp Boss 410.5 -60 2.5"); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	// order says line as Boss and waits for the answer Boss receives
	order := func(line, want string) (string, error) {
		from := len(d.out.String())
		if err := d.say(line); err != nil {
			return "", err
		}
		deadline := time.Now().Add(time.Minute)
		for {
			out := d.out.String()[from:]
			if i := strings.Index(out, want); i >= 0 { // the whole line
				start := strings.LastIndexByte(out[:i], '\n') + 1
				end := strings.IndexByte(out[i:], '\n')
				if end < 0 {
					end = len(out) - i
				}
				return out[start : i+end], nil
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("%q: Boss got no %q; Boss logged:\n%s\nthe robot logged:\n%s", line, want, tail(d.out.String(), 5), tail(r.out.String(), 5))
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	// public chat: the answer comes back as the robot's chat message
	ans, err := order("robot pos", "<"+name+"> ")
	if err != nil {
		return err
	}
	if !strings.Contains(ans, "402.5000 -60.0000 2.5000") {
		return fmt.Errorf("robot pos: Boss got %q", ans)
	}
	if _, err := order("robot come", "<"+name+"> "); err != nil {
		return err
	}
	p, err := r.agree(rcon, name)
	if err != nil {
		return fmt.Errorf("come: %w", err)
	}
	if math.Abs(p[0]-410.5) > 1.5 || math.Abs(p[2]-2.5) > 1.5 {
		return fmt.Errorf("come: the robot stopped at %v, Boss is at 410.5 -60 2.5", p)
	}
	if resp, err := rcon.command("execute if block 412 -61 4 minecraft:grass_block"); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("the server has no grass at 412 -61 4: %q %v", resp, err)
	}
	if got, err := r.ask("block 412 -61 4", 5*time.Second); err != nil || !strings.HasPrefix(got, "minecraft:grass_block") {
		return fmt.Errorf("the robot sees %q at 412 -61 4, the server grass (%v)", got, err)
	}
	// a whisper: the answer is whispered back
	// (the daze bot shows chat in its own language: the answer, not the decoration)
	if ans, err := order("/msg "+name+" dig 412 -61 4", "ticks="); err != nil {
		return err
	} else if !strings.Contains(ans, "was=minecraft:grass_block") || !strings.Contains(ans, "Disguised:") || !strings.Contains(ans, name) {
		return fmt.Errorf("whispered dig: Boss got %q", ans)
	}
	if _, err := r.ask("wait 4", 5*time.Second); err != nil {
		return err
	}
	if resp, err := rcon.command("execute if block 412 -61 4 minecraft:air"); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("whispered dig: the server still has the block: %q %v", resp, err)
	}
	// follow the one who asked, through a teleport
	if _, err := order("robot follow", "<"+name+"> Boss"); err != nil {
		return err
	}
	if _, err := rcon.command("tp Boss 420.5 -60 8.5"); err != nil {
		return err
	}
	deadline := time.Now().Add(40 * time.Second)
	for {
		p, err := r.pos()
		if err != nil {
			return err
		}
		if math.Hypot(p[0]-420.5, p[2]-8.5) < 3 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("follow: the robot is at %v, Boss at 420.5 -60 8.5", p)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if _, err := order("robot stop", "<"+name+"> stopped"); err != nil {
		return err
	}
	if _, err := order("robot dance", "<"+name+"> I do not know"); err != nil {
		return err
	}
	if bad := r.handlerErrors(0); len(bad) > 0 {
		return fmt.Errorf("handler errors:\n%s", strings.Join(bad, "\n"))
	}
	return nil
}

// rconFailed tells a command the server refused from one it ran: an unknown
// or malformed command, a position not loaded (a fill where no player is near
// does nothing).
func rconFailed(resp string) bool {
	for _, m := range []string{"Could not", "nknown", "Invalid", "Incorrect", "not loaded"} {
		if strings.Contains(resp, m) {
			return true
		}
	}
	return false
}

// prepare puts the robot near (x, z) first — a fill in chunks no player is
// near is refused — then runs the commands.
func (b *robotBot) prepare(rcon *rconClient, name string, x, z float64, cmds ...string) error {
	if _, err := rcon.command(fmt.Sprintf("tp %s %.1f -50 %.1f", name, x, z)); err != nil {
		return err
	}
	if _, err := b.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	for _, c := range cmds {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	return nil
}

// robotFarm has the robot till grass with a hoe, sow wheat, bone-meal it
// ripe and harvest it, as a person farms; the server's blocks and the
// robot's inventory judge each step.
func robotFarm(r *robotBot, rcon *rconClient, name string) error {
	if err := r.prepare(rcon, name, 482.5, 2.5,
		"fill 478 -63 -2 490 -50 8 minecraft:air",
		"fill 478 -63 -2 490 -61 8 minecraft:grass_block",
		"setblock 485 -61 2 minecraft:water",
		"kill @e[type=minecraft:item,x=484,y=-60,z=2,distance=..10]",
		"clear "+name,
		"item replace entity "+name+" hotbar.0 with minecraft:iron_hoe",
		"item replace entity "+name+" hotbar.1 with minecraft:wheat_seeds 4",
		"item replace entity "+name+" hotbar.2 with minecraft:bone_meal 16",
		"tp "+name+" 482.5 -60 2.5 -90 30",
	); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	step := func(hold int, cmd, want string) (string, error) {
		if _, err := r.ask(fmt.Sprintf("hold %d", hold), 5*time.Second); err != nil {
			return "", err
		}
		ans, err := r.ask(cmd, 10*time.Second)
		if err != nil {
			return "", fmt.Errorf("%s: %w", cmd, err)
		}
		if !strings.HasPrefix(ans, want) {
			return ans, fmt.Errorf("%s: %q, want %s", cmd, ans, want)
		}
		return ans, nil
	}
	if _, err := step(0, "use 484 -61 2", "minecraft:farmland"); err != nil {
		return fmt.Errorf("till: %w", err)
	}
	if _, err := step(1, "place 484 -60 2", "minecraft:wheat[age=0]"); err != nil {
		return fmt.Errorf("sow: %w", err)
	}
	ripe := false
	for i := 0; i < 8 && !ripe; i++ {
		ans, err := step(2, "use 484 -60 2", "minecraft:wheat")
		if err != nil {
			return fmt.Errorf("bone meal: %w", err)
		}
		ripe = strings.Contains(ans, "age=7")
	}
	if !ripe {
		return errors.New("bone meal: the wheat is not ripe after eight")
	}
	for _, c := range []string{"execute if block 484 -61 2 minecraft:farmland", "execute if block 484 -60 2 minecraft:wheat[age=7]"} {
		if resp, err := rcon.command(c); err != nil || !strings.Contains(resp, "passed") {
			return fmt.Errorf("%s: %q %v", c, resp, err)
		}
	}
	if _, err := step(3, "dig 484 -60 2", "ticks="); err != nil {
		return fmt.Errorf("harvest: %w", err)
	}
	if _, err := r.ask("collect 6", time.Minute); err != nil {
		return fmt.Errorf("harvest: %w", err)
	}
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	if have := itemCounts(resp); have["minecraft:wheat"] < 1 {
		return fmt.Errorf("harvest: the robot has no wheat: %v", have)
	}
	return nil
}

// robotSleep has the robot place a bed, wait for the night and sleep in it:
// the server sends it to bed, skips the night once it slept, wakes it.
func robotSleep(r *robotBot, rcon *rconClient, name string) error {
	if err := r.prepare(rcon, name, 492.5, 2.5,
		"fill 490 -63 -2 500 -50 8 minecraft:air",
		"fill 490 -63 -2 500 -61 8 minecraft:grass_block",
		"clear "+name,
		"item replace entity "+name+" hotbar.4 with minecraft:red_bed",
		"weather clear",
		"time set noon",
		"tp "+name+" 492.5 -60 2.5 -90 30",
	); err != nil {
		return err
	}
	// a night skipped by sleeping wants the clock running
	if resp, err := rcon.command("gamerule advance_time true"); err != nil || rconFailed(resp) {
		return fmt.Errorf("the clock: %q %v", resp, err)
	}
	defer rcon.command("gamerule advance_time false")
	defer rcon.command("time set noon")
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("hold 4", 5*time.Second); err != nil {
		return err
	}
	if ans, err := r.ask("place 494 -60 2", 10*time.Second); err != nil || !strings.HasPrefix(ans, "minecraft:red_bed") {
		return fmt.Errorf("the bed: %q %v", ans, err)
	}
	if resp, err := rcon.command("execute if block 495 -60 2 minecraft:red_bed[part=head,facing=east]"); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("the bed's head is not east of its foot: %q %v", resp, err)
	}
	if _, err := rcon.command("time set 13000"); err != nil {
		return err
	}
	if ans, err := r.ask("use 494 -60 2", 10*time.Second); err != nil || !strings.Contains(ans, "occupied=true") {
		return fmt.Errorf("into bed: %q %v", ans, err)
	}
	// once every player slept a hundred ticks the night is skipped
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := rcon.command("time query minecraft:day")
		if err != nil {
			return err
		}
		// "Timeline minecraft:day is at 504 tick(s)"
		if m := regexp.MustCompile(`(\d+) tick`).FindStringSubmatch(resp); m != nil {
			if t, _ := strconv.Atoi(m[1]); t < 12000 {
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the night was not skipped: %q", resp)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if resp, err := rcon.command("execute if block 494 -60 2 minecraft:red_bed[occupied=false]"); err != nil || !strings.Contains(resp, "passed") {
		return fmt.Errorf("the robot is still in bed: %q %v", resp, err)
	}
	if _, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("woken: %w", err)
	}
	return nil
}

// serverPos is where the server has an entity (a selector or a name).
func serverPos(rcon *rconClient, target string) ([3]float64, error) {
	resp, err := rcon.command("data get entity " + target + " Pos")
	if err != nil {
		return [3]float64{}, err
	}
	return parsePos(resp)
}

// robotMinecart has the robot put a minecart on a rail, get in, push off
// with the forward key toward powered rails, ride to the end of the track
// and get out. While it rides, it sits where the server has it (the cart's
// position from the server, its passenger point, the player's vehicle
// point); getting out, the server puts it down beside the cart.
func robotMinecart(r *robotBot, rcon *rconClient, name string) error {
	if err := r.prepare(rcon, name, 511.5, 3.5,
		"kill @e[type=minecraft:minecart,x=520,y=-60,z=2,distance=..20]",
		"fill 508 -63 -2 534 -50 6 minecraft:air",
		"fill 508 -63 -2 534 -61 6 minecraft:stone",
		"fill 512 -60 2 530 -60 2 minecraft:rail[shape=east_west]",
		"fill 514 -61 2 516 -61 2 minecraft:redstone_block",
		"fill 514 -60 2 516 -60 2 minecraft:powered_rail[shape=east_west,powered=true]",
		"setblock 531 -60 2 minecraft:stone",
		"clear "+name,
		"item replace entity "+name+" hotbar.5 with minecraft:minecart",
		"tp "+name+" 511.5 -60 3.5 -90 30",
	); err != nil {
		return err
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("hold 5", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("useon 512 -60 2", 10*time.Second); err != nil {
		return fmt.Errorf("the minecart onto the rail: %w", err)
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	id, err := r.entityID("minecraft:minecart", 512.5, 2.5)
	if err != nil {
		return fmt.Errorf("the minecart: %w", err)
	}
	if _, err := r.ask("interact "+id, 20*time.Second); err != nil {
		return fmt.Errorf("getting in: %w", err)
	}
	st, err := r.ask("state", 5*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(st, "riding="+id+":minecraft:minecart") {
		return fmt.Errorf("getting in: the robot rides nothing: %s", st)
	}
	// seated: the robot where the server has the player
	// (bracketed: the cart may still roll a little)
	seated := func(what string) error {
		before, err := r.pos()
		if err != nil {
			return err
		}
		theirs, err := serverPos(rcon, name)
		if err != nil {
			return err
		}
		after, err := r.pos()
		if err != nil {
			return err
		}
		for k := range theirs {
			lo, hi := math.Min(before[k], after[k])-0.01, math.Max(before[k], after[k])+0.01
			if theirs[k] < lo || theirs[k] > hi {
				return fmt.Errorf("%s: the robot sits at %v then %v, the server has it at %v", what, before, after, theirs)
			}
		}
		return nil
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if err := seated("in the cart"); err != nil {
		return err
	}
	// facing down the track, forward: the cart creeps to the powered rails
	if _, err := r.ask("look -90 0", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("keys forward 60", 10*time.Second); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		cart, err := serverPos(rcon, "@e[type=minecraft:minecart,limit=1,sort=nearest,x=520,y=-60,z=2]")
		if err != nil {
			return err
		}
		if cart[0] > 528 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the cart did not get to the end of the track: it is at %v", cart)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if _, err := r.ask("wait 40", 5*time.Second); err != nil { // the cart comes to rest
		return err
	}
	if err := seated("at the end"); err != nil {
		return err
	}
	if _, err := r.ask("dismount", 10*time.Second); err != nil {
		return fmt.Errorf("getting out: %w", err)
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("got out: %w", err)
	}
	return nil
}

// robotBoat has the robot put a boat on a pool, get in and row it across and
// turn it. A boat is its steering client's to move: the robot runs the boat's
// physics, and the server, which only checks each move, must take every one
// — no correction sent back, no complaint in its log — and have the robot
// where the robot has itself.
func robotBoat(r *robotBot, rcon *rconClient, name string, srv *smoke.Server) error {
	if err := r.prepare(rcon, name, 540.5, 1.5,
		"kill @e[type=minecraft:oak_boat,x=555,y=-60,z=1,distance=..30]",
		"fill 538 -63 -6 572 -50 10 minecraft:air",
		"fill 538 -63 -6 572 -61 10 minecraft:stone",
		"fill 542 -62 -4 568 -61 6 minecraft:water",
		"clear "+name,
		"item replace entity "+name+" hotbar.6 with minecraft:oak_boat",
		"tp "+name+" 540.5 -60 1.5 -90 30",
	); err != nil {
		return err
	}
	logFrom := serverLogSize(srv)
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("hold 6", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.ask("usetoward 543 -61 1", 10*time.Second); err != nil {
		return fmt.Errorf("the boat onto the water: %w", err)
	}
	if _, err := r.ask("wait 10", 5*time.Second); err != nil {
		return err
	}
	id, err := r.entityID("minecraft:oak_boat", 543.5, 1.5)
	if err != nil {
		return fmt.Errorf("the boat: %w", err)
	}
	if _, err := r.ask("interact "+id, 20*time.Second); err != nil {
		return fmt.Errorf("getting in: %w", err)
	}
	if st, err := r.ask("state", 5*time.Second); err != nil || !strings.Contains(st, "riding="+id+":minecraft:oak_boat") {
		return fmt.Errorf("getting in: %q %v", st, err)
	}
	seated := func(what string) error {
		before, err := r.pos()
		if err != nil {
			return err
		}
		theirs, err := serverPos(rcon, name)
		if err != nil {
			return err
		}
		after, err := r.pos()
		if err != nil {
			return err
		}
		for k := range theirs {
			lo, hi := math.Min(before[k], after[k])-0.01, math.Max(before[k], after[k])+0.01
			if theirs[k] < lo || theirs[k] > hi {
				return fmt.Errorf("%s: the robot sits at %v then %v, the server has it at %v", what, before, after, theirs)
			}
		}
		return nil
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if err := seated("in the boat"); err != nil {
		return err
	}
	start, err := serverPos(rcon, "@e[type=minecraft:oak_boat,limit=1,sort=nearest,x=542,y=-60,z=1]")
	if err != nil {
		return err
	}
	// row: forward, then a turn to the left while rowing on, then let it glide
	for _, k := range []string{"keys forward 40", "keys forward,left 20", "keys none 1", "wait 60"} {
		if _, err := r.ask(k, 20*time.Second); err != nil {
			return err
		}
	}
	end, err := serverPos(rcon, "@e[type=minecraft:oak_boat,limit=1,sort=nearest,x=555,y=-60,z=1]")
	if err != nil {
		return err
	}
	if math.Hypot(end[0]-start[0], end[2]-start[2]) < 6 {
		return fmt.Errorf("the boat hardly moved: from %v to %v", start, end)
	}
	if err := seated("rowed across"); err != nil {
		return err
	}
	st, err := r.ask("state", 5*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(st, "corrections=0") {
		return fmt.Errorf("the server corrected the boat: %s", st)
	}
	if bad := complaints(serverLogFrom(srv, logFrom), name); len(bad) > 0 {
		return fmt.Errorf("the server complained of the boat:\n%s", strings.Join(bad, "\n"))
	}
	if _, err := r.ask("dismount", 10*time.Second); err != nil {
		return fmt.Errorf("getting out: %w", err)
	}
	if _, err := r.ask("wait 20", 5*time.Second); err != nil {
		return err
	}
	if _, err := r.agree(rcon, name); err != nil {
		return fmt.Errorf("got out: %w", err)
	}
	return nil
}

// robotFrames are the lines of a goroutine dump in the robot's own code
// (examples/robot/…), each once, at most n: where it is stuck.
func robotFrames(dump string, n int) string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(dump, "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, "examples/robot/"); i >= 0 && !seen[l] {
			seen[l] = true
			out = append(out, "  "+l[i:])
			if len(out) == n {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}
