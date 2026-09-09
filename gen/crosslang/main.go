package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
)

type record struct {
	State string `json:"state"`
	Flow  string `json:"flow"`
	ID    int64  `json:"id"`
	Data  string `json:"data"`
}

type failure struct {
	name, what string
}

func main() {
	data := flag.String("data", "", "extracted data directory")
	prims := flag.String("prims", "", "prims.json")
	nodes := flag.String("nodes", "", "nodes.json")
	capture := flag.String("capture", "", "capture file (JSON lines: state, flow, id, data as hex)")
	verbose := flag.Bool("verbose", false, "print every packet's value, and both byte strings on a mismatch")
	flag.Parse()
	if *data == "" || *prims == "" || *nodes == "" || *capture == "" {
		fmt.Fprintln(os.Stderr, "usage: crosslang --data <dir> --prims <prims.json> --nodes <nodes.json> --capture <file.jsonl> [--verbose]")
		os.Exit(2)
	}
	os.Exit(run(*data, *prims, *nodes, *capture, *verbose))
}

func run(dataDir, primsPath, nodesPath, capturePath string, verbose bool) int {
	s, err := loadSchema(dataDir, primsPath, nodesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	f, err := os.Open(capturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	defer f.Close()

	total, decoded, matched, nbtPackets := 0, 0, 0, 0
	failures := map[failure]int{}
	first := map[failure]int{}
	fail := func(name, what string, line int) {
		k := failure{name, what}
		failures[k]++
		if _, ok := first[k]; !ok {
			first[k] = line
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		total++
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			fail("?", "bad record: "+err.Error(), lineNo)
			continue
		}
		body, err := hex.DecodeString(rec.Data)
		if err != nil {
			fail("?", "bad hex: "+err.Error(), lineNo)
			continue
		}
		name := fmt.Sprintf("%s/%s id %d", rec.State, rec.Flow, rec.ID)
		pname, entry, err := s.packetNode(rec.State, rec.Flow, rec.ID)
		if err != nil {
			fail(name, "no schema: "+err.Error(), lineNo)
			continue
		}
		name = rec.Flow + "/" + pname
		typ, _ := entry["type"].(node)

		c := newCtx(s)
		r := newReader(body)
		nbtBefore := nbtUsed
		value, err := c.decode(typ, r, nil)
		if err == nil && r.pos != len(body) {
			err = wireErr("%d of %d bytes consumed, %d left over", r.pos, len(body), len(body)-r.pos)
		}
		if err != nil {
			at := ""
			if c.where() != "" {
				at = " at " + c.where()
			}
			var h *Hole
			if errors.As(err, &h) {
				fail(name, "undescribed: "+err.Error()+at, lineNo)
			} else {
				fail(name, "decode: "+err.Error()+at, lineNo)
			}
			continue
		}
		decoded++
		if nbtUsed > nbtBefore {
			nbtPackets++
		}
		w := &writer{}
		if err := newCtx(s).encode(typ, value, w, nil); err != nil {
			fail(name, "encode: "+err.Error(), lineNo)
			continue
		}
		out := w.Bytes()
		if bytes.Equal(out, body) {
			matched++
			if verbose {
				fmt.Printf("ok   %-45s %s\n", name, summarise(value, 0))
			}
			continue
		}
		i := firstDiff(body, out)
		fail(name, fmt.Sprintf("re-encoded differently at byte %d (in %d, out %d)", i, len(body), len(out)), lineNo)
		if verbose {
			fmt.Printf("DIFF %s\n  in  %x\n  out %x\n", name, body, out)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "crosslang:", err)
		return 1
	}
	fmt.Printf("decoded %d of %d packets, %d round-tripped byte for byte\n", decoded, total, matched)
	if nbtPackets > 0 {
		fmt.Printf("(%d of those %d contained NBT, read from the tags table of the NBT definition)\n", nbtPackets, decoded)
	}
	if len(failures) > 0 {
		fmt.Println()
		keys := make([]failure, 0, len(failures))
		for k := range failures {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if failures[keys[i]] != failures[keys[j]] {
				return failures[keys[i]] > failures[keys[j]]
			}
			return keys[i].name+keys[i].what < keys[j].name+keys[j].what
		})
		for _, k := range keys {
			fmt.Printf("%-42s %-90s x%d  (first: line %d)\n", k.name, k.what, failures[k], first[k])
		}
	}
	if len(failures) == 0 && total > 0 {
		return 0
	}
	return 1
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}
