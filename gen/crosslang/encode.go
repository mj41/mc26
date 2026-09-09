package main

import "encoding/binary"

// unitV is the value of a `unit` node: nothing, but present.
type unitV struct{}

func (c *ctx) encode(n node, v Value, w *writer, params node) error {
	switch str(n["k"]) {
	case "prim":
		return c.encPrim(n, v, w, params)
	case "bits":
		return c.encode(node{"k": "prim", "t": n["of"]}, v.(*bitsV).v, w, nil)
	case "unit":
		return nil
	case "struct":
		return c.encStruct(n, v, w)
	case "list":
		l := v.(*listV)
		encVarInt(w, int64(len(l.items)))
		return c.encItems(n, l.items, w)
	case "counted":
		sib := c.siblings[len(c.siblings)-1]
		cv, _, err := fieldValue(sib.node, sib.vals, len(sib.vals), str(n["count"]))
		if err != nil {
			return err
		}
		count, _ := asInt(cv)
		l := v.(*listV)
		if int(count) != len(l.items) {
			return wireErr("counted: %d entries but the count field says %d", len(l.items), count)
		}
		return c.encItems(n, l.items, w)
	case "lenprefixed":
		inner := &writer{}
		if err := c.encode(n["elem"].(node), v.(*windowV).v, inner, nil); err != nil {
			return err
		}
		if l, ok := n["length"].(string); ok {
			sib := c.siblings[len(c.siblings)-1]
			lv, _, err := fieldValue(sib.node, sib.vals, len(sib.vals), l)
			if err != nil {
				return err
			}
			want, _ := asInt(lv)
			if int(want) != inner.Len() {
				return wireErr("lenprefixed: re-encoded to %d bytes, the length field says %d", inner.Len(), want)
			}
		} else {
			encVarInt(w, int64(inner.Len()))
		}
		w.raw(inner.Bytes())
		return nil
	case "rest", "whilelist":
		return c.encItems(n, v.(*listV).items, w)
	case "packed":
		p := v.(*packedV)
		width, err := c.packedWidth(n)
		if err != nil {
			return err
		}
		if width != p.width {
			return wireErr("packed: re-encoding at %d bits, decoded at %d", width, p.width)
		}
		if width == 0 {
			return nil
		}
		vpl := 64 / width
		entries, _ := num(n["entries"])
		longs := (int(entries) + vpl - 1) / vpl
		mask := uint64(1)<<uint(width) - 1
		for i := 0; i < longs; i++ {
			var word uint64
			for j := 0; j < vpl; j++ {
				idx := i*vpl + j
				if idx >= int(entries) {
					break
				}
				word |= (p.vals[idx] & mask) << uint(j*width)
			}
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], word)
			w.raw(b[:])
		}
		return nil
	case "map":
		m := v.(*mapV)
		encVarInt(w, int64(len(m.keys)))
		for i := range m.keys {
			if err := c.encode(n["key"].(node), m.keys[i], w, nil); err != nil {
				return err
			}
			if err := c.encode(n["val"].(node), m.vals[i], w, nil); err != nil {
				return err
			}
		}
		return nil
	case "optional":
		elem, _ := n["elem"].(node)
		if elem["k"] == "nbt" {
			return hole("optional{nbt}: see the reader")
		}
		o := v.(*optV)
		if !o.present {
			w.u8(0)
			return nil
		}
		w.u8(1)
		return c.encode(elem, o.v, w, nil)
	case "either":
		e := v.(*eitherV)
		side := n["right"].(node)
		if e.left {
			w.u8(1)
			side = n["left"].(node)
		} else {
			w.u8(0)
		}
		return c.encode(side, e.v, w, nil)
	case "enum", "registry":
		x, _ := asInt(v)
		encVarInt(w, x)
		return nil
	case "enumset":
		vals, _ := n["values"].([]any)
		raw := make([]byte, (len(vals)+7)/8)
		set := v.(*enumSetV).set
		for i, name := range vals {
			if set[str(name)] {
				raw[i/8] |= 1 << uint(i%8)
			}
		}
		w.raw(raw)
		return nil
	case "stringenum":
		encString(w, []byte(v.(strEnumV)))
		return nil
	case "string", "resourcekey":
		b, err := bytesOf(v)
		if err != nil {
			return err
		}
		encString(w, b)
		return nil
	case "holder":
		h := v.(*holderV)
		encVarInt(w, h.id)
		if direct, ok := n["direct"].(node); ok && h.id == 0 {
			return c.encode(direct, h.direct, w, nil)
		}
		return nil
	case "holderset":
		h := v.(*holderSet)
		if h.ids == nil {
			encVarInt(w, 0)
			encString(w, h.tag)
			return nil
		}
		encVarInt(w, int64(len(h.ids)+1))
		for _, id := range h.ids {
			encVarInt(w, id)
		}
		return nil
	case "ref":
		target := c.lookupRef(str(n["name"]), str(n["of"]))
		if target == nil {
			return hole("ref to %s has no enclosing node", str(n["name"]))
		}
		return c.encode(target, v.(*refV).v, w, nil)
	case "opaque":
		return hole("opaque node")
	case "nbt":
		return encNBT(w, v)
	case "case":
		return c.encode(n["type"].(node), v, w, nil)
	case "dispatch":
		return c.encDispatch(n, v, w)
	}
	return hole("node kind %q has no writer", str(n["k"]))
}

