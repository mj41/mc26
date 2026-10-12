package e2e

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Evidence for a pause: when the server and the bot lost the same seconds together (a 10 s round of the bot while the
// server fell 200 ticks behind), what was it — the JVM stopping the world, the node short of memory, another job
// holding the CPUs? Each run keeps what answers it, in its harness.log (the file the bench ships) and in its
// directory: the server's own log (its "Can't keep up!" lines), the collector's log with its pauses, and the
// pod's pressure (the kernel's PSI: the share of the last 10 s that tasks of the cgroup waited for the CPU, for
// memory, for I/O).

// cgroupDir is where the pod's own cgroup is mounted (cgroup v2 shows a container its own cgroup as the root).
const cgroupDir = "/sys/fs/cgroup"

// pressureDir and the periods are variables for the test.
var (
	pressureDir   = cgroupDir
	pressureEvery = 10 * time.Second
	evidenceEvery = 2 * time.Second
)

// pressureOver is the share of the last ten seconds (avg10, percent) over which a sample is told as it is read.
const pressureOver = 10.0

// psi is a pressure file's two lines: "some" (some task waited) and "full" (every task did), avg10 of each.
type psi struct{ some, full float64 }

var psiLine = regexp.MustCompile(`^(some|full) avg10=([0-9.]+) `)

// readPSI reads <dir>/<kind>.pressure (cpu, memory, io).
func readPSI(dir, kind string) (psi, error) {
	b, err := os.ReadFile(filepath.Join(dir, kind+".pressure"))
	if err != nil {
		return psi{}, err
	}
	var p psi
	for _, l := range strings.Split(string(b), "\n") {
		m := psiLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v, _ := strconv.ParseFloat(m[2], 64)
		if m[1] == "some" {
			p.some = v
		} else {
			p.full = v
		}
	}
	return p, nil
}

// sampler samples the pressure every period; a "some" avg10 over pressureOver is noted at once, and the highest of
// each is kept for the summary.
type sampler struct {
	dir  string
	note func(string)
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	max  map[string]psi
	n    int // samples taken
	seen bool
}

var pressureKinds = []string{"cpu", "memory", "io"}

func startSampler(dir string, every time.Duration, note func(string)) *sampler {
	s := &sampler{dir: dir, note: note, stop: make(chan struct{}), done: make(chan struct{}), max: map[string]psi{}}
	go func() {
		defer close(s.done)
		s.sample() // (the first at once)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.sample()
			}
		}
	}()
	return s
}

func (s *sampler) sample() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var over []string
	for _, k := range pressureKinds {
		p, err := readPSI(s.dir, k)
		if err != nil {
			continue // (no pressure files here: not a cgroup v2 host)
		}
		s.seen = true
		m := s.max[k]
		m.some, m.full = max(m.some, p.some), max(m.full, p.full)
		s.max[k] = m
		if p.some > pressureOver || p.full > pressureOver {
			over = append(over, fmt.Sprintf("%s some %.1f full %.1f", k, p.some, p.full))
		}
	}
	s.n++
	if len(over) > 0 {
		s.note("pressure over " + strconv.Itoa(int(pressureOver)) + "% (avg10): " + strings.Join(over, ", "))
	}
}

// summary stops the sampler and says the highest of each.
func (s *sampler) summary() string {
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.seen {
		return "pressure: not told (no pressure files in " + s.dir + ")"
	}
	var parts []string
	for _, k := range pressureKinds {
		if m, ok := s.max[k]; ok {
			parts = append(parts, fmt.Sprintf("%s some %.1f full %.1f", k, m.some, m.full))
		}
	}
	return fmt.Sprintf("pressure, highest avg10 of %d samples: %s", s.n, strings.Join(parts, ", "))
}

// memoryOf says the job's own cgroup memory (cgroup v2, the pod's): its peak so far, its limit and the
// current use, and what of it is anonymous (the JVM's and the bot's heaps) and what is file cache. The job's
// memory requests were sized from measures; a long run's real peak says whether the job and the bench overcommit
// the node.
func memoryOf(dir string) string {
	num := func(name string) (int64, string) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return -1, "not told"
		}
		v := strings.TrimSpace(string(b))
		if v == "max" {
			return -1, "no limit"
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return -1, "not told"
		}
		return n, ""
	}
	mib := func(name string) string {
		n, why := num(name)
		if n < 0 {
			return why
		}
		return fmt.Sprintf("%d MiB", n>>20)
	}
	anon, file := "not told", "not told"
	if b, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) != 2 {
				continue
			}
			n, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				continue
			}
			switch f[0] {
			case "anon":
				anon = fmt.Sprintf("%d MiB", n>>20)
			case "file":
				file = fmt.Sprintf("%d MiB", n>>20)
			}
		}
	}
	return fmt.Sprintf("memory of the job's cgroup: peak %s, now %s, limit %s; anon %s, file %s",
		mib("memory.peak"), mib("memory.current"), mib("memory.max"), anon, file)
}

