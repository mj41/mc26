package main

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
)

type node = map[string]any

// schema is the JSON bundle: the six files and the two hand-crafted ones.
type schema struct {
	packetSchema map[string]any
	packets      map[string]map[string]map[string]struct {
		ProtocolID int64 `json:"protocol_id"`
	}
	prims        map[string]any // packet_schema.json "prims": what every named primitive is on the wire
	nbtTags      map[string]any // the NBT definition's tags table
	kinds        map[string]bool
	byID         map[[2]string]map[int64]string // (state, flow) → id → packet name
	regByID      map[string]map[int64]string    // registry short name → id → entry
	regByName    map[string]map[string]int64
	serializers  map[int64]node // entity_data.json serializers by id
	idSpaceSizes map[string]int // nodes.json `packed`: a global palette's width is ceillog2 of an id space's size
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func loadSchema(dataDir, nodesPath string) (*schema, error) {
	s := &schema{}
	if err := readJSON(filepath.Join(dataDir, "packet_schema.json"), &s.packetSchema); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dataDir, "packets.json"), &s.packets); err != nil {
		return nil, err
	}
	var registries map[string]struct {
		Entries map[string]struct {
			ProtocolID int64 `json:"protocol_id"`
		} `json:"entries"`
	}
	if err := readJSON(filepath.Join(dataDir, "registries.json"), &registries); err != nil {
		return nil, err
	}
	var entityData struct {
		Serializers []node `json:"serializers"`
	}
	if err := readJSON(filepath.Join(dataDir, "entity_data.json"), &entityData); err != nil {
		return nil, err
	}
	var blocks map[string]struct {
		States []any `json:"states"`
	}
	if err := readJSON(filepath.Join(dataDir, "blocks.json"), &blocks); err != nil {
		return nil, err
	}
	var biomes any
	if err := readJSON(filepath.Join(dataDir, "biomes.json"), &biomes); err != nil {
		return nil, err
	}
	s.prims, _ = s.packetSchema["prims"].(map[string]any)
	if len(s.prims) == 0 {
		return nil, fmt.Errorf("%s has no \"prims\" section", filepath.Join(dataDir, "packet_schema.json"))
	}
	if nbt, ok := s.prims["NBT"].(node); ok {
		if def, ok := nbt["def"].(node); ok {
			s.nbtTags, _ = def["tags"].(node)
		}
	}
	var nodes struct {
		Nodes map[string]any `json:"nodes"`
	}
	if err := readJSON(nodesPath, &nodes); err != nil {
		return nil, err
	}
	s.kinds = map[string]bool{}
	for k := range nodes.Nodes {
		s.kinds[k] = true
	}

	states := 0
	for _, b := range blocks {
		states += len(b.States)
	}
	nBiomes := 0
	switch b := biomes.(type) {
	case []any:
		nBiomes = len(b)
	case map[string]any:
		nBiomes = len(b)
	}
	s.idSpaceSizes = map[string]int{"block_state": states, "worldgen/biome": nBiomes}

	s.byID = map[[2]string]map[int64]string{}
	for state, flows := range s.packets {
		for flow, names := range flows {
			tbl := map[int64]string{}
			for name, info := range names {
				tbl[info.ProtocolID] = name
			}
			s.byID[[2]string{state, flow}] = tbl
		}
	}
	s.regByID = map[string]map[int64]string{}
	s.regByName = map[string]map[string]int64{}
	for rname, r := range registries {
		short := rname
		if i := strings.Index(rname, ":"); i >= 0 {
			short = rname[i+1:]
		}
		fwd, rev := map[int64]string{}, map[string]int64{}
		for ename, e := range r.Entries {
			fwd[e.ProtocolID] = ename
			rev[ename] = e.ProtocolID
		}
		s.regByID[short] = fwd
		s.regByName[short] = rev
	}
	s.serializers = map[int64]node{}
	for _, ser := range entityData.Serializers {
		if id, ok := ser["id"].(float64); ok {
			s.serializers[int64(id)] = ser
		}
	}
	return s, nil
}

// packetNode finds a packet's schema entry by state, flow and id.
func (s *schema) packetNode(state, flow string, pid int64) (string, node, error) {
	tbl, ok := s.byID[[2]string{state, flow}]
	if !ok {
		return "", nil, hole("packets.json has no table for state %s / %s", state, flow)
	}
	name, ok := tbl[pid]
	if !ok {
		return "", nil, hole("no packet with id %d in %s/%s", pid, state, flow)
	}
	packets, _ := s.packetSchema["packets"].(map[string]any)
	entry, ok := packets[flow+"/"+name].(map[string]any)
	if !ok {
		return "", nil, hole("packet_schema.json has no %s/%s", flow, name)
	}
	return name, entry, nil
}

// resolvePrim follows the primitive definitions until a native, bits or composed node is
// reached, gathering the parameters on the way.
func (s *schema) resolvePrim(t string, params node) (node, node, error) {
	seen := map[string]bool{}
	for {
		if seen[t] {
			return nil, nil, hole("primitive %s is defined in terms of itself", t)
		}
		seen[t] = true
		d, ok := s.prims[t].(node)
		if !ok {
			return nil, nil, hole("primitive %s is not defined in the schema", t)
		}
		def, _ := d["def"].(node)
		if def["k"] == "prim" {
			p := node{}
			for k, v := range params {
				p[k] = v
			}
			for k, v := range def {
				if k != "k" && k != "t" {
					p[k] = v
				}
			}
			params = p
			t = str(def["t"])
			continue
		}
		return def, params, nil
	}
}

// ceillog2 as Mojang's Mth.ceillog2: ceillog2(1) = 0, ceillog2(64) = 6.
func ceillog2(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n - 1))
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

func num(v any) (int64, bool) {
	f, ok := v.(float64)
	return int64(f), ok
}
