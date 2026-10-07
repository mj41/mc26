// Package e2e builds the example bots of the kit (its examples/ directory, assembled
// against a built library) and drives them against a vanilla server of the
// same version: the
// server-list ping, the daze bot reacting to RCON-driven chat, items and
// teleports, the minimal bot, the auto-fisher and a small pressure test; then
// mcadump over a region file the server wrote.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mj41/mc26/gen/internal/build"
	"github.com/mj41/mc26/gen/internal/fixtures"
	"github.com/mj41/mc26/gen/internal/kit"
	"github.com/mj41/mc26/gen/internal/limits"
	"github.com/mj41/mc26/gen/internal/smoke"
)

// Options configures one run.
type Options struct {
	Version   string
	DataDir   string // for the expected protocol number
	KitDir    string // the kit tree assembled against the built library of the same version
	CrossLang string // gen/crosslang: the reader written from the JSON alone, for the save check
	NodesPath string // gen/hand-crafted/nodes.json
	Fixtures  string // where to cut the save tests' fixture world from the server's world (empty: do not)
	JarPath   string
	WorkDir   string
	Port      int
	Runtime   string // see smoke.Server
	// Only, when set, runs just the scenarios it names (and skips the rest,
	// which a scenario after the server stopped may depend on).
	Only []string
	Log  func(format string, args ...any)
	// RunsDir keeps every robot run: its events and what it ran (runs.go),
	// to be replayed later (go-mc26-robotview -replay); empty keeps none.
	RunsDir string
	KitSrc  string // the go-mc26-kit checkout the kit was assembled from, for its commit
}

// wanted reports whether the run includes the scenario name.
func (o Options) wanted(name string) bool {
	if len(o.Only) == 0 {
		return true
	}
	for _, n := range o.Only {
		if n == name {
			return true
		}
	}
	return false
}

// named reports whether the run names the scenario: the long ones run only
// when asked for.
func (o Options) named(name string) bool {
	for _, n := range o.Only {
		if n == name {
			return true
		}
	}
	return false
}

// Run builds the bots, runs the scenarios and returns the first failure.
func Run(o Options) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	v, err := build.ReadVersion(o.DataDir)
	if err != nil {
		return err
	}
	var (
		bin string        // the built examples, once built
		srv *smoke.Server // the flat world's server, once started
	)
	type scenario struct {
		name string
		long bool // run only when named (--only)
		fn   func() error
	}
	// on the flat world's server
	scenarios := []scenario{
		{"mcping", false, func() error { return scenarioPing(o, bin, srv, v) }},
		{"daze", false, func() error { return scenarioDaze(o, bin, srv) }},
		{"twobots", false, func() error { return scenarioTwoBots(o, bin, srv) }},
		{"minimal", false, func() error { return scenarioLogin(o, bin, srv, "minimal", "Minimal", 20*time.Second, "Login success") }},
		{"autofish", false, func() error {
			return scenarioLogin(o, bin, srv, "autofish", "Fisher", 25*time.Second, "Login success", "Game start")
		}},
		{"pressureTest", false, func() error { return scenarioPressure(o, bin, srv) }},
		{"robot", false, func() error { return scenarioRobot(o, bin, srv) }},
	}
	// after it stopped: the world it wrote, then servers of their own
	after := []scenario{
		{"mcadump", false, func() error { return scenarioMcadump(o, bin, srv) }},
		{"savechunk", false, func() error { return scenarioSaveChunk(o, srv) }},
		{"saveschema", false, func() error { return scenarioSaveSchema(o, srv) }},
		{"fixtures", false, func() error { return scenarioFixtures(o, srv) }},
		{"survival", false, func() error { return scenarioSurvival(o, bin) }},
		{"days", true, func() error { return scenarioDays(o, bin) }},
		{"week", true, func() error { return scenarioWeek(o, bin) }},
		{"village", true, func() error { return scenarioVillage(o, bin) }},
		{"field", true, func() error { return scenarioField(o, bin) }},
		{"fieldwild", true, func() error { return scenarioFieldWild(o, bin) }},
		{"farmstart", true, func() error { return scenarioFarmStart(o, bin) }},
		{"fish", true, func() error { return scenarioFish(o, bin) }},
	}
	// a name not known (a typo) would run nothing and pass
	var known []string
	for _, sc := range append(scenarios, after...) {
		known = append(known, sc.name)
	}
	for _, n := range o.Only {
		if !slices.Contains(known, n) {
			return fmt.Errorf("--only: no scenario %q (known: %s)", n, strings.Join(known, ", "))
		}
	}
	if bin, err = buildExamples(o); err != nil {
		return err
	}
	// stopped from outside (a test bench's pod going, Ctrl-C): the robots'
	// runs ended as a scenario's end would, every server running stopped
	// (the flat world's, or a survival world's), then out
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	go func() {
		s := <-stop
		o.Log("e2e: %v: the robots stopped, their runs ended", s)
		running.closeAll()
		servers.stopAll()
		os.Exit(128 + int(s.(syscall.Signal)))
	}()
	srv = &smoke.Server{
		Version: o.Version, JarPath: o.JarPath, WorkDir: filepath.Join(o.WorkDir, "server"),
		Port: o.Port, Runtime: o.Runtime, Log: o.Log,
	}
	if err := servers.start(srv); err != nil {
		return err
	}
	defer servers.stop(srv)
	if err := srv.WaitReady(4 * time.Minute); err != nil {
		return err
	}

	failed, ran := 0, 0
	for _, sc := range scenarios {
		if !o.wanted(sc.name) {
			continue
		}
		ran++
		start := time.Now()
		err := sc.fn()
		runResult(sc.name, start, err)
		if err != nil {
			failed++
			o.Log("e2e %-12s FAIL (%s): %v", sc.name, time.Since(start).Round(time.Second), err)
			continue
		}
		o.Log("e2e %-12s ok   (%s)", sc.name, time.Since(start).Round(time.Second))
	}
	servers.stop(srv)
	for _, sc := range after {
		if !o.wanted(sc.name) || sc.long && !o.named(sc.name) {
			continue
		}
		ran++
		start := time.Now()
		err := sc.fn()
		runResult(sc.name, start, err)
		if err != nil {
			failed++
			o.Log("e2e %-12s FAIL: %v", sc.name, err)
			continue
		}
		o.Log("e2e %-12s ok", sc.name)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d scenarios failed", failed, ran)
	}
	return nil
}

