package docgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// data is the version's JSON, loaded once per rendering.
type data struct {
	o      Options
	meta   map[string]any
	ver    map[string]any
	packet map[string]any // packet_schema.json
	ids    map[string]map[string]map[string]struct {
		ProtocolID int `json:"protocol_id"`
	}
	nbt   map[string]any // nbt_schema.json
	prims map[string]any // prims.json "prims"
	nodes map[string]any // nodes.json
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

func load(o Options) (*data, error) {
	d := &data{o: o}
	if err := readJSON(filepath.Join(o.DataDir, "_meta.json"), &d.meta); err != nil {
		// a data checkout has it; a fresh extraction directory too
		d.meta = map[string]any{}
	}
	if err := readJSON(filepath.Join(o.DataDir, "version.json"), &d.ver); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(o.DataDir, "packet_schema.json"), &d.packet); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(o.DataDir, "packets.json"), &d.ids); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(o.DataDir, "nbt_schema.json"), &d.nbt); err != nil {
		return nil, err
	}
	var prims struct {
		Prims map[string]any `json:"prims"`
	}
	if err := readJSON(filepath.Join(o.HandDir, "prims.json"), &prims); err != nil {
		return nil, err
	}
	d.prims = prims.Prims
	if err := readJSON(filepath.Join(o.HandDir, "nodes.json"), &d.nodes); err != nil {
		return nil, err
	}
	return d, nil
}

// values are the inline facts a template may ask for.
var values = map[string]func(d *data) string{
	"id":           func(d *data) string { return str(d.ver["id"]) },
	"name":         func(d *data) string { return str(d.ver["name"]) },
	"protocol":     func(d *data) string { return str(d.ver["protocol_version"]) },
	"data-version": func(d *data) string { return str(d.ver["world_version"]) },
	"java":         func(d *data) string { return str(d.ver["java_version"]) },
	"extracted":    func(d *data) string { return str(d.meta["extracted_at"]) },
	"jar-sha1":     func(d *data) string { return str(d.meta["server_jar_sha1"]) },
	"extractor":    func(d *data) string { return str(d.meta["extractor_commit"]) },
}

func (d *data) value(name string) (string, error) {
	f, ok := values[strings.TrimSpace(name)]
	if !ok {
		return "", fmt.Errorf("unknown value %q", name)
	}
	return f(d), nil
}

// includes are the generated tables and trees.
var includes = map[string]func(d *data, args []string) (string, error){
	"version-facts": (*data).versionFacts,
	"frame":         (*data).frame,
	"prims":         (*data).primsTable,
	"node-kinds":    (*data).nodeKinds,
	"packets":       (*data).packets,
	"components":    (*data).components,
	"registries":    (*data).registries,
	"types":         (*data).types,
}

func (d *data) include(arg string) (string, error) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return "", fmt.Errorf("include needs a name")
	}
	f, ok := includes[fields[0]]
	if !ok {
		return "", fmt.Errorf("unknown include %q", fields[0])
	}
	return f(d, fields[1:])
}

func (d *data) versionFacts(args []string) (string, error) {
	var sb strings.Builder
	sb.WriteString("| fact | value |\n|---|---|\n")
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&sb, "| %s | %s |\n", k, v)
		}
	}
	row("Minecraft", fmt.Sprintf("%s (%s)", str(d.ver["id"]), str(d.ver["name"])))
	row("protocol version", str(d.ver["protocol_version"]))
	row("data version", str(d.ver["world_version"]))
	row("Java", str(d.ver["java_version"]))
	row("server jar", str(d.meta["server_jar_sha1"]))
	row("extracted", str(d.meta["extracted_at"]))
	row("extractor", str(d.meta["extractor_commit"]))
	return sb.String(), nil
}

