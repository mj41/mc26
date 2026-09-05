package component

import (
	"io"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/nbt/dynbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

// IDSet represents a registryEntryHolderSet.
// Wire: type:VarInt. If type==0, read tag name (string). If type>0, read (type-1) VarInt IDs.
type IDSet struct {
	Type pk.VarInt
	Tag  pk.String   // only if Type == 0
	IDs  []pk.VarInt // only if Type > 0, length = Type - 1
}

func (s *IDSet) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = s.Type.ReadFrom(r)
	if err != nil {
		return
	}
	if s.Type == 0 {
		n2, err := s.Tag.ReadFrom(r)
		return n + n2, err
	}
	count := int(s.Type) - 1
	s.IDs = make([]pk.VarInt, count)
	for i := 0; i < count; i++ {
		n2, err := s.IDs[i].ReadFrom(r)
		n += n2
		if err != nil {
			return n, err
		}
	}
	return
}

func (s IDSet) WriteTo(w io.Writer) (n int64, err error) {
	n, err = s.Type.WriteTo(w)
	if err != nil {
		return
	}
	if s.Type == 0 {
		n2, err := s.Tag.WriteTo(w)
		return n + n2, err
	}
	for i := range s.IDs {
		n2, err := s.IDs[i].WriteTo(w)
		n += n2
		if err != nil {
			return n, err
		}
	}
	return
}

// ItemEffectDetail represents a potion effect detail.
// Wire: {amplifier:varint, duration:varint, ambient:bool, showParticles:bool, showIcon:bool, hiddenEffect:Option<ItemEffectDetail>}
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
	n, err = pk.Tuple{
		&d.Amplifier,
		&d.Duration,
		&d.Ambient,
		&d.ShowParticles,
		&d.ShowIcon,
		&d.HasHidden,
	}.ReadFrom(r)
	if err != nil {
		return
	}
	if d.HasHidden {
		d.HiddenEffect = new(ItemEffectDetail)
		n2, err := d.HiddenEffect.ReadFrom(r)
		n += n2
		if err != nil {
			return n, err
		}
	}
	return
}

func (d ItemEffectDetail) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Tuple{
		&d.Amplifier,
		&d.Duration,
		&d.Ambient,
		&d.ShowParticles,
		&d.ShowIcon,
		&d.HasHidden,
	}.WriteTo(w)
	if err != nil {
		return
	}
	if d.HasHidden && d.HiddenEffect != nil {
		n2, err := d.HiddenEffect.WriteTo(w)
		n += n2
		if err != nil {
			return n, err
		}
	}
	return
}

// ItemPotionEffect is {id:varint, details:ItemEffectDetail}
type ItemPotionEffect struct {
	ID      pk.VarInt
	Details ItemEffectDetail
}

func (e *ItemPotionEffect) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = e.ID.ReadFrom(r)
	if err != nil {
		return
	}
	n2, err := e.Details.ReadFrom(r)
	return n + n2, err
}

func (e ItemPotionEffect) WriteTo(w io.Writer) (n int64, err error) {
	n, err = e.ID.WriteTo(w)
	if err != nil {
		return
	}
	n2, err := e.Details.WriteTo(w)
	return n + n2, err
}

// ItemConsumeEffect represents a consume effect.
// Wire: type:VarInt then switch on type.
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
	Sound SoundEvent
}

func (e *ItemConsumeEffect) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = e.Type.ReadFrom(r)
	if err != nil {
		return
	}
	var n2 int64
	switch e.Type {
	case 0: // apply_effects
		n2, err = pk.Tuple{
			pk.Array(&e.Effects),
			&e.Probability,
		}.ReadFrom(r)
	case 1: // remove_effects
		n2, err = e.RemoveEffects.ReadFrom(r)
	case 2: // clear_all_effects — void
	case 3: // teleport_randomly
		n2, err = e.Diameter.ReadFrom(r)
	case 4: // play_sound
		n2, err = e.Sound.ReadFrom(r)
	}
	return n + n2, err
}

func (e ItemConsumeEffect) WriteTo(w io.Writer) (n int64, err error) {
	n, err = e.Type.WriteTo(w)
	if err != nil {
		return
	}
	var n2 int64
	switch e.Type {
	case 0:
		n2, err = pk.Tuple{
			pk.Array(&e.Effects),
			&e.Probability,
		}.WriteTo(w)
	case 1:
		n2, err = e.RemoveEffects.WriteTo(w)
	case 2:
	case 3:
		n2, err = e.Diameter.WriteTo(w)
	case 4:
		n2, err = e.Sound.WriteTo(w)
	}
	return n + n2, err
}