// servers is the servers started and not yet stopped: stopped from outside,
// the harness stops each (a survival world's too, not only the flat one's).
var servers = &serverSet{m: map[*smoke.Server]*sync.Once{}}

type serverSet struct {
	mu sync.Mutex
	m  map[*smoke.Server]*sync.Once
}

// start starts srv and keeps it in the set.
func (s *serverSet) start(srv *smoke.Server) error {
	if err := srv.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	s.m[srv] = new(sync.Once)
	s.mu.Unlock()
	return nil
}

// stop stops srv once, whoever asks first (a scenario's end, the signal);
// a second caller waits for the first one's stop.
func (s *serverSet) stop(srv *smoke.Server) {
	s.mu.Lock()
	once := s.m[srv]
	s.mu.Unlock()
	if once == nil {
		return // stopped before, or never started here
	}
	once.Do(srv.Stop)
	s.mu.Lock()
	delete(s.m, srv)
	s.mu.Unlock()
}

// stopAll stops every server still running.
func (s *serverSet) stopAll() {
	s.mu.Lock()
	all := make([]*smoke.Server, 0, len(s.m))
	for srv := range s.m {
		all = append(all, srv)
	}
	s.mu.Unlock()
	for _, srv := range all {
		s.stop(srv)
	}
}

// buildExamples compiles every example of the kit tree (whose workspace
// points at the built library) and returns the bin directory.
func buildExamples(o Options) (string, error) {
	kitDir, err := filepath.Abs(o.KitDir)
	if err != nil {
		return "", err
	}
	examples := filepath.Join(kitDir, "examples")
	if _, err := os.Stat(kit.WorkFile(kitDir)); err != nil {
		return "", fmt.Errorf("%s is not an assembled kit tree (build first)", kitDir)
	}
	bin := filepath.Join(o.WorkDir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(examples)
	if err != nil {
		return "", err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if m, _ := filepath.Glob(filepath.Join(examples, e.Name(), "*.go")); len(m) == 0 {
			continue
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(bin, e.Name()), "./examples/"+e.Name())
		cmd.Dir = kitDir
		cmd.Env = kit.Env(kitDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("building example %s: %v\n%s", e.Name(), err, out)
		}
		n++
	}
	o.Log("e2e: %d examples built from %s", n, kitDir)
	return bin, nil
}

// runFor starts a bot, lets it run until every want string appeared in its
// output or the timeout passed, then stops it and returns the output.
func runFor(ctx context.Context, bin string, args []string, timeout time.Duration, want ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case <-done:
			return out.String(), missing(out.String(), want)
		case <-time.After(500 * time.Millisecond):
			if missing(out.String(), want) == nil {
				cancel()
				<-done
				return out.String(), nil
			}
		}
	}
}

