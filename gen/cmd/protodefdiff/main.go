// protodefdiff compares our extracted packet schema of a version with the
// hand-written ProtoDef definition PrismarineJS/minecraft-data keeps for it
// (data/pc/<version>/protocol.json), the source node-minecraft-protocol reads.
//
//	go run ./gen/cmd/protodefdiff 26.1 /path/to/minecraft-data/data/pc/26.1/protocol.json
//
// Both descriptions are flattened to the same vocabulary: the natives of
// the schema (varint, i32be, string, …) in wire order, with markers for a
// count, an option, a switch and a loop. The comparison is per state and
// direction: the packet id tables first (an id one side lacks, or names in a
// different order, is a real difference), then every packet both sides have,
// equal or not, with both flattenings when they differ. Naming is not
// compared; a switch's cases are counted, not expanded.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mj41/mc26/gen/internal/paths"
)

type node = map[string]any

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: protodefdiff <version|dir> <protocol.json>")
		os.Exit(2)
	}
	dataDir := paths.Data(os.Args[1])
	ours, err := loadOurs(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protodefdiff:", err)
		os.Exit(1)
	}
	theirs, err := loadProtoDef(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "protodefdiff:", err)
		os.Exit(1)
	}
	fmt.Printf("protodefdiff: %s (mc26) vs %s (ProtoDef)\n", filepath.Base(dataDir), os.Args[2])
	same, differ := 0, 0
	for _, st := range []struct{ ours, theirs string }{{"handshake", "handshaking"}, {"status", "status"}, {"login", "login"}, {"configuration", "configuration"}, {"play", "play"}} {
		for _, fl := range []struct{ ours, theirs string }{{"clientbound", "toClient"}, {"serverbound", "toServer"}} {
			ourTbl := ours.ids[st.ours][fl.ours]
			theirTbl, theirTypes, theirTypeOf := theirs.table(st.theirs, fl.theirs)
			if ourTbl == nil && theirTbl == nil {
				continue
			}
			fmt.Printf("\n## %s %s: %d packets (mc26), %d (ProtoDef)\n", st.ours, fl.ours, len(ourTbl), len(theirTbl))
			ourByID := map[int]string{}
			for name, e := range ourTbl {
				ourByID[e.ProtocolID] = name
			}
			ids := map[int]bool{}
			for id := range ourByID {
				ids[id] = true
			}
			for id := range theirTbl {
				ids[id] = true
			}
			sortedIDs := make([]int, 0, len(ids))
			for id := range ids {
				sortedIDs = append(sortedIDs, id)
			}
			sort.Ints(sortedIDs)
			for _, id := range sortedIDs {
				on, ok1 := ourByID[id]
				tn, ok2 := theirTbl[id]
				switch {
				case !ok1:
					fmt.Printf("  id %d: only ProtoDef (%s)\n", id, tn)
					continue
				case !ok2:
					fmt.Printf("  id %d: only mc26 (%s)\n", id, on)
					continue
				}
				ourEntry, _ := ours.schema[fl.ours+"/"+on].(node)
				if ourEntry == nil {
					fmt.Printf("  id %d %s: no entry in packet_schema.json\n", id, on)
					continue
				}
				ourFlat := ours.flatten(ourEntry["type"].(node), map[string]bool{})
				tt := theirTypeOf[tn]
				if tt == "" {
					tt = "packet_" + tn
				}
				var theirType any = tt
				if tt == "void" {
					theirType = []any{"container", []any{}}
				}
				theirFlat := theirs.flatten(theirType, theirTypes, map[string]bool{})
				if ourFlat == theirFlat {
					same++
					continue
				}
				differ++
				fmt.Printf("  id %d %s / %s\n    mc26:     %s\n    ProtoDef: %s\n", id, on, tn, clip(ourFlat), clip(theirFlat))
			}
		}
	}
	fmt.Printf("\n%d packets flatten the same, %d differ\n", same, differ)
}

func clip(s string) string {
	if len(s) > 400 {
		return s[:400] + " …"
	}
	return s
}

// ---- ours ------------------------------------------------------------------------------

