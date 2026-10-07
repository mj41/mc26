package main

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// ctx is what an inner node may need from the nodes around it: the named
// nodes a `ref` points back to, the EnumSets a `guard` selects by, the
// enclosing struct's values for `counted`, `lenprefixed` and `packed`.
type ctx struct {
	s        *schema
	named    []map[string]node
	enums    []map[string]map[string]bool
	siblings []sibling
	path     []string
}

type sibling struct {
	node node
	vals []Value
}

func newCtx(s *schema) *ctx { return &ctx{s: s} }

func (c *ctx) lookupEnumSet(short string) map[string]bool {
	for i := len(c.enums) - 1; i >= 0; i-- {
		if v, ok := c.enums[i][short]; ok {
			return v
		}
	}
	return nil
}

func (c *ctx) lookupRef(name, of string) node {
	for i := len(c.named) - 1; i >= 0; i-- {
		if n, ok := c.named[i][name]; ok && n["k"] == of {
			return n
		}
	}
	return nil
}

func (c *ctx) where() string { return strings.Join(c.path, "/") }

func (c *ctx) push(p string) { c.path = append(c.path, p) }
func (c *ctx) pop()          { c.path = c.path[:len(c.path)-1] }

// ---- conditions: `when` on a struct field, `while` on a whilelist ------------

// fieldValue is the nearest PRECEDING field with that name (names are not
// unique); a dotted name is one named run of bits of such a field.
func fieldValue(sn node, vals []Value, upto int, name string) (Value, node, error) {
	base, sub, _ := strings.Cut(name, ".")
	fields, _ := sn["fields"].([]any)
	for i := upto - 1; i >= 0; i-- {
		f, _ := fields[i].(node)
		if str(f["name"]) != base {
			continue
		}
		if _, isAbsent := vals[i].(absent); isAbsent {
			return nil, nil, hole("`when` reads field %q, which was itself absent", name)
		}
		if sub != "" {
			return bitField(vals[i], f, sub)
		}
		return vals[i], f, nil
	}
	return nil, nil, hole("`when` names field %q, which is not an earlier field", name)
}

// bitField cuts one named run of bits out of a packed integer (nodes.json `bits`).
func bitField(v Value, fnode node, sub string) (Value, node, error) {
	n, _ := fnode["type"].(node)
	if n["k"] != "bits" {
		return nil, nil, hole("field %q is not a packed integer, so it has no %q", str(fnode["name"]), sub)
	}
	raw, ok := asInt(v)
	if !ok {
		return nil, nil, hole("packed integer is not an integer")
	}
	width, _ := num(n["bits"])
	if width < 64 {
		raw &= (1 << uint(width)) - 1
	}
	fields, _ := n["fields"].([]any)
	for _, fa := range fields {
		f, _ := fa.(node)
		if str(f["name"]) != sub {
			continue
		}
		off, _ := num(f["offset"])
		w, _ := num(f["width"])
		x := (raw >> uint(off)) & ((1 << uint(w)) - 1)
		if f["signed"] == true && x>>(uint(w)-1) != 0 {
			x -= 1 << uint(w)
		}
		var out Value = x
		if w == 1 && f["signed"] != true {
			out = x != 0
		}
		return out, node{"name": sub, "type": node{"k": "prim", "t": n["of"]}}, nil
	}
	return nil, nil, hole("the packed %s has no field %q", str(n["name"]), sub)
}

func asNumber(v Value, fnode node) (int64, error) {
	if n, ok := asInt(v); ok {
		return n, nil
	}
	t, _ := fnode["type"].(node)
	return 0, hole("`when` test on a %s field, which is not a number", str(t["k"]))
}