// ItemBlockProperty represents a block property matcher.
// Wire: {name:string, isExactMatch:bool, value:switch}
type ItemBlockProperty struct {
	Name         pk.String
	IsExactMatch pk.Boolean
	// if exact: exactValue:string
	ExactValue pk.String
	// if range: minValue:string, maxValue:string
	MinValue pk.String
	MaxValue pk.String
}

func (p *ItemBlockProperty) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = pk.Tuple{&p.Name, &p.IsExactMatch}.ReadFrom(r)
	if err != nil {
		return
	}
	if p.IsExactMatch {
		n2, err := p.ExactValue.ReadFrom(r)
		return n + n2, err
	}
	n2, err := pk.Tuple{&p.MinValue, &p.MaxValue}.ReadFrom(r)
	return n + n2, err
}

func (p ItemBlockProperty) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Tuple{&p.Name, &p.IsExactMatch}.WriteTo(w)
	if err != nil {
		return
	}
	if p.IsExactMatch {
		n2, err := p.ExactValue.WriteTo(w)
		return n + n2, err
	}
	n2, err := pk.Tuple{&p.MinValue, &p.MaxValue}.WriteTo(w)
	return n + n2, err
}

// SlotData is a minimal Slot representation for use in components that reference Slot
// without importing bot/screen. Wire format is the same as Slot.
type SlotData struct {
	Count        pk.VarInt
	ItemID       pk.VarInt
	AddedCount   pk.VarInt
	RemovedCount pk.VarInt
	// We store the raw bytes for component data to avoid circular imports
	RawComponents []byte
}

func (s *SlotData) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = s.Count.ReadFrom(r)
	if err != nil || s.Count <= 0 {
		return
	}
	var n2 int64
	n2, err = pk.Tuple{&s.ItemID, &s.AddedCount, &s.RemovedCount}.ReadFrom(r)
	n += n2
	if err != nil {
		return
	}
	// Read added components
	for i := int32(0); i < int32(s.AddedCount); i++ {
		var compType pk.VarInt
		n2, err = compType.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
		comp := NewComponent(int32(compType))
		if comp == nil {
			// Skip unknown component - can't continue safely
			return n, nil
		}
		n2, err = comp.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
	}
	// Read removed component IDs
	for i := int32(0); i < int32(s.RemovedCount); i++ {
		var compType pk.VarInt
		n2, err = compType.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
	}
	return
}

func (s *SlotData) WriteTo(w io.Writer) (n int64, err error) {
	n, err = s.Count.WriteTo(w)
	if err != nil || s.Count <= 0 {
		return
	}
	n2, err := pk.Tuple{
		s.ItemID,
		pk.VarInt(0), // 0 added components
		pk.VarInt(0), // 0 removed components
	}.WriteTo(w)
	return n + n2, err
}

// ItemStackTemplate is the 26.x wire form of an item reference inside components
// (bundle_contents, container, charged_projectiles, use_remainder, sulfur_cube_content).
// Unlike SlotData it is never "empty": Wire: itemID:VarInt, count:VarInt, then a
// DataComponentPatch (addedCount:VarInt, removedCount:VarInt, added components, removed IDs).
type ItemStackTemplate struct {
	ItemID       pk.VarInt
	Count        pk.VarInt
	AddedCount   pk.VarInt
	RemovedCount pk.VarInt
}

func (t *ItemStackTemplate) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = pk.Tuple{&t.ItemID, &t.Count, &t.AddedCount, &t.RemovedCount}.ReadFrom(r)
	if err != nil {
		return
	}
	var n2 int64
	for i := int32(0); i < int32(t.AddedCount); i++ {
		var compType pk.VarInt
		n2, err = compType.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
		comp := NewComponent(int32(compType))
		if comp == nil {
			// Unknown component: the remaining bytes cannot be delimited safely.
			return n, nil
		}
		n2, err = comp.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
	}
	for i := int32(0); i < int32(t.RemovedCount); i++ {
		var compType pk.VarInt
		n2, err = compType.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
	}
	return
}

func (t ItemStackTemplate) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{
		t.ItemID,
		t.Count,
		pk.VarInt(0), // 0 added components
		pk.VarInt(0), // 0 removed components
	}.WriteTo(w)
}