func (c *ctx) encPrim(n node, v Value, w *writer, params node) error {
	p := node{}
	for k, x := range params {
		p[k] = x
	}
	for k, x := range n {
		if k != "k" && k != "t" {
			p[k] = x
		}
	}
	target, p, err := c.s.resolvePrim(str(n["t"]), p)
	if err != nil {
		return err
	}
	if target["k"] == "native" {
		of := str(target["of"])
		if of == "nbt" {
			return encNBT(w, v)
		}
		impl, ok := natives[of]
		if !ok {
			return hole("native %q has no writer", of)
		}
		return impl.enc(w, v, p)
	}
	return c.encode(target, v, w, p)
}

func (c *ctx) encStruct(n node, v Value, w *writer) error {
	sv := v.(*structV)
	c.enums = append(c.enums, map[string]map[string]bool{})
	if name, ok := n["name"].(string); ok && name != "" {
		c.named = append(c.named, map[string]node{name: n})
		defer func() { c.named = c.named[:len(c.named)-1] }()
	}
	c.siblings = append(c.siblings, sibling{n, sv.vals})
	defer func() {
		c.siblings = c.siblings[:len(c.siblings)-1]
		c.enums = c.enums[:len(c.enums)-1]
	}()
	fields, _ := n["fields"].([]any)
	for i, fa := range fields {
		f, _ := fa.(node)
		present, err := fieldPresent(f, n, sv.vals, i, c)
		if err != nil {
			return err
		}
		_, wasAbsent := sv.vals[i].(absent)
		if wasAbsent == present {
			return wireErr("field %s: `when` disagrees between the read and the write", str(f["name"]))
		}
		if !present {
			continue
		}
		ft, _ := f["type"].(node)
		if err := c.encode(ft, sv.vals[i], w, nil); err != nil {
			return err
		}
		if ft["k"] == "enumset" {
			c.enums[len(c.enums)-1][str(ft["name"])] = sv.vals[i].(*enumSetV).set
		}
	}
	return nil
}

func (c *ctx) encItems(n node, items []Value, w *writer) error {
	elem, _ := n["elem"].(node)
	for _, it := range items {
		if err := c.encode(elem, it, w, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *ctx) encDispatch(n node, v Value, w *writer) error {
	if name, ok := n["name"].(string); ok && name != "" {
		c.named = append(c.named, map[string]node{name: n})
		defer func() { c.named = c.named[:len(c.named)-1] }()
	}
	d := v.(*dispatchV)
	if d.inlineKey {
		if err := c.encode(n["key"].(node), d.key, w, nil); err != nil {
			return err
		}
	}
	byKey, err := c.cases(n)
	if err != nil {
		return err
	}
	ki, _ := asInt(d.key)
	cn, _, err := byKey(ki)
	if err != nil {
		return err
	}
	return c.encode(cn, d.v, w, nil)
}