func evalTest(t node, sn node, vals []Value, upto int) (bool, error) {
	v, fnode, err := fieldValue(sn, vals, upto, str(t["field"]))
	if err != nil {
		return false, err
	}
	neg := t["not"] == true
	var res bool
	switch str(t["test"]) {
	case "true":
		n, _ := asInt(v)
		res = n != 0
	case "bit":
		n, err := asNumber(v, fnode)
		if err != nil {
			return false, err
		}
		mask, _ := num(t["value"])
		res = n&mask != 0
	case "maskeq":
		n, err := asNumber(v, fnode)
		if err != nil {
			return false, err
		}
		mask, _ := num(t["mask"])
		want, _ := num(t["value"])
		res = n&mask == want
	case "eq":
		n, err := asNumber(v, fnode)
		if err != nil {
			return false, err
		}
		var want int64
		if s, ok := t["value"].(string); ok {
			// an enum constant compared by name: the wire value is the position
			// in the enum node's `values`, or its id when the node carries `ids`
			en, _ := fnode["type"].(node)
			values, _ := en["values"].([]any)
			if en["k"] != "enum" || values == nil {
				return false, hole("`when` eq %q on a field that is not a valued enum", s)
			}
			at := -1
			for i, x := range values {
				if str(x) == s {
					at = i
				}
			}
			if at < 0 {
				return false, hole("`when` eq %q: not a constant of %s", s, str(en["name"]))
			}
			want = int64(at)
			if ids, ok := en["ids"].([]any); ok {
				want, _ = num(ids[at])
			}
		} else {
			want, _ = num(t["value"])
		}
		res = n == want
	case "cmp":
		n, err := asNumber(v, fnode)
		if err != nil {
			return false, err
		}
		want, _ := num(t["value"])
		switch str(t["op"]) {
		case ">":
			res = n > want
		case ">=":
			res = n >= want
		case "<":
			res = n < want
		case "<=":
			res = n <= want
		}
		if neg { // nodes.json: `not` inverts the operator
			return !res, nil
		}
		return res, nil
	case "in":
		n, err := asNumber(v, fnode)
		if err != nil {
			return false, err
		}
		set, _ := t["value"].([]any)
		for _, x := range set {
			if m, _ := num(x); m == n {
				res = true
			}
		}
	default:
		return false, hole("unknown `when` test kind %q", str(t["test"]))
	}
	if neg {
		return !res, nil
	}
	return res, nil
}

func fieldPresent(f node, sn node, vals []Value, upto int, c *ctx) (bool, error) {
	w, ok := f["when"]
	if !ok {
		return true, nil
	}
	if s, ok := w.(string); ok {
		// nodes.json `guard`: the struct carries `guard`, the selector is an
		// EnumSet of that class carried EARLIER in the enclosing packet
		g, ok := sn["guard"].(string)
		if !ok {
			return false, hole("string `when` %q on a struct with no `guard`", s)
		}
		short := g[strings.LastIndex(g, "/")+1:]
		sel := c.lookupEnumSet(short)
		if sel == nil {
			return false, hole("guard %s: no enumset field with that name was decoded", short)
		}
		return sel[s], nil
	}
	groups, _ := w.([]any) // outer ANDed, inner ORed
	for _, ga := range groups {
		g, _ := ga.([]any)
		any := false
		for _, ta := range g {
			ok, err := evalTest(ta.(node), sn, vals, upto)
			if err != nil {
				return false, err
			}
			if ok {
				any = true
				break
			}
		}
		if !any {
			return false, nil
		}
	}
	return true, nil
}

// ---- decode ------------------------------------------------------------------------

