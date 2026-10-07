// Runstats tells what a robot run's events are made of: for each event
// name, how many, how many a minute of the run, and their lines' sizes
// (median and largest) — and, for blocks, the palettes' sizes. For
// keeping an eye on what a run writes (docs/testing.md, "Kept runs").
//
//	go run ./gen/cmd/runstats ../mc26-runs/<run>/events.jsonl
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"
)

type stat struct {
	sizes    []int
	palettes []int
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "want: runstats <events.jsonl>")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	stats := map[string]*stat{}
	var first, last time.Time
	total := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		var e struct {
			TS   string `json:"ts"`
			Name string `json:"name"`
			Data struct {
				Palette []json.RawMessage `json:"palette"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, e.TS); err == nil {
			if first.IsZero() {
				first = t
			}
			last = t
		}
		s := stats[e.Name]
		if s == nil {
			s = &stat{}
			stats[e.Name] = s
		}
		s.sizes = append(s.sizes, len(sc.Bytes())+1)
		if e.Name == "blocks" {
			s.palettes = append(s.palettes, len(e.Data.Palette))
		}
		total += len(sc.Bytes()) + 1
	}
	minutes := last.Sub(first).Minutes()
	fmt.Printf("%s: %.1f minutes, %.1f MB, %.2f MB a minute\n", os.Args[1], minutes, float64(total)/1e6, float64(total)/1e6/max(minutes, 1e-9))
	var names []string
	for n := range stats {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "name\tcount\ta minute\tmedian bytes\tmax bytes\tpalette median\tpalette max\t")
	for _, n := range names {
		s := stats[n]
		pm, px := "", ""
		if len(s.palettes) > 0 {
			pm, px = fmt.Sprint(median(s.palettes)), fmt.Sprint(maxOf(s.palettes))
		}
		fmt.Fprintf(tw, "%s\t%d\t%.1f\t%d\t%d\t%s\t%s\t\n", n, len(s.sizes), float64(len(s.sizes))/max(minutes, 1e-9), median(s.sizes), maxOf(s.sizes), pm, px)
	}
	tw.Flush()
}

func median(v []int) int {
	s := append([]int(nil), v...)
	sort.Ints(s)
	return s[len(s)/2]
}

func maxOf(v []int) int {
	m := 0
	for _, x := range v {
		m = max(m, x)
	}
	return m
}
