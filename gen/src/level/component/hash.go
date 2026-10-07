package component

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"sort"
	"unicode/utf16"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/registryid"
	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/wire"
	"github.com/mj41/go-mc26/yggdrasil/user"
)

// The hash a client sends for a component of a stack's patch in a container
// click (HashedPatchMap): CRC32C over the component as its codec encodes it
// with HashOps — a tag byte, then the value, little-endian as Guava's hasher
// writes it. A map hashes its entries sorted by the hashes of their keys, a
// list its elements in order.
//
// The library reads components in their network encoding; their codec form
// is rebuilt here per component, field by field as the codec writes it: a
// record's optional fields left out when absent or at their default, enums by
// their serialized names, registry entries by name. Texts and custom data are
// NBT on the wire in the very shape their codec gives, and are hashed from it.

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// HashOps' tags.
const (
	hashTagMapStart       = 2
	hashTagMapEnd         = 3
	hashTagListStart      = 4
	hashTagListEnd        = 5
	hashTagByte           = 6
	hashTagShort          = 7
	hashTagInt            = 8
	hashTagLong           = 9
	hashTagFloat          = 10
	hashTagDouble         = 11
	hashTagString         = 12
	hashTagBoolean        = 13
	hashTagByteArrayStart = 14
	hashTagByteArrayEnd   = 15
	hashTagIntArrayStart  = 16
	hashTagIntArrayEnd    = 17
	hashTagLongArrayStart = 18
	hashTagLongArrayEnd   = 19
)

func hashBytes(b []byte) int32 { return int32(crc32.Checksum(b, castagnoli)) }

func hashByte(v int8) int32 { return hashBytes([]byte{hashTagByte, byte(v)}) }

func hashShort(v int16) int32 {
	return hashBytes(binary.LittleEndian.AppendUint16([]byte{hashTagShort}, uint16(v)))
}

func hashInt(v int32) int32 {
	return hashBytes(binary.LittleEndian.AppendUint32([]byte{hashTagInt}, uint32(v)))
}

func hashLong(v int64) int32 {
	return hashBytes(binary.LittleEndian.AppendUint64([]byte{hashTagLong}, uint64(v)))
}

func hashFloat(v float32) int32 {
	return hashBytes(binary.LittleEndian.AppendUint32([]byte{hashTagFloat}, math.Float32bits(v)))
}

func hashDouble(v float64) int32 {
	return hashBytes(binary.LittleEndian.AppendUint64([]byte{hashTagDouble}, math.Float64bits(v)))
}

func hashBool(v bool) int32 {
	b := [2]byte{hashTagBoolean, 0}
	if v {
		b[1] = 1
	}
	return hashBytes(b[:])
}

// hashString is HashOps.createString: the tag, the length in UTF-16 units,
// the UTF-16 units little-endian (Guava's putUnencodedChars).
func hashString(v string) int32 { return hashUTF16(utf16.Encode([]rune(v))) }