func (c *ctx) decode(n node, r *reader, params node) (Value, error) {
	k := str(n["k"])
	if !c.s.kinds[k] {
		return nil, hole("node kind %q is not described in nodes.json", k)
	}
	switch k {
	case "prim":
		return c.decPrim(n, r, params)
	case "bits":
		v, err := c.decode(node{"k": "prim", "t": n["of"]}, r, nil)
		if err != nil {
			return nil, err
		}
		return &bitsV{v}, nil
	case "unit":
		return unitV{}, nil // zero bytes; the value is present and nothing
	case "struct":
		return c.decStruct(n, r)
	case "list":
		count, err := decVarInt(r)
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, wireErr("negative list count %d", count)
		}
		return c.decRepeat(n, r, int(count))
	case "counted":
		sib := c.siblings[len(c.siblings)-1]
		cv, _, err := fieldValue(sib.node, sib.vals, len(sib.vals), str(n["count"]))
		if err != nil {
			return nil, err
		}
		count, _ := asInt(cv)
		return c.decRepeat(n, r, int(count))
	case "array":
		size, _ := asInt(n["size"])
		return c.decRepeat(n, r, int(size))
	case "lenprefixed":
		// the byte count is a var int right here unless `length` names an
		// earlier sibling that already carried it
		var length int64
		if l, ok := n["length"].(string); ok {
			sib := c.siblings[len(c.siblings)-1]
			lv, _, err := fieldValue(sib.node, sib.vals, len(sib.vals), l)
			if err != nil {
				return nil, err
			}
			length, _ = asInt(lv)
		} else {
			var err error
			if length, err = decVarInt(r); err != nil {
				return nil, err
			}
		}
		if err := r.pushLimit(int(length)); err != nil {
			return nil, err
		}
		v, err := c.decode(n["elem"].(node), r, nil)
		if err != nil {
			return nil, err
		}
		if err := r.popLimit(); err != nil {
			return nil, err
		}
		return &windowV{v}, nil
	case "rest":
		var items []Value
		for r.left() > 0 {
			c.push(fmt.Sprintf("[%d]", len(items)))
			v, err := c.decode(n["elem"].(node), r, nil)
			c.pop()
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return &listV{items}, nil
	case "packed":
		return c.decPacked(n, r)
	case "map":
		count, err := decVarInt(r)
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, wireErr("negative map count %d", count)
		}
		m := &mapV{}
		for i := 0; i < int(count); i++ {
			c.push(fmt.Sprintf("{%d}", i))
			kv, err := c.decode(n["key"].(node), r, nil)
			if err != nil {
				return nil, err
			}
			vv, err := c.decode(n["val"].(node), r, nil)
			if err != nil {
				return nil, err
			}
			c.pop()
			m.keys = append(m.keys, kv)
			m.vals = append(m.vals, vv)
		}
		return m, nil
	case "optional":
		elem, _ := n["elem"].(node)
		if elem["k"] == "nbt" {
			return nil, hole("optional{nbt}: packet_schema.json says a boolean then a tag, but nodes.json says this construct (optionalTagCodec) has no boolean byte and sits inside an erased length prefix -- the two authorities contradict each other, so the bytes are unknowable from the JSON")
		}
		b, err := r.u8()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			return &optV{present: false}, nil
		}
		v, err := c.decode(elem, r, nil)
		if err != nil {
			return nil, err
		}
		return &optV{present: true, v: v}, nil
	case "either":
		b, err := r.u8()
		if err != nil {
			return nil, err
		}
		side := n["right"].(node)
		if b != 0 {
			side = n["left"].(node)
		}
		v, err := c.decode(side, r, nil)
		if err != nil {
			return nil, err
		}
		return &eitherV{left: b != 0, v: v}, nil
	case "enum":
		if _, ok := n["values"]; !ok {
			return nil, hole("enum %s has no `values` (java: %s): the constants are not in the schema", str(n["name"]), str(n["java"]))
		}
		if n["idsUnknown"] == true {
			return nil, hole("enum %s travels as an id of its own that the schema could not read", str(n["name"]))
		}
		return decVarInt(r)
	case "enumset":
		vals, _ := n["values"].([]any)
		raw, err := r.take((len(vals) + 7) / 8)
		if err != nil {
			return nil, err
		}
		set := map[string]bool{}
		for i, name := range vals {
			if raw[i/8]&(1<<uint(i%8)) != 0 {
				set[str(name)] = true
			}
		}
		return &enumSetV{set: set, raw: raw}, nil
	case "stringenum":
		b, err := decString(r)
		if err != nil {
			return nil, err
		}
		names, _ := n["names"].([]any)
		for _, x := range names {
			if str(x) == string(b) {
				return strEnumV(b), nil
			}
		}
		return nil, wireErr("stringenum %s: %q is not one of %v", str(n["name"]), string(b), names)
	case "string", "resourcekey":
		return decString(r)
	case "registry":
		return decVarInt(r)
	case "holder":
		id, err := decVarInt(r)
		if err != nil {
			return nil, err
		}
		if direct, ok := n["direct"].(node); ok && id == 0 {
			v, err := c.decode(direct, r, nil)
			if err != nil {
				return nil, err
			}
			return &holderV{id: 0, direct: v}, nil
		}
		return &holderV{id: id}, nil
	case "holderset":
		count, err := decVarInt(r)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			tag, err := decString(r)
			if err != nil {
				return nil, err
			}
			return &holderSet{tag: tag}, nil
		}
		if count < 0 {
			return nil, wireErr("negative holderset count %d", count)
		}
		ids := make([]int64, 0, count-1)
		for i := int64(0); i < count-1; i++ {
			id, err := decVarInt(r)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return &holderSet{ids: ids}, nil
	case "ref":
		target := c.lookupRef(str(n["name"]), str(n["of"]))
		if target == nil {
			return nil, hole("ref to %s (%s) has no enclosing node of that name", str(n["name"]), str(n["of"]))
		}
		v, err := c.decode(target, r, nil)
		if err != nil {
			return nil, err
		}
		return &refV{v}, nil
	case "opaque":
		return nil, hole("opaque node (%s): the extractor could not describe these bytes", str(n["java"]))
	case "nbt":
		return decNBT(r, c.s.nbtTags)
	case "case":
		return c.decode(n["type"].(node), r, nil)
	case "dispatch":
		return c.decDispatch(n, r)
	case "whilelist":
		return c.decWhileList(n, r)
	}
	return nil, hole("node kind %q has no reader", k)
}

