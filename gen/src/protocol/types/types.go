// Package types holds the wire types shared by the generated protocol packages
// (protocol/play, protocol/login, …): generic containers (List, Map, Holder,
// IDSet), NBT payloads, vectors and the hand-written structures that Minecraft
// encodes with custom code rather than a composite codec.
package types

import (
	"bytes"
	"io"
	"math"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/level"
	"github.com/mj41/go-mc26/level/component"
	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/yggdrasil/user"
)

// Ptr is the pointer-receiver decoder constraint used by the generic containers.
type Ptr[T any] interface {
	*T
	pk.FieldDecoder
}

// List is a VarInt-length-prefixed sequence of T (ByteBufCodecs.list / readList).
type List[T pk.FieldEncoder, P Ptr[T]] []T

func (l *List[T, P]) ReadFrom(r io.Reader) (n int64, err error) {
	var count pk.VarInt
	if n, err = count.ReadFrom(r); err != nil {
		return
	}
	if count < 0 {
		return n, io.ErrUnexpectedEOF
	}
	*l = make([]T, int(count))
	for i := range *l {
		var m int64
		m, err = P(&(*l)[i]).ReadFrom(r)
		n += m
		if err != nil {
			return
		}
	}
	return
}

func (l List[T, P]) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.VarInt(len(l)).WriteTo(w); err != nil {
		return
	}
	for i := range l {
		var m int64
		m, err = l[i].WriteTo(w)
		n += m
		if err != nil {
			return
		}
	}
	return
}

// Entry is one key/value pair of a Map.
type Entry[K pk.FieldEncoder, V pk.FieldEncoder] struct {
	Key K
	Val V
}

// Map is a VarInt-length-prefixed sequence of key/value pairs (ByteBufCodecs.map /
// readMap), kept in wire order.
type Map[K pk.FieldEncoder, PK Ptr[K], V pk.FieldEncoder, PV Ptr[V]] []Entry[K, V]

func (m *Map[K, PK, V, PV]) ReadFrom(r io.Reader) (n int64, err error) {
	var count pk.VarInt
	if n, err = count.ReadFrom(r); err != nil {
		return
	}
	if count < 0 {
		return n, io.ErrUnexpectedEOF
	}
	*m = make([]Entry[K, V], int(count))
	for i := range *m {
		var a, b int64
		if a, err = PK(&(*m)[i].Key).ReadFrom(r); err != nil {
			return n + a, err
		}
		b, err = PV(&(*m)[i].Val).ReadFrom(r)
		n += a + b
		if err != nil {
			return
		}
	}
	return
}

func (m Map[K, PK, V, PV]) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.VarInt(len(m)).WriteTo(w); err != nil {
		return
	}
	for i := range m {
		var a, b int64
		if a, err = m[i].Key.WriteTo(w); err != nil {
			return n + a, err
		}
		b, err = m[i].Val.WriteTo(w)
		n += a + b
		if err != nil {
			return
		}
	}
	return
}

// Holder is a registry reference that may carry an inline value instead
// (ByteBufCodecs.holder): ID is registry id + 1, or 0 followed by Direct.
type Holder[D pk.FieldEncoder, PD Ptr[D]] struct {
	ID     pk.VarInt // registry id + 1; 0 means Direct is present
	Direct D
}

func (h *Holder[D, PD]) ReadFrom(r io.Reader) (n int64, err error) {
	if n, err = h.ID.ReadFrom(r); err != nil || h.ID != 0 {
		return
	}
	m, err := PD(&h.Direct).ReadFrom(r)
	return n + m, err
}

func (h Holder[D, PD]) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = h.ID.WriteTo(w); err != nil || h.ID != 0 {
		return
	}
	m, err := h.Direct.WriteTo(w)
	return n + m, err
}

// IDSet is a HolderSet on the wire (ByteBufCodecs.holderSet): a VarInt that is
// 0 followed by a tag name, or count+1 followed by that many registry ids.
type IDSet struct {
	Tag pk.Identifier // set when IDs is nil
	IDs []pk.VarInt
}