func missing(output string, want []string) error {
	for _, w := range want {
		if !strings.Contains(output, w) {
			return fmt.Errorf("output lacks %q", w)
		}
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func scenarioPing(o Options, bin string, srv *smoke.Server, v *build.Info) error {
	out, err := exec.Command(filepath.Join(bin, "mcping"), srv.Addr()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, out)
	}
	// The server reports its display name ("26.3 Pre-Release 2"), which is
	// the id for releases only.
	proto := fmt.Sprintf("[%d]", v.ProtocolVersion)
	if !strings.Contains(string(out), proto) || !(strings.Contains(string(out), v.Name) || strings.Contains(string(out), v.ID)) {
		return fmt.Errorf("ping output lacks %s and %q/%q:\n%s", proto, v.Name, v.ID, out)
	}
	return nil
}

// scenarioDaze joins with daze, then drives the server over RCON and checks
// that the bot saw the chat, the item and the teleport.
func scenarioDaze(o Options, bin string, srv *smoke.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "daze"), "-address", srv.Addr(), "-name", "Daze")
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	if err := waitFor(out, 40*time.Second, "Login success", "Game start"); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 15))
	}
	// A few chunks before driving it.
	if err := waitFor(out, 20*time.Second, "Load chunk"); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 15))
	}

	rcon, err := dialRCON(srv.RCONAddr(), srv.RCONPassword)
	if err != nil {
		return fmt.Errorf("rcon: %w", err)
	}
	defer rcon.close()
	commands := []struct{ cmd, wantResp string }{
		{"list", "Daze"},
		{"say hello from rcon", ""},
		{`tellraw Daze {"text":"private hi"}`, ""},
		{"give Daze minecraft:stone 3", "Gave 3"},
		{"tp Daze ~ ~5 ~", "Teleported Daze"},
		// the fixture cut needs entity chunks near the spawn, and the animals a
		// flat world generates there are chance (a fresh 26.3 world had none):
		// two of our own in two chunks, last run's pair removed first
		{"kill @e[tag=e2e_fixture]", ""},
		{`summon minecraft:sheep 3 -60 3 {NoAI:1b,Tags:["e2e_fixture"]}`, "Summoned"},
		{`summon minecraft:cow -5 -60 -5 {NoAI:1b,Tags:["e2e_fixture"]}`, "Summoned"},
		// the player's file: written for an online player by save-all, which is
		// what the save-schema scenario reads afterwards
		{"save-all flush", "Saved the game"},
	}
	for _, c := range commands {
		resp, err := rcon.command(c.cmd)
		if err != nil {
			return fmt.Errorf("rcon %q: %w", c.cmd, err)
		}
		if c.wantResp != "" && !strings.Contains(resp, c.wantResp) {
			return fmt.Errorf("rcon %q answered %q, want %q", c.cmd, resp, c.wantResp)
		}
	}
	// What daze must have logged: the broadcast, the private message, the item.
	if err := waitFor(out, 20*time.Second, "hello from rcon", "private hi", "minecraft:stone"); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 25))
	}
	// The chunks keep coming while the commands run, so wait for the count
	// rather than sampling it once the commands are through.
	if err := waitForCount(out, 60*time.Second, "Load chunk", 20); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 25))
	}
	return nil
}

// dazeBot is a running daze whose stdin is a console (chat, /commands).
type dazeBot struct {
	name  string
	cmd   *exec.Cmd
	out   *syncBuffer
	stdin io.WriteCloser
	stop  context.CancelFunc
}

func startDaze(bin string, srv *smoke.Server, name string) (*dazeBot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "daze"), "-address", srv.Addr(), "-name", name)
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
	return &dazeBot{name: name, cmd: cmd, out: out, stdin: stdin, stop: cancel}, nil
}

// say types one line on the bot's console.
func (b *dazeBot) say(line string) error {
	_, err := io.WriteString(b.stdin, line+"\n")
	return err
}

func (b *dazeBot) close() {
	_ = b.stdin.Close()
	b.stop()
	_ = b.cmd.Wait()
}

