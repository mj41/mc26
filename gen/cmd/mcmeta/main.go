// mcmeta previews what a Minecraft version changes using misode/mcmeta, the
// version-controlled archive of the game's data-generator reports — no Java, no
// server jar, works for snapshots the day they ship.
//
//	go run ./gen/cmd/mcmeta versions [N]        # newest N versions with data/protocol versions
//	go run ./gen/cmd/mcmeta diff 26.2 26.3-pre-2  # registries, block states, item components
//	go run ./gen/cmd/mcmeta check 26.2            # temp/data/26.2/registries.json vs mcmeta
//
// Files are fetched from raw.githubusercontent.com/misode/mcmeta/<version>-summary/
// and cached under temp/mcmeta/<version>/. Use -v to list every entry instead of
// the first few.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mj41/mc26/gen/internal/paths"
)

const rawBase = "https://raw.githubusercontent.com/misode/mcmeta/"

var verbose bool

func main() {
	var args []string
	for _, s := range os.Args[1:] {
		if s == "-v" {
			verbose = true
		} else {
			args = append(args, s)
		}
	}
	if len(args) == 0 {
		usage()
	}
	var err error
	switch args[0] {
	case "versions":
		n := 10
		if len(args) > 1 {
			fmt.Sscanf(args[1], "%d", &n)
		}
		err = versions(n)
	case "diff":
		if len(args) != 3 {
			usage()
		}
		err = diff(args[1], args[2])
	case "check":
		if len(args) != 2 {
			usage()
		}
		err = check(args[1])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcmeta:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mcmeta [-v] versions [N] | diff <verA> <verB> | check <ver>")
	os.Exit(2)
}

// ---- data access -------------------------------------------------------------

type versionInfo struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Stable          bool   `json:"stable"`
	DataVersion     int    `json:"data_version"`
	ProtocolVersion int    `json:"protocol_version"`
	ReleaseTime     string `json:"release_time"`
}

// fetch returns the summary file `name` of `version` ("summary" = branch head),
// cached under temp/mcmeta/<version>/<name>.json.
func fetch(version, name string) ([]byte, error) {
	cache := filepath.Join(paths.Temp(), "mcmeta", version, name+".json")
	if version != "summary" { // released tags never change; the branch head does
		if data, err := os.ReadFile(cache); err == nil {
			return data, nil
		}
	}
	ref := version + "-summary"
	if version == "summary" {
		ref = "summary"
	}
	url := rawBase + ref + "/" + name + "/data.min.json"
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d (unknown version? see `mcmeta versions`)", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
		_ = os.WriteFile(cache, data, 0o644)
	}
	return data, nil
}

