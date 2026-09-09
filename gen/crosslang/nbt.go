package main

import "encoding/binary"

// NBT. The schema's NBT definition carries a `tags` table: for every tag id,
// what its payload is. This reader follows that table and nothing else, and
// refuses when the table is absent.

type nbtCompound struct {
	items []nbtEntry
}

type nbtEntry struct {
	tag     byte
	name    []byte
	payload Value
}

type nbtList struct {
	elemTag byte
	n       int32
	items   []Value
}

type nbtArray struct { // byte, int and long arrays: the count and the raw bytes
	n   int32
	raw []byte
}

var nbtUsed int

func nbtString(r *reader) ([]byte, error) {
	b, err := r.take(2)
	if err != nil {
		return nil, err
	}
	return r.take(int(binary.BigEndian.Uint16(b)))
}

func nbtPayload(r *reader, tag byte) (Value, error) {
	switch tag {
	case 1:
		return r.take(1)
	case 2:
		return r.take(2)
	case 3, 5:
		return r.take(4)
	case 4, 6:
		return r.take(8)
	case 7, 11, 12:
		b, err := r.take(4)
		if err != nil {
			return nil, err
		}
		n := int32(binary.BigEndian.Uint32(b))
		size := map[byte]int{7: 1, 11: 4, 12: 8}[tag]
		raw, err := r.take(size * int(n))
		if err != nil {
			return nil, err
		}
		return &nbtArray{n: n, raw: raw}, nil
	case 8:
		return nbtString(r)
	case 9:
		et, err := r.u8()
		if err != nil {
			return nil, err
		}
		b, err := r.take(4)
		if err != nil {
			return nil, err
		}
		n := int32(binary.BigEndian.Uint32(b))
		l := &nbtList{elemTag: et, n: n}
		for i := int32(0); i < n; i++ {
			v, err := nbtPayload(r, et)
			if err != nil {
				return nil, err
			}
			l.items = append(l.items, v)
		}
		return l, nil
	case 10:
		c := &nbtCompound{}
		for {
			t, err := r.u8()
			if err != nil {
				return nil, err
			}
			if t == 0 {
				return c, nil
			}
			name, err := nbtString(r)
			if err != nil {
				return nil, err
			}
			v, err := nbtPayload(r, t)
			if err != nil {
				return nil, err
			}
			c.items = append(c.items, nbtEntry{t, name, v})
		}
	}
	return nil, wireErr("unknown NBT tag id %d", tag)
}

func nbtWritePayload(w *writer, tag byte, v Value) error {
	switch tag {
	case 1, 2, 3, 4, 5, 6:
		w.raw(v.([]byte))
	case 7, 11, 12:
		a := v.(*nbtArray)
		w.be32(uint32(a.n))
		w.raw(a.raw)
	case 8:
		b := v.([]byte)
		w.be16(uint16(len(b)))
		w.raw(b)
	case 9:
		l := v.(*nbtList)
		w.u8(l.elemTag)
		w.be32(uint32(l.n))
		for _, it := range l.items {
			if err := nbtWritePayload(w, l.elemTag, it); err != nil {
				return err
			}
		}
	case 10:
		for _, e := range v.(*nbtCompound).items {
			w.u8(e.tag)
			w.be16(uint16(len(e.name)))
			w.raw(e.name)
			if err := nbtWritePayload(w, e.tag, e.payload); err != nil {
				return err
			}
		}
		w.u8(0)
	default:
		return wireErr("unknown NBT tag id %d", tag)
	}
	return nil
}

func decNBT(r *reader, tags map[string]any) (Value, error) {
	if tags == nil {
		return nil, hole("NBT: the definition gives no tags table, so the per-tag payload layouts are nowhere in the permitted JSON")
	}
	nbtUsed++
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	if _, ok := tags[itoa(int(tag))]; !ok {
		return nil, wireErr("NBT tag id %d is not in the definition's tags table", tag)
	}
	if tag == 0 {
		return &nbtV{tag: 0}, nil
	}
	p, err := nbtPayload(r, tag)
	if err != nil {
		return nil, err
	}
	return &nbtV{tag: tag, payload: p}, nil
}

func encNBT(w *writer, v Value) error {
	n, ok := v.(*nbtV)
	if !ok {
		return wireErr("not an NBT value: %T", v)
	}
	w.u8(n.tag)
	if n.tag != 0 {
		return nbtWritePayload(w, n.tag, n.payload)
	}
	return nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
