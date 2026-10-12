package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testMilestones = []struct{ id, what string }{
	{"minecraft:story/root", "a crafting table"},
	{"minecraft:story/mine_stone", "stone mined"},
}

// The time of a milestone is the bot's event's, not the poll's: a table made at 22.5 s reads 22.5 s, not
// the 30 s of the poll that saw it; a poll never replaces it; a milestone the bot did not announce is the
// server's answer's, to the poll's second; an unrelated event is nothing.
func TestMilestoneTimeFromTheBotsEvent(t *testing.T) {
	start := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	events := filepath.Join(t.TempDir(), "events.jsonl")
	write := func(s string) {
		f, err := os.OpenFile(events, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		f.WriteString(s)
	}
	adv := func(id string, at time.Duration) string {
		return fmt.Sprintf(`{"data":{"id":%q,"title":"x"},"kind":"event","name":"advancement","ts":%q}`+"\n", id, start.Add(at).Format(time.RFC3339Nano))
	}
	var told []string
	var serverHas = map[string]bool{}
	cmd := func(c string) (string, error) {
		for id, has := range serverHas {
			if has && strings.Contains(c, id) {
				return "Test passed", nil
			}
		}
		return "Test failed", nil
	}
	m := newMilestoneTimes(start, testMilestones, events, "Builder", cmd, func(l string) { told = append(told, l) })
	clock := start
	m.now = func() time.Time { return clock }

	// the table made at 22.5 s; the harness's poll comes at 30 s and finds it on the server too
	write(adv("minecraft:story/root", 22500*time.Millisecond))
	write(`{"data":{"id":"minecraft:story/other"},"name":"advancement","ts":"` + start.Add(time.Second).Format(time.RFC3339Nano) + `"}` + "\n")
	serverHas["minecraft:story/root"] = true
	clock = start.Add(30 * time.Second)
	m.poll()
	if got, ok := m.Reached("minecraft:story/root"); !ok || got != 22500*time.Millisecond {
		t.Fatalf("the table: %v %v, want 22.5 s", got, ok)
	}
	if _, ok := m.Reached("minecraft:story/other"); ok {
		t.Error("an advancement that is no milestone was counted")
	}
	// the stone: no event (a bot that does not announce), the server has it when the poll asks at 31.2 s
	if _, ok := m.Reached("minecraft:story/mine_stone"); ok {
		t.Fatal("a milestone nobody earned")
	}
	serverHas["minecraft:story/mine_stone"] = true
	clock = start.Add(31200 * time.Millisecond)
	m.poll()
	if got, ok := m.Reached("minecraft:story/mine_stone"); !ok || got != 31200*time.Millisecond {
		t.Errorf("the stone from the server: %v %v, want 31.2 s", got, ok)
	}
	// a late event for the stone does not move the time already taken; nor a poll the table's
	write(adv("minecraft:story/mine_stone", 31*time.Second))
	clock = start.Add(60 * time.Second)
	m.poll()
	if got, _ := m.Reached("minecraft:story/mine_stone"); got != 31200*time.Millisecond {
		t.Errorf("a later event moved the stone to %v", got)
	}
	if m.Count() != 2 || len(told) != 2 {
		t.Errorf("%d reached, told %q", m.Count(), told)
	}
	// read from the file afresh, the first event of a milestone wins (a later one, even one before the start, is ignored)
	m2 := newMilestoneTimes(start, testMilestones, events, "Builder", nil, nil)
	write(adv("minecraft:story/root", -5*time.Second))
	m2.fromEvents()
	if got, _ := m2.Reached("minecraft:story/root"); got != 22500*time.Millisecond {
		t.Errorf("the first event of the file wins: %v", got)
	}
}
