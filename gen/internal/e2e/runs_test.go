package e2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// TestRunIndex: a run started, stopped and judged leaves its three entries
// in runs.jsonl, its result in meta.json, and a commit in the runs'
// repository with the run and the index.
func TestRunIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@t"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	start := time.Now()
	r, err := newRun(Options{RunsDir: dir, Version: "26.3"}, "days", "Settler", &smoke.Server{Port: 25650, Seed: "26265"})
	if err != nil || r == nil {
		t.Fatalf("newRun: %v", err)
	}
	if err := os.WriteFile(r.events(), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.end("output")
	runResult("days", start, errors.New("the robot died"))

	f, err := os.Open(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			Name, Source string
			Data         map[string]any
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("index line %q: %v", sc.Text(), err)
		}
		if e.Source != "mc26:e2e" || e.Data["run"] != filepath.Base(r.dir) {
			t.Errorf("entry %s: source %q, run %v", e.Name, e.Source, e.Data["run"])
		}
		if e.Name == "run-started" && e.Data["events"] != r.events() {
			t.Errorf("run-started events = %v, want %s", e.Data["events"], r.events())
		}
		names = append(names, e.Name)
	}
	if got := strings.Join(names, ","); got != "run-started,run-stopped,run-result" {
		t.Errorf("index = %s", got)
	}
	b, _ := os.ReadFile(filepath.Join(r.dir, "meta.json"))
	var m runMeta
	if err := json.Unmarshal(b, &m); err != nil || m.Result != "fail" || m.Error != "the robot died" || m.Ended == "" || m.Seed != "26265" {
		t.Errorf("meta.json = %s (%v)", b, err)
	}
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s", "--name-only").Output()
	if err != nil || !strings.Contains(string(out), filepath.Base(r.dir)+": fail") || !strings.Contains(string(out), "runs.jsonl") {
		t.Errorf("git log:\n%s (%v)", out, err)
	}
}

// TestRunNotCommittedInsideRepo: runs kept in a directory inside some other
// repository's tree (temp/e2e/runs in mc26) are not committed to it.
func TestRunNotCommittedInsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	dir := filepath.Join(repo, "temp", "e2e", "runs")
	if isRepoTop(dir) {
		t.Fatalf("%s taken for a repository's top level", dir)
	}
	if !isRepoTop(repo) {
		t.Fatalf("%s not taken for a repository's top level", repo)
	}
	start := time.Now()
	r, err := newRun(Options{RunsDir: dir, Version: "26.3"}, "inside", "Settler", &smoke.Server{Port: 25650})
	if err != nil || r == nil {
		t.Fatalf("newRun: %v", err)
	}
	r.end("output")
	runResult("inside", start, nil)
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", "HEAD").Output(); err == nil {
		t.Errorf("a commit in the enclosing repository: %s", out)
	}
	if out, _ := exec.Command("git", "-C", repo, "diff", "--cached", "--name-only").Output(); len(out) > 0 {
		t.Errorf("staged in the enclosing repository: %s", out)
	}
}

func TestDeliveryOf(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "d.json")
	os.WriteFile(good, []byte(`{"commit":"abc","signer":"owner"}`+"\n"), 0o644)
	if got := string(deliveryOf(good)); got != `{"commit":"abc","signer":"owner"}` {
		t.Errorf("a delivery: %q", got)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`not json`), 0o644)
	for _, p := range []string{"", bad, filepath.Join(dir, "none.json")} {
		if got := deliveryOf(p); got != nil {
			t.Errorf("deliveryOf(%q) = %q, want nothing", p, got)
		}
	}
}

// TestRunDirNames: every stage's kept run, and every scenario's, has a
// directory name a store of runs takes: <time>_<version>_<scenario: letters,
// digits, dashes>_<robot: those and underscores>. The bricks' ("brick: fell a
// tree") had a colon, and none of them was ever stored.
func TestRunDirNames(t *testing.T) {
	re := regexp.MustCompile(`^[0-9]{8}-[0-9]{6}_[A-Za-z0-9.]+_[A-Za-z0-9-]+_[A-Za-z0-9_-]+$`)
	at := time.Date(2026, 10, 9, 9, 58, 0, 0, time.UTC)
	stages := append(append([]flatStage{}, flatStages...), flatBricks...)
	for i, st := range stages {
		for _, stage := range []string{fmt.Sprintf("botflat-%d", i+1), fmt.Sprintf("botprobe-%d-r3", i+1)} {
			if name := runDirName(at, "26.3", flatRunName(stage, st), "Builder2"); !re.MatchString(name) {
				t.Errorf("stage %d (%q): run dir %q is no store's run name", i+1, st.name, name)
			}
		}
	}
	if got := runDirName(at, "26.3", "botflat-9-brick: craft planks, a pickaxe", "Builder"); got != "20261009-095800_26.3_botflat-9-brick-craft-planks-a-pickaxe_Builder" {
		t.Errorf("run dir %q", got)
	}
}