// frame renders nodes.json's frame.data: the length, the body, compression,
// encryption and the state transitions.
func (d *data) frame(args []string) (string, error) {
	fr, _ := d.nodes["frame"].(map[string]any)
	fd, _ := fr["data"].(map[string]any)
	if fd == nil {
		return "", fmt.Errorf("nodes.json has no frame.data")
	}
	var sb strings.Builder
	length, _ := fd["length"].(map[string]any)
	body, _ := fd["body"].(map[string]any)
	fmt.Fprintf(&sb, "| part | on the wire | notes |\n|---|---|---|\n")
	fmt.Fprintf(&sb, "| length | %s, at most %s bytes, %s..%s | %s |\n", nodeSummary(m(length["type"])), str(length["maxBytes"]), str(length["min"]), str(length["max"]), str(length["counts"]))
	id, _ := body["id"].(map[string]any)
	fmt.Fprintf(&sb, "| packet id | %s | %s |\n", nodeSummary(id), str(id["note"]))
	fmt.Fprintf(&sb, "| fields | | %s |\n", str(body["fields"]))
	comp, _ := fd["compression"].(map[string]any)
	if comp != nil {
		en, _ := comp["enabledBy"].(map[string]any)
		dl, _ := comp["dataLength"].(map[string]any)
		sb.WriteString("\n**Compression**\n\n| | |\n|---|---|\n")
		fmt.Fprintf(&sb, "| enabled by | %s/%s `%s`, field `%s`: %s |\n", str(en["state"]), str(en["flow"]), str(en["packet"]), str(en["field"]), str(en["on"]))
		fmt.Fprintf(&sb, "| data length | %s; 0: %s; else: %s |\n", nodeSummary(m(dl["type"])), str(dl["zero"]), str(dl["else"]))
		fmt.Fprintf(&sb, "| compressed when | %s |\n", str(comp["compressWhen"]))
		fmt.Fprintf(&sb, "| largest inflated body | %s bytes |\n", str(comp["maxInflated"]))
		rc, _ := comp["readerChecks"].(map[string]any)
		for _, side := range []string{"server", "client"} {
			checks, _ := rc[side].([]any)
			var cs []string
			for _, c := range checks {
				cs = append(cs, str(c))
			}
			if len(cs) == 0 {
				cs = []string{"none"}
			}
			fmt.Fprintf(&sb, "| the %s checks | %s |\n", side, strings.Join(cs, "; "))
		}
	}
	enc, _ := fd["encryption"].(map[string]any)
	if enc != nil {
		rq, _ := enc["requestedBy"].(map[string]any)
		en, _ := enc["enabledBy"].(map[string]any)
		sb.WriteString("\n**Encryption**\n\n| | |\n|---|---|\n")
		fmt.Fprintf(&sb, "| requested by | %s/%s `%s` |\n", str(rq["state"]), str(rq["flow"]), str(rq["packet"]))
		fmt.Fprintf(&sb, "| enabled by | %s/%s `%s`: %s |\n", str(en["state"]), str(en["flow"]), str(en["packet"]), str(en["then"]))
		fmt.Fprintf(&sb, "| cipher | %s |\n", str(enc["cipher"]))
		fmt.Fprintf(&sb, "| scope | %s |\n", str(enc["scope"]))
	}
	st, _ := fd["states"].(map[string]any)
	if st != nil {
		sb.WriteString("\n**States**\n\n")
		fmt.Fprintf(&sb, "The connection starts in `%s`.\n\n| after | the state is |\n|---|---|\n", str(st["initial"]))
		trs, _ := st["transitions"].([]any)
		for _, ta := range trs {
			t, _ := ta.(map[string]any)
			after, _ := t["after"].(map[string]any)
			where := fmt.Sprintf("%s/%s `%s`", str(after["state"]), str(after["flow"]), str(after["packet"]))
			if to, ok := t["to"].(string); ok {
				fmt.Fprintf(&sb, "| %s | `%s` |\n", where, to)
				continue
			}
			vals, _ := t["values"].(map[string]any)
			var parts []string
			for _, k := range sortedKeys(vals) {
				parts = append(parts, fmt.Sprintf("%s → `%s`", k, str(vals[k])))
			}
			fmt.Fprintf(&sb, "| %s, by its field `%s` | %s (%s) |\n", where, str(t["byField"]), strings.Join(parts, ", "), str(t["note"]))
		}
		if note := str(st["note"]); note != "" {
			sb.WriteString("\n" + note + "\n")
		}
	}
	return sb.String(), nil
}