type ourSchema struct {
	schema map[string]any // packet_schema.json "packets"
	ids    map[string]map[string]map[string]struct {
		ProtocolID int `json:"protocol_id"`
	}
	prims map[string]any
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func loadOurs(dataDir string) (*ourSchema, error) {
	o := &ourSchema{}
	var ps struct {
		Packets map[string]any `json:"packets"`
		Prims   map[string]any `json:"prims"`
	}
	if err := readJSON(filepath.Join(dataDir, "packet_schema.json"), &ps); err != nil {
		return nil, err
	}
	o.schema = ps.Packets
	if err := readJSON(filepath.Join(dataDir, "packets.json"), &o.ids); err != nil {
		return nil, err
	}
	o.prims = ps.Prims
	if len(o.prims) == 0 {
		return nil, fmt.Errorf("%s has no \"prims\" section (re-run extraction)", filepath.Join(dataDir, "packet_schema.json"))
	}
	return o, nil
}

// flatten renders one of our nodes as natives in wire order.
func (o *ourSchema) flatten(n node, seen map[string]bool) string {
	var parts []string
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	switch n["k"] {
	case "prim":
		t := str(n["t"])
		if seen[t] {
			return "ref(" + t + ")"
		}
		d, ok := o.prims[t].(node)
		if !ok {
			return "prim?" + t
		}
		def, _ := d["def"].(node)
		if def["k"] == "native" {
			return str(def["of"])
		}
		seen2 := map[string]bool{t: true}
		for k := range seen {
			seen2[k] = true
		}
		return o.flatten(def, seen2)
	case "bits":
		return o.flatten(node{"k": "prim", "t": n["of"]}, seen)
	case "struct":
		fields, _ := n["fields"].([]any)
		for _, fa := range fields {
			f, _ := fa.(node)
			s := o.flatten(f["type"].(node), seen)
			if _, ok := f["when"]; ok {
				s = "?" + s
			}
			add(s)
		}
	case "list":
		elem := o.flatten(n["elem"].(node), seen)
		if elem == "i8" || elem == "u8" {
			return "bytes[varint]"
		}
		return "varint[" + elem + "]"
	case "counted":
		return "count=" + str(n["count"]) + "[" + o.flatten(n["elem"].(node), seen) + "]"
	case "lenprefixed":
		if l, ok := n["length"]; ok {
			return "window=" + str(l) + "{" + o.flatten(n["elem"].(node), seen) + "}"
		}
		return "varint{" + o.flatten(n["elem"].(node), seen) + "}"
	case "rest":
		return "rest[" + o.flatten(n["elem"].(node), seen) + "]"
	case "packed":
		return "packed"
	case "map":
		return "varint[" + o.flatten(n["key"].(node), seen) + " " + o.flatten(n["val"].(node), seen) + "]"
	case "optional":
		return "?(" + o.flatten(n["elem"].(node), seen) + ")"
	case "either":
		return "bool<" + o.flatten(n["left"].(node), seen) + "|" + o.flatten(n["right"].(node), seen) + ">"
	case "enum", "registry":
		return "varint"
	case "enumset":
		vals, _ := n["values"].([]any)
		return fmt.Sprintf("bits%d", (len(vals)+7)/8*8)
	case "stringenum", "string", "resourcekey":
		return "string"
	case "holder":
		if d, ok := n["direct"].(node); ok {
			return "holder(" + o.flatten(d, seen) + ")"
		}
		return "varint"
	case "holderset":
		return "holderset"
	case "ref":
		return "ref(" + str(n["name"]) + ")"
	case "nbt":
		return "nbt"
	case "unit":
		return ""
	case "dispatch":
		key, _ := n["key"].(node)
		cases, _ := n["cases"].([]any)
		k := "varint"
		if key != nil {
			if key["k"] != nil {
				k = o.flatten(key, seen)
			} else {
				k = "field=" + str(key["field"])
			}
		}
		if cf := str(n["casesFrom"]); cf != "" {
			return k + " switch(" + cf + ")"
		}
		return fmt.Sprintf("%s switch(%d)", k, len(cases))
	case "whilelist":
		return "loop[" + o.flatten(n["elem"].(node), seen) + "]"
	case "case":
		return o.flatten(n["type"].(node), seen)
	case "opaque":
		return "opaque"
	}
	return strings.Join(parts, " ")
}

// ---- ProtoDef ------------------------------------------------------------------------

type protoDef struct {
	types map[string]any            // global types
	state map[string]map[string]any // "play" → {"toClient": {types: …}}
}

func loadProtoDef(path string) (*protoDef, error) {
	var raw map[string]any
	if err := readJSON(path, &raw); err != nil {
		return nil, err
	}
	p := &protoDef{state: map[string]map[string]any{}}
	p.types, _ = raw["types"].(map[string]any)
	for k, v := range raw {
		if k == "types" {
			continue
		}
		if m, ok := v.(map[string]any); ok {
			p.state[k] = m
		}
	}
	return p, nil
}

// table is a direction's id → packet name, its local types, and the packet
// name → type name map of the params switch (a packet shared by several
// states is a global packet_common_* type).
func (p *protoDef) table(state, flow string) (map[int]string, map[string]any, map[string]string) {
	typeOf := map[string]string{}
	st, ok := p.state[state]
	if !ok {
		return nil, nil, typeOf
	}
	dir, _ := st[flow].(map[string]any)
	types, _ := dir["types"].(map[string]any)
	pk, _ := types["packet"].([]any)
	if len(pk) != 2 {
		return nil, types, typeOf
	}
	fields, _ := pk[1].([]any)
	ids := map[int]string{}
	for _, fa := range fields {
		f, _ := fa.(map[string]any)
		if str(f["name"]) == "params" {
			sw, _ := f["type"].([]any)
			if len(sw) == 2 {
				a, _ := sw[1].(map[string]any)
				cases, _ := a["fields"].(map[string]any)
				for name, t := range cases {
					typeOf[name] = str(t)
				}
			}
			continue
		}
		if str(f["name"]) != "name" {
			continue
		}
		mapper, _ := f["type"].([]any)
		if len(mapper) != 2 {
			continue
		}
		args, _ := mapper[1].(map[string]any)
		mappings, _ := args["mappings"].(map[string]any)
		for hex, name := range mappings {
			id, err := strconv.ParseInt(strings.TrimPrefix(str(hex), "0x"), 16, 32)
			if err == nil {
				ids[int(id)] = str(name)
			}
		}
	}
	return ids, types, typeOf
}

var protoNatives = map[string]string{
	"varint": "varint", "varlong": "varlong", "i8": "i8", "u8": "u8", "i16": "i16be", "u16": "u16be",
	"i32": "i32be", "u32": "i32be", "i64": "i64be", "u64": "i64be", "f32": "f32be", "f64": "f64be",
	"bool": "bool", "string": "string", "UUID": "i64be i64be", "restBuffer": "rest_bytes",
	"anonymousNbt": "nbt", "anonOptionalNbt": "nbt", "optionalNbt": "nbt", "nbt": "nbt",
	"optvarint": "optional_var_int", "lpVec3": "lp_vec3", "ByteArray": "bytes[varint]",
	"void": "", "entityMetadataLoop": "loop[entity_metadata]", "topBitSetTerminatedArray": "loop",
	"registryEntryHolderSet": "holderset", "position": "i64be",
	"vec2f": "f32be f32be", "vec3f": "f32be f32be f32be", "vec4f": "f32be f32be f32be f32be",
	"vec3f64": "f64be f64be f64be", "vec3i": "i32be i32be i32be", "vec3i32": "i32be i32be i32be",
}

// flatten renders a ProtoDef type as natives in wire order; local types
// shadow global ones.
func (p *protoDef) flatten(t any, local map[string]any, seen map[string]bool) string {
	switch x := t.(type) {
	case string:
		if s, ok := protoNatives[x]; ok {
			return s
		}
		if seen[x] {
			return "ref(" + x + ")"
		}
		def, ok := local[x]
		if !ok {
			def, ok = p.types[x]
		}
		if !ok || def == "native" {
			return x + "?"
		}
		seen2 := map[string]bool{x: true}
		for k := range seen {
			seen2[k] = true
		}
		return p.flatten(def, local, seen2)
	case []any:
		if len(x) != 2 {
			return "?"
		}
		kind := str(x[0])
		args := x[1]
		switch kind {
		case "container":
			var parts []string
			fields, _ := args.([]any)
			for _, fa := range fields {
				f, _ := fa.(map[string]any)
				if s := p.flatten(f["type"], local, seen); s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, " ")
		case "array":
			a, _ := args.(map[string]any)
			elem := p.flatten(a["type"], local, seen)
			if elem == "i8" || elem == "u8" {
				elem = "bytes"
			}
			if ct, ok := a["countType"]; ok {
				if elem == "bytes" {
					return "bytes[" + p.flatten(ct, local, seen) + "]"
				}
				return p.flatten(ct, local, seen) + "[" + elem + "]"
			}
			return "count=" + str(a["count"]) + "[" + elem + "]"
		case "buffer":
			a, _ := args.(map[string]any)
			if ct, ok := a["countType"]; ok {
				return "bytes[" + p.flatten(ct, local, seen) + "]"
			}
			if c, ok := a["count"]; ok {
				if n, ok := c.(float64); ok {
					return fmt.Sprintf("bytes%d", int(n))
				}
				return "count=" + str(c) + "[bytes]"
			}
			return "rest_bytes"
		case "pstring":
			return "string"
		case "option":
			return "?(" + p.flatten(args, local, seen) + ")"
		case "switch":
			a, _ := args.(map[string]any)
			fields, _ := a["fields"].(map[string]any)
			// a switch over an earlier field is a guard on our side, over a value read here a dispatch
			return fmt.Sprintf("switch(%s:%d)", str(a["compareTo"]), len(fields))
		case "bitfield":
			fields, _ := args.([]any)
			bits := 0
			for _, fa := range fields {
				f, _ := fa.(map[string]any)
				n, _ := f["size"].(float64)
				bits += int(n)
			}
			switch {
			case bits <= 8:
				return "i8"
			case bits <= 16:
				return "i16be"
			case bits <= 32:
				return "i32be"
			}
			return "i64be"
		case "bitflags", "mapper":
			a, _ := args.(map[string]any)
			return p.flatten(a["type"], local, seen)
		case "registryEntryHolder":
			a, _ := args.(map[string]any)
			other, _ := a["otherwise"].(map[string]any)
			return "holder(" + p.flatten(other["type"], local, seen) + ")"
		case "topBitSetTerminatedArray":
			a, _ := args.(map[string]any)
			return "loop[" + p.flatten(a["type"], local, seen) + "]"
		case "entityMetadataLoop":
			return "loop[entity_metadata]"
		case "registryEntryHolderSet":
			return "holderset"
		}
		return kind + "?"
	}
	return "?"
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
