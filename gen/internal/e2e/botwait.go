package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A legal night with no legal move: a bot with no pickaxe and no wood, at night, does right to wait closed in
// for dawn — and botday's hard limit (standing still four minutes) failed it. The bot announces such a wait with
// an event (a `health` event: {"what": "waiting for dawn", "detail": "no pickaxe, no wood"}, once at the start of
// the wait; the same line on its stdout, which the harness does not read); the harness takes the announcement only
// where it can verify the claim itself, from the server alone:
//
//   - night by the server's clock (13000 to 23000 of the day);
//   - the bot not under the open sky (the check botday's night rule makes);
//   - its inventory, read over RCON (data get entity <bot> Inventory), has no pickaxe and no logs or planks.
//
// While a wait is taken, standing still does not count; from dawn it counts again after botDayDawnGrace.
// harness.log gets "wait taken: …" or "wait refused: <why>" once for each change of the verdict, not each poll.

// botDayDawnGrace is how long after dawn a bot that waited may still stand before the still limit counts again.
const botDayDawnGrace = 60 * time.Second

// rconCmd runs a server command (rconClient.command, or a fake).
type rconCmd func(command string) (string, error)

// dayTicks is the length of a game day in ticks.
const dayTicks = 24000

var clockRe = regexp.MustCompile(`is at (-?[0-9]+) tick`)

// serverClock reads the server's day clock: the ticks the default clock (the overworld's) has run, from
// `time query time` — "Clock minecraft:overworld is at 13560 tick(s)". Since 26.1 the game has world clocks and
// no `time query day` or `daytime`: the server answers those with "Unknown or incomplete command", which is
// no error of the RCON call, and the number is not the last word of the answer. ok is false when the answer is
// not a clock.
func serverClock(cmd rconCmd) (ticks int, ok bool) {
	resp, err := cmd("time query time")
	if err != nil {
		return 0, false
	}
	m := clockRe.FindStringSubmatch(resp)
	if m == nil {
		return 0, false
	}
	t, err := strconv.Atoi(m[1])
	return t, err == nil
}

// serverNight reports whether the server's day clock is in the night (mobs spawn), and false for ok when
// the clock could not be read.
func serverNight(cmd rconCmd) (night, ok bool) {
	t, ok := serverClock(cmd)
	if !ok {
		return false, false
	}
	t %= dayTicks
	return t >= 13000 && t < 23000, true
}

// underOpenSky reports whether nothing but air (leaves are no roof) is over the player's head: the world
// surface without leaves is at its feet.
func underOpenSky(cmd rconCmd, name string) bool {
	resp, err := cmd(fmt.Sprintf("execute at %s positioned over motion_blocking_no_leaves if entity %s[distance=..1.5]", name, name))
	return err == nil && strings.Contains(resp, "passed")
}

// watcherSpaceCells is the most open cells the watcher lets a bot's own space hold: the night's mine (stairs, room,
// tunnels, branches) is some 1500 cells; the open land, or a pit open on a side to it, is more.
const watcherSpaceCells = 3000

// openSpaceLeaks reports whether the cells a mob can walk or fall through, from the player's feet and head cells
// (cells the game calls replaceable: air, plants, water, snow), are more than a space of its own holds
// (watcherSpaceCells) — a roof is not a shelter: a room open on a side to the outside lets the night in. The
// flood asks the server one cell at a time (`execute if block`); true also when the player's place cannot be read
// is NOT assumed: ok false then.
func openSpaceLeaks(cmd rconCmd, name string) (leaks, ok bool) {
	resp, err := cmd("data get entity " + name + " Pos")
	if err != nil {
		return false, false
	}
	m := posRe.FindStringSubmatch(resp)
	if m == nil {
		return false, false
	}
	f := func(s string) int { v, _ := strconv.ParseFloat(s, 64); return int(math.Floor(v)) }
	start := [3]int{f(m[1]), f(m[2]), f(m[3])}
	seen := map[[3]int]bool{}
	queue := [][3]int{start, {start[0], start[1] + 1, start[2]}}
	for _, c := range queue {
		seen[c] = true
	}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		for _, d := range [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			n := [3]int{c[0] + d[0], c[1] + d[1], c[2] + d[2]}
			if seen[n] {
				continue
			}
			seen[n] = true
			r, err := cmd(fmt.Sprintf("execute if block %d %d %d #minecraft:replaceable", n[0], n[1], n[2]))
			if err != nil {
				return false, false
			}
			if !strings.Contains(r, "passed") {
				continue
			}
			if len(seen) > watcherSpaceCells {
				return true, true
			}
			queue = append(queue, n)
		}
	}
	return false, true
}

var invIDRe = regexp.MustCompile(`id: "([^"]+)"`)

