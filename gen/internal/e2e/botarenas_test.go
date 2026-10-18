package e2e

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestArenaBotNames: three bots named by slot for the server, and a run name each that a store takes.
func TestArenaBotNames(t *testing.T) {
	if got := strings.Join(arenaBotNames(3), ","); got != "Bot1,Bot2,Bot3" {
		t.Errorf("names %q", got)
	}
	if got := strings.Join(arenaRunNames(3), ","); got != "ipmbot-a1,ipmbot-a2,ipmbot-a3" {
		t.Errorf("run names %q", got)
	}
}

// TestArenaJoinDelays: the logins one after another, three seconds apart, the first at once.
func TestArenaJoinDelays(t *testing.T) {
	for i, want := range []time.Duration{0, 3 * time.Second, 6 * time.Second} {
		if got := arenaJoinDelay(i); got != want {
			t.Errorf("bot %d joins after %s, want %s", i, got, want)
		}
	}
}

// TestArenaBotSetupPutsTheBotInItsArena: each bot is taken to the start of its own arena and keeps it as its
// spawn; nothing in it touches the clock or the weather, which the arenas share.
func TestArenaBotSetupPutsTheBotInItsArena(t *testing.T) {
	for k, name := range arenaBotNames(3) {
		ox := (k + 1) * flatApart
		cmds := arenaBotSetup(name, ox)
		joined := strings.Join(cmds, "\n")
		for _, want := range []string{"tp " + name + " " + itoa(ox) + " 64 0", "spawnpoint " + name + " " + itoa(ox) + " 64 0"} {
			if !strings.Contains(joined, want) {
				t.Errorf("%s: no %q in\n%s", name, want, joined)
			}
		}
		for _, c := range cmds {
			if strings.HasPrefix(c, "time ") || strings.HasPrefix(c, "weather ") || strings.Contains(c, "advance_time") || strings.HasPrefix(c, "fill ") {
				t.Errorf("%s: %q touches the shared clock, weather or the world", name, c)
			}
		}
	}
}

// TestArenaServerSetupHasNoClock: the server's own setup is peaceful with no monsters and leaves the clock and
// the weather alone.
func TestArenaServerSetupHasNoClock(t *testing.T) {
	cmds := arenaServerSetup()
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"difficulty peaceful", "gamerule spawn_monsters false", "scoreboard objectives add deaths deathCount"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in\n%s", want, joined)
		}
	}
	for _, c := range cmds {
		if strings.HasPrefix(c, "time ") || strings.HasPrefix(c, "weather ") || strings.Contains(c, "advance_time") {
			t.Errorf("%q touches the shared clock or weather", c)
		}
	}
}

// TestArenaJob: the job of the wave is the bench's when it gave one, else one made for the wave.
func TestArenaJob(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	if got := arenaJob("job-77", at); got != "job-77" {
		t.Errorf("given job: %q", got)
	}
	if got := arenaJob("", at); got != "botarenas3-20261018-093000" {
		t.Errorf("made job: %q", got)
	}
}

// TestArenaVerdict: alive and moving passes; a death or a stall fails.
func TestArenaVerdict(t *testing.T) {
	if err := arenaVerdict("0", 10*time.Second); err != nil {
		t.Errorf("alive: %v", err)
	}
	if err := arenaVerdict("1", 0); err == nil {
		t.Error("a death passed")
	}
	if err := arenaVerdict("0", botDayStillLimit); err == nil {
		t.Error("a stall passed")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