func fetchJSON(version, name string, v any) error {
	data, err := fetch(version, name)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// ---- versions ----------------------------------------------------------------

func versions(n int) error {
	var list []versionInfo
	if err := fetchJSON("summary", "versions", &list); err != nil {
		return err
	}
	fmt.Printf("%-22s %-9s %-7s %-12s %s\n", "id", "type", "data", "protocol", "released")
	for i, v := range list {
		if i >= n {
			break
		}
		proto := fmt.Sprint(v.ProtocolVersion)
		if v.ProtocolVersion >= 1<<30 {
			proto = fmt.Sprintf("snap %d", v.ProtocolVersion-(1<<30))
		}
		fmt.Printf("%-22s %-9s %-7d %-12s %s\n", v.ID, v.Type, v.DataVersion, proto, v.ReleaseTime[:10])
	}
	return nil
}

func versionInfoOf(id string) *versionInfo {
	var list []versionInfo
	if err := fetchJSON("summary", "versions", &list); err != nil {
		return nil
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// ---- diff --------------------------------------------------------------------

func diff(a, b string) error {
	for _, v := range []string{a, b} {
		if vi := versionInfoOf(v); vi != nil {
			fmt.Printf("%s: %s, data version %d, protocol %d\n", v, vi.Type, vi.DataVersion, vi.ProtocolVersion)
		}
	}
	fmt.Println()
	if err := diffRegistries(a, b); err != nil {
		return err
	}
	if err := diffBlocks(a, b); err != nil {
		return err
	}
	return diffItemComponents(a, b)
}

func diffRegistries(a, b string) error {
	var ra, rb map[string][]string
	if err := fetchJSON(a, "registries", &ra); err != nil {
		return err
	}
	if err := fetchJSON(b, "registries", &rb); err != nil {
		return err
	}
	fmt.Printf("## registries: %d -> %d\n", len(ra), len(rb))
	changed := 0
	for _, reg := range union(keys(ra), keys(rb)) {
		ea, aok := ra[reg]
		eb, bok := rb[reg]
		switch {
		case !aok:
			changed++
			fmt.Printf("  + registry %s (%d entries)\n", reg, len(eb))
		case !bok:
			changed++
			fmt.Printf("  - registry %s (%d entries)\n", reg, len(ea))
		default:
			add, rem := setDiff(ea, eb)
			if len(add)+len(rem) == 0 {
				continue
			}
			changed++
			fmt.Printf("  %-32s %5d -> %-5d  +%d -%d\n", reg, len(ea), len(eb), len(add), len(rem))
			listSome("      + ", add)
			listSome("      - ", rem)
		}
	}
	if changed == 0 {
		fmt.Println("  (no changes)")
	}
	fmt.Println()
	return nil
}

// blocks/data.json: name -> [ {property: [values]}, {property: default} ]
func diffBlocks(a, b string) error {
	var ba, bb map[string][]map[string]any
	if err := fetchJSON(a, "blocks", &ba); err != nil {
		return err
	}
	if err := fetchJSON(b, "blocks", &bb); err != nil {
		return err
	}
	var changed []string
	for _, name := range union(keys(ba), keys(bb)) {
		pa, aok := ba[name]
		pb, bok := bb[name]
		if !aok || !bok || len(pa) == 0 || len(pb) == 0 {
			continue // additions/removals are in the block registry above
		}
		if canon(pa[0]) != canon(pb[0]) {
			changed = append(changed, fmt.Sprintf("%s: %s -> %s", name, props(pa[0]), props(pb[0])))
		}
	}
	fmt.Printf("## block state properties: %d blocks changed\n", len(changed))
	listSome("  ~ ", changed)
	fmt.Println()
	return nil
}

func props(m map[string]any) string {
	var parts []string
	for _, k := range keys(m) {
		vals, _ := m[k].([]any)
		var vs []string
		for _, v := range vals {
			vs = append(vs, fmt.Sprint(v))
		}
		parts = append(parts, k+"="+strings.Join(vs, "|"))
	}
	if len(parts) == 0 {
		return "{}"
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// item_components/data.json: item -> {component: value}
func diffItemComponents(a, b string) error {
	var ia, ib map[string]map[string]any
	if err := fetchJSON(a, "item_components", &ia); err != nil {
		return err
	}
	if err := fetchJSON(b, "item_components", &ib); err != nil {
		return err
	}
	var changed []string
	compAdded, compRemoved := map[string]int{}, map[string]int{}
	for _, item := range union(keys(ia), keys(ib)) {
		ca, aok := ia[item]
		cb, bok := ib[item]
		if !aok || !bok {
			continue
		}
		var deltas []string
		for _, c := range union(keys(ca), keys(cb)) {
			va, ina := ca[c]
			vb, inb := cb[c]
			switch {
			case !ina:
				compAdded[c]++
				deltas = append(deltas, "+"+c)
			case !inb:
				compRemoved[c]++
				deltas = append(deltas, "-"+c)
			case canon(va) != canon(vb):
				deltas = append(deltas, "~"+c)
			}
		}
		if len(deltas) > 0 {
			changed = append(changed, item+": "+strings.Join(deltas, " "))
		}
	}
	fmt.Printf("## item default components: %d items changed\n", len(changed))
	for _, c := range keys(compAdded) {
		fmt.Printf("  + %s on %d items\n", c, compAdded[c])
	}
	for _, c := range keys(compRemoved) {
		fmt.Printf("  - %s on %d items\n", c, compRemoved[c])
	}
	listSome("  ~ ", changed)
	fmt.Println()
	return nil
}

// ---- check -------------------------------------------------------------------

// Mojang's registries.json report: "minecraft:block" -> {"entries": {"minecraft:stone": {...}}}
type mojangRegistries map[string]struct {
	Entries map[string]json.RawMessage `json:"entries"`
}

func check(ver string) error {
	path := filepath.Join(paths.Data(ver), "registries.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%v (extract first: go run ./gen/cmd/mc26 extract --version %s)", err, ver)
	}
	var ours mojangRegistries
	if err := json.Unmarshal(data, &ours); err != nil {
		return err
	}
	var theirs map[string][]string
	if err := fetchJSON(ver, "registries", &theirs); err != nil {
		return err
	}
	mismatched := 0
	compared := 0
	for _, reg := range keys(theirs) {
		o, ok := ours["minecraft:"+reg]
		if !ok {
			continue
		}
		compared++
		var names []string
		for n := range o.Entries {
			names = append(names, strings.TrimPrefix(n, "minecraft:"))
		}
		add, rem := setDiff(theirs[reg], names)
		if len(add)+len(rem) == 0 {
			continue
		}
		mismatched++
		fmt.Printf("  %s: ours has +%d -%d vs mcmeta\n", reg, len(add), len(rem))
		listSome("      only ours:   ", add)
		listSome("      only mcmeta: ", rem)
	}
	fmt.Printf("mcmeta check %s: %d registries compared, %d differ\n", ver, compared, mismatched)
	if mismatched > 0 {
		os.Exit(1)
	}
	return nil
}

// ---- helpers -----------------------------------------------------------------

func setDiff(a, b []string) (added, removed []string) {
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, s := range a {
		sa[s] = true
	}
	for _, s := range b {
		sb[s] = true
	}
	for _, s := range b {
		if !sa[s] {
			added = append(added, s)
		}
	}
	for _, s := range a {
		if !sb[s] {
			removed = append(removed, s)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

func listSome(prefix string, items []string) {
	const max = 12
	for i, s := range items {
		if !verbose && i == max {
			fmt.Printf("%s… %d more (-v lists all)\n", prefix, len(items)-max)
			return
		}
		fmt.Println(prefix + s)
	}
}

func canon(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func union(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		set[s] = true
	}
	return keys(set)
}
