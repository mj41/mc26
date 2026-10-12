// gen_recipes generates data/recipe/recipes_gen.go from the recipes of the
// vanilla data pack (data/minecraft/recipe/*.json): crafting, smelting and
// cutting recipes, their result and their ingredients (items or tags), which
// data/recipe/recipe.go looks up — and, from the recipes' advancements
// (data/minecraft/advancement/recipes/**.json), what unlocks each: the items a
// player must hold (a stick for the wooden pickaxe, not the planks it is made
// of) before the server puts the recipe in its book.
package generate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func genRecipes(jsonDir, outRoot string) error {
	dir := filepath.Join(jsonDir, "data", "minecraft", "recipe")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("genRecipes: %w (re-run extraction: the data must include the data packs)", err)
	}
	type rec struct {
		name, kind, result string
		count, w, h        int
		ings               [][]string
		unlock             [][]string
		cooking            int // a cooking recipe's ticks
	}
	unlocks, err := recipeUnlocks(jsonDir)
	if err != nil {
		return fmt.Errorf("genRecipes: %w", err)
	}
	var out []rec
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var r struct {
			Type        string                     `json:"type"`
			Pattern     json.RawMessage            `json:"pattern"` // a string in a smithing trim
			Key         map[string]json.RawMessage `json:"key"`
			Ingredients []json.RawMessage          `json:"ingredients"`
			Ingredient  json.RawMessage            `json:"ingredient"`
			Result      json.RawMessage            `json:"result"`
			CookingTime int                        `json:"cookingtime"`
		}
		if err := readJSON(filepath.Join(dir, e.Name()), &r); err != nil {
			return fmt.Errorf("genRecipes: %w", err)
		}
		x := rec{name: "minecraft:" + strings.TrimSuffix(e.Name(), ".json"), kind: r.Type, count: 1}
		var res struct {
			ID    string `json:"id"`
			Count int    `json:"count"`
		}
		if json.Unmarshal(r.Result, &res) != nil || res.ID == "" {
			continue // a special recipe (map cloning, dyeing armour): no fixed result
		}
		x.result = res.ID
		if res.Count > 0 {
			x.count = res.Count
		}
		switch strings.TrimPrefix(r.Type, "minecraft:") {
		case "crafting_shaped":
			var pattern []string
			if err := json.Unmarshal(r.Pattern, &pattern); err != nil {
				return fmt.Errorf("genRecipes: %s pattern: %w", x.name, err)
			}
			x.h = len(pattern)
			for _, row := range pattern {
				x.w = max(x.w, len(row))
			}
			for _, row := range pattern {
				for i := 0; i < x.w; i++ {
					c := " "
					if i < len(row) {
						c = row[i : i+1]
					}
					if c == " " {
						x.ings = append(x.ings, nil)
						continue
					}
					ing, err := ingredient(r.Key[c])
					if err != nil {
						return fmt.Errorf("genRecipes: %s key %q: %w", x.name, c, err)
					}
					x.ings = append(x.ings, ing)
				}
			}
		case "crafting_shapeless":
			for _, raw := range r.Ingredients {
				ing, err := ingredient(raw)
				if err != nil {
					return fmt.Errorf("genRecipes: %s: %w", x.name, err)
				}
				x.ings = append(x.ings, ing)
			}
		case "smelting", "blasting", "smoking", "campfire_cooking", "stonecutting":
			ing, err := ingredient(r.Ingredient)
			if err != nil {
				return fmt.Errorf("genRecipes: %s: %w", x.name, err)
			}
			x.ings = [][]string{ing}
			// the cooking time of the pack: 200 ticks smelting, 100 blasting and smoking, 600 on a campfire when the file names none
			x.cooking = r.CookingTime
			if x.cooking == 0 {
				x.cooking = map[string]int{"smelting": 200, "blasting": 100, "smoking": 100, "campfire_cooking": 600}[strings.TrimPrefix(r.Type, "minecraft:")]
			}
		default:
			continue
		}
		x.unlock = unlocks[x.name]
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_recipes.go", "data/minecraft/recipe/*.json"))
	sb.WriteString("\npackage recipe\n\nvar recipes = []Recipe{\n")
	byResult := map[string][]int{}
	for i, x := range out {
		fmt.Fprintf(&sb, "\t{%q, %q, %q, %d, %d, %d, [][]string{", x.name, x.kind, x.result, x.count, x.w, x.h)
		for j, ing := range x.ings {
			if j > 0 {
				sb.WriteString(", ")
			}
			if ing == nil {
				sb.WriteString("nil")
				continue
			}
			sb.WriteString("{")
			for k, s := range ing {
				if k > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "%q", s)
			}
			sb.WriteString("}")
		}
		sb.WriteString("}, ")
		writeStringLists(&sb, x.unlock)
		fmt.Fprintf(&sb, ", %d},\n", x.cooking)
		byResult[x.result] = append(byResult[x.result], i)
	}
	sb.WriteString("}\n\n// byResult indexes recipes by the item they make.\nvar byResult = map[string][]int{\n")
	var results []string
	for r := range byResult {
		results = append(results, r)
	}
	sort.Strings(results)
	for _, r := range results {
		fmt.Fprintf(&sb, "\t%q: {", r)
		for k, i := range byResult[r] {
			if k > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%d", i)
		}
		sb.WriteString("},\n")
	}
	sb.WriteString("}\n")
	if err := writeGo(filepath.Join(outRoot, "data", "recipe", "recipes_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genRecipes: %w", err)
	}
	logf("genRecipes: %d recipes", len(out))
	return nil
}

// ingredient reads a recipe ingredient: an item, a tag ("#minecraft:planks"),
// a list of them, or the older objects ({"item": …}, {"tag": …}).
func ingredient(raw json.RawMessage) ([]string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		var out []string
		for _, l := range list {
			ing, err := ingredient(l)
			if err != nil {
				return nil, err
			}
			out = append(out, ing...)
		}
		return out, nil
	}
	var o struct {
		Item string `json:"item"`
		Tag  string `json:"tag"`
	}
	if json.Unmarshal(raw, &o) == nil && (o.Item != "" || o.Tag != "") {
		if o.Tag != "" {
			return []string{"#" + o.Tag}, nil
		}
		return []string{o.Item}, nil
	}
	return nil, fmt.Errorf("ingredient %s", raw)
}

// writeStringLists writes a [][]string literal, nil when empty.
func writeStringLists(sb *strings.Builder, ls [][]string) {
	if len(ls) == 0 {
		sb.WriteString("nil")
		return
	}
	sb.WriteString("[][]string{")
	for i, l := range ls {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("{")
		for j, s := range l {
			if j > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(sb, "%q", s)
		}
		sb.WriteString("}")
	}
	sb.WriteString("}")
}

// recipeUnlocks reads the recipes' advancements: for each recipe the server
// gives, the groups of items it asks the player to hold (an inventory_changed
// criterion) — every group has to be met, one item of a group is enough. A
// recipe unlocked at once (a tick criterion) has no group.
func recipeUnlocks(jsonDir string) (map[string][][]string, error) {
	root := filepath.Join(jsonDir, "data", "minecraft", "advancement", "recipes")
	out := map[string][][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		var adv struct {
			Criteria map[string]struct {
				Trigger    string `json:"trigger"`
				Conditions struct {
					Items []struct {
						Items json.RawMessage `json:"items"`
					} `json:"items"`
				} `json:"conditions"`
			} `json:"criteria"`
			Requirements [][]string `json:"requirements"`
			Rewards      struct {
				Recipes []string `json:"recipes"`
			} `json:"rewards"`
		}
		if err := readJSON(path, &adv); err != nil {
			return err
		}
		if len(adv.Rewards.Recipes) == 0 {
			return nil
		}
		var groups [][]string
		for _, group := range adv.Requirements {
			var items []string
			met := false
			for _, name := range group {
				c, ok := adv.Criteria[name]
				switch {
				case !ok:
					return fmt.Errorf("%s: requirement %q is no criterion", path, name)
				case c.Trigger == "minecraft:recipe_unlocked":
					continue // (the recipe given by the game: a command, a book)
				case c.Trigger == "minecraft:tick":
					items = nil
					met = true // unlocked at once: nothing to hold
				case c.Trigger == "minecraft:enter_block" || c.Trigger == "minecraft:impossible":
					items = append(items, "@"+c.Trigger) // an event, not an item held (a boat: entering water)
					continue
				case c.Trigger != "minecraft:inventory_changed":
					return fmt.Errorf("%s: criterion %q has the trigger %s, which the generator does not read", path, name, c.Trigger)
				}
				if met {
					break
				}
				for _, it := range c.Conditions.Items {
					ing, err := ingredient(it.Items)
					if err != nil {
						return fmt.Errorf("%s: %w", path, err)
					}
					items = append(items, ing...)
				}
			}
			// a group met at once (a tick criterion) asks for nothing; the others are met
			// by holding one of their items
			if !met && len(items) > 0 {
				sort.Strings(items)
				groups = append(groups, items)
			}
		}
		for _, r := range adv.Rewards.Recipes {
			out[r] = groups
		}
		return nil
	})
	return out, err
}
