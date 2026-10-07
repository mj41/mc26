package loot

import (
	"slices"
	"testing"
)

func TestDrops(t *testing.T) {
	for item, block := range map[string]string{
		"minecraft:cobblestone": "minecraft:stone",
		"minecraft:dirt":        "minecraft:grass_block",
		"minecraft:coal":        "minecraft:coal_ore",
		"minecraft:oak_log":     "minecraft:oak_log",
	} {
		if by := DroppedBy(item); !slices.Contains(by, block) {
			t.Errorf("%s is dropped by %v, want %s among them", item, by, block)
		}
	}
	if slices.Contains(DroppedBy("minecraft:stone"), "minecraft:stone") {
		t.Errorf("stone drops stone without silk touch")
	}
	var silk bool
	for _, d := range BlockDrops("minecraft:stone") {
		if d.Item == "minecraft:stone" && d.Needs == "silk_touch" {
			silk = true
		}
	}
	if !silk {
		t.Errorf("stone: %v, want stone with silk touch", BlockDrops("minecraft:stone"))
	}
}
