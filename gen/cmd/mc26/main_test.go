package main

import (
	"testing"
)

func TestTagBase(t *testing.T) {
	for in, want := range map[string]string{"26.2": "v0.262.", "26.3-pre-2": "v0.263.0-pre2.", "26.3-rc-1": "v0.263.0-rc1."} {
		got, err := tagBase(in)
		if err != nil || got != want {
			t.Errorf("tagBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
