// nbtdiff compares the NBT schemas of two extracted MC versions: what the
// registry elements sent in the configuration phase, and the shared types
// (chat style, text component, save records), gained, lost or changed.
//
//	go run ./gen/cmd/nbtdiff 26.2 26.3               # temp/data/<version> (or MC26_DATA)
//	go run ./gen/cmd/nbtdiff ../mc26-data-a ../mc26-data-b
//
// It reads nbt_schema.json (from GenNbtSchema) from each directory and prints,
// per section, the entries added and removed, then for every entry in both
// the keys that appeared, disappeared or changed type; then the same for
// every type-keyed union the entries use (a dispatch: density functions, int
// providers, dialog actions…), once each, by case. A key is its path in the
// compound: "effects.ambient", "[minecraft:block].state" inside a case,
// "entries[].weight" inside a list, "attributes{}" for a map's values. A
// record inlined in several places is reported at each; a union is one
// table, so where the schema first spells it out does not matter.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mj41/mc26/gen/internal/paths"
)

type entry struct {
	Class    string         `json:"class"`
	Coverage string         `json:"coverage"`
	Type     map[string]any `json:"type"`
}

type schema struct {
	Registries map[string]entry `json:"registries"`
	Types      map[string]entry `json:"types"`
}

type version struct {
	name   string
	schema schema
	// named collects every dispatch met while flattening, by name and key
	// registry, so each is diffed once as its own table
	named map[string]map[string]any
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: nbtdiff <versionA|dirA> <versionB|dirB>")
		os.Exit(2)
	}
	a, err := load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "nbtdiff:", err)
		os.Exit(1)
	}
	b, err := load(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "nbtdiff:", err)
		os.Exit(1)
	}
	fmt.Printf("nbtdiff: %s -> %s\n", a.name, b.name)
	changed := 0
	changed += section("registries", a, b, a.schema.Registries, b.schema.Registries)
	changed += section("types", a, b, a.schema.Types, b.schema.Types)
	changed += dispatches(a, b)
	if changed == 0 {
		fmt.Println("\nno difference")
	}
}

// dispatches diffs every union the entries use, by case, once each; a union
// met only inside another is flattened when that one is.
func dispatches(a, b *version) int {
	fa, fb := map[string]map[string]string{}, map[string]map[string]string{}
	for _, v := range []*version{a, b} {
		tables := fa
		if v == b {
			tables = fb
		}
		done := map[string]bool{}
		for {
			progress := false
			for _, name := range sortedKeys(v.named) {
				if done[name] {
					continue
				}
				done[name], progress = true, true
				out := map[string]string{}
				cases, _ := v.named[name]["cases"].([]any)
				for _, ca := range cases {
					c, _ := ca.(map[string]any)
					ct, _ := c["type"].(map[string]any)
					p := "[" + str(c["id"]) + "]"
					if isLeaf(ct) {
						out[p] = summary(ct)
					} else {
						flatten(ct, p, out, v.named)
					}
				}
				tables[name] = out
			}
			if !progress {
				break
			}
		}
	}
	changed := 0
	header := false
	for _, name := range union(sortedKeys(fa), sortedKeys(fb)) {
		ta, okA := fa[name]
		tb, okB := fb[name]
		var lines []string
		switch {
		case !okA:
			lines = append(lines, "    + (new union)")
		case !okB:
			lines = append(lines, "    - (union gone)")
		default:
			lines = diffLines(ta, tb)
		}
		if len(lines) == 0 {
			continue
		}
		if !header {
			fmt.Printf("\nunions the entries use (by case):\n")
			header = true
		}
		fmt.Printf("  %s\n%s\n", name, strings.Join(lines, "\n"))
		changed++
	}
	return changed
}