// scenarioTwoBots runs two daze bots on the server: each sees the other's
// chat, and one of them, made an operator over RCON, teleports itself to the
// other and gives it an item — the other bot sees the item, the server sees
// the positions.
func scenarioTwoBots(o Options, bin string, srv *smoke.Server) error {
	one, err := startDaze(bin, srv, "DazeOne")
	if err != nil {
		return err
	}
	defer one.close()
	if err := waitFor(one.out, 40*time.Second, "Game start"); err != nil {
		return fmt.Errorf("DazeOne: %v\n%s", err, tail(one.out.String(), 10))
	}
	two, err := startDaze(bin, srv, "DazeTwo")
	if err != nil {
		return err
	}
	defer two.close()
	if err := waitFor(two.out, 40*time.Second, "Game start"); err != nil {
		return fmt.Errorf("DazeTwo: %v\n%s", err, tail(two.out.String(), 10))
	}
	// DazeTwo greeted the server on start; DazeOne must have heard it.
	if err := waitFor(one.out, 15*time.Second, "<DazeTwo> Hello, world"); err != nil {
		return fmt.Errorf("DazeOne did not see DazeTwo's greeting: %v\n%s", err, tail(one.out.String(), 10))
	}
	// Chat both ways.
	if err := one.say("ping from one"); err != nil {
		return err
	}
	if err := waitFor(two.out, 15*time.Second, "<DazeOne> ping from one"); err != nil {
		return fmt.Errorf("DazeTwo did not see DazeOne's chat: %v\n%s", err, tail(two.out.String(), 10))
	}
	if err := two.say("pong from two"); err != nil {
		return err
	}
	if err := waitFor(one.out, 15*time.Second, "<DazeTwo> pong from two"); err != nil {
		return fmt.Errorf("DazeOne did not see DazeTwo's chat: %v\n%s", err, tail(one.out.String(), 10))
	}

	// DazeOne becomes an operator and acts on DazeTwo with commands of its own.
	rcon, err := dialRCON(srv.RCONAddr(), srv.RCONPassword)
	if err != nil {
		return fmt.Errorf("rcon: %w", err)
	}
	defer rcon.close()
	defer rcon.command("deop DazeOne")
	for _, c := range []string{"op DazeOne", "tp DazeTwo 10 -60 10"} {
		if _, err := rcon.command(c); err != nil {
			return fmt.Errorf("rcon %q: %w", c, err)
		}
	}
	if err := one.say("/tp DazeOne DazeTwo"); err != nil {
		return err
	}
	var pos [3]float64
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := rcon.command("data get entity DazeOne Pos")
		if err != nil {
			return fmt.Errorf("rcon data get: %w", err)
		}
		pos, err = parsePos(resp)
		if err == nil && near(pos, [3]float64{10, -60, 10}) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DazeOne did not arrive next to DazeTwo: %q", resp)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := one.say("/give DazeTwo minecraft:diamond 1"); err != nil {
		return err
	}
	if err := waitFor(two.out, 15*time.Second, "minecraft:diamond"); err != nil {
		return fmt.Errorf("DazeTwo did not receive DazeOne's diamond: %v\n%s", err, tail(two.out.String(), 10))
	}
	return nil
}

// parsePos reads "X has the following entity data: [1.5d, -60.0d, 2.5d]".
func parsePos(resp string) ([3]float64, error) {
	var pos [3]float64
	i, j := strings.Index(resp, "["), strings.LastIndex(resp, "]")
	if i < 0 || j <= i {
		return pos, fmt.Errorf("no position in %q", resp)
	}
	parts := strings.Split(resp[i+1:j], ",")
	if len(parts) != 3 {
		return pos, fmt.Errorf("no position in %q", resp)
	}
	for k, p := range parts {
		p = strings.TrimSuffix(strings.TrimSpace(p), "d")
		if _, err := fmt.Sscanf(p, "%g", &pos[k]); err != nil {
			return pos, fmt.Errorf("position %q: %w", resp, err)
		}
	}
	return pos, nil
}

func near(a, b [3]float64) bool {
	for k := range a {
		d := a[k] - b[k]
		if d < -1.5 || d > 1.5 {
			return false
		}
	}
	return true
}

