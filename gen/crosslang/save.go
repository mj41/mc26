package main

// A world on disk, read from the JSON alone: the region container from
// nodes.json's `region` entry and the chunk's shape from save_schema.json.
// Every chunk is decoded as generic NBT through the tags table, re-encoded and
// compared byte for byte, and its tree is walked against the format: a key
// the format does not name, a key whose tag does not fit the node it is
// described by, or a required key that is absent is a finding. That is the
// proof the save schema is a description and not a hint, the way the packet
// capture is for the packet schema.

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type regionLayout struct {
	sector  int
	entries int
	locAt   int
}

// regionFrom reads the container's layout out of nodes.json's `region` entry.
func regionFrom(nodes map[string]any) (*regionLayout, error) {
	r, _ := nodes["region"].(map[string]any)
	if r == nil {
		return nil, hole("nodes.json has no `region` entry: the container of a saved chunk is nowhere in the permitted JSON")
	}
	sec, _ := r["sector"].(map[string]any)
	loc, _ := r["locations"].(map[string]any)
	sectorBytes, ok1 := num(sec["bytes"])
	entries, ok2 := num(loc["entries"])
	at, ok3 := num(loc["at"])
	if !ok1 || !ok2 || !ok3 {
		return nil, hole("nodes.json `region`: sector.bytes, locations.entries and locations.at are needed")
	}
	return &regionLayout{sector: int(sectorBytes), entries: int(entries), locAt: int(at)}, nil
}

// chunks yields every chunk's raw NBT in a region file, with its index.
func (l *regionLayout) chunks(path string, fn func(index int, nbt []byte) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil // a region file the server created and never wrote a chunk into
	}
	if len(data) < l.sector*2 {
		return wireErr("%s: shorter than the two table sectors", filepath.Base(path))
	}
	for i := 0; i < l.entries; i++ {
		e := binary.BigEndian.Uint32(data[l.locAt+i*4:])
		off, count := int(e>>8), int(e&0xff)
		if off == 0 {
			continue
		}
		start := off * l.sector
		if start+5 > len(data) {
			return wireErr("%s: chunk %d starts past the end", filepath.Base(path), i)
		}
		length := int(binary.BigEndian.Uint32(data[start:]))
		if length < 1 || start+4+length > len(data) || length > count*l.sector {
			return wireErr("%s: chunk %d claims %d bytes in %d sectors", filepath.Base(path), i, length, count)
		}
		comp := data[start+4]
		body := data[start+5 : start+4+length]
		var r io.Reader = bytes.NewReader(body)
		switch comp {
		case 1:
			if r, err = gzip.NewReader(r); err != nil {
				return err
			}
		case 2:
			if r, err = zlib.NewReader(r); err != nil {
				return err
			}
		case 3:
		default:
			return hole("%s: chunk %d uses compression %d, which nodes.json's region entry does not describe a reader for", filepath.Base(path), i, comp)
		}
		raw, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if err := fn(i, raw); err != nil {
			return err
		}
	}
	return nil
}

// tagOf is the NBT tag a schema node is stored as, or 0 when any tag fits.
func tagOf(n node) (byte, string) {
	switch n["k"] {
	case "prim":
		switch str(n["t"]) {
		case "BOOL", "BYTE", "UNSIGNED_BYTE":
			return 1, "byte"
		case "SHORT", "UNSIGNED_SHORT":
			return 2, "short"
		case "INT", "VAR_INT", "UNSIGNED_INT", "RGB_COLOR":
			return 3, "int"
		case "LONG", "VAR_LONG", "INSTANT":
			return 4, "long"
		case "FLOAT":
			return 5, "float"
		case "DOUBLE":
			return 6, "double"
		case "STRING", "IDENTIFIER", "UUID_LENIENT":
			return 8, "string"
		case "BYTE_ARRAY":
			return 7, "byte array"
		case "INT_ARRAY", "UUID":
			return 11, "int array"
		case "LONG_ARRAY":
			return 12, "long array"
		}
	case "registry", "resourcekey", "enum", "stringenum":
		return 8, "string"
	case "holder":
		if n["direct"] != nil {
			return 0, "string or compound"
		}
		return 8, "string"
	case "struct", "map", "dispatch":
		return 10, "compound"
	case "list":
		return 9, "list"
	}
	return 0, "any"
}

