package e2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
