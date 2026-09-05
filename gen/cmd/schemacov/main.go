// schemacov reports how much of packet_schema.json (format 2) is fully typed and
// what the rest is missing, so extractor work can be aimed at the biggest gaps.
//
//	go run ./gen/cmd/schemacov 26.2            # summary + hole kinds (temp/data/26.2 or MC26_DATA)
//	go run ./gen/cmd/schemacov -partial 26.2   # also list every partial packet with its holes
//	go run ./gen/cmd/schemacov -show serverbound/minecraft:interact 26.2   # print one typed tree
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mj41/mc26/gen/internal/paths"
)

type entry struct {
	State    string         `json:"state"`
	Class    string         `json:"class"`
	Coverage string         `json:"coverage"`
	Type     map[string]any `json:"type"`
}

type schema struct {
	Version int              `json:"version"`
	Packets map[string]entry `json:"packets"`
	Structs map[string]entry `json:"structs"`
}

func main() {
	partial := flag.Bool("partial", false, "list every partial packet with its holes")
	show := flag.String("show", "", "print the typed tree of one packet key (flow/name)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: schemacov [-partial] [-show flow/name] <version|dir>")
		os.Exit(2)
	}
	dir := paths.Data(flag.Arg(0))
	data, err := os.ReadFile(filepath.Join(dir, "packet_schema.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemacov:", err)
		os.Exit(1)
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		fmt.Fprintln(os.Stderr, "schemacov:", err)
		os.Exit(1)
	}
	if *show != "" {
		e, ok := s.Packets[*show]
		if !ok {
			e, ok = s.Structs[*show]
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "schemacov: no such packet or struct:", *show)
			os.Exit(1)
		}
		fmt.Printf("%s (%s, %s, %s)\n", *show, e.State, e.Class, e.Coverage)
		printTree(e.Type, "  ")
		return
	}

	full, total := 0, 0
	holes := map[string]int{}
	var partials []string
	perState := map[string][2]int{}
	for _, key := range sortedKeys(s.Packets) {
		e := s.Packets[key]
		total++
		st := perState[e.State]
		st[1]++
		if e.Coverage == "full" {
			full++
			st[0]++
		} else {
			hs := findHoles(e.Type)
			seen := map[string]bool{}
			for _, h := range hs {
				if !seen[h] {
					seen[h] = true
					holes[h]++
				}
			}
			partials = append(partials, fmt.Sprintf("  %-55s %s", key, strings.Join(dedupe(hs), ", ")))
		}
		perState[e.State] = st
	}
	fmt.Printf("packet_schema.json format %d: %d of %d packets fully typed (%d structs)\n", s.Version, full, total, len(s.Structs))
	for _, st := range sortedKeys(perState) {
		fmt.Printf("  %-14s %3d / %3d\n", st, perState[st][0], perState[st][1])
	}
	fmt.Println("\nhole kinds across partial packets:")
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range holes {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].v > kvs[j].v || (kvs[i].v == kvs[j].v && kvs[i].k < kvs[j].k) })
	for _, e := range kvs {
		fmt.Printf("  %3d  %s\n", e.v, e.k)
	}
	if *partial {
		fmt.Println("\npartial packets:")
		for _, p := range partials {
			fmt.Println(p)
		}
	}
}

// findHoles lists what keeps a tree from being fully typed.
func findHoles(n map[string]any) []string {
	var out []string
	switch n["k"] {
	case "opaque":
		out = append(out, "opaque:"+fmt.Sprint(n["java"]))
	case "dispatch", "either":
		out = append(out, fmt.Sprint(n["k"]))
	case "enum", "enumset":
		if n["values"] == nil {
			out = append(out, "enum-without-values:"+fmt.Sprint(n["name"]))
		}
	}
	if b, _ := n["conditional"].(bool); b {
		out = append(out, "conditional reader")
	}
	for _, v := range n {
		switch t := v.(type) {
		case map[string]any:
			if _, ok := t["k"]; ok {
				out = append(out, findHoles(t)...)
			}
		case []any:
			for _, f := range t {
				if fm, ok := f.(map[string]any); ok {
					if tt, ok := fm["type"].(map[string]any); ok {
						out = append(out, findHoles(tt)...)
					}
				}
			}
		}
	}
	return out
}

func printTree(n map[string]any, indent string) {
	k := fmt.Sprint(n["k"])
	switch k {
	case "struct":
		fmt.Printf("%sstruct %v%s\n", indent, n["name"], cond(n))
		fields, _ := n["fields"].([]any)
		for _, f := range fields {
			fm := f.(map[string]any)
			fmt.Printf("%s  %v:\n", indent, fm["name"])
			if t, ok := fm["type"].(map[string]any); ok {
				printTree(t, indent+"    ")
			}
		}
	case "list", "optional":
		fmt.Printf("%s%s\n", indent, k)
		if e, ok := n["elem"].(map[string]any); ok {
			printTree(e, indent+"  ")
		}
	case "map":
		fmt.Printf("%smap\n", indent)
		if e, ok := n["key"].(map[string]any); ok {
			printTree(e, indent+"  key: ")
		}
		if e, ok := n["val"].(map[string]any); ok {
			printTree(e, indent+"  val: ")
		}
	case "holder":
		fmt.Printf("%sholder %v\n", indent, n["registry"])
		if d, ok := n["direct"].(map[string]any); ok {
			printTree(d, indent+"  direct: ")
		}
	default:
		desc := k
		for _, key := range []string{"t", "name", "registry", "java", "bits", "max"} {
			if v, ok := n[key]; ok {
				desc += fmt.Sprintf(" %s=%v", key, v)
			}
		}
		if vals, ok := n["values"].([]any); ok {
			desc += fmt.Sprintf(" values=%d", len(vals))
		}
		fmt.Printf("%s%s\n", indent, desc)
	}
}

func cond(n map[string]any) string {
	if b, _ := n["conditional"].(bool); b {
		return " (conditional)"
	}
	return ""
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