func (c *ctx) decPrim(n node, r *reader, params node) (Value, error) {
	p := node{}
	for k, v := range params {
		p[k] = v
	}
	for k, v := range n {
		if k != "k" && k != "t" {
			p[k] = v
		}
	}
	target, p, err := c.s.resolvePrim(str(n["t"]), p)
	if err != nil {
		return nil, err
	}
	if target["k"] == "native" {
		of := str(target["of"])
		if of == "nbt" {
			return decNBT(r, c.s.nbtTags)
		}
		impl, ok := natives[of]
		if !ok {
			return nil, hole("native %q has no reader", of)
		}
		return impl.dec(r, p)
	}
	return c.decode(target, r, p)
}

func (c *ctx) decStruct(n node, r *reader) (Value, error) {
	if n["conditional"] == true {
		return nil, hole("struct %s is marked conditional: the extractor could not say which fields are on the wire", str(n["name"]))
	}
	sv := &structV{}
	c.enums = append(c.enums, map[string]map[string]bool{})
	if name, ok := n["name"].(string); ok && name != "" {
		c.named = append(c.named, map[string]node{name: n})
		defer func() { c.named = c.named[:len(c.named)-1] }()
	}
	c.siblings = append(c.siblings, sibling{n, nil})
	defer func() {
		c.siblings = c.siblings[:len(c.siblings)-1]
		c.enums = c.enums[:len(c.enums)-1]
	}()
	fields, _ := n["fields"].([]any)
	for i, fa := range fields {
		f, _ := fa.(node)
		ok, err := fieldPresent(f, n, sv.vals, i, c)
		if err != nil {
			return nil, err
		}
		if !ok {
			sv.vals = append(sv.vals, absent{})
			c.siblings[len(c.siblings)-1].vals = sv.vals
			continue
		}
		ft, _ := f["type"].(node)
		c.push(str(f["name"]))
		v, err := c.decode(ft, r, nil)
		c.pop()
		if err != nil {
			return nil, err
		}
		sv.vals = append(sv.vals, v)
		c.siblings[len(c.siblings)-1].vals = sv.vals
		if ft["k"] == "enumset" {
			c.enums[len(c.enums)-1][str(ft["name"])] = v.(*enumSetV).set
		}
	}
	return sv, nil
}