// ItemBlockPredicate represents a block predicate for can_place_on / can_break.
// Wire: {blockSet:Option<IDSet>, properties:Option<Array<ItemBlockProperty>>, nbt:anonOptionalNbt, components:DataComponentMatchers}
type ItemBlockPredicate struct {
	BlockSet      pk.Option[IDSet, *IDSet]
	HasProperties pk.Boolean
	Properties    []ItemBlockProperty
	NBT           dynbt.Value
	HasNBT        bool
	// DataComponentMatchers: exactMatchers (Array<SlotComponent>) + partialMatchers (Array<varint>)
	// For simplicity, store as exact component count + raw data + partial matcher IDs
	ExactMatcherCount pk.VarInt
	PartialMatchers   []pk.VarInt
}

func (p *ItemBlockPredicate) ReadFrom(r io.Reader) (n int64, err error) {
	// blockSet: Option<IDSet>
	n, err = p.BlockSet.ReadFrom(r)
	if err != nil {
		return
	}
	// properties: Option<Array<ItemBlockProperty>>
	n2, err := p.HasProperties.ReadFrom(r)
	n += n2
	if err != nil {
		return
	}
	if p.HasProperties {
		n2, err = pk.Array(&p.Properties).ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
	}
	// nbt: anonOptionalNbt — read a tag type byte; if TagEnd (0), no NBT data; else read NBT
	n2, err = pk.NBTField{V: &p.NBT, AllowUnknownFields: true}.ReadFrom(r)
	n += n2
	if err != nil {
		return
	}
	// DataComponentMatchers: exactMatchers then partialMatchers
	// exactMatchers: Array<SlotComponent> — for simplicity, read count and skip each component
	var exactCount pk.VarInt
	n2, err = exactCount.ReadFrom(r)
	n += n2
	if err != nil {
		return
	}
	for i := int32(0); i < int32(exactCount); i++ {
		var compType pk.VarInt
		n2, err = compType.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
		comp := NewComponent(int32(compType))
		if comp == nil {
			return
		}
		n2, err = comp.ReadFrom(r)
		n += n2
		if err != nil {
			return
		}
	}
	// partialMatchers: Array<varint>
	n2, err = pk.Array(&p.PartialMatchers).ReadFrom(r)
	n += n2
	return
}

func (p ItemBlockPredicate) WriteTo(w io.Writer) (n int64, err error) {
	n, err = p.BlockSet.WriteTo(w)
	if err != nil {
		return
	}
	n2, err := p.HasProperties.WriteTo(w)
	n += n2
	if err != nil {
		return
	}
	if p.HasProperties {
		n2, err = pk.Array(&p.Properties).WriteTo(w)
		n += n2
		if err != nil {
			return
		}
	}
	// NBT
	n2, err = pk.NBTField{V: &p.NBT, AllowUnknownFields: true}.WriteTo(w)
	n += n2
	if err != nil {
		return
	}
	// exact matchers — write 0 for now
	n2, err = pk.VarInt(0).WriteTo(w)
	n += n2
	if err != nil {
		return
	}
	// partial matchers
	n2, err = pk.Array(&p.PartialMatchers).WriteTo(w)
	n += n2
	return
}

// ItemFireworkExplosion wire type.
type ItemFireworkExplosion struct {
	Shape      pk.VarInt
	Colors     []pk.Int
	FadeColors []pk.Int
	HasTrail   pk.Boolean
	HasTwinkle pk.Boolean
}

func (e *ItemFireworkExplosion) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{
		&e.Shape,
		pk.Array(&e.Colors),
		pk.Array(&e.FadeColors),
		&e.HasTrail,
		&e.HasTwinkle,
	}.ReadFrom(r)
}

func (e ItemFireworkExplosion) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{
		&e.Shape,
		pk.Array(&e.Colors),
		pk.Array(&e.FadeColors),
		&e.HasTrail,
		&e.HasTwinkle,
	}.WriteTo(w)
}

// GameProfileProperty for profile component.
type GameProfileProperty struct {
	Name      pk.String
	Value     pk.String
	Signature pk.Option[pk.String, *pk.String]
}

// ArmorTrimMaterial wire type.
type ArmorTrimMaterial struct {
	AssetBase           pk.String
	OverrideArmorAssets []struct {
		Key   pk.String
		Value pk.String
	}
	Description chat.Message
}

