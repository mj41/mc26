// Package loot says what blocks drop, from the vanilla data pack's block
// loot tables (generated: blocks_gen.go).
package loot

import "sort"

// Drop is an item a block's loot table can give, and what it needs: "" for
// nothing (a plain hand or tool, though a block that requires the right
// tool drops nothing without it — level/block.RequiresCorrectTool), or
// "silk_touch", "shears", "chance" (it may drop), "tool" or "other" (a
// condition this table does not name).
type Drop struct {
	Item  string
	Needs string
}

// BlockDrops returns what the block ("minecraft:stone") can drop.
func BlockDrops(block string) []Drop { return blockDrops[block] }

// DroppedBy returns the blocks that drop item with nothing special (no silk
// touch, no shears, not by chance): what to dig for it.
func DroppedBy(item string) []string {
	var out []string
	for b, ds := range blockDrops {
		for _, d := range ds {
			if d.Item == item && d.Needs == "" {
				out = append(out, b)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// DroppedByChance returns the blocks that may drop item to a plain hand or
// tool (their loot table gives it by chance: wheat seeds from grass) — what
// to break many of when no block drops it for sure.
func DroppedByChance(item string) []string {
	var out []string
	for b, ds := range blockDrops {
		for _, d := range ds {
			if d.Item == item && d.Needs == "chance" {
				out = append(out, b)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
