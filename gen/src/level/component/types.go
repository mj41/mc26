package component

import (
	"bytes"
	"fmt"
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/wire"
)

// IDSet is a HolderSet on the wire (a tag name or a list of registry ids).
type IDSet = wire.IDSet

// SoundHolder is a sound event by registry id, or an inline SoundEvent.
type SoundHolder = wire.Holder[SoundEvent, *SoundEvent]

// Typed is one typed data component (TypedDataComponent): the component type
// id and its value, decoded through NewComponent.
type Typed struct {
	Type  pk.VarInt
	Value DataComponent
}

func (t *Typed) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = t.Type.ReadFrom(r); err != nil {
		return
	}
	t.Value = NewComponent(int32(t.Type))
	if t.Value == nil {
		return n, io.ErrUnexpectedEOF // an unknown component cannot be delimited
	}
	m, err := t.Value.ReadFrom(r)
	return n + m, err
}

func (t Typed) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = t.Type.WriteTo(w); err != nil {
		return
	}
	v := t.Value
	if v == nil {
		if v = NewComponent(int32(t.Type)); v == nil {
			return n, io.ErrUnexpectedEOF
		}
	}
	m, err := v.WriteTo(w)
	return n + m, err
}

// Patch is a DataComponentPatch: added components (type id + value) and removed
// component type ids.
type Patch struct {
	Added   []Typed
	Removed []pk.VarInt
}

func (p *Patch) ReadFrom(r io.Reader) (n int64, err error) {
	var added, removed pk.VarInt
	if n, err = (pk.Tuple{&added, &removed}).ReadFrom(r); err != nil {
		return
	}
	p.Added = make([]Typed, int(added))
	for i := range p.Added {
		var m int64
		m, err = p.Added[i].ReadFrom(r)
		n += m
		if err != nil {
			return
		}
	}
	p.Removed = make([]pk.VarInt, int(removed))
	for i := range p.Removed {
		var m int64
		m, err = p.Removed[i].ReadFrom(r)
		n += m
		if err != nil {
			return
		}
	}
	return
}

func (p Patch) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = (pk.Tuple{pk.VarInt(len(p.Added)), pk.VarInt(len(p.Removed))}).WriteTo(w); err != nil {
		return
	}
	for _, c := range p.Added {
		var m int64
		m, err = c.WriteTo(w)
		n += m
		if err != nil {
			return
		}
	}
	for _, id := range p.Removed {
		var m int64
		m, err = id.WriteTo(w)
		n += m
		if err != nil {
			return
		}
	}
	return
}

// SlotData is an item stack on the wire (ItemStack.STREAM_CODEC): a count, and
// when it is not zero the item id and the component patch.
// DelimitedTyped is one typed data component with its value's length in front
// of it (DataComponentPatch.DELIMITED_STREAM_CODEC): the length lets a reader
// step over a component it does not know instead of losing the rest of the
// packet. An unknown component keeps its bytes so writing it back reproduces
// what arrived.
type DelimitedTyped struct {
	Type  pk.VarInt
	Value DataComponent
	Raw   []byte // set when Value is nil: a component this version does not know
}

func (t *DelimitedTyped) ReadFrom(r io.Reader) (n int64, err error) {
	var size pk.VarInt
	if n, err = (pk.Tuple{&t.Type, &size}).ReadFrom(r); err != nil {
		return
	}
	if size < 0 {
		return n, fmt.Errorf("component %d: negative length %d", t.Type, size)
	}
	body := make([]byte, int(size))
	m, err := io.ReadFull(r, body)
	n += int64(m)
	if err != nil {
		return n, err
	}
	t.Value, t.Raw = NewComponent(int32(t.Type)), nil
	if t.Value == nil {
		t.Raw = body
		return n, nil
	}
	if _, err := t.Value.ReadFrom(bytes.NewReader(body)); err != nil {
		return n, fmt.Errorf("component %d: %w", t.Type, err)
	}
	return n, nil
}

