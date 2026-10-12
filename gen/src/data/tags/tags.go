// Package tags is the block, item, entity type and enchantment tags of the
// vanilla data pack (generated: tags_gen.go), each resolved to the names it
// holds — the tags inside tags followed. A tool's rules name tags (minecraft:mineable/pickaxe),
// a block's drop needs one (minecraft:needs_stone_tool), a recipe takes one
// (minecraft:planks): the server tells them to the client (UpdateTags); this is
// the data it tells from. A loot table asks of a killer's kind
// (minecraft:skeletons) and of a weapon's enchantments (minecraft:smelts_loot).
package tags

import (
	"slices"
	"sort"
)

// Block returns the blocks of the block tag ("minecraft:mineable/pickaxe", no
// "#"), sorted; none for a tag the pack does not have.
func Block(tag string) []string { return blockTags[tag] }

// Item returns the items of the item tag ("minecraft:planks"), sorted.
func Item(tag string) []string { return itemTags[tag] }

// BlockHas reports whether the block is in the block tag.
func BlockHas(tag, block string) bool { return has(blockTags[tag], block) }

// ItemHas reports whether the item is in the item tag.
func ItemHas(tag, item string) bool { return has(itemTags[tag], item) }

// EntityTypeHas reports whether the entity type ("minecraft:skeleton") is in the entity type tag.
func EntityTypeHas(tag, entityType string) bool { return has(entityTypeTags[tag], entityType) }

// EnchantmentHas reports whether the enchantment ("minecraft:fire_aspect") is in the enchantment tag.
func EnchantmentHas(tag, enchantment string) bool { return has(enchantmentTags[tag], enchantment) }

func has(sorted []string, name string) bool {
	_, ok := slices.BinarySearch(sorted, name)
	return ok
}

// BlockTags returns the names of the block tags, sorted.
func BlockTags() []string { return names(blockTags) }

// ItemTags returns the names of the item tags, sorted.
func ItemTags() []string { return names(itemTags) }

func names(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
