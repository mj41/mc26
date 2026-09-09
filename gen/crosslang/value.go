// Schema-driven Minecraft packet codec, written from the JSON alone.
//
// Reads a capture of packet bodies, decodes each one against packet_schema.json
// using only the node kinds nodes.json lists and the primitives prims.json
// defines, then re-encodes the decoded value and checks the bytes match.
// Nothing here special-cases a packet: the reader is driven by the node kind.
//
//	go run . --data <dir> --prims <prims.json> --nodes <nodes.json> --capture <file.jsonl> [--verbose]
//
// It is a module of its own so that it cannot import the Go library or the
// generators: what it can read, the JSON describes.
package main

import "fmt"

// A Hole is what the JSON does not describe. Never guessed around, always raised.
type Hole struct{ msg string }

func (h *Hole) Error() string { return h.msg }

func hole(format string, args ...any) error { return &Hole{fmt.Sprintf(format, args...)} }

// A WireError is bytes that did not match what the schema says they should be.
type WireError struct{ msg string }

func (w *WireError) Error() string { return w.msg }

func wireErr(format string, args ...any) error { return &WireError{fmt.Sprintf(format, args...)} }

// Value is what a node decoded to. The concrete types below say which node
// kind produced them; the encoder needs nothing more than that.
type Value any

type (
	absent  struct{}                // a field whose `when` was false
	structV struct{ vals []Value }  // one Value per field, absent{} where the field was not on the wire
	listV   struct{ items []Value } // list, counted, rest, whilelist
	windowV struct{ v Value }       // lenprefixed
	packedV struct {
		width int
		vals  []uint64
	}
	mapV struct{ keys, vals []Value }
	optV struct {
		present bool
		v       Value
	}
	eitherV struct {
		left bool
		v    Value
	}
	enumSetV struct {
		set map[string]bool
		raw []byte
	}
	strEnumV string
	holderV  struct {
		id     int64
		direct Value
	} // direct set when id == 0 and the node has one
	holderSet struct {
		tag []byte
		ids []int64
	} // ids nil means the tag form
	refV struct{ v Value }
	nbtV struct {
		tag     byte
		payload Value
	}
	dispatchV struct {
		key       Value
		v         Value
		inlineKey bool
	}
	bitsV  struct{ v Value }
	lpVec3 struct {
		b0, b1 byte
		u      uint32
		h      int64
	}
)

// summarise is a short rendering of a value for --verbose.
func summarise(v Value, depth int) string {
	switch x := v.(type) {
	case absent:
		return "<absent>"
	case []byte:
		s := fmt.Sprintf("0x%x", x)
		if len(x) > 16 {
			s = fmt.Sprintf("0x%x...", x[:16])
		}
		return s
	case *structV:
		if depth > 3 {
			return "{...}"
		}
		s := "{"
		for i, e := range x.vals {
			if i > 0 {
				s += ", "
			}
			s += summarise(e, depth+1)
		}
		return s + "}"
	case *listV:
		s := "["
		for i, e := range x.items {
			if i == 4 {
				s += "..."
				break
			}
			if i > 0 {
				s += ", "
			}
			s += summarise(e, depth+1)
		}
		return s + "]"
	case *dispatchV:
		return fmt.Sprintf("dispatch(%v, %s)", x.key, summarise(x.v, depth+1))
	case *optV:
		if !x.present {
			return "opt()"
		}
		return "opt(" + summarise(x.v, depth+1) + ")"
	case *holderV:
		if x.direct != nil {
			return "holder(" + summarise(x.direct, depth+1) + ")"
		}
		return fmt.Sprintf("holder(%d)", x.id)
	case *nbtV:
		return fmt.Sprintf("nbt(tag %d)", x.tag)
	}
	return fmt.Sprintf("%v", v)
}