// saveCheck walks a decoded NBT value against a schema node and reports what
// does not fit, one line per finding, prefixed by the path.
func (s *schema) saveCheck(n node, tag byte, v Value, path string, out *[]string) {
	n = unrecurse(n)
	want, name := tagOf(n)
	if want != 0 && tag != want {
		// a list of bytes, ints or longs may be stored as the array tag
		if !(tag == 7 || tag == 11 || tag == 12) || n["k"] != "list" {
			if !(n["k"] == "either") {
				*out = append(*out, fmt.Sprintf("%s: the schema says %s, the tag is %d", path, name, tag))
				return
			}
		}
	}
	switch n["k"] {
	case "struct":
		c, ok := v.(*nbtCompound)
		if !ok {
			return
		}
		fields, _ := n["fields"].([]any)
		known := map[string]node{}
		required := map[string]bool{}
		var collect func(fs []any)
		collect = func(fs []any) {
			for _, fa := range fs {
				f, _ := fa.(node)
				ft, _ := f["type"].(node)
				ft = unrecurse(ft)
				if f["inline"] == true && ft["k"] == "struct" {
					inner, _ := ft["fields"].([]any)
					collect(inner)
					continue
				}
				key := str(f["key"])
				known[key] = ft
				if f["optional"] != true {
					required[key] = true
				}
			}
		}
		collect(fields)
		seen := map[string]bool{}
		for _, e := range c.items {
			key := string(e.name)
			seen[key] = true
			ft, ok := known[key]
			if !ok {
				*out = append(*out, fmt.Sprintf("%s.%s: on disk, not in the schema", path, key))
				continue
			}
			s.saveCheck(ft, e.tag, e.payload, path+"."+key, out)
		}
		for key := range required {
			if !seen[key] {
				*out = append(*out, fmt.Sprintf("%s.%s: required by the schema, absent on disk", path, key))
			}
		}
	case "map":
		c, ok := v.(*nbtCompound)
		if !ok {
			return
		}
		val, _ := n["val"].(node)
		for _, e := range c.items {
			s.saveCheck(val, e.tag, e.payload, path+"["+string(e.name)+"]", out)
		}
	case "list":
		elem, _ := n["elem"].(node)
		switch l := v.(type) {
		case *nbtList:
			for i, it := range l.items {
				s.saveCheck(elem, l.elemTag, it, fmt.Sprintf("%s[%d]", path, i), out)
			}
		case *nbtArray:
			et, _ := tagOf(elem)
			if et != tag-6 && !(tag == 7 && et == 1) && !(tag == 11 && et == 3) && !(tag == 12 && et == 4) {
				*out = append(*out, fmt.Sprintf("%s: an array of tag %d holds a list whose element the schema says is tag %d", path, tag, et))
			}
		}
	case "either":
		left, _ := n["left"].(node)
		right, _ := n["right"].(node)
		// a list of eithers is stored as compounds throughout, a bare side under the empty key
		if c, ok := v.(*nbtCompound); ok && len(c.items) == 1 && len(c.items[0].name) == 0 {
			s.saveCheck(n, c.items[0].tag, c.items[0].payload, path, out)
			return
		}
		var lf, rf []string
		s.saveCheck(left, tag, v, path, &lf)
		s.saveCheck(right, tag, v, path, &rf)
		if len(lf) > 0 && len(rf) > 0 {
			*out = append(*out, fmt.Sprintf("%s: neither side of the either fits (left: %s; right: %s)", path, lf[0], rf[0]))
		}
	case "dispatch":
		// the key is checked; the cases' own fields are the registry's business
		c, ok := v.(*nbtCompound)
		if !ok {
			return
		}
		key, _ := n["key"].(node)
		field := str(key["field"])
		if field == "" {
			field = "type"
		}
		for _, e := range c.items {
			if string(e.name) == field && e.tag != 8 {
				*out = append(*out, fmt.Sprintf("%s.%s: the dispatch key is tag %d, not a string", path, field, e.tag))
			}
		}
	}
}

