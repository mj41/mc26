package e2e

import (
	"fmt"
	"os/exec"
	"strings"
)

// procExit watches a bot's process for its end. The harness polls the server for the bot's position and
// milestones, and for a long time read "the process ended" off cmd.ProcessState — which is set only by a Wait
// that nobody had called while the run went on: a bot that died (a lost connection, a panic) was seen minutes
// later as "stalled", its last words long gone. The Wait is made here, at once, and its end is told as it is.
type procExit struct {
	done chan struct{}
	err  error // what Wait returned: nil on exit status 0, else an *exec.ExitError, "signal: killed", …
}

// watchProc starts the Wait. The process's output must be read by someone else (the harness's pipe reader).
func watchProc(cmd *exec.Cmd) *procExit {
	p := &procExit{done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p
}

// ended says whether the process is over.
func (p *procExit) ended() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// stop kills the process (if it still runs) and waits for its end.
func (p *procExit) stop(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
	<-p.done
}

// said is what the run reports when the process ended before the run did: its exit, its last line, its tail.
func (p *procExit) said(scenario, out string) string {
	last := ""
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			last = s
			break
		}
	}
	how := "exit status 0"
	if p.err != nil {
		how = p.err.Error()
	}
	return fmt.Sprintf("%s: the bot's process ended (%s); its last line: %s\n%s", scenario, how, last, tail(out, 30))
}
