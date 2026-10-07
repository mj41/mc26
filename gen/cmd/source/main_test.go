package main

import (
	"sort"
	"testing"
)

func TestWanted(t *testing.T) {
	sorted := []string{"net/minecraft/client/player/LocalPlayer", "net/minecraft/world/entity/Entity"}
	sort.Strings(sorted)
	for name, want := range map[string]bool{
		"net/minecraft/client/player/LocalPlayer":         true,
		"net/minecraft/client/player/LocalPlayer$1":       true,
		"net/minecraft/client/player/LocalPlayer$Sub$Sub": true,
		"net/minecraft/client/player/LocalPlayerResolver": false, // a prefix, not a nested class
		"net/minecraft/world/entity/Entity$RemovalReason": true,
		"net/minecraft/world/entity/EntityType":           false,
	} {
		if got := wanted(sorted, name); got != want {
			t.Errorf("wanted(%s) = %v, want %v", name, got, want)
		}
	}
	if got := topLevel("a.b.C$D$1"); got != "a.b.C" {
		t.Errorf("topLevel = %s", got)
	}
}
