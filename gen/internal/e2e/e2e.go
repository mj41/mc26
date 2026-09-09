// Package e2e builds the example bots of go-mc26-examples against a built
// library and drives them against a vanilla server of the same version: the
// server-list ping, the daze bot reacting to RCON-driven chat, items and
// teleports, the minimal bot, the auto-fisher and a small pressure test; then
// mcadump over a region file the server wrote.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mj41/mc26/gen/internal/limits"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mj41/mc26/gen/internal/build"
	"github.com/mj41/mc26/gen/internal/smoke"
)

// Options configures one run.
type Options struct {
	Version     string
	DataDir     string // for the expected protocol number
	LibDir      string // a built library tree
	ExamplesDir string // a go-mc26-examples checkout
	JarPath     string
	WorkDir     string
	Port        int
	Runtime     string // see smoke.Server
	Log         func(format string, args ...any)
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
	bin, err := buildExamples(o)
	if err != nil {
		return err
	}
	srv := &smoke.Server{
		Version: o.Version, JarPath: o.JarPath, WorkDir: filepath.Join(o.WorkDir, "server"),
		Port: o.Port, Runtime: o.Runtime, Log: o.Log,
	}
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Stop()
	if err := srv.WaitReady(4 * time.Minute); err != nil {
		return err
	}

	type scenario struct {
		name string
		fn   func() error
	}
	scenarios := []scenario{
		{"mcping", func() error { return scenarioPing(o, bin, srv, v) }},
		{"daze", func() error { return scenarioDaze(o, bin, srv) }},
		{"twobots", func() error { return scenarioTwoBots(o, bin, srv) }},
		{"minimal", func() error { return scenarioLogin(o, bin, srv, "minimal", "Minimal", 20*time.Second, "Login success") }},
		{"autofish", func() error {
			return scenarioLogin(o, bin, srv, "autofish", "Fisher", 25*time.Second, "Login success", "Game start")
		}},
		{"pressureTest", func() error { return scenarioPressure(o, bin, srv) }},
	}
	failed := 0
	for _, sc := range scenarios {
		start := time.Now()
		if err := sc.fn(); err != nil {
			failed++
			o.Log("e2e %-12s FAIL (%s): %v", sc.name, time.Since(start).Round(time.Second), err)
			continue
		}
		o.Log("e2e %-12s ok   (%s)", sc.name, time.Since(start).Round(time.Second))
	}
	srv.Stop()
	if err := scenarioMcadump(o, bin, srv); err != nil {
		failed++
		o.Log("e2e %-12s FAIL: %v", "mcadump", err)
	} else {
		o.Log("e2e %-12s ok", "mcadump")
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d scenarios failed", failed, len(scenarios)+1)
	}
	return nil
}

// buildExamples compiles every example against o.LibDir through a temporary
// go.work and returns the bin directory.
func buildExamples(o Options) (string, error) {
	examples, err := filepath.Abs(o.ExamplesDir)
	if err != nil {
		return "", err
	}
	lib, err := filepath.Abs(o.LibDir)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(examples, "go.mod")); err != nil {
		return "", fmt.Errorf("no examples module in %s", examples)
	}
	bin := filepath.Join(o.WorkDir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", err
	}
	work := filepath.Join(o.WorkDir, "go.work")
	content := fmt.Sprintf("go 1.25\n\nuse %s\n\nreplace github.com/mj41/go-mc26 => %s\n", examples, lib)
	if err := os.WriteFile(work, []byte(content), 0o644); err != nil {
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
		if _, err := os.Stat(filepath.Join(examples, e.Name(), "main.go")); err != nil {
			if m, _ := filepath.Glob(filepath.Join(examples, e.Name(), "*.go")); len(m) == 0 {
				continue
			}
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(bin, e.Name()), "./"+e.Name())
		cmd.Dir = examples
		cmd.Env = limits.GoEnv("GOWORK=" + work)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("building example %s: %v\n%s", e.Name(), err, out)
		}
		n++
	}
	o.Log("e2e: %d examples built against %s", n, lib)
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