// primsTable lists every primitive of prims.json with what it is.
func (d *data) primsTable(args []string) (string, error) {
	used := d.primUses()
	var sb strings.Builder
	sb.WriteString("| primitive | definition | read from | uses |\n|---|---|---|---:|\n")
	for _, name := range sortedKeys(d.prims) {
		p, _ := d.prims[name].(map[string]any)
		def, _ := p["def"].(map[string]any)
		fmt.Fprintf(&sb, "| `%s` | %s | `%s` | %d |\n", name, escape(defSummary(def)), str(p["java"]), used[name])
	}
	sb.WriteString("\n")
	for _, name := range sortedKeys(d.prims) {
		p, _ := d.prims[name].(map[string]any)
		if note := str(p["note"]); note != "" {
			fmt.Fprintf(&sb, "- `%s` — %s\n", name, note)
		}
	}
	return sb.String(), nil
}

// primUses counts the primitives the version's packets and components name.
func (d *data) primUses() map[string]int {
	out := map[string]int{}
	var walk func(n any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if v["k"] == "prim" {
				out[str(v["t"])]++
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(d.packet["packets"])
	walk(d.packet["components"])
	return out
}

// defSummary is one line for a primitive's definition.
func defSummary(def map[string]any) string {
	switch def["k"] {
	case "native":
		s := "native `" + str(def["of"]) + "`"
		if n := str(def["note"]); n != "" {
			s += ": " + n
		}
		return s
	case "prim":
		s := "`" + str(def["t"]) + "`"
		var extra []string
		for _, k := range sortedKeys(def) {
			if k != "k" && k != "t" && k != "note" {
				extra = append(extra, fmt.Sprintf("%s=%v", k, def[k]))
			}
		}
		if len(extra) > 0 {
			s += " (" + strings.Join(extra, ", ") + ")"
		}
		return s
	case "bits":
		fs, _ := def["fields"].([]any)
		var parts []string
		for _, fa := range fs {
			f, _ := fa.(map[string]any)
			parts = append(parts, fmt.Sprintf("%s@%v:%v", str(f["name"]), f["offset"], f["width"]))
		}
		return fmt.Sprintf("bits of `%s`: %s", str(def["of"]), strings.Join(parts, ", "))
	}
	return nodeSummary(def)
}

// nodeKinds lists the kinds the version's schemas use, with counts.
func (d *data) nodeKinds(args []string) (string, error) {
	counts := map[string]int{}
	var walk func(n any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if k, ok := v["k"].(string); ok {
				counts[k]++
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(d.packet["packets"])
	walk(d.packet["components"])
	kinds, _ := d.nodes["nodes"].(map[string]any)
	var sb strings.Builder
	sb.WriteString("| kind | in this version's packets and components | what it is |\n|---|---:|---|\n")
	for _, k := range sortedKeys(kinds) {
		e, _ := kinds[k].(map[string]any)
		fmt.Fprintf(&sb, "| [`%s`](#%s) | %d | %s |\n", k, k, counts[k], escape(str(e["summary"])))
	}
	return sb.String(), nil
}

// packets renders every state and flow: a table of ids and names, then each
// packet's field tree.
func (d *data) packets(args []string) (string, error) {
	entries, _ := d.packet["packets"].(map[string]any)
	var sb strings.Builder
	states := sortedKeys(d.ids)
	sort.Slice(states, func(i, j int) bool { return stateOrder(states[i]) < stateOrder(states[j]) })
	for _, state := range states {
		for _, flow := range []string{"clientbound", "serverbound"} {
			tbl, ok := d.ids[state][flow]
			if !ok {
				continue
			}
			names := make([]string, 0, len(tbl))
			for n := range tbl {
				names = append(names, n)
			}
			sort.Slice(names, func(i, j int) bool { return tbl[names[i]].ProtocolID < tbl[names[j]].ProtocolID })
			fmt.Fprintf(&sb, "## %s %s\n\n| id | packet | Java class |\n|---:|---|---|\n", state, flow)
			for _, n := range names {
				e, _ := entries[flow+"/"+n].(map[string]any)
				fmt.Fprintf(&sb, "| %d | [`%s`](#%s) | `%s` |\n", tbl[n].ProtocolID, n, anchor("pkt-"+flow+"-"+n), str(e["class"]))
			}
			sb.WriteString("\n")
			for _, n := range names {
				e, _ := entries[flow+"/"+n].(map[string]any)
				fmt.Fprintf(&sb, "<a id=\"%s\"></a>\n### %s (%s, id %d)\n\n", anchor("pkt-"+flow+"-"+n), n, flow, tbl[n].ProtocolID)
				if e == nil {
					sb.WriteString("Not in packet_schema.json.\n\n")
					continue
				}
				if c := str(e["coverage"]); c != "full" {
					fmt.Fprintf(&sb, "Coverage: %s.\n\n", c)
				}
				t, _ := e["type"].(map[string]any)
				sb.WriteString(packetTree(t))
				sb.WriteString("\n")
			}
		}
	}
	return sb.String(), nil
}

func stateOrder(s string) int {
	for i, x := range []string{"handshake", "status", "login", "configuration", "play"} {
		if x == s {
			return i
		}
	}
	return 9
}

// components renders every data component's schema.
func (d *data) components(args []string) (string, error) {
	entries, _ := d.packet["components"].(map[string]any)
	var sb strings.Builder
	sb.WriteString("| component | wire form |\n|---|---|\n")
	keys := sortedKeys(entries)
	for _, k := range keys {
		e, _ := entries[k].(map[string]any)
		t, _ := e["type"].(map[string]any)
		fmt.Fprintf(&sb, "| [`%s`](#%s) | %s |\n", k, anchor("cmp-"+k), escape(nodeSummary(t)))
	}
	sb.WriteString("\n")
	for _, k := range keys {
		e, _ := entries[k].(map[string]any)
		t, _ := e["type"].(map[string]any)
		fmt.Fprintf(&sb, "<a id=\"%s\"></a>\n### %s\n\n", anchor("cmp-"+k), k)
		if c := str(e["coverage"]); c != "full" {
			fmt.Fprintf(&sb, "Coverage: %s.\n\n", c)
		}
		sb.WriteString(packetTree(t))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// registries renders the NBT shape of every synchronized registry's elements.
func (d *data) registries(args []string) (string, error) {
	return d.nbtSection("registries")
}

// types renders the NBT shape of the shared types (chat, save records).
func (d *data) types(args []string) (string, error) {
	return d.nbtSection("types")
}

// seenRecursive is the recursive codecs already spelled out in the entry being
// rendered; a second occurrence is one line, since the schema embeds the whole
// codec again wherever a field is not a ref.
var seenRecursive map[string]bool

func (d *data) nbtSection(section string) (string, error) {
	entries, _ := d.nbt[section].(map[string]any)
	if entries == nil {
		return "", fmt.Errorf("nbt_schema.json has no %s", section)
	}
	var sb strings.Builder
	keys := sortedKeys(entries)
	sb.WriteString("| entry | Java | shape |\n|---|---|---|\n")
	for _, k := range keys {
		e, _ := entries[k].(map[string]any)
		t, _ := e["type"].(map[string]any)
		fmt.Fprintf(&sb, "| [`%s`](#%s) | `%s` | %s |\n", k, anchor(section+"-"+k), str(e["class"]), escape(nodeSummary(t)))
	}
	sb.WriteString("\n")
	for _, k := range keys {
		e, _ := entries[k].(map[string]any)
		t, _ := e["type"].(map[string]any)
		fmt.Fprintf(&sb, "<a id=\"%s\"></a>\n### %s\n\n`%s`", anchor(section+"-"+k), k, str(e["class"]))
		if f := str(e["field"]); f != "" {
			fmt.Fprintf(&sb, ".%s", f)
		}
		sb.WriteString("\n\n")
		if c := str(e["coverage"]); c != "full" {
			fmt.Fprintf(&sb, "Coverage: %s.\n\n", c)
		}
		seenRecursive = map[string]bool{}
		sb.WriteString(nbtTree(t, 0))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// ---- trees --------------------------------------------------------------------------

// tree renders the children of a packet node as a nested list at indent
// depth: the fields of a struct, the cases of a dispatch, the element of a
// list, the sides of an either. The caller has written the node's own line.
func tree(n map[string]any, depth int) string {
	ind := strings.Repeat("  ", depth)
	var sb strings.Builder
	item := func(label string, node map[string]any, suffix string) {
		if isLeafNode(node) {
			fmt.Fprintf(&sb, "%s- %s%s%s\n", ind, label, escape(nodeSummary(node)), suffix)
			return
		}
		fmt.Fprintf(&sb, "%s- %s%s%s\n", ind, label, escape(nodeHead(node)), suffix)
		sb.WriteString(tree(node, depth+1))
	}
	switch n["k"] {
	case "struct":
		fields, _ := n["fields"].([]any)
		for _, fa := range fields {
			f, _ := fa.(map[string]any)
			ft, _ := f["type"].(map[string]any)
			item("`"+str(f["name"])+"`: ", ft, whenText(f["when"]))
		}
	case "dispatch":
		cases, _ := n["cases"].([]any)
		if cases == nil {
			fmt.Fprintf(&sb, "%s- (cases not described)\n", ind)
		}
		for _, ca := range cases {
			c, _ := ca.(map[string]any)
			ct, _ := c["type"].(map[string]any)
			id := str(c["id"])
			if num, ok := c["num"]; ok {
				id = fmt.Sprintf("%s (%s)", id, str(num))
			}
			item("`"+id+"`: ", ct, "")
		}
	case "list", "optional", "lenprefixed", "rest", "counted", "whilelist":
		e, _ := n["elem"].(map[string]any)
		if !isLeafNode(e) {
			item("", e, "")
		}
	case "map":
		item("key: ", m(n["key"]), "")
		item("value: ", m(n["val"]), "")
	case "either":
		item("left: ", m(n["left"]), "")
		item("right: ", m(n["right"]), "")
	case "holder":
		if direct, ok := n["direct"].(map[string]any); ok && !isLeafNode(direct) {
			item("inline: ", direct, "")
		}
	}
	return sb.String()
}

// packetTree renders a packet's or component's whole type.
func packetTree(t map[string]any) string {
	if t == nil {
		return "- (no type)\n"
	}
	if t["k"] == "struct" {
		fields, _ := t["fields"].([]any)
		if len(fields) == 0 {
			return "- (no fields)\n"
		}
		return tree(t, 0)
	}
	if isLeafNode(t) {
		return "- " + escape(nodeSummary(t)) + "\n"
	}
	return "- " + escape(nodeHead(t)) + "\n" + tree(t, 1)
}

func structNotes(n map[string]any) string {
	var notes []string
	if n["conditional"] == true {
		notes = append(notes, "conditional")
	}
	if g := str(n["guard"]); g != "" {
		notes = append(notes, "guarded by an EnumSet of "+g[strings.LastIndex(g, "/")+1:])
	}
	if len(notes) == 0 {
		return ""
	}
	return " (" + strings.Join(notes, ", ") + ")"
}

// whenText says when a field is present.
func whenText(w any) string {
	switch v := w.(type) {
	case string:
		return " (when the set has `" + v + "`)"
	case []any:
		var groups []string
		for _, ga := range v {
			g, _ := ga.([]any)
			var alts []string
			for _, ta := range g {
				alts = append(alts, testText(ta.(map[string]any)))
			}
			groups = append(groups, strings.Join(alts, " or "))
		}
		return " (when " + strings.Join(groups, ", and ") + ")"
	}
	return ""
}

func testText(t map[string]any) string {
	f := "`" + str(t["field"]) + "`"
	not := t["not"] == true
	switch str(t["test"]) {
	case "true":
		if not {
			return f + " is false"
		}
		return f + " is true"
	case "bit":
		if not {
			return fmt.Sprintf("%s & %v == 0", f, t["value"])
		}
		return fmt.Sprintf("%s & %v != 0", f, t["value"])
	case "maskeq":
		op := "=="
		if not {
			op = "!="
		}
		return fmt.Sprintf("%s & %v %s %v", f, t["mask"], op, t["value"])
	case "eq":
		op := "=="
		if not {
			op = "!="
		}
		return fmt.Sprintf("%s %s %v", f, op, t["value"])
	case "cmp":
		return fmt.Sprintf("%s %v %v", f, t["op"], t["value"])
	case "in":
		vals, _ := t["value"].([]any)
		if len(vals) > 8 {
			return fmt.Sprintf("%s in a set of %d values", f, len(vals))
		}
		return fmt.Sprintf("%s in %v", f, vals)
	}
	return f + " " + str(t["test"])
}

func isLeafNode(n map[string]any) bool {
	switch n["k"] {
	case "struct", "dispatch":
		return false
	case "map":
		return isLeafNode(m(n["key"])) && isLeafNode(m(n["val"]))
	case "either":
		return isLeafNode(m(n["left"])) && isLeafNode(m(n["right"]))
	case "list", "optional", "lenprefixed", "rest", "counted", "whilelist":
		e, _ := n["elem"].(map[string]any)
		return isLeafNode(e)
	case "holder":
		direct, ok := n["direct"].(map[string]any)
		return !ok || isLeafNode(direct)
	}
	return true
}

// nodeHead is the first line of a node that has children.
func nodeHead(n map[string]any) string {
	switch n["k"] {
	case "struct":
		return "struct `" + str(n["name"]) + "`" + structNotes(n)
	case "dispatch":
		key, _ := n["key"].(map[string]any)
		return "dispatch `" + str(n["name"]) + "` on " + nodeSummary(key)
	case "list":
		return "list of" + maxText(n)
	case "optional":
		return "optional"
	case "lenprefixed":
		if l := str(n["length"]); l != "" {
			return "in the `" + l + "` bytes"
		}
		return "length-prefixed"
	case "rest":
		return "until the end, each"
	case "counted":
		return "`" + str(n["count"]) + "` times"
	case "whilelist":
		w, _ := n["while"].(map[string]any)
		return "entries until " + testText(w)
	case "map":
		return "map" + maxText(n)
	case "either":
		return "either (a boolean, then one side)"
	case "holder":
		return "id in " + str(n["registry"]) + ", or 0 and the element inline"
	}
	return nodeSummary(n)
}

func maxText(n map[string]any) string {
	if m, ok := n["max"]; ok {
		return fmt.Sprintf(" (at most %v)", m)
	}
	return ""
}

// nodeSummary is one line for a node with no children worth a line of their own.
func nodeSummary(n map[string]any) string {
	if n == nil {
		return "?"
	}
	switch n["k"] {
	case "prim":
		s := "`" + str(n["t"]) + "`"
		var extra []string
		for _, k := range sortedKeys(n) {
			if k != "k" && k != "t" && k != "note" {
				extra = append(extra, fmt.Sprintf("%s=%s", k, str(n[k])))
			}
		}
		if len(extra) > 0 {
			s += " (" + strings.Join(extra, ", ") + ")"
		}
		return s
	case "string":
		if m, ok := n["max"]; ok {
			return fmt.Sprintf("string (at most %v characters)", m)
		}
		return "string"
	case "unit":
		return "nothing"
	case "nbt":
		return "an NBT tag"
	case "text":
		return "a text component"
	case "registry":
		return "id in " + str(n["registry"])
	case "resourcekey":
		return "resource key in " + str(n["registry"])
	case "holderset":
		return "set of " + str(n["registry"]) + " (a tag or ids)"
	case "holder":
		if d, ok := n["direct"].(map[string]any); ok {
			return "id in " + str(n["registry"]) + " or inline " + nodeSummary(d)
		}
		return "id in " + str(n["registry"])
	case "enum":
		vals, _ := n["values"].([]any)
		var names []string
		for _, v := range vals {
			names = append(names, str(v))
		}
		s := fmt.Sprintf("enum `%s` (var int", str(n["name"]))
		if ids, ok := n["ids"].([]any); ok {
			var is []string
			for _, v := range ids {
				is = append(is, str(v))
			}
			s += ", ids " + strings.Join(is, "/")
		} else if n["idsUnknown"] == true {
			s += ", ids unknown"
		} else {
			s += ", ordinal"
		}
		if len(names) > 0 {
			s += ": " + strings.Join(names, ", ")
		}
		return s + ")"
	case "stringenum":
		names, _ := n["names"].([]any)
		var ns []string
		for _, v := range names {
			ns = append(ns, str(v))
		}
		return fmt.Sprintf("string enum `%s` (%s)", str(n["name"]), strings.Join(ns, ", "))
	case "enumset":
		vals, _ := n["values"].([]any)
		return fmt.Sprintf("enum set of `%s` (%d bits)", str(n["name"]), len(vals))
	case "bits":
		fs, _ := n["fields"].([]any)
		var parts []string
		for _, fa := range fs {
			f, _ := fa.(map[string]any)
			parts = append(parts, fmt.Sprintf("%s@%v:%v", str(f["name"]), f["offset"], f["width"]))
		}
		return fmt.Sprintf("`%s` as bits (%s)", str(n["of"]), strings.Join(parts, ", "))
	case "ref":
		return "a `" + str(n["name"]) + "` again"
	case "opaque":
		return "opaque (" + str(n["java"]) + ")"
	case "struct":
		return "struct `" + str(n["name"]) + "`"
	case "dispatch":
		key, _ := n["key"].(map[string]any)
		return "dispatch `" + str(n["name"]) + "` on " + nodeSummary(key)
	case "list":
		e, _ := n["elem"].(map[string]any)
		return "list of " + nodeSummary(e) + maxText(n)
	case "optional":
		e, _ := n["elem"].(map[string]any)
		return "optional " + nodeSummary(e)
	case "lenprefixed":
		e, _ := n["elem"].(map[string]any)
		return "length-prefixed " + nodeSummary(e)
	case "rest":
		e, _ := n["elem"].(map[string]any)
		return nodeSummary(e) + " until the end"
	case "counted":
		e, _ := n["elem"].(map[string]any)
		return nodeSummary(e) + " `" + str(n["count"]) + "` times"
	case "whilelist":
		e, _ := n["elem"].(map[string]any)
		return "entries of " + nodeSummary(e) + " with a continuation bit"
	case "map":
		key, _ := n["key"].(map[string]any)
		val, _ := n["val"].(map[string]any)
		return "map of " + nodeSummary(key) + " to " + nodeSummary(val) + maxText(n)
	case "either":
		l, _ := n["left"].(map[string]any)
		r, _ := n["right"].(map[string]any)
		return "either " + nodeSummary(l) + " or " + nodeSummary(r)
	case "recursive":
		t, _ := n["type"].(map[string]any)
		return "recursive `" + str(n["name"]) + "`: " + nodeSummary(t)
	case "case":
		t, _ := n["type"].(map[string]any)
		return nodeSummary(t)
	}
	return str(n["k"])
}

// nbtTree renders an NBT schema node: keys of a compound, cases of a dispatch.
func nbtTree(n map[string]any, depth int) string {
	ind := strings.Repeat("  ", depth)
	var sb strings.Builder
	switch n["k"] {
	case "struct", "group":
		fields, _ := n["fields"].([]any)
		if depth == 0 && len(fields) == 0 {
			return ind + "- (no keys)\n"
		}
		for _, fa := range fields {
			f, _ := fa.(map[string]any)
			ft, _ := f["type"].(map[string]any)
			if f["inline"] == true {
				sb.WriteString(nbtTree(ft, depth))
				continue
			}
			key := str(f["key"])
			if key == "" {
				key = str(f["name"])
			}
			opt := ""
			if f["optional"] == true {
				opt = "?"
				if dflt := str(f["default"]); dflt != "" {
					opt += " (default " + dflt + ")"
				}
			}
			if nbtLeaf(ft) {
				fmt.Fprintf(&sb, "%s- `%s`%s: %s\n", ind, key, opt, escape(nodeSummary(ft)))
			} else {
				fmt.Fprintf(&sb, "%s- `%s`%s: %s\n", ind, key, opt, escape(nbtHead(ft)))
				sb.WriteString(nbtTree(ft, depth+1))
			}
		}
	case "dispatch":
		if depth == 0 {
			key, _ := n["keyType"].(map[string]any)
			fmt.Fprintf(&sb, "%s- `%s`: %s selects the case\n", ind, str(n["key"]), escape(nodeSummary(key)))
		}
		cases, _ := n["cases"].([]any)
		if cases == nil {
			fmt.Fprintf(&sb, "%s- (cases not described)\n", ind)
		}
		for _, ca := range cases {
			c, _ := ca.(map[string]any)
			ct, _ := c["type"].(map[string]any)
			if nbtLeaf(ct) {
				fmt.Fprintf(&sb, "%s- `%s`: %s\n", ind, str(c["id"]), escape(nodeSummary(ct)))
			} else {
				fmt.Fprintf(&sb, "%s- `%s`: %s\n", ind, str(c["id"]), escape(nbtHead(ct)))
				sb.WriteString(nbtTree(ct, depth+1))
			}
		}
	case "list", "map", "holder", "either", "recursive":
		if n["k"] == "recursive" {
			name := str(n["name"])
			if seenRecursive[name] {
				fmt.Fprintf(&sb, "%s- the codec `%s`, spelled out above\n", ind, name)
				break
			}
			seenRecursive[name] = true
		}
		for _, child := range nbtChildren(n) {
			if nbtLeaf(child.node) {
				fmt.Fprintf(&sb, "%s- %s: %s\n", ind, child.name, escape(nodeSummary(child.node)))
			} else {
				fmt.Fprintf(&sb, "%s- %s: %s\n", ind, child.name, escape(nbtHead(child.node)))
				sb.WriteString(nbtTree(child.node, depth+1))
			}
		}
	default:
		fmt.Fprintf(&sb, "%s- %s\n", ind, escape(nodeSummary(n)))
	}
	return sb.String()
}

type child struct {
	name string
	node map[string]any
}

func nbtChildren(n map[string]any) []child {
	switch n["k"] {
	case "list":
		return []child{{"each", m(n["elem"])}}
	case "map":
		return []child{{"keys", m(n["key"])}, {"values", m(n["val"])}}
	case "holder":
		if d, ok := n["direct"].(map[string]any); ok {
			return []child{{"inline", d}}
		}
	case "either":
		return []child{{"either", m(n["left"])}, {"or", m(n["right"])}}
	case "recursive":
		return []child{{"the codec", m(n["type"])}}
	}
	return nil
}

func nbtHead(n map[string]any) string {
	switch n["k"] {
	case "struct", "group":
		return "compound `" + str(n["name"]) + "`"
	case "dispatch":
		key, _ := n["keyType"].(map[string]any)
		return "compound, `" + str(n["key"]) + "` (" + nodeSummary(key) + ") selects"
	case "list":
		return "list"
	case "map":
		return "compound of"
	case "holder":
		return "id in " + str(n["registry"]) + " or inline"
	case "either":
		return "one of"
	case "recursive":
		return "recursive `" + str(n["name"]) + "`"
	}
	return nodeSummary(n)
}

func nbtLeaf(n map[string]any) bool {
	switch n["k"] {
	case "struct", "group", "dispatch":
		return false
	case "list", "map", "holder", "either", "recursive":
		for _, c := range nbtChildren(n) {
			if !nbtLeaf(c.node) {
				return false
			}
		}
	}
	return true
}

// ---- helpers ------------------------------------------------------------------------

func m(v any) map[string]any {
	mm, _ := v.(map[string]any)
	return mm
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprint(v)
}

// escape keeps a cell's text inside its table column.
func escape(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// anchor is what a Markdown renderer makes of a heading: lower case, spaces
// and punctuation to hyphens.
func anchor(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		case r == ' ', r == '/', r == ':', r == '.':
			sb.WriteRune('-')
		}
	}
	return sb.String()
}

func sortedKeys[V any](mm map[string]V) []string {
	keys := make([]string, 0, len(mm))
	for k := range mm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