func (m *ArmorTrimMaterial) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{
		&m.AssetBase,
		pk.Array(&m.OverrideArmorAssets),
		&m.Description,
	}.ReadFrom(r)
}

func (m ArmorTrimMaterial) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{
		&m.AssetBase,
		pk.Array(&m.OverrideArmorAssets),
		&m.Description,
	}.WriteTo(w)
}

// ArmorTrimPattern wire type.
type ArmorTrimPattern struct {
	AssetID     pk.String
	Description chat.Message
	Decal       pk.Boolean
}

func (p *ArmorTrimPattern) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{&p.AssetID, &p.Description, &p.Decal}.ReadFrom(r)
}

func (p ArmorTrimPattern) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{&p.AssetID, &p.Description, &p.Decal}.WriteTo(w)
}

// RegistryEntryHolder reads a registryEntryHolder pattern:
// type:VarInt. If type==0, read inline data. If type>0, value is type-1 (registry reference).
type RegistryEntryHolder struct {
	Type pk.VarInt
	// Inline data is read/written by the caller using Opt pattern
}

// BannerPattern wire type.
type BannerPattern struct {
	AssetID        pk.String
	TranslationKey pk.String
}

func (p *BannerPattern) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{&p.AssetID, &p.TranslationKey}.ReadFrom(r)
}

func (p BannerPattern) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{&p.AssetID, &p.TranslationKey}.WriteTo(w)
}

// SoundEvent represents an ItemSoundHolder (registryEntryHolder for sound events).
// Wire: soundId:VarInt. If 0, read inline {soundName:string, fixedRange:Option<f32>}.
// If >0, value is soundId-1 (registry reference).
type SoundEvent struct {
	Type       pk.VarInt
	SoundName  pk.Identifier
	FixedRange pk.Option[pk.Float, *pk.Float]
}

func (s *SoundEvent) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{
		&s.Type,
		pk.Opt{
			Has: func() bool { return s.Type == 0 },
			Field: pk.Tuple{
				&s.SoundName,
				&s.FixedRange,
			},
		},
	}.ReadFrom(r)
}

func (s SoundEvent) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		&s.Type,
		pk.Opt{
			Has: func() bool { return s.Type == 0 },
			Field: pk.Tuple{
				&s.SoundName,
				&s.FixedRange,
			},
		},
	}.WriteTo(w)
}

// ResolvableInt is a loot number provider reference or a constant (Minecraft
// 26.3+: ResolvableInt.STREAM_CODEC = either(INT, ResourceKey)): a boolean
// selects the constant (true, a 4-byte int) or the key (false, an identifier
// of the context_int_provider registry).
type ResolvableInt struct {
	Constant bool
	Value    int32
	Key      string
}

func (v *ResolvableInt) ReadFrom(r io.Reader) (n int64, err error) {
	var constant pk.Boolean
	if n, err = constant.ReadFrom(r); err != nil {
		return
	}
	v.Constant = bool(constant)
	var m int64
	if v.Constant {
		m, err = (*pk.Int)(&v.Value).ReadFrom(r)
	} else {
		m, err = (*pk.Identifier)(&v.Key).ReadFrom(r)
	}
	return n + m, err
}

func (v ResolvableInt) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.Boolean(v.Constant).WriteTo(w); err != nil {
		return
	}
	var m int64
	if v.Constant {
		m, err = pk.Int(v.Value).WriteTo(w)
	} else {
		m, err = pk.Identifier(v.Key).WriteTo(w)
	}
	return n + m, err
}

// ResolvableFloat is the float twin of ResolvableInt (FLOAT or a key of the
// context_float_provider registry).
type ResolvableFloat struct {
	Constant bool
	Value    float32
	Key      string
}

func (v *ResolvableFloat) ReadFrom(r io.Reader) (n int64, err error) {
	var constant pk.Boolean
	if n, err = constant.ReadFrom(r); err != nil {
		return
	}
	v.Constant = bool(constant)
	var m int64
	if v.Constant {
		m, err = (*pk.Float)(&v.Value).ReadFrom(r)
	} else {
		m, err = (*pk.Identifier)(&v.Key).ReadFrom(r)
	}
	return n + m, err
}

func (v ResolvableFloat) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.Boolean(v.Constant).WriteTo(w); err != nil {
		return
	}
	var m int64
	if v.Constant {
		m, err = pk.Float(v.Value).WriteTo(w)
	} else {
		m, err = pk.Identifier(v.Key).WriteTo(w)
	}
	return n + m, err
}