func (s *IDSet) ReadFrom(r io.Reader) (n int64, err error) {
	var count pk.VarInt
	if n, err = count.ReadFrom(r); err != nil {
		return
	}
	if count == 0 {
		m, err := s.Tag.ReadFrom(r)
		s.IDs = nil
		return n + m, err
	}
	s.IDs = make([]pk.VarInt, int(count-1))
	for i := range s.IDs {
		var m int64
		m, err = s.IDs[i].ReadFrom(r)
		n += m
		if err != nil {
			return
		}
	}
	return
}

func (s IDSet) WriteTo(w io.Writer) (n int64, err error) {
	if s.IDs == nil {
		if n, err = pk.VarInt(0).WriteTo(w); err != nil {
			return
		}
		m, err := s.Tag.WriteTo(w)
		return n + m, err
	}
	if n, err = pk.VarInt(len(s.IDs) + 1).WriteTo(w); err != nil {
		return
	}
	for _, id := range s.IDs {
		var m int64
		m, err = id.WriteTo(w)
		n += m
		if err != nil {
			return
		}
	}
	return
}

// NBT is a network NBT payload (an unnamed tag) kept undecoded.
type NBT struct{ nbt.RawMessage }

func (t *NBT) ReadFrom(r io.Reader) (int64, error) { return pk.NBT(&t.RawMessage).ReadFrom(r) }
func (t NBT) WriteTo(w io.Writer) (int64, error) {
	if t.RawMessage.Type == 0 || len(t.RawMessage.Data) == 0 {
		return pk.NBT(map[string]any{}).WriteTo(w)
	}
	return pk.NBT(t.RawMessage).WriteTo(w)
}

// OptionalNBT is an NBT payload that may be absent (a single TAG_End byte).
type OptionalNBT struct {
	Has bool
	NBT
}

func (t *OptionalNBT) ReadFrom(r io.Reader) (int64, error) {
	var tagType [1]byte
	if _, err := io.ReadFull(r, tagType[:]); err != nil {
		return 0, err
	}
	if tagType[0] == 0 {
		t.Has = false
		return 1, nil
	}
	t.Has = true
	n, err := t.NBT.ReadFrom(io.MultiReader(bytes.NewReader(tagType[:]), r))
	return n, err
}

func (t OptionalNBT) WriteTo(w io.Writer) (int64, error) {
	if !t.Has {
		m, err := w.Write([]byte{0})
		return int64(m), err
	}
	return t.NBT.WriteTo(w)
}

// Text is a chat component encoded as network NBT.
type Text = chat.Message

// ChatTypeBound is ChatType$Bound: a chat-type holder (registry id + 1, or 0
// followed by an inline definition) with sender and target names.
type ChatTypeBound = chat.Type

// Vec3 is three doubles.
type Vec3 struct{ X, Y, Z pk.Double }

func (v *Vec3) ReadFrom(r io.Reader) (int64, error) { return pk.Tuple{&v.X, &v.Y, &v.Z}.ReadFrom(r) }
func (v Vec3) WriteTo(w io.Writer) (int64, error)   { return pk.Tuple{v.X, v.Y, v.Z}.WriteTo(w) }

// Vector3f is three floats.
type Vector3f struct{ X, Y, Z pk.Float }

func (v *Vector3f) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&v.X, &v.Y, &v.Z}.ReadFrom(r)
}
func (v Vector3f) WriteTo(w io.Writer) (int64, error) { return pk.Tuple{v.X, v.Y, v.Z}.WriteTo(w) }

// Quaternionf is four floats.
type Quaternionf struct{ X, Y, Z, W pk.Float }

func (q *Quaternionf) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&q.X, &q.Y, &q.Z, &q.W}.ReadFrom(r)
}
func (q Quaternionf) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{q.X, q.Y, q.Z, q.W}.WriteTo(w)
}

// GlobalPos is a dimension name and a block position.
type GlobalPos struct {
	Dimension pk.Identifier
	Pos       pk.Position
}

func (g *GlobalPos) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&g.Dimension, &g.Pos}.ReadFrom(r)
}
func (g GlobalPos) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{g.Dimension, g.Pos}.WriteTo(w)
}

// ChunkPos is a chunk position packed into one long.
type ChunkPos = level.ChunkPos

