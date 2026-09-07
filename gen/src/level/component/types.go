package component

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mj41/go-mc26/nbt/dynbt"
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

// ItemEffectDetail is a potion effect's detail (MobEffectInstance$Details),
// recursive through the optional hidden effect.
type ItemEffectDetail struct {
	Amplifier     pk.VarInt
	Duration      pk.VarInt
	Ambient       pk.Boolean
	ShowParticles pk.Boolean
	ShowIcon      pk.Boolean
	HasHidden     pk.Boolean
	HiddenEffect  *ItemEffectDetail
}

func (d *ItemEffectDetail) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = pk.Tuple{&d.Amplifier, &d.Duration, &d.Ambient, &d.ShowParticles, &d.ShowIcon, &d.HasHidden}.ReadFrom(r)
	if err != nil {
		return
	}
	if d.HasHidden {
		d.HiddenEffect = new(ItemEffectDetail)
		m, err := d.HiddenEffect.ReadFrom(r)
		return n + m, err
	}
	return
}

func (d ItemEffectDetail) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Tuple{d.Amplifier, d.Duration, d.Ambient, d.ShowParticles, d.ShowIcon, d.HasHidden}.WriteTo(w)
	if err != nil {
		return
	}
	if d.HasHidden && d.HiddenEffect != nil {
		m, err := d.HiddenEffect.WriteTo(w)
		return n + m, err
	}
	return
}

// ItemPotionEffect is a mob effect instance: the effect id and its details.
type ItemPotionEffect struct {
	ID      pk.VarInt
	Details ItemEffectDetail
}

func (e *ItemPotionEffect) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&e.ID, &e.Details}.ReadFrom(r)
}

func (e ItemPotionEffect) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{e.ID, e.Details}.WriteTo(w)
}

// ItemConsumeEffect is a ConsumeEffect: a type id, then a payload that
// depends on it (the dispatch the schema cannot type yet).
type ItemConsumeEffect struct {
	Type pk.VarInt
	// type 0: apply_effects
	Effects     []ItemPotionEffect
	Probability pk.Float
	// type 1: remove_effects
	RemoveEffects IDSet
	// type 3: teleport_randomly
	Diameter pk.Float
	// type 4: play_sound
	Sound SoundHolder
}

func (e *ItemConsumeEffect) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = e.Type.ReadFrom(r); err != nil {
		return
	}
	var m int64
	switch e.Type {
	case 0:
		m, err = pk.Tuple{pk.Array(&e.Effects), &e.Probability}.ReadFrom(r)
	case 1:
		m, err = e.RemoveEffects.ReadFrom(r)
	case 2: // clear_all_effects: no payload
	case 3:
		m, err = e.Diameter.ReadFrom(r)
	case 4:
		m, err = e.Sound.ReadFrom(r)
	}
	return n + m, err
}

func (e ItemConsumeEffect) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = e.Type.WriteTo(w); err != nil {
		return
	}
	var m int64
	switch e.Type {
	case 0:
		m, err = pk.Tuple{pk.Array(&e.Effects), e.Probability}.WriteTo(w)
	case 1:
		m, err = e.RemoveEffects.WriteTo(w)
	case 2:
	case 3:
		m, err = e.Diameter.WriteTo(w)
	case 4:
		m, err = e.Sound.WriteTo(w)
	}
	return n + m, err
}

// ItemBlockProperty is a block state property matcher: an exact value, or a
// min/max range.
type ItemBlockProperty struct {
	Name         pk.String
	IsExactMatch pk.Boolean
	ExactValue   pk.String
	MinValue     pk.String
	MaxValue     pk.String
}

func (p *ItemBlockProperty) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = (pk.Tuple{&p.Name, &p.IsExactMatch}).ReadFrom(r); err != nil {
		return
	}
	var m int64
	if p.IsExactMatch {
		m, err = p.ExactValue.ReadFrom(r)
	} else {
		m, err = pk.Tuple{&p.MinValue, &p.MaxValue}.ReadFrom(r)
	}
	return n + m, err
}

func (p ItemBlockProperty) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = (pk.Tuple{p.Name, p.IsExactMatch}).WriteTo(w); err != nil {
		return
	}
	var m int64
	if p.IsExactMatch {
		m, err = p.ExactValue.WriteTo(w)
	} else {
		m, err = pk.Tuple{p.MinValue, p.MaxValue}.WriteTo(w)
	}
	return n + m, err
}

// ItemBlockPredicate is a block predicate of can_place_on / can_break: an
// optional block set, optional property matchers, an optional NBT and the
// component matchers (exact components are decoded, partial ones kept as ids).
type ItemBlockPredicate struct {
	BlockSet        pk.Option[IDSet, *IDSet]
	HasProperties   pk.Boolean
	Properties      []ItemBlockProperty
	NBT             dynbt.Value
	ExactMatchers   []Typed
	PartialMatchers []pk.VarInt
}

func (p *ItemBlockPredicate) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = p.BlockSet.ReadFrom(r); err != nil {
		return
	}
	var m int64
	if m, err = p.HasProperties.ReadFrom(r); err != nil {
		return n + m, err
	}
	n += m
	if p.HasProperties {
		if m, err = pk.Array(&p.Properties).ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
	}
	if m, err = (pk.NBTField{V: &p.NBT, AllowUnknownFields: true}).ReadFrom(r); err != nil {
		return n + m, err
	}
	n += m
	if m, err = pk.Array(&p.ExactMatchers).ReadFrom(r); err != nil {
		return n + m, err
	}
	n += m
	m, err = pk.Array(&p.PartialMatchers).ReadFrom(r)
	return n + m, err
}

func (p ItemBlockPredicate) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = p.BlockSet.WriteTo(w); err != nil {
		return
	}
	var m int64
	if m, err = p.HasProperties.WriteTo(w); err != nil {
		return n + m, err
	}
	n += m
	if p.HasProperties {
		if m, err = pk.Array(&p.Properties).WriteTo(w); err != nil {
			return n + m, err
		}
		n += m
	}
	if m, err = (pk.NBTField{V: &p.NBT, AllowUnknownFields: true}).WriteTo(w); err != nil {
		return n + m, err
	}
	n += m
	if m, err = pk.Array(&p.ExactMatchers).WriteTo(w); err != nil {
		return n + m, err
	}
	n += m
	m, err = pk.Array(&p.PartialMatchers).WriteTo(w)
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
