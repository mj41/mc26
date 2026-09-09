// Package prims reads the definitions of the schema's named primitives and
// checks that a version's schema is covered by them.
//
// packet_schema.json describes every packet as a tree whose leaves are named:
// VAR_INT, ITEM_STACK, COMPONENT_PATCH. Those names are all the Go generator
// needs, because the library has a type for each; a generator for another
// language has nothing. gen/hand-crafted/prims.json says what each name is on
// the wire, and this package is what keeps that file honest: a version that
// introduces a primitive nobody has defined fails the build rather than
// producing a binding with a hole in it.
package prims

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Def is one entry: what the primitive is on the wire, and where that was read
// from in Mojang's code.
type Def struct {
	Def  map[string]any `json:"def"`
	Java string         `json:"java"`
	Note string         `json:"note"`
}

// Set is every definition, by primitive name.
type Set map[string]Def

// Load reads a definitions file.
func Load(path string) (Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var file struct {
		Prims Set `json:"prims"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(file.Prims) == 0 {
		return nil, fmt.Errorf("%s has no prims", path)
	}
	return file.Prims, nil
}

// DefKinds counts every distinct node kind the definitions themselves use, so
// that a kind a definition introduces — packed, rest — is described in
// nodes.json like any the schema emits.
func DefKinds(defs Set) map[string]int {
	out := map[string]int{}
	for _, d := range defs {
		walkNodes(d.Def, func(n map[string]any) {
			if k, ok := n["k"].(string); ok && k != "native" {
				out[k]++
			}
		})
	}
	return out
}

// Kinds counts every distinct node kind a schema file uses. A generator that
// reads the JSON has to know what each one is on the wire, so they are defined
// alongside the primitives.
func Kinds(schemaPath string) (map[string]int, error) {
	doc, err := readJSON(schemaPath)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	walkNodes(doc, func(n map[string]any) {
		if k, ok := n["k"].(string); ok {
			out[k]++
		}
	})
	return out, nil
}

// Used counts every {"k":"prim","t":…} leaf of a schema file.
func Used(schemaPath string) (map[string]int, error) {
	doc, err := readJSON(schemaPath)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	walkNodes(doc, func(n map[string]any) {
		if n["k"] == "prim" {
			if t, ok := n["t"].(string); ok {
				out[t]++
			}
		}
		// a packed integer names the primitive it is read as, not a prim node
		if n["k"] == "bits" {
			if t, ok := n["of"].(string); ok {
				out[t]++
			}
		}
	})
	return out, nil
}

// Report is what a check found.
type Report struct {
	Used      map[string]int
	Undefined []string // used by the schema, with no definition
	Dangling  []string // a definition naming a primitive that has none
	Cycles    []string // definitions that resolve in a circle
	Unused    []string // defined, but this version does not use it
}

// OK reports whether a reader could resolve every primitive of the schema.
// An unused definition is not a fault: versions differ.
func (r Report) OK() bool {
	return len(r.Undefined) == 0 && len(r.Dangling) == 0 && len(r.Cycles) == 0
}

// Err is what to fail a build with.
func (r Report) Err() error {
	if r.OK() {
		return nil
	}
	var parts []string
	if len(r.Undefined) > 0 {
		parts = append(parts, "used but not defined: "+strings.Join(r.Undefined, ", "))
	}
	if len(r.Dangling) > 0 {
		parts = append(parts, "defined but referring to something undefined: "+strings.Join(r.Dangling, ", "))
	}
	if len(r.Cycles) > 0 {
		parts = append(parts, "circular: "+strings.Join(r.Cycles, ", "))
	}
	return fmt.Errorf("%s (see gen/hand-crafted/prims.json)", strings.Join(parts, "; "))
}

// Check compares a set of definitions with the primitives a schema uses.
func Check(defs Set, used map[string]int) Report {
	r := Report{Used: used}
	for _, t := range sortedKeys(used) {
		if _, ok := defs[t]; !ok {
			r.Undefined = append(r.Undefined, fmt.Sprintf("%s (%d uses)", t, used[t]))
		}
	}
	// A definition is in use when the schema names it, or when a definition the
	// schema names reaches it: CHUNK_SECTIONS is on the wire through the chunk
	// packet, and the two paletted containers through CHUNK_SECTIONS.
	reached := map[string]bool{}
	var reach func(string)
	reach = func(t string) {
		if reached[t] {
			return
		}
		reached[t] = true
		if d, ok := defs[t]; ok {
			for _, ref := range Refs(d.Def) {
				reach(ref)
			}
		}
	}
	for t := range used {
		reach(t)
	}
	for _, t := range sortedKeys(defs) {
		for _, ref := range Refs(defs[t].Def) {
			if _, ok := defs[ref]; !ok {
				r.Dangling = append(r.Dangling, t+" -> "+ref)
			}
		}
		if !reached[t] {
			r.Unused = append(r.Unused, t)
		}
	}
	r.Cycles = cycles(defs)
	return r
}

// Refs are the primitives a definition names, at any depth.
func Refs(d map[string]any) []string {
	var out []string
	walkNodes(d, func(n map[string]any) {
		if n["k"] == "prim" {
			if t, ok := n["t"].(string); ok {
				out = append(out, t)
			}
		}
		if n["k"] == "bits" {
			if t, ok := n["of"].(string); ok {
				out = append(out, t)
			}
		}
	})
	return out
}

// cycles reports definitions that reach themselves. A format really can
// recurse — a text component holds text components — so a definition may say
// so with "recursive": true and is then left alone.
func cycles(defs Set) []string {
	var out []string
	state := map[string]int{} // 0 unseen, 1 on the path, 2 done
	var path []string
	var walk func(string)
	walk = func(t string) {
		if d, ok := defs[t]; ok && d.Def["recursive"] == true {
			state[t] = 2
			return
		}
		switch state[t] {
		case 1:
			out = append(out, strings.Join(append(path, t), " -> "))
			return
		case 2:
			return
		}
		state[t] = 1
		path = append(path, t)
		for _, ref := range Refs(defs[t].Def) {
			if ref == t {
				out = append(out, t+" -> itself")
				continue
			}
			walk(ref)
		}
		path = path[:len(path)-1]
		state[t] = 2
	}
	for _, t := range sortedKeys(defs) {
		walk(t)
	}
	sort.Strings(out)
	return out
}

func readJSON(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// Nodes lists the node kinds, from the hand-crafted file, each with a one-line
// summary; what a kind is on the wire is written in gen/docs/protocol.mc26tmpl.md.
type Nodes map[string]struct {
	Summary string `json:"summary"`
}

// LoadNodes reads the node kind definitions.
func LoadNodes(path string) (Nodes, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var file struct {
		Nodes Nodes `json:"nodes"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(file.Nodes) == 0 {
		return nil, fmt.Errorf("%s has no nodes", path)
	}
	return file.Nodes, nil
}

// CheckKinds reports node kinds a schema uses with no definition.
func CheckKinds(nodes Nodes, kinds map[string]int) []string {
	var out []string
	for _, k := range sortedKeys(kinds) {
		if _, ok := nodes[k]; !ok {
			out = append(out, fmt.Sprintf("%s (%d uses)", k, kinds[k]))
		}
	}
	return out
}

func walkNodes(n any, f func(map[string]any)) {
	switch v := n.(type) {
	case map[string]any:
		f(v)
		for _, x := range v {
			walkNodes(x, f)
		}
	case []any:
		for _, x := range v {
			walkNodes(x, f)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