// SectionPos is a chunk-section position packed into one long (x:22 z:22 y:20).
type SectionPos struct{ X, Y, Z int32 }

func (s *SectionPos) ReadFrom(r io.Reader) (int64, error) {
	var v pk.Long
	n, err := v.ReadFrom(r)
	s.X = int32(int64(v) >> 42)
	s.Y = int32(int64(v) << 44 >> 44)
	s.Z = int32(int64(v) << 22 >> 42)
	return n, err
}

func (s SectionPos) WriteTo(w io.Writer) (int64, error) {
	v := (int64(s.X)&0x3FFFFF)<<42 | (int64(s.Z)&0x3FFFFF)<<20 | int64(s.Y)&0xFFFFF
	return pk.Long(v).WriteTo(w)
}

// LpVec3 is Minecraft's low-precision packed vector (net.minecraft.network.LpVec3):
// one byte of scale/continuation bits, three 15-bit quantised components and,
// when the scale needs it, a VarInt with the high scale bits.
type LpVec3 struct{ X, Y, Z float64 }

const (
	lpDataMask  = 32767
	lpMaxQuant  = 32766.0
	lpAbsMax    = 1.7179869183e10
	lpAbsMin    = 3.051944088384301e-5
	lpContinued = 4
)

func lpUnpack(v int64) float64 { return (float64(v&lpDataMask)/lpMaxQuant - 0.5) * 2 }
func lpPack(v float64) int64   { return int64(math.Round((v*0.5 + 0.5) * lpMaxQuant)) }
func lpSanitize(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(-lpAbsMax, math.Min(lpAbsMax, v))
}

func (v *LpVec3) ReadFrom(r io.Reader) (n int64, err error) {
	var lowest, middle pk.UnsignedByte
	if n, err = lowest.ReadFrom(r); err != nil {
		return
	}
	if lowest == 0 {
		*v = LpVec3{}
		return
	}
	var highest pk.Int
	m, err := pk.Tuple{&middle, &highest}.ReadFrom(r)
	n += m
	if err != nil {
		return
	}
	buffer := int64(uint32(highest))<<16 | int64(middle)<<8 | int64(lowest)
	scale := int64(lowest & 3)
	if lowest&lpContinued == lpContinued {
		var hi pk.VarInt
		m, err = hi.ReadFrom(r)
		n += m
		if err != nil {
			return
		}
		scale |= (int64(hi) & 0xFFFFFFFF) << 2
	}
	s := float64(scale)
	*v = LpVec3{lpUnpack(buffer>>3) * s, lpUnpack(buffer>>18) * s, lpUnpack(buffer>>33) * s}
	return
}

func (v LpVec3) WriteTo(w io.Writer) (int64, error) {
	x, y, z := lpSanitize(v.X), lpSanitize(v.Y), lpSanitize(v.Z)
	longest := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if longest < lpAbsMin {
		return pk.UnsignedByte(0).WriteTo(w)
	}
	scale := int64(math.Ceil(longest))
	partial := scale&3 != scale
	markers := scale
	if partial {
		markers = scale&3 | lpContinued
	}
	buffer := markers | lpPack(x/float64(scale))<<3 | lpPack(y/float64(scale))<<18 | lpPack(z/float64(scale))<<33
	fields := pk.Tuple{pk.UnsignedByte(buffer), pk.UnsignedByte(buffer >> 8), pk.Int(buffer >> 16)}
	if partial {
		fields = append(fields, pk.VarInt(scale>>2))
	}
	return fields.WriteTo(w)
}

// ItemStack is an item stack with its component patch (ItemStack.STREAM_CODEC;
// count 0 is the empty stack, also used for OPTIONAL_STREAM_CODEC).
type ItemStack struct{ component.SlotData }

func (s *ItemStack) ReadFrom(r io.Reader) (int64, error) { return s.SlotData.ReadFrom(r) }
func (s ItemStack) WriteTo(w io.Writer) (int64, error)   { return (&s.SlotData).WriteTo(w) }

// enumType is any generated enum (a VarInt ordinal that knows its constant count).
type enumType interface {
	~int32
	Count() int
}

// EnumSet is an EnumSet<E> on the wire: a fixed bit set of ceil(Count/8) bytes.
type EnumSet[E enumType] []byte

