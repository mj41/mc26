// Package schemacheck says whether the two schemas an extraction produced
// describe everything: every packet, component, registry element and shared
// type is `full`, no node is `opaque`, no dispatch is without its cases, no
// case is unnamed, and every `ref` points at an enclosing node of its name
// and kind. A build refuses a schema with a hole unless told otherwise, so a
// Minecraft version that opens one stops the pipeline instead of leaving a
// raw field for a reader of the file to notice.
package schemacheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Report lists what is wrong, by file and entry.
type Report struct {
	Entries  int      // packets, components, registries and shared types looked at
	Refs     int      // refs resolved
	Problems []string // "<file> <entry>: <what>", empty when the schemas are complete
}

func (r Report) OK() bool { return len(r.Problems) == 0 }

// Err is nil when the report is fine, else one error naming the first
// problems and how many there are.
func (r Report) Err() error {
	if r.OK() {
		return nil
	}
	shown := r.Problems
	more := ""
	if len(shown) > 12 {
		more = fmt.Sprintf("\n  … and %d more", len(shown)-12)
		shown = shown[:12]
	}
	return fmt.Errorf("%d holes in the schemas:\n  %s%s", len(r.Problems), strings.Join(shown, "\n  "), more)
}

// Check reads packet_schema.json and nbt_schema.json under dataDir.
func Check(dataDir string) (Report, error) {
	var out Report
	for _, f := range []struct {
		name     string
		sections []string
	}{
		{"packet_schema.json", []string{"packets", "components"}},
		{"nbt_schema.json", []string{"registries", "types"}},
	} {
		r, err := CheckFile(filepath.Join(dataDir, f.name), f.sections...)
		if err != nil {
			return out, err
		}
		out.Entries += r.Entries
		out.Refs += r.Refs
		out.Problems = append(out.Problems, r.Problems...)
	}
	return out, nil
}

// CheckFile checks the named top-level sections of one schema file: each is
// a map of entries {coverage, type}. The packet schema's `structs` section is
// the walker's shared table and is not an output (its chunk shapes are what
// prims.json describes), so it is not a section here.
func CheckFile(path string, sections ...string) (Report, error) {
	var out Report
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		return out, fmt.Errorf("%s: %w", path, err)
	}
	base := filepath.Base(path)
	for _, sec := range sections {
		entries, _ := file[sec].(map[string]any)
		if entries == nil {
			out.Problems = append(out.Problems, fmt.Sprintf("%s: no %q section", base, sec))
			continue
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e, _ := entries[k].(map[string]any)
			out.Entries++
			where := base + " " + k
			if c, _ := e["coverage"].(string); c != "full" {
				out.Problems = append(out.Problems, fmt.Sprintf("%s: coverage %q", where, c))
			}
			t, _ := e["type"].(map[string]any)
			if t == nil {
				out.Problems = append(out.Problems, where+": no type")
				continue
			}
			c := checker{where: where}
			c.walk(t, nil, "")
			out.Refs += c.refs
			out.Problems = append(out.Problems, c.problems...)
		}
	}
	return out, nil
}

type checker struct {
	where    string
	refs     int
	problems []string
}

type named struct{ name, kind string }

func (c *checker) problem(path, what string) {
	c.problems = append(c.problems, fmt.Sprintf("%s: %s at %s", c.where, what, strings.TrimPrefix(path, ".")))
}

// walk visits every node; enclosing carries the named struct, dispatch and
// recursive nodes above, innermost last.
func (c *checker) walk(n map[string]any, enclosing []named, path string) {
	k, _ := n["k"].(string)
	switch k {
	case "opaque":
		c.problem(path, fmt.Sprintf("opaque node (%v)", n["java"]))
		return
	case "dispatch":
		if _, ok := n["cases"]; !ok {
			c.problem(path, fmt.Sprintf("dispatch %v on %q without cases", n["name"], n["key"]))
		}
	case "case":
		if id, _ := n["id"].(string); id == "?" {
			c.problem(path, "a case without an id")
		}
	case "ref":
		c.refs++
		name, _ := n["name"].(string)
		of, _ := n["of"].(string)
		if of == "" {
			c.problem(path, fmt.Sprintf("ref %q says not what kind of node it names", name))
			return
		}
		for _, e := range slices.Backward(enclosing) {
			if e.name == name && e.kind == of {
				return
			}
		}
		c.problem(path, fmt.Sprintf("ref to %s %q with no enclosing node of that name", of, name))
		return
	}
	if name, _ := n["name"].(string); name != "" && (k == "struct" || k == "dispatch" || k == "recursive") {
		enclosing = append(enclosing, named{name, k})
	}
	for _, key := range sortedKeys(n) {
		switch v := n[key].(type) {
		case map[string]any:
			c.walk(v, enclosing, path+"."+key)
		case []any:
			for i, e := range v {
				if m, ok := e.(map[string]any); ok {
					c.walk(m, enclosing, fmt.Sprintf("%s.%s[%d]", path, key, i))
				}
			}
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