func (c *ctx) decRepeat(n node, r *reader, count int) (Value, error) {
	l := &listV{}
	elem, _ := n["elem"].(node)
	for i := 0; i < count; i++ {
		c.push(fmt.Sprintf("[%d]", i))
		v, err := c.decode(elem, r, nil)
		c.pop()
		if err != nil {
			return nil, err
		}
		l.items = append(l.items, v)
	}
	return l, nil
}

// packedWidth is the storage width nodes.json `packed` gives for the palette
// byte the earlier sibling holds.
func (c *ctx) packedWidth(n node) (int, error) {
	sib := c.siblings[len(c.siblings)-1]
	bv, _, err := fieldValue(sib.node, sib.vals, len(sib.vals), str(n["bits"]))
	if err != nil {
		return 0, err
	}
	bits, _ := asInt(bv)
	widths, _ := n["width"].(node)
	w, ok := widths[itoa(int(bits))]
	if !ok {
		w, ok = widths["*"]
	}
	if !ok {
		return 0, hole("packed: no width for a %s of %d", str(n["bits"]), bits)
	}
	if m, ok := w.(node); ok {
		size, ok := c.s.idSpaceSizes[str(m["registryBits"])]
		if !ok {
			return 0, hole("packed: no size known for id space %q", str(m["registryBits"]))
		}
		return ceillog2(size), nil
	}
	f, _ := num(w)
	return int(f), nil
}

func (c *ctx) decPacked(n node, r *reader) (Value, error) {
	width, err := c.packedWidth(n)
	if err != nil {
		return nil, err
	}
	entries, _ := num(n["entries"])
	if width == 0 {
		return &packedV{width: 0}, nil
	}
	vpl := 64 / width
	longs := (int(entries) + vpl - 1) / vpl
	raw, err := r.take(8 * longs)
	if err != nil {
		return nil, err
	}
	p := &packedV{width: width}
	mask := uint64(1)<<uint(width) - 1
	for i := 0; i < longs; i++ {
		word := binary.BigEndian.Uint64(raw[8*i:])
		for j := 0; j < vpl && len(p.vals) < int(entries); j++ {
			p.vals = append(p.vals, (word>>(uint(j*width)))&mask)
		}
	}
	return p, nil
}

// cases gives, for a dispatch, the case a key selects and its id.
func (c *ctx) cases(n node) (func(kv int64) (node, string, error), error) {
	switch str(n["casesFrom"]) {
	case "packet_schema.json#components":
		reg := c.s.regByID["data_component_type"]
		comps, _ := c.s.packetSchema["components"].(map[string]any)
		return func(kv int64) (node, string, error) {
			name, ok := reg[kv]
			if !ok {
				return nil, "", wireErr("data_component_type id %d is in no registry entry", kv)
			}
			comp, ok := comps[name].(map[string]any)
			if !ok {
				return nil, "", hole("component %s has no schema entry", name)
			}
			return comp["type"].(node), name, nil
		}, nil
	case "entity_data.json#serializers":
		return func(kv int64) (node, string, error) {
			ser, ok := c.s.serializers[kv]
			if !ok {
				return nil, "", wireErr("entity data serializer id %d is not in entity_data.json", kv)
			}
			return ser["type"].(node), str(ser["name"]), nil
		}, nil
	}
	cases, ok := n["cases"].([]any)
	if !ok {
		return nil, hole("dispatch %s has no `cases`: the payloads are not in the schema", str(n["name"]))
	}
	key, _ := n["key"].(node)
	table := map[int64]node{}
	switch key["k"] {
	case "registry":
		reg, ok := c.s.regByName[str(key["registry"])]
		if !ok {
			return nil, hole("dispatch key registry %q is not in registries.json", str(key["registry"]))
		}
		for _, ca := range cases {
			cn := ca.(node)
			pid, ok := reg[str(cn["id"])]
			if !ok {
				return nil, hole("case id %q is not an entry of registry %q", str(cn["id"]), str(key["registry"]))
			}
			table[pid] = cn
		}
	case "enum":
		for _, ca := range cases {
			cn := ca.(node)
			id, _ := num(cn["num"])
			table[id] = cn
		}
	default:
		return nil, hole("dispatch key kind %q", str(key["k"]))
	}
	return func(kv int64) (node, string, error) {
		cn, ok := table[kv]
		if !ok {
			return nil, "", wireErr("dispatch %s: no case for key %d", str(n["name"]), kv)
		}
		return cn["type"].(node), str(cn["id"]), nil
	}, nil
}

