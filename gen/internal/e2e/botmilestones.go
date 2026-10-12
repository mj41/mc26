package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// A milestone's time (an advancement a bot earns: the crafting table, stone, the stone pickaxe…) used to be
// the time of the harness's poll that saw it, every 10 s from its own start: a table made at 22.5 s read as
// 30 s, and every comparison of a bench run with the simulator's was off by up to ten seconds a milestone. Now:
//
//   - from the bot's own event: the advancement event in the run's events.jsonl, which the bot writes the
//     moment the server tells it (its ts, minus the harness's start) — the time as it happened, to the
//     millisecond;
//   - for a bot that announces none, from the server by RCON as before but every second, so a milestone is
//     late by under a second, not ten;
//   - the earlier of the two stays: a poll never replaces the event's time, an event never a time
//     already taken from the server (the first sight wins).
//
// The harness's own poll (every 10 s) goes on judging a run (stalls, deadlines, deaths); only the milestones
// have their own faster clock.

// milestoneTimes are the milestones reached so far and when (since the harness's start).
type milestoneTimes struct {
	start time.Time
	list  []struct{ id, what string }
	now   func() time.Time
	cmd   rconCmd // the server's answer to a command (RCON); nil: events only
	name  string  // the bot's name, for the server's check
	log   func(string)

	mu      sync.Mutex
	reached map[string]time.Duration
	events  string // the run's events.jsonl ("": none kept)
	off     int64  // how far of it is read
}

func newMilestoneTimes(start time.Time, list []struct{ id, what string }, events, name string, cmd rconCmd, log func(string)) *milestoneTimes {
	return &milestoneTimes{start: start, list: list, now: time.Now, cmd: cmd, name: name, log: log,
		reached: map[string]time.Duration{}, events: events}
}

// note records a milestone's time unless one is kept already, and says it once.
func (m *milestoneTimes) note(id string, at time.Duration, source string) {
	if at < 0 {
		at = 0
	}
	m.mu.Lock()
	if _, ok := m.reached[id]; ok {
		m.mu.Unlock()
		return
	}
	m.reached[id] = at
	m.mu.Unlock()
	for _, ms := range m.list {
		if ms.id == id && m.log != nil {
			m.log(fmt.Sprintf("%s after %s (%s)", ms.what, at.Round(10*time.Millisecond), source))
		}
	}
}

// Reached is a milestone's time, and whether it has one.
func (m *milestoneTimes) Reached(id string) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.reached[id]
	return t, ok
}

// Count is how many milestones have a time.
func (m *milestoneTimes) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reached)
}

// fromEvents takes the bot's advancement events read since the last call: a milestone's time is its event's
// timestamp less the start.
func (m *milestoneTimes) fromEvents() {
	if m.events == "" {
		return
	}
	f, err := os.Open(m.events)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(m.off, io.SeekStart); err != nil {
		return
	}
	b, _ := io.ReadAll(f)
	i := strings.LastIndexByte(string(b), '\n')
	if i < 0 {
		return
	}
	m.off += int64(i + 1)
	for _, l := range strings.Split(string(b[:i]), "\n") {
		if !strings.Contains(l, `"name":"advancement"`) {
			continue
		}
		var e struct {
			Name string    `json:"name"`
			TS   time.Time `json:"ts"`
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(l), &e) != nil || e.Name != "advancement" {
			continue
		}
		for _, ms := range m.list {
			if ms.id == e.Data.ID {
				m.note(ms.id, e.TS.Sub(m.start), "the bot's event")
			}
		}
	}
}

// fromServer asks the server for each milestone not yet reached; one it has is taken as of now.
func (m *milestoneTimes) fromServer() {
	if m.cmd == nil {
		return
	}
	for _, ms := range m.list {
		if _, ok := m.Reached(ms.id); ok {
			continue
		}
		resp, err := m.cmd(fmt.Sprintf("execute if entity @a[name=%s,advancements={%s=true}]", m.name, ms.id))
		if err == nil && strings.Contains(resp, "passed") {
			m.note(ms.id, m.now().Sub(m.start), "the server's answer")
		}
	}
}

// poll is one round: the bot's events first (their times are the exact ones), then the server's answer for
// what no event told.
func (m *milestoneTimes) poll() {
	m.fromEvents()
	m.fromServer()
}

// run polls every period till stop is closed.
func (m *milestoneTimes) run(period time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			m.poll()
		}
	}
}