func (s *EnumSet[E]) ReadFrom(r io.Reader) (int64, error) {
	var e E
	*s = make([]byte, (e.Count()+7)/8)
	n, err := io.ReadFull(r, *s)
	return int64(n), err
}

func (s EnumSet[E]) WriteTo(w io.Writer) (int64, error) {
	var e E
	buf := make([]byte, (e.Count()+7)/8)
	copy(buf, s)
	n, err := w.Write(buf)
	return int64(n), err
}

// AddedComponent is one added entry of a ComponentPatch: the component type id
// and its value.
type AddedComponent struct {
	Type  pk.VarInt
	Value component.DataComponent
}

// ComponentPatch is a DataComponentPatch: added components (type id + value) and
// removed component type ids.
type ComponentPatch struct {
	Added   []AddedComponent
	Removed []pk.VarInt
}

func (p *ComponentPatch) ReadFrom(r io.Reader) (n int64, err error) {
	var added, removed pk.VarInt
	if n, err = (pk.Tuple{&added, &removed}).ReadFrom(r); err != nil {
		return
	}
	p.Added = p.Added[:0]
	for i := 0; i < int(added); i++ {
		var typ pk.VarInt
		var m int64
		if m, err = typ.ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
		comp := component.NewComponent(int32(typ))
		if comp == nil {
			return n, io.ErrUnexpectedEOF
		}
		if m, err = comp.ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
		p.Added = append(p.Added, AddedComponent{Type: typ, Value: comp})
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

func (p ComponentPatch) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = (pk.Tuple{pk.VarInt(len(p.Added)), pk.VarInt(len(p.Removed))}).WriteTo(w); err != nil {
		return
	}
	for _, c := range p.Added {
		var m int64
		if m, err = (pk.Tuple{c.Type, c.Value}).WriteTo(w); err != nil {
			return n + m, err
		}
		n += m
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

// GameProfile is ByteBufCodecs.GAME_PROFILE: uuid, name (≤ 16) and properties
// (name, value, optional signature; the same user.Property the session server
// returns).
type GameProfile struct {
	ID         pk.UUID
	Name       pk.String
	Properties List[user.Property, *user.Property]
}

func (g *GameProfile) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&g.ID, &g.Name, &g.Properties}.ReadFrom(r)
}
func (g GameProfile) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{g.ID, g.Name, g.Properties}.WriteTo(w)
}

// MessageSignature is a fixed 256-byte chat signature.
type MessageSignature [256]byte

func (s *MessageSignature) ReadFrom(r io.Reader) (int64, error) {
	n, err := io.ReadFull(r, s[:])
	return int64(n), err
}
func (s MessageSignature) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(s[:])
	return int64(n), err
}

// PublicKey is an encoded public key (ByteBufCodecs.PUBLIC_KEY): a byte array.
type PublicKey = pk.ByteArray

// RestBytes is the remainder of the packet (custom payloads).
type RestBytes = pk.PluginMessageData

// OptionalVarInt is ByteBufCodecs.OPTIONAL_VAR_INT: value + 1, or 0 for absent.
type OptionalVarInt = pk.VarInt

// Instant is a millisecond timestamp.
type Instant = pk.Long

// BlockHitResult is FriendlyByteBuf.readBlockHitResult.
type BlockHitResult struct {
	Pos            pk.Position
	Direction      pk.VarInt
	CursorX        pk.Float
	CursorY        pk.Float
	CursorZ        pk.Float
	Inside         pk.Boolean
	WorldBorderHit pk.Boolean
}

func (b *BlockHitResult) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&b.Pos, &b.Direction, &b.CursorX, &b.CursorY, &b.CursorZ, &b.Inside, &b.WorldBorderHit}.ReadFrom(r)
}
func (b BlockHitResult) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{b.Pos, b.Direction, b.CursorX, b.CursorY, b.CursorZ, b.Inside, b.WorldBorderHit}.WriteTo(w)
}

// Empty is a packet or structure with no payload.
type Empty struct{}

func (Empty) ReadFrom(io.Reader) (int64, error) { return 0, nil }
func (Empty) WriteTo(io.Writer) (int64, error)  { return 0, nil }