func (c *ctx) decDispatch(n node, r *reader) (Value, error) {
	key, _ := n["key"].(node)
	if name, ok := n["name"].(string); ok && name != "" {
		c.named = append(c.named, map[string]node{name: n})
		defer func() { c.named = c.named[:len(c.named)-1] }()
	}
	var kv Value
	inlineKey := true
	switch {
	case key["k"] == nil && key["field"] != nil:
		// DELIMITED_COMPONENT_PATCH: the key is an earlier sibling
		// field, already on the wire, not read again here
		sib := c.siblings[len(c.siblings)-1]
		v, _, err := fieldValue(sib.node, sib.vals, len(sib.vals), str(key["field"]))
		if err != nil {
			return nil, err
		}
		kv = v
		inlineKey = false
	case key["k"] == "either":
		return nil, hole("dispatch %s has an `either` key and no cases", str(n["name"]))
	case key["k"] == "enum" && (key["ids"] != nil || key["idsUnknown"] == true):
		return nil, hole("dispatch %s is keyed by an enum that does not travel as its ordinal", str(n["name"]))
	default:
		v, err := c.decode(key, r, nil)
		if err != nil {
			return nil, err
		}
		kv = v
	}
	byKey, err := c.cases(n)
	if err != nil {
		return nil, err
	}
	ki, _ := asInt(kv)
	cn, cid, err := byKey(ki)
	if err != nil {
		return nil, err
	}
	c.push(cid)
	v, err := c.decode(cn, r, nil)
	c.pop()
	if err != nil {
		return nil, err
	}
	return &dispatchV{key: kv, v: v, inlineKey: inlineKey}, nil
}

func (c *ctx) decWhileList(n node, r *reader) (Value, error) {
	l := &listV{}
	elem, _ := n["elem"].(node)
	for {
		c.push(fmt.Sprintf("[%d]", len(l.items)))
		e, err := c.decode(elem, r, nil)
		c.pop()
		if err != nil {
			return nil, err
		}
		l.items = append(l.items, e)
		done, err := whileDone(n, e)
		if err != nil {
			return nil, err
		}
		if done {
			return l, nil
		}
		if r.left() <= 0 {
			return nil, wireErr("whilelist ran off the end without its terminating entry")
		}
	}
}

func whileDone(n node, entry Value) (bool, error) {
	cond, _ := n["while"].(node)
	elem, _ := n["elem"].(node)
	if elem["k"] != "struct" {
		return false, hole("whilelist `while` on a non-struct element")
	}
	if str(cond["field"]) == "" {
		return false, hole("whilelist `while` with an empty `field`: the tested value is the entry itself, which the schema cannot name")
	}
	sv := entry.(*structV)
	return evalTest(cond, elem, sv.vals, len(sv.vals))
}
