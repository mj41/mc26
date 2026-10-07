// gen_recipes generates data/recipe/recipes_gen.go from the recipes of the
// vanilla data pack (data/minecraft/recipe/*.json): crafting, smelting and
// cutting recipes, their result and their ingredients (items or tags), which
// data/recipe/recipe.go looks up.
package generate

import (
	"encoding/json"
	"fmt"
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
		default:
			continue
		}
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
		sb.WriteString("}},\n")
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
