package e2e

import (
	"strings"
	"testing"
	"time"
)

type buf struct{ s string }

func (b *buf) String() string { return b.s }

// What the harness reads of a downstream bot's output goes through one place, the prefix configured
// (MC26_BOT_PREFIX, default "bot: "), and a bot whose lines do not carry it is told at once.
func TestBotLines(t *testing.T) {
	t.Setenv("MC26_BOT_PREFIX", "")
	t.Setenv("MC26_BOT", "")
	if botPackage() != DefaultBot || botPrefix() != DefaultBotPrefix {
		t.Fatalf("defaults: %q %q", botPackage(), botPrefix())
	}
	log := "10:00:00 bot: task: logs: 5 (get 5 oak_log)\n10:00:05 other: noise\n10:00:09 bot: do gather:x: done in 3s\n10:00:10 bot: task: stone (get 6)\n"
	if said, ok := botSays("10:00:00 bot: task: x"); !ok || said != "task: x" {
		t.Errorf("botSays: %q %v", said, ok)
	}
	if _, ok := botSays("10:00:05 other: noise"); ok {
		t.Error("a line of another's is the bot's")
	}
	if !botHas(log, "do gather:x: ") || botHas(log, "do gather:y: ") || botCount(log, "task: ") != 2 {
		t.Error("botHas/botCount")
	}
	if last, ok := botLast(log, "task: "); !ok || !strings.HasPrefix(last, "stone") {
		t.Errorf("botLast: %q %v", last, ok)
	}
	// another bot's prefix, by configuration: the same lines are read through it
	t.Setenv("MC26_BOT", "./mybot/cmd/mybot")
	t.Setenv("MC26_BOT_PREFIX", "mybot: ")
	if botPackage() != "./mybot/cmd/mybot" {
		t.Errorf("package: %q", botPackage())
	}
	if _, ok := botSays("10:00:00 bot: task: x"); ok {
		t.Error("the default prefix still read")
	}
	if said, ok := botSays("10:00:00 mybot: task: y"); !ok || said != "task: y" {
		t.Errorf("configured: %q %v", said, ok)
	}
}

func TestWaitBotPrefixFailsFast(t *testing.T) {
	t.Setenv("MC26_BOT_PREFIX", "ipm: ")
	// the bot's lines carry another prefix: told, with how many lines and what to set
	err := waitBotPrefix(&buf{"x: loaded\nx: task: a\nx: task: b\n"}, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "none holds") || !strings.Contains(err.Error(), "MC26_BOT_PREFIX") || !strings.Contains(err.Error(), "3 lines") {
		t.Errorf("a bot with another prefix: %v", err)
	}
	if err := waitBotPrefix(&buf{"10:00 ipm: loaded\n"}, 300*time.Millisecond); err != nil {
		t.Errorf("a bot that carries it: %v", err)
	}
}