// TestWaitSkillEnd: a stage whose goal is counted in the pack waits for the
// bot to say its skill's end (its last trace comes before it), not longer
// than its limit.
func TestWaitSkillEnd(t *testing.T) {
	out := &syncBuffer{}
	go func() {
		time.Sleep(200 * time.Millisecond)
		out.Write([]byte("10:00:00 bot: do gather:minecraft:stone: done in 6.1s\n"))
	}()
	t0 := time.Now()
	if !waitSkillEnd(out, "gather:minecraft:stone", 3*time.Second) || time.Since(t0) < 200*time.Millisecond {
		t.Fatalf("the end said after 200 ms: waited %v", time.Since(t0))
	}
	t0 = time.Now()
	if waitSkillEnd(&syncBuffer{}, "gather:minecraft:stone", 300*time.Millisecond) || time.Since(t0) > 2*time.Second {
		t.Fatalf("no end said: waited %v", time.Since(t0))
	}
}

// TestReadBotLinesLongLine: a bot's line of 200 KB does not stop the reading
// (its writer never blocks); the lines after it come.
func TestReadBotLinesLongLine(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("10:00 bot: before\n"))
		pw.Write([]byte(strings.Repeat("x", 200<<10) + "\n"))
		pw.Write([]byte("10:01 bot: after\n"))
		pw.Close()
	}()
	var got []string
	done := make(chan error, 1)
	go func() { done <- readBotLines(pr, func(l string) { got = append(got, l) }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the reading stopped: the bot's writes would block")
	}
	if len(got) != 3 || got[2] != "10:01 bot: after" || !strings.Contains(got[1], "a line of 204800 bytes, cut") {
		t.Fatalf("lines %d: %q … %q", len(got), got[0], got[len(got)-1])
	}
}

// TestRunHarnessLog: a run keeps the harness's own lines from its start to its verdict in harness.log, beside its
// events: the start, what the harness said through Log meanwhile, the verdict; not the lines from before it.
func TestRunHarnessLog(t *testing.T) {
	dir := t.TempDir()
	log := teeLog(func(string, ...any) {})
	log("before the run: %s", "not in it")
	start := time.Now()
	r, err := newRun(Options{RunsDir: dir, Version: "26.3"}, "hlogscenario", "Settler", &smoke.Server{Port: 25650})
	if err != nil || r == nil {
		t.Fatalf("newRun: %v", err)
	}
	log("hlogscenario: step %d of %d", 2, 5)
	r.end("robot output")
	runResult("hlogscenario", start, errors.New("the robot stood still 90 s"))
	b, err := os.ReadFile(filepath.Join(r.dir, "harness.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"run started: scenario hlogscenario", "hlogscenario: step 2 of 5", "run ended", "verdict: scenario hlogscenario fail the robot stood still 90 s"} {
		if !strings.Contains(got, want) {
			t.Errorf("harness.log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "before the run") {
		t.Errorf("harness.log holds a line from before the run:\n%s", got)
	}
}

// The bench's version is in each run's meta.json (MC26_BENCH_VERSION), and left out where it is not set (a
// workstation): the runs viewer tells which bench code ran a run.
func TestRunMetaBenchVersion(t *testing.T) {
	for _, c := range []struct {
		name, env string
		set       bool
		want      string // the meta.json's benchVersion; "" for the key absent
	}{
		{"a tag", "v0.5.23", true, "v0.5.23"},
		{"a revision, dirty", "0123456789ab-dirty", true, "0123456789ab-dirty"},
		{"unknown", "unknown", true, "unknown"},
		{"empty", "", true, ""},
		{"unset", "", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("MC26_BENCH_VERSION", c.env)
			} else {
				if v, ok := os.LookupEnv("MC26_BENCH_VERSION"); ok {
					t.Setenv("MC26_BENCH_VERSION", v) // (restored at the end)
				}
				os.Unsetenv("MC26_BENCH_VERSION")
			}
			dir := t.TempDir()
			r, err := newRun(Options{RunsDir: dir, Version: "26.3"}, "benchversion"+c.name, "Settler", &smoke.Server{Port: 25650})
			if err != nil || r == nil {
				t.Fatalf("newRun: %v", err)
			}
			b, err := os.ReadFile(filepath.Join(r.dir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			got, present := m["benchVersion"]
			if c.want == "" && present {
				t.Errorf("benchVersion present (%v) with the variable %q", got, c.env)
			}
			if c.want != "" && got != c.want {
				t.Errorf("benchVersion %v, want %q", got, c.want)
			}
		})
	}
}