// runSave checks a world against the save schema: every chunk of the
// overworld's region files, every entity of its entity region files (each by
// the format of its type, which the `id` key names), and every player file.
func runSave(dataDir, nodesPath, world string, verbose bool) int {
	s, err := loadSchema(dataDir, nodesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	var nodesFile map[string]any
	if err := readJSON(nodesPath, &nodesFile); err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	layout, err := regionFrom(nodesFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	var saveSchema struct {
		Formats map[string]node `json:"formats"`
	}
	if err := readJSON(filepath.Join(dataDir, "save_schema.json"), &saveSchema); err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	format := func(name string) node {
		f, _ := saveSchema.Formats[name]
		t, _ := f["type"].(node)
		return t
	}
	chunkNode, entitiesNode, playerNode, levelNode := format("chunk"), format("entities"), format("player"), format("level")
	if chunkNode == nil || entitiesNode == nil || playerNode == nil || levelNode == nil {
		fmt.Fprintln(os.Stderr, "crosslang: save_schema.json lacks the chunk, entities, player or level format")
		return 1
	}
	overworld := filepath.Join(world, "dimensions", "minecraft", "overworld")
	regions, _ := filepath.Glob(filepath.Join(overworld, "region", "r.*.mca"))
	entityRegions, _ := filepath.Glob(filepath.Join(overworld, "entities", "r.*.mca"))
	// players/data/ since 26.x, playerdata/ before
	players, _ := filepath.Glob(filepath.Join(world, "players", "data", "*.dat"))
	if older, _ := filepath.Glob(filepath.Join(world, "playerdata", "*.dat")); len(older) > 0 {
		players = append(players, older...)
	}
	if len(regions) == 0 {
		fmt.Fprintf(os.Stderr, "crosslang: no region files under %s\n", overworld)
		return 1
	}
	sort.Strings(regions)
	sort.Strings(entityRegions)
	sort.Strings(players)
	chunks, matched, entities, playersRead := 0, 0, 0, 0
	findings := map[string]int{}
	firstAt := map[string]string{}
	note := func(out []string, where string) {
		for _, line := range out {
			// findings are counted by their text without the index, so one
			// wrong key across a world is one line
			k := stripIndexes(line)
			findings[k]++
			if _, ok := firstAt[k]; !ok {
				firstAt[k] = where
			}
		}
		if verbose && len(out) > 0 {
			fmt.Printf("%s: %s\n", where, strings.Join(out, "; "))
		}
	}
	// decodeRoot reads the named root tag a file or a chunk holds and proves the
	// generic codec round-trips it byte for byte.
	decodeRoot := func(raw []byte, where string) (byte, Value, error) {
		r := newReader(raw)
		tag, err := r.u8()
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", where, err)
		}
		if _, ok := s.nbtTags[itoa(int(tag))]; !ok {
			return 0, nil, fmt.Errorf("%s: root tag id %d is not in the tags table", where, tag)
		}
		name, err := nbtString(r)
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", where, err)
		}
		payload, err := nbtPayload(r, tag)
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", where, err)
		}
		if r.left() != 0 {
			return 0, nil, fmt.Errorf("%s: %d bytes after the root tag", where, r.left())
		}
		var w writer
		w.u8(tag)
		w.be16(uint16(len(name)))
		w.raw(name)
		if err := nbtWritePayload(&w, tag, payload); err != nil {
			return 0, nil, fmt.Errorf("%s: %w", where, err)
		}
		if bytes.Equal(w.Bytes(), raw) {
			matched++
		} else {
			findings["the re-encoding differs from the file"]++
		}
		return tag, payload, nil
	}
	// entityCheck checks one entity's compound by the format of its type.
	entityCheck := func(tag byte, v Value, where, path string) {
		var out []string
		c, ok := v.(*nbtCompound)
		if !ok {
			note([]string{path + ": an entity that is not a compound"}, where)
			return
		}
		id := ""
		for _, e := range c.items {
			if string(e.name) == "id" && e.tag == 8 {
				id = string(e.payload.([]byte))
			}
		}
		n := format("entity/" + id)
		if n == nil {
			note([]string{fmt.Sprintf("%s: entity type %q has no format in the schema", path, id)}, where)
			return
		}
		s.saveCheck(n, tag, v, path+"("+id+")", &out)
		note(out, where)
		entities++
	}
	for _, f := range regions {
		err := layout.chunks(f, func(index int, raw []byte) error {
			chunks++
			where := fmt.Sprintf("%s chunk %d", filepath.Base(f), index)
			// On disk the root tag is named (the name is empty), unlike the network
			// form the packets carry
			tag, payload, err := decodeRoot(raw, where)
			if err != nil {
				return err
			}
			var out []string
			s.saveCheck(chunkNode, tag, payload, "chunk", &out)
			note(out, where)
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "crosslang:", err)
			return 1
		}
	}
	for _, f := range entityRegions {
		err := layout.chunks(f, func(index int, raw []byte) error {
			where := fmt.Sprintf("%s chunk %d", filepath.Base(f), index)
			tag, payload, err := decodeRoot(raw, where)
			if err != nil {
				return err
			}
			var out []string
			s.saveCheck(entitiesNode, tag, payload, "entities", &out)
			note(out, where)
			// every entity in the chunk's list, by the format of its type
			if c, ok := payload.(*nbtCompound); ok {
				for _, e := range c.items {
					if string(e.name) != "Entities" {
						continue
					}
					if l, ok := e.payload.(*nbtList); ok {
						for i, it := range l.items {
							entityCheck(l.elemTag, it, where, fmt.Sprintf("entities.Entities[%d]", i))
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "crosslang:", err)
			return 1
		}
	}
	for _, f := range players {
		raw, err := gunzipFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "crosslang:", err)
			return 1
		}
		where := filepath.Base(f)
		tag, payload, err := decodeRoot(raw, where)
		if err != nil {
			fmt.Fprintln(os.Stderr, "crosslang:", err)
			return 1
		}
		var out []string
		s.saveCheck(playerNode, tag, payload, "player", &out)
		note(out, where)
		playersRead++
	}
	// level.dat: gzipped, one compound whose Data is the level
	{
		f := filepath.Join(world, "level.dat")
		raw, err := gunzipFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "crosslang:", err)
			return 1
		}
		tag, payload, err := decodeRoot(raw, "level.dat")
		if err != nil {
			fmt.Fprintln(os.Stderr, "crosslang:", err)
			return 1
		}
		var out []string
		s.saveCheck(levelNode, tag, payload, "level", &out)
		note(out, "level.dat")
	}
	fmt.Printf("read %d chunks from %d region files, %d entities from %d entity regions, %d player files, level.dat; %d roots re-encoded byte for byte\n",
		chunks, len(regions), entities, len(entityRegions), playersRead, matched)
	if len(findings) > 0 {
		keys := make([]string, 0, len(findings))
		for k := range findings {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return findings[keys[i]] > findings[keys[j]] || findings[keys[i]] == findings[keys[j]] && keys[i] < keys[j]
		})
		fmt.Printf("%d kinds of finding against save_schema.json:\n", len(keys))
		for _, k := range keys {
			fmt.Printf("  %-100s x%d  (first: %s)\n", k, findings[k], firstAt[k])
		}
		return 1
	}
	if chunks == 0 {
		fmt.Println("no chunks: nothing was checked")
		return 1
	}
	return 0
}

func gunzipFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return io.ReadAll(z)
}

// stripIndexes turns chunk.sections[3].palette[7] into chunk.sections[].palette[].
func stripIndexes(s string) string {
	var b strings.Builder
	skip := false
	for _, c := range s {
		switch {
		case c == '[':
			skip = true
			b.WriteRune(c)
		case c == ']':
			skip = false
			b.WriteRune(c)
		case !skip:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// unrecurse is the struct a recursive node wraps: the walker marks a type
// that contains itself (ItemStack, through its components) and keeps the
// shape under it.
func unrecurse(n node) node {
	for n["k"] == "recursive" {
		inner, ok := n["type"].(node)
		if !ok {
			break
		}
		n = inner
	}
	return n
}
