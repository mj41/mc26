package e2e

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A bot's process that ends is told at once, with how and its last line — not read off cmd.ProcessState, which is
// set only by a Wait nobody made.
func TestProcExitIsTold(t *testing.T) {
	p := watchProc(exec.Command("sh", "-c", "exit 3")) // (not started: Wait fails at once, which is an ended process too)
	deadline := time.Now().Add(5 * time.Second)
	for !p.ended() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !p.ended() {
		t.Fatal("not told that the process ended")
	}
	got := p.said("botdayx", "first\nbot: connection lost\n\n")
	for _, want := range []string{"botdayx: the bot's process ended", "its last line: bot: connection lost"} {
		if !strings.Contains(got, want) {
			t.Errorf("said %q lacks %q", got, want)
		}
	}
}

func TestProcExitOfARealProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	if err := cmd.Start(); err != nil {
		t.Skip("no sh")
	}
	p := watchProc(cmd)
	deadline := time.Now().Add(5 * time.Second)
	for !p.ended() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !p.ended() || !strings.Contains(p.said("x", "a\n"), "exit status 3") {
		t.Errorf("ended %v: %s", p.ended(), p.said("x", "a\n"))
	}
	p.stop(cmd) // already over: returns
}