func hashUTF16(units []uint16) int32 {
	b := make([]byte, 5, 5+2*len(units))
	b[0] = hashTagString
	binary.LittleEndian.PutUint32(b[1:], uint32(len(units)))
	for _, u := range units {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return hashBytes(b)
}

// hashMap is HashOps.createMap: the entries' key and value hashes, sorted by
// the key hash (unsigned, as padToLong compares) then the value hash, between
// the map's start and end tags; a hash is its four bytes little-endian.
func hashMap(entries [][2]int32) int32 {
	sort.Slice(entries, func(i, j int) bool {
		ki, kj := uint32(entries[i][0]), uint32(entries[j][0])
		if ki != kj {
			return ki < kj
		}
		return uint32(entries[i][1]) < uint32(entries[j][1])
	})
	b := []byte{hashTagMapStart}
	for _, e := range entries {
		b = binary.LittleEndian.AppendUint32(b, uint32(e[0]))
		b = binary.LittleEndian.AppendUint32(b, uint32(e[1]))
	}
	return hashBytes(append(b, hashTagMapEnd))
}

// hashList is HashOps.createList: the elements' hashes in order.
func hashList(elems []int32) int32 {
	b := []byte{hashTagListStart}
	for _, e := range elems {
		b = binary.LittleEndian.AppendUint32(b, uint32(e))
	}
	return hashBytes(append(b, hashTagListEnd))
}

// hashUnit is a unit codec's value: an empty map.
func hashUnit() int32 { return hashBytes([]byte{hashTagMapStart, hashTagMapEnd}) }

// record collects a record codec's fields.
type record [][2]int32

func (r *record) put(name string, h int32) { *r = append(*r, [2]int32{hashString(name), h}) }
func (r record) hash() int32               { return hashMap(r) }

// Names gives the name of an entry of a registry the server sent
// (minecraft:enchantment), which a component holds by id on the wire and by
// name in its codec.
type Names func(registry string, id int32) (string, bool)

// StaticNames names the entries of the registries built into the game, which
// a server does not send (items, effects, potions, component types, attributes).
func StaticNames(registry string, id int32) (string, bool) {
	var l []string
	switch registry {
	case "minecraft:item":
		l = registryid.Item
	case "minecraft:mob_effect":
		l = registryid.MobEffect
	case "minecraft:potion":
		l = registryid.Potion
	case "minecraft:data_component_type":
		l = registryid.DataComponentType
	case "minecraft:attribute":
		l = registryid.Attribute
	}
	if id < 0 || int(id) >= len(l) {
		return "", false
	}
	return l[id], true
}

// Hash returns the hash of a component's value as a container click carries
// it, and false for a component it cannot encode — one that names an entry of
// a registry the server sent (HashWith), or whose codec is not written here:
// a click with such a stack is answered by the server sending the slot.
func Hash(c DataComponent) (int32, bool) { return HashWith(c, nil) }

// HashWith is Hash with the names of the entries of the registries the server
// sent; the built-in registries are named by StaticNames.
func HashWith(c DataComponent, names Names) (int32, bool) {
	h := hasher{names: names, ok: true}
	v := h.component(c)
	return v, h.ok
}

type hasher struct {
	names Names
	ok    bool
}

func (h *hasher) fail() int32 {
	h.ok = false
	return 0
}

// name is a registry entry by its name.
func (h *hasher) name(registry string, id int32) int32 {
	if h.names != nil {
		if n, ok := h.names(registry, id); ok {
			return hashString(n)
		}
	}
	if n, ok := StaticNames(registry, id); ok {
		return hashString(n)
	}
	return h.fail()
}

// holder is a registry entry a holder refers to; one given inline has no name.
func holder[D pk.FieldEncoder, PD wire.Ptr[D]](h *hasher, registry string, v wire.Holder[D, PD]) int32 {
	if v.ID == 0 {
		return h.fail()
	}
	return h.name(registry, int32(v.ID)-1)
}

func (h *hasher) enum(name string) int32 {
	if name == "" {
		return h.fail()
	}
	return hashString(name)
}

func (h *hasher) component(c DataComponent) int32 {
	switch v := c.(type) {
	case *Damage:
		return hashInt(int32(v.Value))
	case *RepairCost:
		return hashInt(int32(v.Value))
	case *MaxDamage:
		return hashInt(int32(v.Value))
	case *MaxStackSize:
		return hashInt(int32(v.Value))
	case *MapID:
		return hashInt(int32(v.Value))
	case *Unbreakable:
		return hashUnit()
	case *EnchantmentGlintOverride:
		return hashBool(bool(v.Value))
	case *Enchantments:
		return h.levels(v.Enchantments)
	case *StoredEnchantments:
		return h.levels(v.Enchantments)
	case *CustomName:
		return h.text(v.Value)
	case *ItemName:
		return h.text(v.Value)
	case *Lore:
		var l []int32
		for _, m := range v.Value {
			l = append(l, h.text(m))
		}
		return hashList(l)
	case *Rarity:
		return h.enum(v.Name())
	case *BaseColor:
		return h.enum(v.Value.Name())
	case *DyedColor:
		return hashInt(int32(v.Rgb))
	case *CustomData:
		return h.nbt(v.Value.RawMessage, false)
	case *TooltipDisplay:
		var r record
		if v.HideTooltip {
			r.put("hide_tooltip", hashBool(true))
		}
		if len(v.HiddenComponents) > 0 {
			var l []int32
			for _, id := range v.HiddenComponents {
				l = append(l, h.name("minecraft:data_component_type", int32(id)))
			}
			r.put("hidden_components", hashList(l))
		}
		return r.hash()
	case *WrittenBookContent:
		var r record
		r.put("title", h.filterable(hashString(string(v.Title.Raw)), bool(v.Title.Filtered.Has), string(v.Title.Filtered.Val)))
		r.put("author", hashString(string(v.Author)))
		if v.Generation != 0 {
			r.put("generation", hashInt(int32(v.Generation)))
		}
		if len(v.Pages) > 0 {
			var l []int32
			for _, p := range v.Pages {
				var fr record
				fr.put("raw", h.text(p.Raw))
				if p.Filtered.Has {
					fr.put("filtered", h.text(p.Filtered.Val))
				}
				l = append(l, fr.hash())
			}
			r.put("pages", hashList(l))
		}
		if v.Resolved {
			r.put("resolved", hashBool(true))
		}
		return r.hash()
	case *WritableBookContent:
		var r record
		if len(v.Value) > 0 {
			var l []int32
			for _, p := range v.Value {
				l = append(l, h.filterable(hashString(string(p.Raw)), bool(p.Filtered.Has), string(p.Filtered.Val)))
			}
			r.put("pages", hashList(l))
		}
		return r.hash()
	case *Fireworks:
		var r record
		if v.FlightDuration != 0 {
			r.put("flight_duration", hashByte(int8(v.FlightDuration)))
		}
		if len(v.Explosions) > 0 {
			var l []int32
			for _, e := range v.Explosions {
				l = append(l, h.explosion(FireworkExplosion(e)))
			}
			r.put("explosions", hashList(l))
		}
		return r.hash()
	case *FireworkExplosion:
		return h.explosion(*v)
	case *PotionContents:
		var r record
		if v.Potion.Has {
			r.put("potion", h.name("minecraft:potion", int32(v.Potion.Val)))
		}
		if v.CustomColor.Has {
			r.put("custom_color", hashInt(int32(v.CustomColor.Val)))
		}
		if len(v.CustomEffects) > 0 {
			var l []int32
			for _, e := range v.CustomEffects {
				er := h.details(e.AsDetails)
				er.put("id", h.name("minecraft:mob_effect", int32(e.Effect)))
				l = append(l, er.hash())
			}
			r.put("custom_effects", hashList(l))
		}
		if v.CustomName.Has {
			r.put("custom_name", hashString(string(v.CustomName.Val)))
		}
		return r.hash()
	case *SuspiciousStewEffects:
		var l []int32
		for _, e := range v.Value {
			var r record
			r.put("id", h.name("minecraft:mob_effect", int32(e.Effect)))
			if e.Duration != 160 {
				r.put("duration", hashInt(int32(e.Duration)))
			}
			l = append(l, r.hash())
		}
		return hashList(l)
	case *Trim:
		var r record
		r.put("material", holder(h, "minecraft:trim_material", v.Material))
		r.put("pattern", holder(h, "minecraft:trim_pattern", v.Pattern))
		return r.hash()
	case *Instrument:
		return holder(h, "minecraft:instrument", v.Value)
	case *BannerPatterns:
		var l []int32
		for _, layer := range v.Value {
			var r record
			r.put("pattern", holder(h, "minecraft:banner_pattern", layer.Pattern))
			r.put("color", h.enum(layer.Color.Name()))
			l = append(l, r.hash())
		}
		return hashList(l)
	case *Profile:
		return h.profile(v)
	case *LodestoneTracker:
		var r record
		if v.Target.Has {
			var t record
			t.put("dimension", hashString(string(v.Target.Val.Dimension)))
			p := v.Target.Val.Pos
			t.put("pos", hashInts([]int32{int32(p.X), int32(p.Y), int32(p.Z)}))
			r.put("target", t.hash())
		}
		if !v.Tracked {
			r.put("tracked", hashBool(false))
		}
		return r.hash()
	case *BundleContents:
		var l []int32
		for _, s := range v.Value {
			l = append(l, h.stack(s))
		}
		return hashList(l)
	case *ChargedProjectiles:
		var l []int32
		for _, s := range v.Value {
			l = append(l, h.stack(s))
		}
		return hashList(l)
	case *Container:
		var l []int32
		for i, s := range v.Value {
			if !s.Has || s.Val.Count <= 0 {
				continue
			}
			var r record
			r.put("slot", hashInt(int32(i)))
			r.put("item", h.stack(s.Val))
			l = append(l, r.hash())
		}
		return hashList(l)
	case *AttributeModifiers:
		var l []int32
		for _, e := range v.Modifiers {
			var r record
			r.put("type", h.name("minecraft:attribute", int32(e.Attribute)))
			r.put("id", hashString(string(e.Modifier.ID)))
			r.put("amount", hashDouble(float64(e.Modifier.Amount)))
			r.put("operation", h.enum(e.Modifier.Operation.Name()))
			if e.Slot != EquipmentSlotGroupAny {
				r.put("slot", h.enum(e.Slot.Name()))
			}
			switch e.Display.Type {
			case ItemAttributeModifiersDisplayTypeDefault:
			case ItemAttributeModifiersDisplayTypeOverride:
				var d record
				d.put("type", hashString("override"))
				d.put("value", h.text(e.Display.Component))
				r.put("display", d.hash())
			case ItemAttributeModifiersDisplayTypeHidden:
				var d record
				d.put("type", hashString("hidden"))
				r.put("display", d.hash())
			default:
				h.fail()
			}
			l = append(l, r.hash())
		}
		return hashList(l)
	case *CustomModelData:
		var r record
		if len(v.Floats) > 0 {
			var l []int32
			for _, f := range v.Floats {
				l = append(l, hashFloat(float32(f)))
			}
			r.put("floats", hashList(l))
		}
		if len(v.Flags) > 0 {
			var l []int32
			for _, f := range v.Flags {
				l = append(l, hashBool(bool(f)))
			}
			r.put("flags", hashList(l))
		}
		if len(v.Strings) > 0 {
			var l []int32
			for _, s := range v.Strings {
				l = append(l, hashString(string(s)))
			}
			r.put("strings", hashList(l))
		}
		if len(v.Colors) > 0 {
			var l []int32
			for _, c := range v.Colors {
				l = append(l, hashInt(int32(c)))
			}
			r.put("colors", hashList(l))
		}
		return r.hash()
	}
	return h.fail()
}

// levels is an enchantment-to-level map.
func (h *hasher) levels(es []wire.Entry[pk.VarInt, pk.VarInt]) int32 {
	var m [][2]int32
	for _, e := range es {
		m = append(m, [2]int32{h.name("minecraft:enchantment", int32(e.Key)), hashInt(int32(e.Val))})
	}
	return hashMap(m)
}

// filterable is Filterable's full codec: the raw value and, when it has one,
// the filtered.
func (h *hasher) filterable(raw int32, hasFiltered bool, filtered string) int32 {
	var r record
	r.put("raw", raw)
	if hasFiltered {
		r.put("filtered", hashString(filtered))
	}
	return r.hash()
}

func (h *hasher) explosion(e FireworkExplosion) int32 {
	var r record
	r.put("shape", h.enum(e.Shape.Name()))
	ints := func(name string, l []pk.Int) {
		if len(l) == 0 {
			return
		}
		var hs []int32
		for _, c := range l {
			hs = append(hs, hashInt(int32(c)))
		}
		r.put(name, hashList(hs))
	}
	ints("colors", e.Colors)
	ints("fade_colors", e.FadeColors)
	if e.HasTrail {
		r.put("has_trail", hashBool(true))
	}
	if e.HasTwinkle {
		r.put("has_twinkle", hashBool(true))
	}
	return r.hash()
}

// details is MobEffectInstance.Details' map codec: the fields an effect
// instance writes beside its id.
func (h *hasher) details(d MobEffectInstanceDetails) record {
	var r record
	if d.Amplifier != 0 {
		r.put("amplifier", hashByte(int8(d.Amplifier)))
	}
	if d.Duration != 0 {
		r.put("duration", hashInt(int32(d.Duration)))
	}
	if d.Ambient {
		r.put("ambient", hashBool(true))
	}
	if !d.ShowParticles {
		r.put("show_particles", hashBool(false))
	}
	r.put("show_icon", hashBool(bool(d.ShowIcon)))
	if d.HiddenEffect.Has && d.HiddenEffect.Val.V != nil {
		r.put("hidden_effect", h.details(*d.HiddenEffect.Val.V).hash())
	}
	return r
}

// hashInts is HashOps.createIntList (an IntStream codec: a block position, a UUID).
func hashInts(v []int32) int32 {
	b := []byte{hashTagIntArrayStart}
	for _, i := range v {
		b = binary.LittleEndian.AppendUint32(b, uint32(i))
	}
	return hashBytes(append(b, hashTagIntArrayEnd))
}

func hashUUID(u [16]byte) int32 {
	var v [4]int32
	for i := range v {
		v[i] = int32(binary.BigEndian.Uint32(u[4*i:]))
	}
	return hashInts(v[:])
}

// profile is ResolvableProfile's full codec: a resolved game profile or the
// parts given, and the skin patch, in one map.
func (h *hasher) profile(v *Profile) int32 {
	var r record
	properties := func(ps []user.Property) {
		if len(ps) == 0 {
			return
		}
		var l []int32
		for _, p := range ps {
			var pr record
			pr.put("name", hashString(p.Name))
			pr.put("value", hashString(p.Value))
			if p.Signature != "" {
				pr.put("signature", hashString(p.Signature))
			}
			l = append(l, pr.hash())
		}
		r.put("properties", hashList(l))
	}
	if v.Unpack.IsLeft {
		g := v.Unpack.Left
		r.put("id", hashUUID(g.ID))
		r.put("name", hashString(string(g.Name)))
		properties(g.Properties)
	} else {
		p := v.Unpack.Right
		if p.Name.Has {
			r.put("name", hashString(string(p.Name.Val)))
		}
		if p.ID.Has {
			r.put("id", hashUUID(p.ID.Val))
		}
		properties(p.Properties)
	}
	s := v.SkinPatch
	for _, t := range []struct {
		name string
		v    pk.Option[pk.Identifier, *pk.Identifier]
	}{{"texture", s.Body}, {"cape", s.Cape}, {"elytra", s.Elytra}} {
		if t.v.Has {
			r.put(t.name, hashString(string(t.v.Val)))
		}
	}
	if s.Model.Has {
		model := "wide"
		if s.Model.Val {
			model = "slim"
		}
		r.put("model", hashString(model))
	}
	return r.hash()
}

// stack is ItemStackTemplate's map codec: the item, its count unless one, its
// patch unless empty.
func (h *hasher) stack(s ItemStackTemplate) int32 {
	var r record
	r.put("id", h.name("minecraft:item", int32(s.Item)))
	if s.Count != 1 {
		r.put("count", hashInt(int32(s.Count)))
	}
	if len(s.Components.Positive)+len(s.Components.Negative) > 0 {
		var m [][2]int32
		for _, c := range s.Components.Positive {
			name, ok := StaticNames("minecraft:data_component_type", int32(c.Type))
			if !ok {
				return h.fail()
			}
			m = append(m, [2]int32{hashString(name), h.component(c.Value)})
		}
		for _, id := range s.Components.Negative {
			name, ok := StaticNames("minecraft:data_component_type", int32(id))
			if !ok {
				return h.fail()
			}
			m = append(m, [2]int32{hashString("!" + name), hashUnit()})
		}
		r.put("components", hashMap(m))
	}
	return r.hash()
}

// text is a text component: the NBT it was read from, which is its codec's
// form; a message made in Go is hashed when it is plain text.
func (h *hasher) text(m chat.Message) int32 {
	if raw := m.Raw(); raw.Type != 0 {
		return h.nbt(raw, true)
	}
	if m.Translate == "" && len(m.With) == 0 && len(m.Extra) == 0 &&
		m.Color == "" && m.Font == "" && m.Insertion == "" && m.ClickEvent == nil && m.HoverEvent == nil &&
		!m.Bold && !m.Italic && !m.UnderLined && !m.StrikeThrough && !m.Obfuscated {
		return hashString(m.Text)
	}
	return h.fail()
}

// nbt hashes an NBT value as NbtOps hands it to another DynamicOps. A list
// element wrapped in a compound under the empty key is the element, in every
// list (ListTag unwraps it as it loads); in a text (asText), a byte under a
// style key is the boolean its codec writes.
func (h *hasher) nbt(raw nbt.RawMessage, asText bool) int32 {
	if raw.Type == 0 {
		return h.fail()
	}
	r := bytes.NewReader(raw.Data)
	v, err := hashTag(r, raw.Type, "", asText)
	if err != nil || r.Len() != 0 {
		return h.fail()
	}
	return v
}

// textBooleans are the keys of a text component whose values are booleans.
var textBooleans = map[string]bool{"bold": true, "italic": true, "underlined": true, "strikethrough": true, "obfuscated": true, "interpret": true}

var errNBT = errors.New("component: bad NBT")

func hashTag(r *bytes.Reader, typ byte, key string, asText bool) (int32, error) {
	read := func(n int) ([]byte, error) {
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		return b, nil
	}
	length := func() (int, error) {
		b, err := read(4)
		if err != nil {
			return 0, err
		}
		n := int32(binary.BigEndian.Uint32(b))
		if n < 0 || int(n) > r.Len() {
			return 0, errNBT
		}
		return int(n), nil
	}
	str := func() ([]uint16, error) {
		b, err := read(2)
		if err != nil {
			return nil, err
		}
		if b, err = read(int(binary.BigEndian.Uint16(b))); err != nil {
			return nil, err
		}
		return decodeMUTF8(b), nil
	}
	switch typ {
	case nbt.TagByte:
		b, err := read(1)
		if err != nil {
			return 0, err
		}
		if asText && textBooleans[key] {
			return hashBool(b[0] != 0), nil
		}
		return hashByte(int8(b[0])), nil
	case nbt.TagShort:
		b, err := read(2)
		if err != nil {
			return 0, err
		}
		return hashShort(int16(binary.BigEndian.Uint16(b))), nil
	case nbt.TagInt:
		b, err := read(4)
		if err != nil {
			return 0, err
		}
		return hashInt(int32(binary.BigEndian.Uint32(b))), nil
	case nbt.TagLong:
		b, err := read(8)
		if err != nil {
			return 0, err
		}
		return hashLong(int64(binary.BigEndian.Uint64(b))), nil
	case nbt.TagFloat:
		b, err := read(4)
		if err != nil {
			return 0, err
		}
		return hashFloat(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
	case nbt.TagDouble:
		b, err := read(8)
		if err != nil {
			return 0, err
		}
		return hashDouble(math.Float64frombits(binary.BigEndian.Uint64(b))), nil
	case nbt.TagString:
		s, err := str()
		if err != nil {
			return 0, err
		}
		return hashUTF16(s), nil
	case nbt.TagByteArray:
		n, err := length()
		if err != nil {
			return 0, err
		}
		b, err := read(n)
		if err != nil {
			return 0, err
		}
		return hashBytes(append(append([]byte{hashTagByteArrayStart}, b...), hashTagByteArrayEnd)), nil
	case nbt.TagIntArray, nbt.TagLongArray:
		n, err := length()
		if err != nil {
			return 0, err
		}
		size, start, end := 4, byte(hashTagIntArrayStart), byte(hashTagIntArrayEnd)
		if typ == nbt.TagLongArray {
			size, start, end = 8, hashTagLongArrayStart, hashTagLongArrayEnd
		}
		b, err := read(n * size)
		if err != nil {
			return 0, err
		}
		out := []byte{start}
		for i := 0; i < len(b); i += size {
			for j := size - 1; j >= 0; j-- { // big-endian to little-endian
				out = append(out, b[i+j])
			}
		}
		return hashBytes(append(out, end)), nil
	case nbt.TagList:
		et, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		n, err := length()
		if err != nil {
			return 0, err
		}
		var l []int32
		for range n {
			if et == nbt.TagCompound {
				if v, ok, err := unwrapped(r, asText); err != nil {
					return 0, err
				} else if ok {
					l = append(l, v)
					continue
				}
			}
			v, err := hashTag(r, et, key, asText)
			if err != nil {
				return 0, err
			}
			l = append(l, v)
		}
		return hashList(l), nil
	case nbt.TagCompound:
		var m [][2]int32
		for {
			t, err := r.ReadByte()
			if err != nil {
				return 0, err
			}
			if t == nbt.TagEnd {
				return hashMap(m), nil
			}
			k, err := str()
			if err != nil {
				return 0, err
			}
			ks := string(utf16.Decode(k))
			v, err := hashTag(r, t, ks, asText)
			if err != nil {
				return 0, err
			}
			m = append(m, [2]int32{hashUTF16(k), v})
		}
	}
	return 0, errNBT
}

// unwrapped reads a list's compound element that is a wrapper — one entry,
// under the empty key — and hashes the value it wraps. It leaves the reader
// where it was when the element is not one.
func unwrapped(r *bytes.Reader, asText bool) (int32, bool, error) {
	start, _ := r.Seek(0, io.SeekCurrent)
	rewind := func() { r.Seek(start, io.SeekStart) }
	t, err := r.ReadByte()
	if err != nil || t == nbt.TagEnd {
		rewind()
		return 0, false, nil
	}
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil || n != [2]byte{} {
		rewind()
		return 0, false, nil
	}
	v, err := hashTag(r, t, "", asText)
	if err != nil {
		return 0, false, err
	}
	if end, err := r.ReadByte(); err != nil || end != nbt.TagEnd {
		rewind() // more entries: an ordinary compound
		return 0, false, nil
	}
	return v, true, nil
}

// decodeMUTF8 decodes Java's modified UTF-8 into UTF-16 units.
func decodeMUTF8(b []byte) []uint16 {
	var out []uint16
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			out = append(out, uint16(c))
			i++
		case c&0xE0 == 0xC0 && i+1 < len(b):
			out = append(out, uint16(c&0x1F)<<6|uint16(b[i+1]&0x3F))
			i += 2
		case c&0xF0 == 0xE0 && i+2 < len(b):
			out = append(out, uint16(c&0x0F)<<12|uint16(b[i+1]&0x3F)<<6|uint16(b[i+2]&0x3F))
			i += 3
		default:
			out = append(out, 0xFFFD)
			i++
		}
	}
	return out
}