var (
	behindRe = regexp.MustCompile(`Can't keep up! Is the server overloaded\? Running (\d+)ms or (\d+) ticks behind`)
	pauseRe  = regexp.MustCompile(`Pause.* ([0-9.]+)ms\s*$`)
)

// serverEvidence follows the server's console log (its "Can't keep up!" lines, told as they come) and its collector's
// log (a pause over a second is told; all are counted), and at the end says what they held.
type serverEvidence struct {
	serverLog, gcLog string
	note             func(string)
	stop, done       chan struct{}

	serverFrom, gcFrom int64 // the sizes when the run began
	serverAt, gcAt     int64 // read up to
	behind             int
	behindMax          int // the most ms behind
	pauses             int
	pauseMax           float64
	pauseSum           float64
}

func watchServer(serverLog, gcLog string, every time.Duration, note func(string)) *serverEvidence {
	e := &serverEvidence{serverLog: serverLog, gcLog: gcLog, note: note, stop: make(chan struct{}), done: make(chan struct{})}
	e.serverFrom, e.gcFrom = sizeOf(serverLog), sizeOf(gcLog) // (the run's span: a server may have had runs before)
	e.serverAt, e.gcAt = e.serverFrom, e.gcFrom
	go func() {
		defer close(e.done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			e.read()
			select {
			case <-e.stop:
				return
			case <-t.C:
			}
		}
	}()
	return e
}

func sizeOf(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// tailOf reads what a file has past offset (whole lines only), and the new offset.
func tailOf(path string, at int64) ([]string, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, at
	}
	defer f.Close()
	if _, err := f.Seek(at, io.SeekStart); err != nil {
		return nil, at
	}
	b, _ := io.ReadAll(f)
	i := strings.LastIndexByte(string(b), '\n')
	if i < 0 {
		return nil, at
	}
	return strings.Split(string(b[:i]), "\n"), at + int64(i+1)
}

func (e *serverEvidence) read() {
	var lines []string
	lines, e.serverAt = tailOf(e.serverLog, e.serverAt)
	for _, l := range lines {
		if m := behindRe.FindStringSubmatch(l); m != nil {
			ms, _ := strconv.Atoi(m[1])
			e.behind++
			e.behindMax = max(e.behindMax, ms)
			e.note("server: " + strings.TrimSpace(l))
		}
	}
	lines, e.gcAt = tailOf(e.gcLog, e.gcAt)
	for _, l := range lines {
		if m := pauseRe.FindStringSubmatch(l); m != nil {
			ms, _ := strconv.ParseFloat(m[1], 64)
			e.pauses++
			e.pauseSum += ms
			e.pauseMax = max(e.pauseMax, ms)
			if ms >= 1000 {
				e.note("gc: " + strings.TrimSpace(l))
			}
		}
	}
}

// summary stops the watch, reads the last lines and says what the logs held.
func (e *serverEvidence) summary() string {
	close(e.stop)
	<-e.done
	return fmt.Sprintf("server: %d \"Can't keep up\" lines (most %d ms behind); gc: %d pauses, longest %.0f ms, together %.0f ms",
		e.behind, e.behindMax, e.pauses, e.pauseMax, e.pauseSum)
}

// keep writes what the server's console log and its collector's log got since the run began into the run's
// directory (server.log, gc.log): the span of this run, a server may have had runs before.
func (e *serverEvidence) keep(dir string) {
	for _, f := range []struct {
		from string
		at   int64
		to   string
	}{{e.serverLog, e.serverFrom, "server.log"}, {e.gcLog, e.gcFrom, "gc.log"}} {
		src, err := os.Open(f.from)
		if err != nil {
			continue
		}
		if _, err := src.Seek(f.at, io.SeekStart); err == nil {
			if dst, err := os.Create(filepath.Join(dir, f.to)); err == nil {
				_, _ = io.Copy(dst, src)
				dst.Close()
			}
		}
		src.Close()
	}
}