// syncBuffer is a bytes.Buffer safe for a writer (the bot's output) and a
// reader (the checks) on different goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForCount waits until want has appeared at least n times in the output.
func waitForCount(out interface{ String() string }, timeout time.Duration, want string, n int) error {
	deadline := time.Now().Add(timeout)
	for {
		got := strings.Count(out.String(), want)
		if got >= n {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%q appeared %d times in %s, want %d", want, got, timeout, n)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func waitFor(out interface{ String() string }, timeout time.Duration, want ...string) error {
	deadline := time.Now().Add(timeout)
	for {
		if missing(out.String(), want) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return missing(out.String(), want)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func scenarioLogin(o Options, bin string, srv *smoke.Server, example, name string, timeout time.Duration, want ...string) error {
	out, err := runFor(context.Background(), filepath.Join(bin, example), []string{"-address", srv.Addr(), "-name", name}, timeout, want...)
	if err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out, 15))
	}
	return nil
}

func scenarioPressure(o Options, bin string, srv *smoke.Server) error {
	const n = 3
	var want []string
	for i := 0; i < n; i++ {
		want = append(want, fmt.Sprintf("[%d]Login success", i), fmt.Sprintf("[%d]Game start", i))
	}
	out, err := runFor(context.Background(), filepath.Join(bin, "pressureTest"), []string{"-address", srv.Addr(), "-number", fmt.Sprint(n)}, 40*time.Second, want...)
	if err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out, 15))
	}
	return nil
}

// scenarioMcadump dumps a region file the server wrote (after it stopped).
// scenarioSaveChunk converts every chunk of a region file the server wrote,
// which is the only place a real save file of this version is read.
func scenarioSaveChunk(o Options, srv *smoke.Server) error {
	dir := filepath.Join(srv.WorkDir, "world", "dimensions", "minecraft", "overworld", "region")
	regions, _ := filepath.Glob(filepath.Join(dir, "r.*.mca"))
	if len(regions) == 0 {
		return fmt.Errorf("the server wrote no overworld region file under %s", srv.WorkDir)
	}
	path, err := filepath.Abs(dir) // every region: one of them held what the others did not
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "test", kit.LibModule+"/level", "-run", "TestSaveChunk", "-count=1", "-v")
	cmd.Dir = o.KitDir
	cmd.Env = kit.Env(o.KitDir, "MC26_SAVE_REGION="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, tail(string(out), 12))
	}
	// `go test` is happy when a pattern matches nothing, which would make this
	// scenario pass without reading a chunk
	if !strings.Contains(string(out), "converted ") {
		return fmt.Errorf("the test did not run (no chunk was converted):\n%s", tail(string(out), 12))
	}
	return nil
}

// scenarioFixtures cuts the save package's fixture world out of the world the
// server wrote (a few chunks, entities, level.dat, the bot's player file), so
// the library's own tests read this version's files from the next build on.
func scenarioFixtures(o Options, srv *smoke.Server) error {
	if o.Fixtures == "" {
		return nil
	}
	_, err := fixtures.Make(fixtures.Options{
		Version: o.Version, World: filepath.Join(srv.WorkDir, "world"), Out: o.Fixtures, Log: o.Log,
	})
	return err
}

// scenarioSaveSchema reads the same world with the reader that has only the
// JSON: the region container from nodes.json, the chunk from save_schema.json.
// It is the proof the save schema describes a world rather than hinting at it.
func scenarioSaveSchema(o Options, srv *smoke.Server) error {
	if o.CrossLang == "" {
		return fmt.Errorf("no cross-language reader directory given")
	}
	dir, err := filepath.Abs(filepath.Join(srv.WorkDir, "world"))
	if err != nil {
		return err
	}
	data, err := filepath.Abs(o.DataDir)
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "run", ".", "--data", data, "--nodes", o.NodesPath, "--world", dir)
	cmd.Dir = o.CrossLang
	cmd.Env = limits.GoEnv()
	out, err := cmd.CombinedOutput()
	o.Log("%s", strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("the JSON-only reader did not read the world the way save_schema.json says: %w", err)
	}
	return nil
}

func scenarioMcadump(o Options, bin string, srv *smoke.Server) error {
	// 26.x keeps every dimension under world/dimensions/<namespace>/<name>/.
	regions, _ := filepath.Glob(filepath.Join(srv.WorkDir, "world", "dimensions", "minecraft", "overworld", "region", "r.*.mca"))
	if len(regions) == 0 {
		return fmt.Errorf("the server wrote no overworld region file under %s", srv.WorkDir)
	}
	cmd := exec.Command(filepath.Join(bin, "mcadump"), regions[0])
	cmd.Dir = filepath.Join(o.WorkDir, "mcadump")
	if err := os.RemoveAll(cmd.Dir); err != nil { // chunks of a previous run
		return err
	}
	if err := os.MkdirAll(cmd.Dir, 0o755); err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, tail(string(out), 10))
	}
	return nil
}
