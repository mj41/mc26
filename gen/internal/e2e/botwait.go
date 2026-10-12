package e2e

import (
	"encoding/json"
	"fmt"
	"io"
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

// serverNight reports whether the server's day clock is in the night (mobs spawn), and false for ok when
// the clock could not be read.
func serverNight(cmd rconCmd) (night, ok bool) {
	resp, err := cmd("time query daytime")
	if err != nil {
		return false, false
	}
	f := strings.Fields(resp)
	if len(f) == 0 {
		return false, false
	}
	t, err := strconv.Atoi(f[len(f)-1])
	if err != nil {
		return false, false
	}
	return t >= 13000 && t < 23000, true
}

// underOpenSky reports whether nothing but air (leaves are no roof) is over the player's head: the world
// surface without leaves is at its feet.
func underOpenSky(cmd rconCmd, name string) bool {
	resp, err := cmd(fmt.Sprintf("execute at %s positioned over motion_blocking_no_leaves if entity %s[distance=..1.5]", name, name))
	return err == nil && strings.Contains(resp, "passed")
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