func (t DelimitedTyped) WriteTo(w io.Writer) (n int64, err error) {
	body := t.Raw
	if t.Value != nil {
		var buf bytes.Buffer
		if _, err = t.Value.WriteTo(&buf); err != nil {
			return 0, err
		}
		body = buf.Bytes()
	}
	if n, err = (pk.Tuple{t.Type, pk.VarInt(len(body))}).WriteTo(w); err != nil {
		return
	}
	m, err := w.Write(body)
	return n + int64(m), err
}

// DelimitedPatch is a DataComponentPatch whose added components carry their
// lengths (DELIMITED_STREAM_CODEC). It is what a client sends in the creative
// mode slot packet, where the server does not trust the components to be ones
// it knows.
type DelimitedPatch struct {
	Added   []DelimitedTyped
	Removed []pk.VarInt
}

func (p *DelimitedPatch) ReadFrom(r io.Reader) (n int64, err error) {
	var added, removed pk.VarInt
	if n, err = (pk.Tuple{&added, &removed}).ReadFrom(r); err != nil {
		return
	}
	p.Added = make([]DelimitedTyped, int(added))
	p.Removed = make([]pk.VarInt, int(removed))
	for i := range p.Added {
		var m int64
		if m, err = p.Added[i].ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
	}
	for i := range p.Removed {
		var m int64
		if m, err = p.Removed[i].ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
	}
	return n, nil
}

func (p DelimitedPatch) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = (pk.Tuple{pk.VarInt(len(p.Added)), pk.VarInt(len(p.Removed))}).WriteTo(w); err != nil {
		return
	}
	for _, a := range p.Added {
		var m int64
		if m, err = a.WriteTo(w); err != nil {
			return n + m, err
		}
		n += m
	}
	for _, id := range p.Removed {
		var m int64
		if m, err = id.WriteTo(w); err != nil {
			return n + m, err
		}
		n += m
	}
	return n, nil
}

// UntrustedSlotData is an ItemStack whose components are delimited
// (ItemStack.OPTIONAL_UNTRUSTED_STREAM_CODEC), which is the form a client sends
// rather than one it receives.
type UntrustedSlotData struct {
	Count      pk.VarInt
	ItemID     pk.VarInt
	Components DelimitedPatch
}

func (s *UntrustedSlotData) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = s.Count.ReadFrom(r); err != nil || s.Count <= 0 {
		s.ItemID, s.Components = 0, DelimitedPatch{}
		return
	}
	m, err := pk.Tuple{&s.ItemID, &s.Components}.ReadFrom(r)
	return n + m, err
}

func (s *UntrustedSlotData) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = s.Count.WriteTo(w); err != nil || s.Count <= 0 {
		return
	}
	m, err := pk.Tuple{s.ItemID, s.Components}.WriteTo(w)
	return n + m, err
}

type SlotData struct {
	Count      pk.VarInt
	ItemID     pk.VarInt
	Components Patch
}

func (s *SlotData) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = s.Count.ReadFrom(r); err != nil || s.Count <= 0 {
		s.ItemID, s.Components = 0, Patch{}
		return
	}
	m, err := pk.Tuple{&s.ItemID, &s.Components}.ReadFrom(r)
	return n + m, err
}

func (s *SlotData) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = s.Count.WriteTo(w); err != nil || s.Count <= 0 {
		return
	}
	m, err := pk.Tuple{s.ItemID, s.Components}.WriteTo(w)
	return n + m, err
}

// GameProfileProperty is one property of a game profile.
type GameProfileProperty struct {
	Name      pk.String
	Value     pk.String
	Signature pk.Option[pk.String, *pk.String]
}

func (p *GameProfileProperty) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.Name, &p.Value, &p.Signature}.ReadFrom(r)
}

func (p GameProfileProperty) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.Name, p.Value, p.Signature}.WriteTo(w)
}
