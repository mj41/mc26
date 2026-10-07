package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mj41/mc26/gen/internal/paths"
	"github.com/mj41/mc26/gen/internal/smoke"
)

// A run is one robot's part in a scenario, kept for later: its events as
// the robot wrote them (events.jsonl, the home event hub's envelope, each
// stamped with its time) and what was run (meta.json) — the version, the
// scenario, when, and the commits of mc26 and of the kit, each with whether
// its tree had changes not committed. go-mc26-robotview -replay plays the
// events again, as fast as asked: runs side by side, a run seen in minutes.
//
//	<RunsDir>/20261005-153012_26.3_days_Settler/{meta.json,events.jsonl,robot.log}
//
// Beside the runs, <RunsDir>/runs.jsonl is their index, append-only, in the
// events' envelope: run-started (where its events are, its meta), then
// run-stopped and run-result (ok or the error) — a reader follows it and
// switches to a new run as it starts.
type run struct {
	dir   string
	index string // <RunsDir>/runs.jsonl
	meta  runMeta
}

// runsByScenario is the latest run of each scenario, for its result.
var (
	runsMu         sync.Mutex
	runsByScenario = map[string]*run{}
)

type runMeta struct {
	Started  string `json:"started"`         // RFC 3339
	Ended    string `json:"ended,omitempty"` // when the robot was stopped
	Version  string `json:"version"`
	Scenario string `json:"scenario"`
	Robot    string `json:"robot"`
	Server   string `json:"server"`
	// the world: its generator and seed (empty: the flat world) — the same
	// seed again (MC26_SEED) is the same world, the robot's choices on it the
	// same; what differs is the mobs, the server's other chances, the timing
	LevelType string    `json:"levelType,omitempty"`
	Seed      string    `json:"seed,omitempty"`
	Mc26      gitCommit `json:"mc26"`
	Kit       gitCommit `json:"kit"`
	Host      string    `json:"host,omitempty"`
	// Bench is the test bench it ran on (MC26_BENCH: a cluster's
	// "bench-1"), empty on a workstation
	Bench string `json:"bench,omitempty"`
	// Delivery is the robot's code when a bench's supervisor put it there
	// (MC26_DELIVERY_FILE: a JSON object — commit, signer, who, when), as
	// it stood when this run started
	Delivery json.RawMessage `json:"delivery,omitempty"`
	Resumes  string          `json:"resumes,omitempty"` // the run this one goes on from (held, the robot started again)
	Result   string          `json:"result,omitempty"`  // "ok", "fail", or "held" (resumed by a run after it)
	Error    string          `json:"error,omitempty"`   // why it failed
}

type gitCommit struct {
	Commit  string `json:"commit,omitempty"`
	Subject string `json:"subject,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"` // changes not committed when it ran
}