// inventoryFacts reads the answer of "data get entity <bot> Inventory": whether the bot has a pickaxe, and
// whether it has any log or planks. ok is false for an answer that is no inventory (no such entity).
func inventoryFacts(resp string) (pickaxe, wood, ok bool) {
	if !strings.Contains(resp, "entity data") {
		return false, false, false
	}
	for _, m := range invIDRe.FindAllStringSubmatch(resp, -1) {
		id := strings.TrimPrefix(m[1], "minecraft:")
		switch {
		case strings.HasSuffix(id, "_pickaxe"):
			pickaxe = true
		case strings.HasSuffix(id, "_log"), strings.HasSuffix(id, "_planks"):
			wood = true
		}
	}
	return pickaxe, wood, true
}

// waitAnnounce is the bot's announcement of a wait.
type waitAnnounce struct {
	For, Why string
}

// waitTail follows a run's events file for the bot's "waiting for dawn" health events; the latest not yet consumed is its
// announcement.
type waitTail struct {
	path string
	off  int64
	cur  *waitAnnounce
}

func (t *waitTail) poll() *waitAnnounce {
	f, err := os.Open(t.path)
	if err != nil {
		return t.cur
	}
	defer f.Close()
	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return t.cur
	}
	b, _ := io.ReadAll(f)
	i := strings.LastIndexByte(string(b), '\n')
	if i < 0 {
		return t.cur
	}
	t.off += int64(i + 1)
	for _, l := range strings.Split(string(b[:i]), "\n") {
		if !strings.Contains(l, `"name":"health"`) {
			continue
		}
		var e struct {
			Name string `json:"name"`
			Data struct {
				What   string `json:"what"`
				Detail string `json:"detail"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(l), &e) == nil && e.Name == "health" && e.Data.What == "waiting for dawn" {
			t.cur = &waitAnnounce{For: "dawn", Why: e.Data.Detail}
		}
	}
	return t.cur
}

// consume forgets the announcement: the wait is over.
func (t *waitTail) consume() { t.cur = nil }

// waitStep is what a poll of the judge says.
type waitStep struct {
	Taken    bool // a wait is taken now: standing still does not count
	Dawn     bool // a taken wait ended at this poll (the morning came)
	Consumed bool // the announcement is over: forget it
}

// adjustStill gives the still clock after a step: kept from running while a wait is taken, and restarted
// botDayDawnGrace after the dawn that ended one.
func (s waitStep) adjustStill(stillSince, now time.Time) time.Time {
	switch {
	case s.Taken:
		return now
	case s.Dawn:
		return now.Add(botDayDawnGrace)
	}
	return stillSince
}

// waitJudge takes or refuses the bot's announced waits.
type waitJudge struct {
	cmd   rconCmd
	name  string
	log   func(string)
	last  string // the verdict told last (told again only when it changes)
	taken bool
}

func newWaitJudge(cmd rconCmd, name string, log func(string)) *waitJudge {
	return &waitJudge{cmd: cmd, name: name, log: log}
}

func (j *waitJudge) tell(verdict string) {
	if verdict != j.last {
		j.last = verdict
		j.log(verdict)
	}
}

// step judges the bot's current announcement (nil: none) at this poll.
func (j *waitJudge) step(a *waitAnnounce) waitStep {
	if a == nil {
		return waitStep{}
	}
	night, ok := serverNight(j.cmd)
	if !ok {
		return waitStep{Taken: j.taken} // (the clock not readable now: the verdict as it was)
	}
	if !night {
		was := j.taken
		j.taken = false
		if was {
			j.tell("wait ended: dawn")
		} else {
			j.tell("wait refused: it is not night by the server's clock")
		}
		j.last = ""
		return waitStep{Dawn: was, Consumed: true}
	}
	refuse := func(why string) waitStep {
		j.taken = false
		j.tell("wait refused: " + why)
		return waitStep{}
	}
	if a.For != "dawn" {
		return refuse(fmt.Sprintf("it waits for %q, not for dawn", a.For))
	}
	if underOpenSky(j.cmd, j.name) {
		return refuse("the bot is under the open sky")
	}
	resp, err := j.cmd("data get entity " + j.name + " Inventory")
	if err != nil {
		return refuse("the inventory could not be read: " + err.Error())
	}
	pickaxe, wood, ok := inventoryFacts(resp)
	switch {
	case !ok:
		return refuse("the inventory could not be read: " + strings.TrimSpace(resp))
	case pickaxe:
		return refuse("the server's inventory has a pickaxe")
	case wood:
		return refuse("the server's inventory has logs or planks")
	}
	j.taken = true
	j.tell(fmt.Sprintf("wait taken: for dawn (%s): night by the server's clock, not under the sky, no pickaxe and no logs or planks in its inventory", a.Why))
	return waitStep{Taken: true}
}
