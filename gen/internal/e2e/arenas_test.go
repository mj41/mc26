package e2e

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type fakeArenaRCON struct{ cmds []string }

func (f *fakeArenaRCON) command(c string) (string, error) {
	f.cmds = append(f.cmds, c)
	return "", nil
}

func TestArenaBuildCoversWallsRoofFloor(t *testing.T) {
	rc := &fakeArenaRCON{}
	stopped := false
	if err := buildArenaWorld(3, rc, func() error { stopped = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Error("the server was not stopped")
	}
	boxes := map[int]bool{1000: false, 2000: false, 3000: false}
	roofs := map[int]bool{}
	reFill := regexp.MustCompile(`^fill (-?\d+) (-?\d+) (-?\d+) (-?\d+) (-?\d+) (-?\d+) (\S+)`)
	for _, c := range rc.cmds {
		if strings.Contains(c, "time ") || strings.Contains(c, "weather") {
			t.Errorf("arenas share one clock, got %q", c)
		}
		m := reFill.FindStringSubmatch(c)
		if m == nil {
			continue
		}
		v := make([]int, 6)
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+1])
		}
		ox := (v[0] + v[3]) / 2
		in := false
		for b := range boxes {
			if v[0] >= b-arenaHalf && v[3] <= b+arenaHalf && v[2] >= -arenaHalf && v[5] <= arenaHalf {
				in = true
				boxes[b] = true
				if v[1] == 128 && v[4] == 128 && v[0] == b-arenaHalf && v[3] == b+arenaHalf {
					roofs[b] = true
				}
			}
		}
		if !in {
			t.Errorf("wall command outside every arena (ox %d): %q", ox, c)
		}
		if (v[3]-v[0]+1)*(v[4]-v[1]+1)*(v[5]-v[2]+1) > 32768 {
			t.Errorf("fill over the limit: %q", c)
		}
	}
	for b, ok := range boxes {
		if !ok {
			t.Errorf("no wall command for the arena at %d", b)
		}
		if !roofs[b] {
			t.Errorf("no barrier roof at y=128 for the arena at %d", b)
		}
	}
}

func TestArenaWorldCache(t *testing.T) {
	work := t.TempDir() + "/work"
	os.MkdirAll(work, 0o755)
	rc := &fakeArenaRCON{}
	build := func(dir string) error {
		return buildArenaWorld(3, rc, func() error { return nil })
	}
	d1, err := arenaWorld(work, "26.1", "abc", 3, build)
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.cmds) == 0 {
		t.Fatal("the cold build ran no command")
	}
	n := len(rc.cmds)
	d2, err := arenaWorld(work, "26.1", "abc", 3, build)
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.cmds) != n {
		t.Errorf("a cached world ran %d commands", len(rc.cmds)-n)
	}
	if d1 != d2 {
		t.Errorf("dir %q then %q", d1, d2)
	}
	d3, _ := arenaWorld(work, "26.1", "abd", 3, build)
	if d3 == d1 {
		t.Errorf("a changed recipe kept the dir %q", d1)
	}
	if !strings.HasSuffix(d1, fmt.Sprintf("26.1-arenas-abc-n%d", 3)) {
		t.Errorf("dir %q", d1)
	}
}
