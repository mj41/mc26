// gen_componenthash generates level/component/hash_gen_test.go from
// component_hashes.json (GenComponentHashes): sample components in their
// network encoding with the hash a vanilla client sends for each in a
// container click. The test decodes every one and checks HashWith agrees.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func genComponentHash(jsonDir, outRoot string) error {
	var in struct {
		Registries map[string][]string `json:"registries"`
		Samples    []struct {
			Stack     string `json:"stack"`
			Component string `json:"component"`
			Wire      string `json:"wire"`
			Hash      int32  `json:"hash"`
		} `json:"samples"`
	}
	if err := readJSON(filepath.Join(jsonDir, "component_hashes.json"), &in); err != nil {
		return fmt.Errorf("genComponentHash: %w (re-run extraction; GenComponentHashes writes it)", err)
	}
	var regs []string
	for r := range in.Registries {
		// the registries a server sends to its clients; the world generation's it keeps
		if strings.Contains(r, "worldgen/") && r != "minecraft:worldgen/biome" ||
			strings.Contains(r, "test_") || r == "minecraft:villager_trade" || r == "minecraft:trade_set" || r == "minecraft:trial_spawner" {
			continue
		}
		regs = append(regs, r)
	}
	sort.Strings(regs)
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_componenthash.go", "component_hashes.json"))
	sb.WriteString(`
package component

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// hashRegistries are the data-driven registries the samples' holders were
// written with, by name in id order.
var hashRegistries = map[string][]string{
`)
	for _, r := range regs {
		fmt.Fprintf(&sb, "\t%q: {", r)
		for i, n := range in.Registries[r] {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%q", n)
		}
		sb.WriteString("},\n")
	}
	sb.WriteString(`}

// hashSamples are components of stacks a give command makes, in their network
// encoding (Typed), with the hash a vanilla client computes for each.
var hashSamples = []struct {
	stack, component, wire string
	hash                   int32
}{
`)
	for _, s := range in.Samples {
		fmt.Fprintf(&sb, "\t{%q, %q, %q, %d},\n", s.Stack, s.Component, s.Wire, s.Hash)
	}
	sb.WriteString(`}

func TestHashSamples(t *testing.T) {
	names := func(registry string, id int32) (string, bool) {
		if l, ok := hashRegistries[registry]; ok {
			if id < 0 || int(id) >= len(l) {
				return "", false
			}
			return l[id], true
		}
		return StaticNames(registry, id)
	}
	for _, s := range hashSamples {
		b, err := base64.StdEncoding.DecodeString(s.wire)
		if err != nil {
			t.Fatal(err)
		}
		var c Typed
		r := bytes.NewReader(b)
		if _, err := c.ReadFrom(r); err != nil {
			t.Errorf("%s %s: decoding: %v", s.stack, s.component, err)
			continue
		}
		if r.Len() != 0 {
			t.Errorf("%s %s: %d bytes left after decoding", s.stack, s.component, r.Len())
			continue
		}
		h, ok := HashWith(c.Value, names)
		if !ok {
			t.Errorf("%s %s: not hashed", s.stack, s.component)
		} else if h != s.hash {
			t.Errorf("%s %s: hash %d, the game's %d", s.stack, s.component, h, s.hash)
		}
	}
}
`)
	if err := writeGo(filepath.Join(outRoot, "level", "component", "hash_gen_test.go"), sb.String()); err != nil {
		return fmt.Errorf("genComponentHash: %w", err)
	}
	logf("genComponentHash: %d samples", len(in.Samples))
	return nil
}
