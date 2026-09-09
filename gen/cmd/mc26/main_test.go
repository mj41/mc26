package main

import (
	"sort"
	"strings"
	"testing"
)

func TestVersionOrder(t *testing.T) {
	vs := []string{"26.3", "26.1", "26.3-pre-2", "26.10", "26.3-rc-1", "26.3-pre-1", "26.2", "26.3-snapshot-4"}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	want := "26.1 26.2 26.3-snapshot-4 26.3-pre-1 26.3-pre-2 26.3-rc-1 26.3 26.10"
	if got := strings.Join(vs, " "); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestTagBase(t *testing.T) {
	for in, want := range map[string]string{"26.2": "v0.262.", "26.3-pre-2": "v0.263.0-pre2.", "26.3-rc-1": "v0.263.0-rc1."} {
		got, err := tagBase(in)
		if err != nil || got != want {
			t.Errorf("tagBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