// diffLines is the key diff of two flattened compounds.
func diffLines(fa, fb map[string]string) []string {
	var lines []string
	for _, p := range collapse(only(fb, fa)) {
		lines = append(lines, fmt.Sprintf("    + %-45s %s", p, fb[p]))
	}
	for _, p := range collapse(only(fa, fb)) {
		lines = append(lines, fmt.Sprintf("    - %-45s %s", p, fa[p]))
	}
	for _, p := range sortedKeys(fa) {
		if tb, ok := fb[p]; ok && tb != fa[p] {
			lines = append(lines, fmt.Sprintf("    ~ %-45s %s -> %s", p, fa[p], tb))
		}
	}
	return lines
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string{}, a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func load(arg string) (*version, error) {
	dir := paths.Data(arg)
	v := &version{name: filepath.Base(dir), named: map[string]map[string]any{}}
	data, err := os.ReadFile(filepath.Join(dir, "nbt_schema.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &v.schema); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return v, nil
}

// section prints one section's differences and returns how many entries differ.
func section(name string, va, vb *version, a, b map[string]entry) int {
	fmt.Printf("\n%s: %d -> %d\n", name, len(a), len(b))
	changed := 0
	for _, k := range sortedKeys(b) {
		if _, ok := a[k]; !ok {
			fmt.Printf("  + %s (%s)\n", k, shortClass(b[k].Class))
			flatten(b[k].Type, "", map[string]string{}, vb.named)
			changed++
		}
	}
	for _, k := range sortedKeys(a) {
		if _, ok := b[k]; !ok {
			fmt.Printf("  - %s (%s)\n", k, shortClass(a[k].Class))
			flatten(a[k].Type, "", map[string]string{}, va.named)
			changed++
		}
	}
	for _, k := range sortedKeys(a) {
		eb, ok := b[k]
		if !ok {
			continue
		}
		ea := a[k]
		fa, fb := map[string]string{}, map[string]string{}
		flatten(ea.Type, "", fa, va.named)
		flatten(eb.Type, "", fb, vb.named)
		lines := diffLines(fa, fb)
		if ea.Coverage != eb.Coverage {
			lines = append(lines, fmt.Sprintf("    ~ %-45s %s -> %s", "(coverage)", ea.Coverage, eb.Coverage))
		}
		if len(lines) > 0 {
			fmt.Printf("  %s (%s)\n%s\n", k, shortClass(eb.Class), strings.Join(lines, "\n"))
			changed++
		}
	}
	return changed
}

// only lists the keys of a that b lacks, sorted.
func only(a, b map[string]string) []string {
	var out []string
	for _, p := range sortedKeys(a) {
		if _, ok := b[p]; !ok {
			out = append(out, p)
		}
	}
	return out
}

// collapse drops the keys under a key that is itself new (or gone): a whole
// subtree that appeared is one line, its top, not every leaf in it.
func collapse(ps []string) []string {
	bases := map[string]bool{}
	for _, p := range ps {
		bases[base(p)] = true
	}
	var out []string
	for _, p := range ps {
		under := false
		for b := range bases {
			if b != base(p) && strings.HasPrefix(p, b) && strings.ContainsAny(p[len(b):len(b)+1], ".[{<?") {
				under = true
				break
			}
		}
		if !under {
			out = append(out, p)
		}
	}
	return out
}

// base is a key without its " (id)" / " (either)" / " keys" annotation.
func base(p string) string {
	if i := strings.Index(p, " "); i >= 0 {
		return p[:i]
	}
	return p
}

// flatten writes every key of a node's compound, with its type, into out.
// Path pieces: ".key" for a field, "[id]" for a dispatch case, "[]" for a
// list's elements, "{}" for a map's values.
func flatten(n map[string]any, path string, out map[string]string, named map[string]map[string]any) {
	switch n["k"] {
	case "struct", "group":
		fs, _ := n["fields"].([]any)
		for _, fa := range fs {
			f, _ := fa.(map[string]any)
			ft, _ := f["type"].(map[string]any)
			if f["inline"] == true {
				flatten(ft, path, out, named)
				continue
			}
			key, _ := f["key"].(string)
			if key == "" {
				key, _ = f["name"].(string)
			}
			p := path + "." + key
			opt := ""
			if f["optional"] == true {
				opt = "?"
			}
			if isLeaf(ft) {
				out[strings.TrimPrefix(p, ".")+opt] = summary(ft)
			} else {
				flatten(ft, p+opt, out, named)
			}
		}
	case "dispatch":
		// a union is one table of its own (dispatches), whichever entry spells it out
		key, _ := n["key"].(string)
		out[strings.TrimPrefix(path+"."+key, ".")] = "dispatch " + dispatchName(n)
		if _, ok := named[dispatchName(n)]; !ok && n["cases"] != nil {
			named[dispatchName(n)] = n
		}
	case "case":
		if ct, ok := n["type"].(map[string]any); ok {
			flatten(ct, path, out, named)
		}
	case "list":
		e, _ := n["elem"].(map[string]any)
		p := path + "[]"
		if isLeaf(e) {
			out[strings.TrimPrefix(p, ".")] = summary(e)
		} else {
			flatten(e, p, out, named)
		}
	case "map":
		v, _ := n["val"].(map[string]any)
		p := path + "{}"
		out[strings.TrimPrefix(p, ".")+" keys"] = summary(n["key"].(map[string]any))
		if isLeaf(v) {
			out[strings.TrimPrefix(p, ".")] = summary(v)
		} else {
			flatten(v, p, out, named)
		}
	case "holder":
		out[strings.TrimPrefix(path, ".")+" (id)"] = "id in " + str(n["registry"])
		if d, ok := n["direct"].(map[string]any); ok {
			flatten(d, path, out, named)
		}
	case "either":
		l, _ := n["left"].(map[string]any)
		r, _ := n["right"].(map[string]any)
		out[strings.TrimPrefix(path, ".")+" (either)"] = summary(l) + " | " + summary(r)
		if !isLeaf(l) {
			flatten(l, path+"<left>", out, named)
		}
		if !isLeaf(r) {
			flatten(r, path+"<right>", out, named)
		}
	case "recursive":
		if t, ok := n["type"].(map[string]any); ok {
			flatten(t, path, out, named)
		}
	default:
		out[strings.TrimPrefix(path, ".")] = summary(n)
	}
}

func isLeaf(n map[string]any) bool {
	switch n["k"] {
	case "struct", "group", "dispatch", "case", "list", "map", "holder", "either", "recursive":
		return false
	}
	return true
}

// summary is one line for a node: what a reader of the wire would say.
func summary(n map[string]any) string {
	if n == nil {
		return "?"
	}
	switch n["k"] {
	case "prim":
		return str(n["t"])
	case "enum":
		vals, _ := n["ids"].([]any)
		return fmt.Sprintf("enum %s (%d)", str(n["name"]), len(vals))
	case "registry", "resourcekey":
		return "id in " + str(n["registry"])
	case "holderset":
		return "set of " + str(n["registry"])
	case "holder":
		if d, ok := n["direct"].(map[string]any); ok {
			return "id in " + str(n["registry"]) + " or " + summary(d)
		}
		return "id in " + str(n["registry"])
	case "struct", "dispatch", "recursive":
		return str(n["k"]) + " " + str(n["name"])
	case "ref":
		return "a " + str(n["name"]) + " again"
	case "list":
		e, _ := n["elem"].(map[string]any)
		return "list of " + summary(e)
	case "map":
		v, _ := n["val"].(map[string]any)
		return "map of " + summary(v)
	case "either":
		l, _ := n["left"].(map[string]any)
		r, _ := n["right"].(map[string]any)
		return summary(l) + " | " + summary(r)
	case "opaque":
		return "opaque " + str(n["java"])
	}
	return str(n["k"])
}

// dispatchName identifies a union: its name and what its key is.
func dispatchName(n map[string]any) string {
	return str(n["name"]) + " on " + summary(keyType(n))
}

func keyType(n map[string]any) map[string]any {
	kt, _ := n["keyType"].(map[string]any)
	return kt
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func shortClass(c string) string {
	return c[strings.LastIndex(c, ".")+1:]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
