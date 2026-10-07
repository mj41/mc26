// Package recipe is every recipe of the vanilla data pack (generated:
// recipes_gen.go): what a player can make, whether or not the server has put
// it in the player's recipe book yet — the book holds only what the player
// unlocked (a log picked up unlocks planks), a person knows the rest.
package recipe

// Recipe is one recipe: crafting (shaped or shapeless), smelting, cutting.
type Recipe struct {
	Name   string // the data pack's name: minecraft:oak_planks
	Kind   string // minecraft:crafting_shaped, minecraft:crafting_shapeless, minecraft:smelting, …
	Result string // minecraft:oak_planks
	Count  int    // how many one craft makes
	// Width and Height are a shaped recipe's grid; 0 for the others.
	Width, Height int
	// Ingredients are the slots: a shaped recipe's grid row by row (nil for an
	// empty cell), a shapeless recipe's items, a furnace's input. Each is the
	// items that fit, a tag as "#minecraft:planks".
	Ingredients [][]string
}

// For returns the recipes that make item ("minecraft:stick").
func For(item string) []Recipe {
	var out []Recipe
	for _, i := range byResult[item] {
		out = append(out, recipes[i])
	}
	return out
}

// All returns every recipe.
func All() []Recipe { return recipes }
