package tags

import "testing"

func TestTags(t *testing.T) {
	for tag, member := range map[string]string{
		"minecraft:mineable/pickaxe": "minecraft:stone",
		"minecraft:mineable/axe":     "minecraft:oak_log", // through #minecraft:logs, a tag inside a tag
		"minecraft:mineable/shovel":  "minecraft:dirt",
		"minecraft:needs_stone_tool": "minecraft:iron_ore",
		"minecraft:needs_iron_tool":  "minecraft:diamond_ore",
		"minecraft:logs_that_burn":   "minecraft:oak_log",
	} {
		if !BlockHas(tag, member) {
			t.Errorf("block tag %s lacks %s", tag, member)
		}
	}
	if BlockHas("minecraft:mineable/pickaxe", "minecraft:oak_log") {
		t.Errorf("a log is no pickaxe's block")
	}
	for tag, member := range map[string]string{
		"minecraft:planks": "minecraft:oak_planks",
		"minecraft:logs":   "minecraft:birch_log",
	} {
		if !ItemHas(tag, member) {
			t.Errorf("item tag %s lacks %s", tag, member)
		}
	}
	if !EntityTypeHas("minecraft:skeletons", "minecraft:stray") || EntityTypeHas("minecraft:skeletons", "minecraft:zombie") {
		t.Errorf("entity type tag minecraft:skeletons")
	}
	if !EnchantmentHas("minecraft:smelts_loot", "minecraft:fire_aspect") || EnchantmentHas("minecraft:smelts_loot", "minecraft:looting") {
		t.Errorf("enchantment tag minecraft:smelts_loot")
	}
	if Block("minecraft:no_such_tag") != nil {
		t.Errorf("a tag that is not there")
	}
	if len(BlockTags()) < 50 || len(ItemTags()) < 50 {
		t.Errorf("%d block tags, %d item tags: too few", len(BlockTags()), len(ItemTags()))
	}
}
