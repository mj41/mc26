package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServer answers the commands the judge asks, from a table.
type fakeServer struct {
	daytime   string
	underSky  bool // "passed" for the open-sky check
	inventory string
	invErr    error
}

func (f *fakeServer) cmd(c string) (string, error) {
	switch {
	case strings.HasPrefix(c, "time query daytime"):
		return "The time is " + f.daytime, nil
	case strings.HasPrefix(c, "execute at"):
		if f.underSky {
			return "Test passed", nil
		}
		return "Test failed", nil
	case strings.HasPrefix(c, "data get entity"):
		return f.inventory, f.invErr
	}
	return "", fmt.Errorf("unknown command %q", c)
}

const invHeader = "Settler has the following entity data: "

func inv(items ...string) string {
	var parts []string
	for i, it := range items {
		parts = append(parts, fmt.Sprintf(`{Slot: %db, id: "%s", count: 1}`, i, it))
	}
	return invHeader + "[" + strings.Join(parts, ", ") + "]"
}

func TestInventoryFacts(t *testing.T) {
	for _, c := range []struct {
		resp          string
		pickaxe, wood bool
		ok            bool
	}{
		{inv(), false, false, true},
		{inv("minecraft:dirt", "minecraft:wooden_sword"), false, false, true},
		{inv("minecraft:stone_pickaxe"), true, false, true},
		{inv("minecraft:oak_log"), false, true, true},
		{inv("minecraft:birch_planks", "minecraft:stick"), false, true, true},
		{inv("minecraft:oak_wood"), false, false, true}, // (stripped wood blocks and the like are not counted)
		{"No entity was found", false, false, false},
	} {
		p, w, ok := inventoryFacts(c.resp)
		if p != c.pickaxe || w != c.wood || ok != c.ok {
			t.Errorf("%q: pickaxe %v wood %v ok %v, want %v %v %v", c.resp, p, w, ok, c.pickaxe, c.wood, c.ok)
		}
	}
}

func newJudge(f *fakeServer) (*waitJudge, *[]string) {
	var told []string
	return newWaitJudge(f.cmd, "Settler", func(l string) { told = append(told, l) }), &told
}

var dawnWait = &waitAnnounce{For: "dawn", Why: "no pickaxe, no wood"}

// A wait the server confirms is taken, told once however many polls; standing still does not count while it is
// taken, and counts again 60 s after the dawn that ends it.
func TestWaitTakenAndDawn(t *testing.T) {
	f := &fakeServer{daytime: "14000", inventory: inv("minecraft:dirt", "minecraft:wooden_sword")}
	j, told := newJudge(f)
	still := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	now := still.Add(5 * time.Minute)
	for i := 0; i < 5; i++ {
		st := j.step(dawnWait)
		if !st.Taken || st.Dawn || st.Consumed {
			t.Fatalf("poll %d: %+v", i, st)
		}
		if got := st.adjustStill(still, now); !got.Equal(now) {
			t.Errorf("a taken wait left the still clock at %v", got)
		}
	}
	if len(*told) != 1 || !strings.HasPrefix((*told)[0], "wait taken: for dawn (no pickaxe, no wood)") {
		t.Fatalf("told: %q", *told)
	}
	f.daytime = "23500" // the morning
	st := j.step(dawnWait)
	if st.Taken || !st.Dawn || !st.Consumed {
		t.Fatalf("at dawn: %+v", st)
	}
	if got := st.adjustStill(still, now); !got.Equal(now.Add(60 * time.Second)) {
		t.Errorf("after dawn the still clock restarts at %v, want now+60s", got)
	}
	if len(*told) != 2 || (*told)[1] != "wait ended: dawn" {
		t.Errorf("told: %q", *told)
	}
	// the announcement is consumed: nothing to judge
	if st := j.step(nil); st != (waitStep{}) {
		t.Errorf("after the wait: %+v", st)
	}
}

