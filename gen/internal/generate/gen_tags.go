// gen_tags generates data/tags/tags_gen.go from the block, item, entity type and
// enchantment tags of the vanilla data pack (data/minecraft/tags/block/**.json,
// tags/item/**.json, tags/entity_type/**.json, tags/enchantment/**.json): each
// tag resolved to the blocks or items it holds, the tags inside tags followed
// (a value "#minecraft:logs" is that tag's members). data/tags/tags.go looks
// them up (Block, Item, BlockHas, ItemHas, EntityTypeHas, EnchantmentHas). What a tool mines (tags
// minecraft:mineable/pickaxe), what a block needs (needs_stone_tool) and what a
// recipe takes (#minecraft:planks) are tags: a server tells them to its
// client (UpdateTags) from this data.
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

// tagFile is a tag's JSON: its values are names, "#" names another tag, or an
// object {"id": …, "required": …}.
type tagFile struct {
	Values []json.RawMessage `json:"values"`
}

func genTags(jsonDir, outRoot string) error {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_tags.go", "data/minecraft/tags/block/**.json", "data/minecraft/tags/item/**.json",
		"data/minecraft/tags/entity_type/**.json", "data/minecraft/tags/enchantment/**.json"))
	sb.WriteString("\npackage tags\n\n")
	total := 0
	for _, kind := range []string{"block", "item", "entity_type", "enchantment"} {
		tags, err := readTags(filepath.Join(jsonDir, "data", "minecraft", "tags", kind))
		if err != nil {
			return fmt.Errorf("genTags: %w (re-run extraction: the data must include the data packs)", err)
		}
		resolved, err := resolveTags(tags)
		if err != nil {
			return fmt.Errorf("genTags: %s: %w", kind, err)
		}
		names := make([]string, 0, len(resolved))
		for n := range resolved {
			names = append(names, n)
		}
		sort.Strings(names)
		what := strings.ReplaceAll(kind, "_", " ")
		fmt.Fprintf(&sb, "// %sTags are the %s tags, each with the %ss it holds, tags inside tags followed, sorted.\n", goVarName(kind), what, what)
		fmt.Fprintf(&sb, "var %sTags = map[string][]string{\n", goVarName(kind))
		for _, n := range names {
			fmt.Fprintf(&sb, "\t%q: {", n)
			for i, m := range resolved[n] {
				if i > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "%q", m)
				total++
			}
			sb.WriteString("},\n")
		}
		sb.WriteString("}\n\n")
		logf("genTags: %d %s tags", len(names), kind)
	}
	if err := writeGo(filepath.Join(outRoot, "data", "tags", "tags_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genTags: %w", err)
	}
	logf("genTags: %d members", total)
	return nil
}

// readTags reads every tag file under dir: a tag's name is its path without
// the extension ("mineable/pickaxe" → minecraft:mineable/pickaxe), its values as written.
func readTags(dir string) (map[string][]string, error) {
	tags := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		name := "minecraft:" + strings.TrimSuffix(filepath.ToSlash(rel), ".json")
		var f tagFile
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		vals := []string{}
		for _, raw := range f.Values {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				vals = append(vals, s)
				continue
			}
			var o struct {
				ID       string `json:"id"`
				Required *bool  `json:"required"`
			}
			if err := json.Unmarshal(raw, &o); err != nil || o.ID == "" {
				return fmt.Errorf("%s: a value that is neither a name nor an {id}: %s", path, raw)
			}
			vals = append(vals, o.ID)
		}
		tags[name] = vals
		return nil
	})
	return tags, err
}

// resolveTags follows each tag's "#tag" values to the names they hold; a tag
// that names itself or another not in the pack is an error (the pack is
// whole: an optional value naming a missing tag would be skipped by the
// game, and none does at present).
func resolveTags(tags map[string][]string) (map[string][]string, error) {
	out := map[string][]string{}
	var resolve func(name string, stack []string) ([]string, error)
	resolve = func(name string, stack []string) ([]string, error) {
		if r, ok := out[name]; ok {
			return r, nil
		}
		for _, s := range stack {
			if s == name {
				return nil, fmt.Errorf("tag %s holds itself (%s)", name, strings.Join(append(stack, name), " > "))
			}
		}
		vals, ok := tags[name]
		if !ok {
			return nil, fmt.Errorf("tag %s is not in the pack (%s)", name, strings.Join(stack, " > "))
		}
		seen := map[string]bool{}
		for _, v := range vals {
			if ref, isTag := strings.CutPrefix(v, "#"); isTag {
				inner, err := resolve(ref, append(stack, name))
				if err != nil {
					return nil, err
				}
				for _, m := range inner {
					seen[m] = true
				}
				continue
			}
			seen[v] = true
		}
		members := make([]string, 0, len(seen))
		for m := range seen {
			members = append(members, m)
		}
		sort.Strings(members)
		out[name] = members
		return members, nil
	}
	for name := range tags {
		if _, err := resolve(name, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// goVarName is a tag kind as the start of a Go name: entity_type → entityType.
func goVarName(kind string) string {
	parts := strings.Split(kind, "_")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
