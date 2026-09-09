package main

import (
	"encoding/binary"
	"math"
)

// The natives the schema's primitive definitions bottom out in, and nothing else. Each is a decode and an
// encode; params are the keys the prim node or its definition carries (len,
// bits, max).
type native struct {
	dec func(r *reader, p map[string]any) (Value, error)
	enc func(w *writer, v Value, p map[string]any) error
}

func decVarInt(r *reader) (int64, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := r.u8()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int64(int32(v)), nil
		}
	}
	return 0, wireErr("var int longer than 5 bytes")
}

func encVarInt(w *writer, v int64) {
	u := uint32(int32(v))
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			w.u8(b | 0x80)
		} else {
			w.u8(b)
			return
		}
	}
}

func decVarLong(r *reader) (int64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		b, err := r.u8()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int64(v), nil
		}
	}
	return 0, wireErr("var long longer than 10 bytes")
}

func encVarLong(w *writer, v int64) {
	u := uint64(v)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			w.u8(b | 0x80)
		} else {
			w.u8(b)
			return
		}
	}
}

func decString(r *reader) ([]byte, error) {
	n, err := decVarInt(r)
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, wireErr("negative string length %d", n)
	}
	return r.take(int(n))
}

func encString(w *writer, b []byte) {
	encVarInt(w, int64(len(b)))
	w.raw(b)
}

func asInt(v Value) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case *bitsV:
		return asInt(x.v)
	}
	return 0, false
}

func fixedInt(size int, signed bool) native {
	return native{
		dec: func(r *reader, p map[string]any) (Value, error) {
			b, err := r.take(size)
			if err != nil {
				return nil, err
			}
			var u uint64
			for _, x := range b {
				u = u<<8 | uint64(x)
			}
			if signed {
				shift := uint(64 - 8*size)
				return int64(u<<shift) >> shift, nil
			}
			return int64(u), nil
		},
		enc: func(w *writer, v Value, p map[string]any) error {
			n, ok := asInt(v)
			if !ok {
				return wireErr("not an integer: %T", v)
			}
			u := uint64(n)
			for i := size - 1; i >= 0; i-- {
				w.u8(byte(u >> (8 * i)))
			}
			return nil
		},
	}
}

func decLpVec3(r *reader, p map[string]any) (Value, error) {
	b0, err := r.u8()
	if err != nil {
		return nil, err
	}
	v := &lpVec3{b0: b0}
	if b0 == 0 {
		return v, nil
	}
	if v.b1, err = r.u8(); err != nil {
		return nil, err
	}
	b, err := r.take(4)
	if err != nil {
		return nil, err
	}
	v.u = binary.BigEndian.Uint32(b)
	if b0&4 != 0 {
		if v.h, err = decVarInt(r); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func encLpVec3(w *writer, v Value, p map[string]any) error {
	x, ok := v.(*lpVec3)
	if !ok {
		return wireErr("not an LP_VEC3: %T", v)
	}
	w.u8(x.b0)
	if x.b0 == 0 {
		return nil
	}
	w.u8(x.b1)
	w.be32(x.u)
	if x.b0&4 != 0 {
		encVarInt(w, x.h)
	}
	return nil
}

func bytesOf(v Value) ([]byte, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, wireErr("not bytes: %T", v)
	}
	return b, nil
}

var natives = map[string]native{
	"bool": {
		dec: func(r *reader, p map[string]any) (Value, error) { b, err := r.u8(); return b != 0, err },
		enc: func(w *writer, v Value, p map[string]any) error {
			n, ok := asInt(v)
			if !ok {
				return wireErr("not a boolean: %T", v)
			}
			if n != 0 {
				w.u8(1)
			} else {
				w.u8(0)
			}
			return nil
		},
	},
	"i8":    fixedInt(1, true),
	"u8":    fixedInt(1, false),
	"i16be": fixedInt(2, true),
	"u16be": fixedInt(2, false),
	"i32be": fixedInt(4, true),
	"i64be": fixedInt(8, true),
	"f32be": {
		dec: func(r *reader, p map[string]any) (Value, error) {
			b, err := r.take(4)
			if err != nil {
				return nil, err
			}
			return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
		},
		enc: func(w *writer, v Value, p map[string]any) error { w.f32(v.(float32)); return nil },
	},
	"f64be": {
		dec: func(r *reader, p map[string]any) (Value, error) {
			b, err := r.take(8)
			if err != nil {
				return nil, err
			}
			return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
		},
		enc: func(w *writer, v Value, p map[string]any) error { w.f64(v.(float64)); return nil },
	},
	"varint": {
		dec: func(r *reader, p map[string]any) (Value, error) { return decVarInt(r) },
		enc: func(w *writer, v Value, p map[string]any) error { n, _ := asInt(v); encVarInt(w, n); return nil },
	},
	"varlong": {
		dec: func(r *reader, p map[string]any) (Value, error) { return decVarLong(r) },
		enc: func(w *writer, v Value, p map[string]any) error { n, _ := asInt(v); encVarLong(w, n); return nil },
	},
	"string": {
		// a var-int byte count, then exactly that many UTF-8 bytes; kept as
		// bytes, the cap in `max` is a character cap and validation only
		dec: func(r *reader, p map[string]any) (Value, error) { return decString(r) },
		enc: func(w *writer, v Value, p map[string]any) error {
			b, err := bytesOf(v)
			if err != nil {
				return err
			}
			encString(w, b)
			return nil
		},
	},
	"rest_bytes": {
		dec: func(r *reader, p map[string]any) (Value, error) { return r.rest(), nil },
		enc: func(w *writer, v Value, p map[string]any) error { b, err := bytesOf(v); w.raw(b); return err },
	},
	"fixed_bytes": {
		dec: func(r *reader, p map[string]any) (Value, error) {
			n, ok := p["len"].(float64)
			if !ok {
				return nil, hole("FIXED_BYTES with no `len` on the node")
			}
			return r.take(int(n))
		},
		enc: func(w *writer, v Value, p map[string]any) error { b, err := bytesOf(v); w.raw(b); return err },
	},
	"fixed_bit_set": {
		dec: func(r *reader, p map[string]any) (Value, error) {
			n, ok := p["bits"].(float64)
			if !ok {
				return nil, hole("FIXED_BIT_SET with no `bits` on the node")
			}
			return r.take((int(n) + 7) / 8)
		},
		enc: func(w *writer, v Value, p map[string]any) error { b, err := bytesOf(v); w.raw(b); return err },
	},
	"optional_var_int": {
		// 0 absent, n means n-1
		dec: func(r *reader, p map[string]any) (Value, error) {
			n, err := decVarInt(r)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return &optV{present: false}, nil
			}
			return &optV{present: true, v: n - 1}, nil
		},
		enc: func(w *writer, v Value, p map[string]any) error {
			o := v.(*optV)
			if !o.present {
				encVarInt(w, 0)
			} else {
				n, _ := asInt(o.v)
				encVarInt(w, n+1)
			}
			return nil
		},
	},
	"lp_vec3": {dec: decLpVec3, enc: encLpVec3},
	// "nbt" is deliberately absent: see nbt.go
}