// Each lie is refused, once for each change of the verdict; the clock keeps running.
func TestWaitRefused(t *testing.T) {
	base := func() *fakeServer {
		return &fakeServer{daytime: "15000", inventory: inv("minecraft:dirt")}
	}
	for _, c := range []struct {
		name string
		f    func(*fakeServer)
		want string
	}{
		{"daytime", func(f *fakeServer) { f.daytime = "6000" }, "it is not night"},
		{"under the sky", func(f *fakeServer) { f.underSky = true }, "under the open sky"},
		{"a pickaxe", func(f *fakeServer) { f.inventory = inv("minecraft:wooden_pickaxe") }, "has a pickaxe"},
		{"logs", func(f *fakeServer) { f.inventory = inv("minecraft:spruce_log") }, "has logs or planks"},
		{"planks", func(f *fakeServer) { f.inventory = inv("minecraft:oak_planks") }, "has logs or planks"},
		{"no entity", func(f *fakeServer) { f.inventory = "No entity was found" }, "could not be read"},
		{"rcon error", func(f *fakeServer) { f.invErr = fmt.Errorf("broken pipe") }, "could not be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := base()
			c.f(f)
			j, told := newJudge(f)
			still := time.Now()
			a := dawnWait
			for i := 0; i < 4; i++ {
				st := j.step(a)
				if st.Consumed {
					a = nil // (the harness forgets an announcement that is over: made by day, it is told once)
				}
				if st.Taken {
					t.Fatalf("a wait that is not true was taken: %+v", st)
				}
				if got := st.adjustStill(still, still.Add(time.Minute)); !got.Equal(still) {
					t.Errorf("a refused wait moved the still clock")
				}
			}
			if len(*told) != 1 || !strings.HasPrefix((*told)[0], "wait refused: ") || !strings.Contains((*told)[0], c.want) {
				t.Errorf("told: %q, want one refusal with %q", *told, c.want)
			}
		})
	}
	// a verdict that changes is told again: refused for a pickaxe, then taken once it is gone
	f := base()
	f.inventory = inv("minecraft:stone_pickaxe")
	j, told := newJudge(f)
	j.step(dawnWait)
	f.inventory = inv("minecraft:dirt")
	if st := j.step(dawnWait); !st.Taken {
		t.Errorf("not taken after the pickaxe was gone: %+v", st)
	}
	if len(*told) != 2 || !strings.HasPrefix((*told)[1], "wait taken") {
		t.Errorf("told: %q", *told)
	}
}

// With no announcement nothing is asked of the server.
func TestNoAnnouncementNoJudgement(t *testing.T) {
	f := &fakeServer{daytime: "15000"}
	j, told := newJudge(f)
	j.cmd = func(string) (string, error) { t.Error("the server was asked"); return "", nil }
	if st := j.step(nil); st != (waitStep{}) || len(*told) != 0 {
		t.Errorf("%+v %q", st, *told)
	}
}

func TestWaitTailReadsTheBotsHealthEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	tl := &waitTail{path: path}
	if tl.poll() != nil {
		t.Error("an announcement without a file")
	}
	write := func(s string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, _ = f.WriteString(s)
	}
	write(`{"data":{"what":"slow round","detail":"1200 ms"},"name":"health"}` + "\n")
	write(`{"data":{"t":1},"name":"tick"}` + "\n")
	if tl.poll() != nil {
		t.Error("a slow round is no wait")
	}
	write(`{"data":{"what":"waiting for dawn","detail":"no pickaxe, no wood"},"name":"health"}` + "\n" + `{"data":{"what":"waiting for dawn","detail":"half a li`)
	got := tl.poll()
	if got == nil || got.For != "dawn" || got.Why != "no pickaxe, no wood" {
		t.Fatalf("announcement: %+v", got)
	}
	tl.consume()
	if tl.poll() != nil {
		t.Error("a consumed announcement came back (the half line is not whole yet)")
	}
}