// deliveryOf reads the delivery a bench's supervisor wrote: its JSON object,
// nil when there is none or it is not one.
func deliveryOf(path string) json.RawMessage {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 64<<10 {
		return nil
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return json.RawMessage(bytes.TrimSpace(b))
}

// newRun makes the run's directory and writes its meta.json; nil, nil when
// runs are not kept.
func newRun(o Options, scenario, robot string, srv *smoke.Server) (*run, error) {
	if o.RunsDir == "" {
		return nil, nil
	}
	now := time.Now()
	dir := filepath.Join(o.RunsDir, fmt.Sprintf("%s_%s_%s_%s", now.Format("20060102-150405"), o.Version, scenario, robot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	r := &run{dir: dir, index: filepath.Join(o.RunsDir, "runs.jsonl"), meta: runMeta{
		Started: now.Format(time.RFC3339), Version: o.Version, Scenario: scenario, Robot: robot,
		Server: srv.Addr(), LevelType: srv.LevelType, Seed: srv.Seed,
		Mc26: commitOf(paths.MustRoot()), Kit: commitOf(o.KitSrc), Host: host,
		Bench: os.Getenv("MC26_BENCH"), Delivery: deliveryOf(os.Getenv("MC26_DELIVERY_FILE")),
	}}
	runsMu.Lock()
	if prev := runsByScenario[scenario]; prev != nil && prev.meta.Result == "held" {
		r.meta.Resumes = filepath.Base(prev.dir)
	}
	runsByScenario[scenario] = r
	runsMu.Unlock()
	if err := r.write(); err != nil {
		return nil, err
	}
	r.announce("run-started", map[string]any{"events": r.events(), "meta": r.meta})
	return r, nil
}

// announce appends an entry about the run to the index, in one write.
func (r *run) announce(name string, data map[string]any) {
	data["run"], data["dir"] = filepath.Base(r.dir), r.dir
	now := time.Now()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	line, err := json.Marshal(map[string]any{
		"id": fmt.Sprintf("%016x%s", now.UnixNano(), hex.EncodeToString(b)), "ts": now.Format(time.RFC3339Nano),
		"source": "mc26:e2e", "kind": "event", "name": name, "data": data,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(r.index, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// runResult notes how a scenario ended on its latest run (one begun at or
// after since): in its meta.json and the index.
func runResult(scenario string, since time.Time, err error) {
	runsMu.Lock()
	r := runsByScenario[scenario]
	runsMu.Unlock()
	if r == nil {
		return
	}
	if t, perr := time.Parse(time.RFC3339, r.meta.Started); perr != nil || t.Before(since.Truncate(time.Second)) {
		return
	}
	r.meta.Result, r.meta.Error = "ok", ""
	if err != nil {
		r.meta.Result, r.meta.Error = "fail", err.Error()
	}
	_ = r.write()
	r.announce("run-result", map[string]any{"result": r.meta.Result, "error": r.meta.Error})
	r.commit()
}

// hold marks the run held — its robot stopped on err, to be started again
// in the same world once fixed (MC26_HOLD) — and commits it as it is.
func (r *run) hold(err error) {
	if r == nil {
		return
	}
	r.meta.Result, r.meta.Error = "held", err.Error()
	_ = r.write()
	r.announce("run-result", map[string]any{"result": "held", "error": r.meta.Error})
	r.commit()
}

// commit records the run, once it has its result, in the git repository the
// runs are kept in (mc26-runs) — its directory and the index, nothing else
// (another run may be writing beside it). Only a runs directory that is a
// repository's top level is one: runs under temp/e2e/runs (inside mc26), or
// in any other repository's tree, are not committed there.
func (r *run) commit() {
	repo := filepath.Dir(r.dir)
	if !isRepoTop(repo) {
		return
	}
	name := filepath.Base(r.dir)
	if out, err := exec.Command("git", "-C", repo, "add", "--", name, "runs.jsonl").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "runs: git add %s: %v %s\n", name, err, out)
		return
	}
	msg := fmt.Sprintf("%s: %s", name, r.meta.Result)
	if r.meta.Error != "" {
		msg += "\n\n" + r.meta.Error
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-q", "-m", msg, "--", name, "runs.jsonl").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "runs: git commit %s: %v %s\n", name, err, out)
	}
}

// isRepoTop reports whether dir is the top level of a git work tree.
func isRepoTop(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false
	}
	return sameDir(strings.TrimSpace(string(out)), dir)
}

// sameDir reports whether a and b name the same directory (absolute, clean,
// links resolved).
func sameDir(a, b string) bool {
	real := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return real(a) == real(b)
}

func (r *run) events() string { return filepath.Join(r.dir, "events.jsonl") }

func (r *run) write() error {
	b, err := json.MarshalIndent(r.meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "meta.json"), append(b, '\n'), 0o644)
}

// end notes when the run ended and keeps the robot's output beside its events.
func (r *run) end(output string) {
	if r == nil {
		return
	}
	r.meta.Ended = time.Now().Format(time.RFC3339)
	_ = r.write()
	_ = os.WriteFile(filepath.Join(r.dir, "robot.log"), []byte(output), 0o644)
	r.announce("run-stopped", map[string]any{"ended": r.meta.Ended})
}

// commitOf is the commit checked out in dir, its subject and whether the tree
// has changes; empty where dir is no git tree.
func commitOf(dir string) gitCommit {
	if dir == "" {
		return gitCommit{}
	}
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%H%x00%s").Output()
	if err != nil {
		return gitCommit{}
	}
	c := gitCommit{}
	if h, s, ok := strings.Cut(strings.TrimSpace(string(out)), "\x00"); ok {
		c.Commit, c.Subject = h, s
	}
	if st, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=no").Output(); err == nil {
		c.Dirty = len(strings.TrimSpace(string(st))) > 0
	}
	return c
}
