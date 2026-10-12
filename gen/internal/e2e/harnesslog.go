package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The harness's own lines of a run (harness.log, beside the events and the robot's output): what the harness
// said while the run lived — its start, the scenario's steps, the hard-limit failures, the verdict. The harness
// writes them through Options.Log for the whole process, so every line is kept (teeLog) and a run takes those
// from its start to its end: scenarios that run at once (the probe's bricks) share their lines, each told with
// its time and, where the harness names it, its scenario.

const maxHarnessLines = 200_000

var harnessLog struct {
	mu    sync.Mutex
	lines []string
	base  int // lines dropped from the front (past maxHarnessLines): a run's offset is counted from the first ever
}

// noteHarness keeps a line of the harness's, with its time.
func noteHarness(line string) {
	harnessLog.mu.Lock()
	defer harnessLog.mu.Unlock()
	harnessLog.lines = append(harnessLog.lines, time.Now().Format("2006-01-02T15:04:05.000Z07:00")+" "+strings.TrimRight(line, "\n"))
	if n := len(harnessLog.lines); n > maxHarnessLines {
		harnessLog.lines = harnessLog.lines[n-maxHarnessLines:]
		harnessLog.base += n - maxHarnessLines
	}
}

// harnessMark is where the harness's lines are now (for a run to take those after it).
func harnessMark() int {
	harnessLog.mu.Lock()
	defer harnessLog.mu.Unlock()
	return harnessLog.base + len(harnessLog.lines)
}

// harnessSince are the lines from the mark on (those dropped for room are not).
func harnessSince(mark int) []string {
	harnessLog.mu.Lock()
	defer harnessLog.mu.Unlock()
	from := max(mark-harnessLog.base, 0)
	if from > len(harnessLog.lines) {
		return nil
	}
	return append([]string(nil), harnessLog.lines[from:]...)
}

// teeLog is a Log that keeps each line for the runs as well as passing it on.
func teeLog(log func(format string, args ...any)) func(format string, args ...any) {
	return func(format string, args ...any) {
		noteHarness(fmt.Sprintf(format, args...))
		log(format, args...)
	}
}

// writeHarness writes the run's harness.log: the harness's lines since the run began (rewritten as the run
// goes on to its end and its verdict).
func (r *run) writeHarness() {
	if r == nil {
		return
	}
	_ = os.WriteFile(filepath.Join(r.dir, "harness.log"), []byte(strings.Join(harnessSince(r.hmark), "\n")+"\n"), 0o644)
}
