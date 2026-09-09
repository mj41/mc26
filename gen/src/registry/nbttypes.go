package registry

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mj41/go-mc26/nbt"
)

// The NBT forms of the generated element types (elements_gen.go) that a Go
// struct field cannot express by itself.

// Holder is a registry element referenced by id or given inline
// (RegistryFileCodec): a string tag holds the id, any other tag the element.
type Holder[T any] struct {
	ID    string // the element's id, when referenced
	Value *T     // the element itself, when inline
}

func (h Holder[T]) TagType() byte {
	if h.Value == nil {
		return nbt.TagString
	}
	t, _, err := marshalPayload(h.Value)
	if err != nil {
		return nbt.TagCompound
	}
	return t
}

func (h Holder[T]) MarshalNBT(w io.Writer) error {
	if h.Value == nil {
		return writeString(w, h.ID)
	}
	_, payload, err := marshalPayload(h.Value)
	if err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func (h *Holder[T]) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	var raw nbt.RawMessage
	if err := raw.UnmarshalNBT(tagType, r); err != nil {
		return err
	}
	if tagType == nbt.TagString {
		h.Value = nil
		return raw.Unmarshal(&h.ID)
	}
	h.ID = ""
	h.Value = new(T)
	return raw.Unmarshal(h.Value)
}

// HolderSet is a set of registry elements (RegistryCodecs.homogeneousList):
// a tag ("#minecraft:infiniburn_overworld"), a single id, or a list of ids.
type HolderSet struct {
	Tag string   // the tag name without '#', when the set is a tag
	IDs []string // the element ids otherwise
}

func (s HolderSet) TagType() byte {
	if s.Tag != "" || len(s.IDs) == 1 {
		return nbt.TagString
	}
	return nbt.TagList
}

func (s HolderSet) MarshalNBT(w io.Writer) error {
	if s.Tag != "" {
		return writeString(w, "#"+s.Tag)
	}
	if len(s.IDs) == 1 {
		return writeString(w, s.IDs[0])
	}
	_, payload, err := marshalPayload(s.IDs)
	if err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func (s *HolderSet) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	var raw nbt.RawMessage
	if err := raw.UnmarshalNBT(tagType, r); err != nil {
		return err
	}
	*s = HolderSet{}
	switch tagType {
	case nbt.TagString:
		var v string
		if err := raw.Unmarshal(&v); err != nil {
			return err
		}
		if strings.HasPrefix(v, "#") {
			s.Tag = v[1:]
		} else {
			s.IDs = []string{v}
		}
		return nil
	case nbt.TagList:
		return raw.Unmarshal(&s.IDs)
	}
	return fmt.Errorf("registry: holder set must be a string or a list, got tag %d", tagType)
}

// Either is a value one of two codecs reads (Codec.either): the first side
// that decodes it. Left is taken when the tag decodes as L without an unknown
// key (a string for a string side, a compound with only L's keys for a record),
// Right otherwise; exactly one of the two is set.
type Either[L, R any] struct {
	Left  *L
	Right *R
}

func (e Either[L, R]) side() any {
	if e.Left != nil {
		return e.Left
	}
	return e.Right
}

func (e Either[L, R]) TagType() byte {
	t, _, err := marshalPayload(e.side())
	if err != nil {
		return nbt.TagCompound
	}
	return t
}

func (e Either[L, R]) MarshalNBT(w io.Writer) error {
	_, payload, err := marshalPayload(e.side())
	if err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func (e *Either[L, R]) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	var raw nbt.RawMessage
	if err := raw.UnmarshalNBT(tagType, r); err != nil {
		return err
	}
	l := new(L)
	if err := raw.UnmarshalDisallowUnknownField(l); err == nil {
		e.Left, e.Right = l, nil
		return nil
	}
	rv := new(R)
	if err := raw.Unmarshal(rv); err != nil {
		return fmt.Errorf("registry: either %T: neither side reads the tag: %w", e, err)
	}
	e.Left, e.Right = nil, rv
	return nil
}

// Color is an RGB colour (ExtraCodecs.STRING_RGB_COLOR): written as "#rrggbb",
// read from that or from an int.
type Color int32

func (c Color) TagType() byte { return nbt.TagString }

func (c Color) MarshalNBT(w io.Writer) error {
	return writeString(w, fmt.Sprintf("#%06x", uint32(c)&0xffffff))
}

func (c *Color) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	var raw nbt.RawMessage
	if err := raw.UnmarshalNBT(tagType, r); err != nil {
		return err
	}
	switch tagType {
	case nbt.TagInt:
		var v int32
		if err := raw.Unmarshal(&v); err != nil {
			return err
		}
		*c = Color(v)
		return nil
	case nbt.TagString:
		var v string
		if err := raw.Unmarshal(&v); err != nil {
			return err
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(v, "#"), 16, 32)
		if err != nil {
			return fmt.Errorf("registry: colour %q: %w", v, err)
		}
		*c = Color(n)
		return nil
	}
	return errors.New("registry: colour must be an int or a string")
}

// marshalPayload encodes v as an NBT tag and returns its type and payload
// (Marshal writes type, an empty name and the payload).
func marshalPayload(v any) (byte, []byte, error) {
	data, err := nbt.Marshal(v)
	if err != nil {
		return 0, nil, err
	}
	if len(data) < 3 {
		return 0, nil, errors.New("registry: short NBT")
	}
	return data[0], data[3:], nil
}

func writeString(w io.Writer, s string) error {
	var buf bytes.Buffer
	if len(s) > 0xffff {
		return errors.New("registry: string too long for NBT")
	}
	buf.Write([]byte{byte(len(s) >> 8), byte(len(s))})
	buf.WriteString(s)
	_, err := w.Write(buf.Bytes())
	return err
}
