package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// a fake cgroup directory: the kernel's pressure files, as it writes them.
func writePSI(t *testing.T, dir, kind string, some, full float64) {
	t.Helper()
	s := "some avg10=" + trim(some) + " avg60=0.00 avg300=0.00 total=1234\nfull avg10=" + trim(full) + " avg60=0.00 avg300=0.00 total=99\n"
	if err := os.WriteFile(filepath.Join(dir, kind+".pressure"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func trim(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

type lines struct {
	mu sync.Mutex
	l  []string
}

func (n *lines) add(s string) { n.mu.Lock(); n.l = append(n.l, s); n.mu.Unlock() }
func (n *lines) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.l...)
}

func TestReadPSI(t *testing.T) {
	dir := t.TempDir()
	writePSI(t, dir, "cpu", 12.5, 3)
	p, err := readPSI(dir, "cpu")
	if err != nil || p.some != 12.5 || p.full != 3 {
		t.Fatalf("got %+v, %v", p, err)
	}
	if _, err := readPSI(dir, "io"); err == nil {
		t.Error("a missing file is an error")
	}
}

func TestSamplerTellsOverTenAndSummarises(t *testing.T) {
	dir := t.TempDir()
	writePSI(t, dir, "cpu", 2, 0)
	writePSI(t, dir, "memory", 1, 0)
	writePSI(t, dir, "io", 0, 0)
	var n lines
	s := startSampler(dir, 10*time.Millisecond, n.add)
	time.Sleep(60 * time.Millisecond)
	if got := n.all(); len(got) != 0 {
		t.Fatalf("under 10%% nothing is told: %v", got)
	}
	writePSI(t, dir, "cpu", 41.5, 20)
	time.Sleep(80 * time.Millisecond)
	writePSI(t, dir, "cpu", 3, 0)
	writePSI(t, dir, "memory", 7, 2)
	time.Sleep(60 * time.Millisecond)
	sum := s.summary()
	got := n.all()
	if len(got) == 0 || !strings.Contains(got[0], "cpu some 41.5 full 20.0") {
		t.Errorf("the cpu pressure was not told: %v", got)
	}
	for _, l := range got {
		if strings.Contains(l, "memory") {
			t.Errorf("memory was under 10%% but told: %s", l)
		}
	}
	for _, w := range []string{"cpu some 41.5 full 20.0", "memory some 7.0 full 2.0", "io some 0.0 full 0.0"} {
		if !strings.Contains(sum, w) {
			t.Errorf("summary lacks %q: %s", w, sum)
		}
	}
}

func TestSamplerWithoutPressureFiles(t *testing.T) {
	var n lines
	s := startSampler(t.TempDir(), 10*time.Millisecond, n.add)
	time.Sleep(30 * time.Millisecond)
	if sum := s.summary(); !strings.Contains(sum, "not told") || len(n.all()) != 0 {
		t.Errorf("summary %q, lines %v", sum, n.all())
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestServerEvidence(t *testing.T) {
	dir := t.TempDir()
	srv, gc := filepath.Join(dir, "server.log"), filepath.Join(dir, "gc.log")
	// what an earlier run on this server left: not this run's
	appendTo(t, srv, "[10:00:00] [Server thread/WARN]: Can't keep up! Is the server overloaded? Running 9999ms or 199 ticks behind\n")
	appendTo(t, gc, "[1.0s][info][gc] GC(0) Pause Young (Normal) (G1 Evacuation Pause) 50M->5M(512M) 9000.000ms\n")
	var n lines
	e := watchServer(srv, gc, 10*time.Millisecond, n.add)
	appendTo(t, srv, "[10:01:00] [Server thread/INFO]: Done (1.0s)!\n")
	appendTo(t, srv, "[10:01:10] [Server thread/WARN]: Can't keep up! Is the server overloaded? Running 4100ms or 82 ticks behind\n")
	appendTo(t, gc, "[2.0s][info][gc] GC(1) Pause Young (Normal) (G1 Evacuation Pause) 60M->6M(512M) 12.500ms\n")
	appendTo(t, gc, "[3.0s][info][gc] GC(2) Pause Full (System.gc()) 70M->7M(512M) 1500.250ms\n")
	appendTo(t, gc, "[4.0s][info][gc] GC(3) Pause Young (Nor") // (a line not yet whole)
	time.Sleep(80 * time.Millisecond)
	sum := e.summary()
	want := `server: 1 "Can't keep up" lines (most 4100 ms behind); gc: 2 pauses, longest 1500 ms, together 1513 ms`
	if sum != want {
		t.Errorf("summary\n got %s\nwant %s", sum, want)
	}
	got := strings.Join(n.all(), "\n")
	if !strings.Contains(got, "82 ticks behind") || !strings.Contains(got, "1500.250ms") || strings.Contains(got, "12.500ms") || strings.Contains(got, "9999") {
		t.Errorf("told:\n%s", got)
	}
	out := t.TempDir()
	e.keep(out)
	b, _ := os.ReadFile(filepath.Join(out, "server.log"))
	if strings.Contains(string(b), "9999") || !strings.Contains(string(b), "4100ms") {
		t.Errorf("server.log of the run: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(out, "gc.log"))
	if strings.Contains(string(b), "9000.000") || !strings.Contains(string(b), "1500.250ms") {
		t.Errorf("gc.log of the run: %s", b)
	}
}

func TestMemoryOf(t *testing.T) {
	dir := t.TempDir()
	put := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("memory.peak", "2147483648\n") // 2048 MiB
	put("memory.current", "1073741824\n")
	put("memory.max", "3758096384\n") // 3584 MiB
	put("memory.stat", "anon 805306368\nfile 268435456\nkernel 1\n")
	want := "memory of the job's cgroup: peak 2048 MiB, now 1024 MiB, limit 3584 MiB; anon 768 MiB, file 256 MiB"
	if got := memoryOf(dir); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	put("memory.max", "max\n")
	if got := memoryOf(dir); !strings.Contains(got, "limit no limit") {
		t.Errorf("an unlimited cgroup: %s", got)
	}
	if got := memoryOf(t.TempDir()); got != "memory of the job's cgroup: peak not told, now not told, limit not told; anon not told, file not told" {
		t.Errorf("no cgroup files: %s", got)
	}
}
