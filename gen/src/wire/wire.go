// Package wire holds the wire-level building blocks the generated code is
// assembled from and that depend on nothing but the packet field types:
// generic containers (List, Map, Holder, IDSet, EnumSet), NBT payloads, the
// packed positions, fixed-size blocks; the plain records (Vec3, GlobalPos,
// GameProfile, …) are generated into structs_gen.go from the jar's codecs. protocol/types re-exports them for the
// packets; level/component uses them for the data components.
package wire

import (
	"reflect"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

// LenPrefixed is ByteBufCodecs.lengthPrefixed: a var int giving the number of
// bytes that follow, then the value inside exactly those bytes. The length is
// what lets a reader step over a value it cannot decode, so reading the value
// must not run past it and must not leave any of it behind.
type LenPrefixed[T pk.FieldEncoder, PT Ptr[T]] struct{ V T }

func (l *LenPrefixed[T, PT]) ReadFrom(r io.Reader) (n int64, err error) {
	var size pk.VarInt
	if n, err = size.ReadFrom(r); err != nil {
		return n, err
	}
	if size < 0 {
		return n, fmt.Errorf("length-prefixed value of %d bytes", size)
	}
	body := make([]byte, int(size))
	m, err := io.ReadFull(r, body)
	n += int64(m)
	if err != nil {
		return n, err
	}
	rest := bytes.NewReader(body)
	if _, err := PT(&l.V).ReadFrom(rest); err != nil {
		return n, err
	}
	if rest.Len() != 0 {
		return n, fmt.Errorf("length-prefixed value left %d of its %d bytes unread", rest.Len(), size)
	}
	return n, nil
}

func (l LenPrefixed[T, PT]) WriteTo(w io.Writer) (n int64, err error) {
	var buf bytes.Buffer
	if _, err = l.V.WriteTo(&buf); err != nil {
		return 0, err
	}
	if n, err = pk.VarInt(buf.Len()).WriteTo(w); err != nil {
		return n, err
	}
	m, err := w.Write(buf.Bytes())
	return n + int64(m), err
}

// Rest is a value repeated until the enclosing window has no bytes left (a
// length-prefixed buffer, the packet frame): no count anywhere, the reader
// knows it has the last one when the next read finds nothing. The chunk's
// sections are one, their number being the dimension's height, which the
// packet does not carry (nodes.json `rest`).
type Rest[T pk.FieldEncoder, P Ptr[T]] []T

func (l *Rest[T, P]) ReadFrom(r io.Reader) (n int64, err error) {
	*l = (*l)[:0]
	var first [1]byte
	for {
		// whether anything is left is known before an element is read, not from
		// the error an element's own reader wraps around the end
		if _, err := io.ReadFull(r, first[:]); err != nil {
			if err == io.EOF {
				return n, nil
			}
			return n, err
		}
		n++
		var elem T
		m, err := P(&elem).ReadFrom(io.MultiReader(bytes.NewReader(first[:]), r))
		n += m - 1
		if err != nil {
			return n, err
		}
		*l = append(*l, elem)
	}
}

func (l Rest[T, P]) WriteTo(w io.Writer) (n int64, err error) {
	for i := range l {
		m, err := l[i].WriteTo(w)
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Packed is a run of values packed into big-endian 64-bit longs, kept as the
// longs: floor(64/width) values per long, none crossing a long, exactly
// ceil(entries/that) longs and no count in front of them, none at all when
// the width is 0 (nodes.json `packed`). The count and the width are not on the
// wire — the palette byte read before it selects the width — so a Packed reads
// and writes through PackedOf, which is handed both.
type Packed []uint64

// PackedOf pairs a Packed with its count of values and its width.
func PackedOf(p *Packed, entries, width int) *packedOf {
	return &packedOf{p, entries, width}
}

type packedOf struct {
	p              *Packed
	entries, width int
}

// PackedLongs is how many longs a run of entries values of width bits takes.
func PackedLongs(entries, width int) int {
	if width <= 0 {
		return 0
	}
	perLong := 64 / width
	return (entries + perLong - 1) / perLong
}

func (c *packedOf) ReadFrom(r io.Reader) (n int64, err error) {
	longs := PackedLongs(c.entries, c.width)
	*c.p = make(Packed, longs)
	var buf [8]byte
	for i := range *c.p {
		m, err := io.ReadFull(r, buf[:])
		n += int64(m)
		if err != nil {
			return n, err
		}
		(*c.p)[i] = binary.BigEndian.Uint64(buf[:])
	}
	return n, nil
}

func (c *packedOf) WriteTo(w io.Writer) (n int64, err error) {
	if want := PackedLongs(c.entries, c.width); len(*c.p) != want {
		return 0, fmt.Errorf("%d longs where %d values of %d bits take %d", len(*c.p), c.entries, c.width, want)
	}
	var buf [8]byte
	for _, v := range *c.p {
		binary.BigEndian.PutUint64(buf[:], v)
		m, err := w.Write(buf[:])
		n += int64(m)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

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

// Array is exactly as many T as the array type N is long, with no count on
// the wire (ByteBufCodecs.fixedSizeList: the four lines of a sign). The
// length is a type, [4]struct{}, so an Array reads and writes like any other
// field, in an optional too.
type Array[T pk.FieldEncoder, P Ptr[T], N any] []T

func arrayLen[N any]() int { return reflect.TypeFor[N]().Len() }

func (a *Array[T, P, N]) ReadFrom(r io.Reader) (n int64, err error) {
	*a = make([]T, arrayLen[N]())
	for i := range *a {
		var m int64
		m, err = P(&(*a)[i]).ReadFrom(r)
		n += m
		if err != nil {
			return
		}
	}
	return
}

// WriteTo writes the elements; an empty Array writes as many zero values,
// as the zero value of any other field writes its zero.
func (a Array[T, P, N]) WriteTo(w io.Writer) (n int64, err error) {
	if len(a) == 0 {
		a = make(Array[T, P, N], arrayLen[N]())
	}
	if len(a) != arrayLen[N]() {
		return 0, fmt.Errorf("an array of %d entries holds %d", arrayLen[N](), len(a))
	}
	for i := range a {
		var m int64
		m, err = a[i].WriteTo(w)
		n += m
		if err != nil {
			return
		}
	}
	return
}

// Counted is a repetition with no count in front of it: how many entries there
// are is the value of another field of the same structure, so the count is not
// adjacent to what it counts and the reader has to be handed it.
type Counted[T pk.FieldEncoder, P Ptr[T]] []T

// CountedOf pairs a Counted with the count another field holds, giving a value
// that reads and writes like any other field. Written into a pk.Tuple, the
// count is read by then, because the field holding it comes first on the wire.
func CountedOf[T pk.FieldEncoder, P Ptr[T]](s *Counted[T, P], count int) *countedOf[T, P] {
	return &countedOf[T, P]{s, count}
}

type countedOf[T pk.FieldEncoder, P Ptr[T]] struct {
	s     *Counted[T, P]
	count int
}

func (c *countedOf[T, P]) ReadFrom(r io.Reader) (n int64, err error) {
	if c.count < 0 {
		return 0, fmt.Errorf("a count of %d entries", c.count)
	}
	*c.s = make(Counted[T, P], c.count)
	for i := range *c.s {
		var m int64
		m, err = P(&(*c.s)[i]).ReadFrom(r)
		n += m
		if err != nil {
			return
		}
	}
	return
}

func (c *countedOf[T, P]) WriteTo(w io.Writer) (n int64, err error) {
	// The count is a field of its own, written already: a value whose length
	// disagrees with it would produce a packet nothing can read back.
	if len(*c.s) != c.count {
		return 0, fmt.Errorf("%d entries where the count field says %d", len(*c.s), c.count)
	}
	for i := range *c.s {
		var m int64
		m, err = (*c.s)[i].WriteTo(w)
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

// SetRaw stores an undecoded payload.
func (t *NBT) SetRaw(v nbt.RawMessage) { t.RawMessage = v }

// OptionalNBT is an NBT payload that may be absent (a single TAG_End byte).
type OptionalNBT struct {
	Has bool
	NBT
}

// SetRaw stores an undecoded payload and marks it present. A field that one
// version carries as NBT and another as OptionalNBT (the chunk's block entity
// tag) is set the same way in both.
func (t *OptionalNBT) SetRaw(v nbt.RawMessage) {
	t.Has = true
	t.RawMessage = v
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

// EnumType is any generated enum (a VarInt ordinal that knows its constant count).
type EnumType interface {
	~int32
	Count() int
}

// EnumSet is an EnumSet<E> on the wire: a fixed bit set of ceil(Count/8) bytes.
type EnumSet[E EnumType] []byte

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

// Has reports whether e is in the set.
func (s EnumSet[E]) Has(e E) bool {
	i := int(e)
	return i >= 0 && i/8 < len(s) && s[i/8]&(1<<(i%8)) != 0
}

// Set adds e to the set (growing it to the enum's size).
func (s *EnumSet[E]) Set(e E) {
	var z E
	if n := (z.Count() + 7) / 8; len(*s) < n {
		grown := make([]byte, n)
		copy(grown, *s)
		*s = grown
	}
	(*s)[int(e)/8] |= 1 << (int(e) % 8)
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

// ByteBitSet is ByteBufCodecs.BIT_SET: a var-int-prefixed byte array read as
// java.util.BitSet.valueOf(byte[]), so bit i is bit (i mod 8) of byte i/8. It is
// a different wire form from pk.BitSet, which FriendlyByteBuf.readBitSet reads
// as a long array; 26.3 moved the chunk light masks from one to the other.
type ByteBitSet []byte

func (s *ByteBitSet) ReadFrom(r io.Reader) (int64, error) { return (*pk.ByteArray)(s).ReadFrom(r) }
func (s ByteBitSet) WriteTo(w io.Writer) (int64, error)   { return pk.ByteArray(s).WriteTo(w) }

// Has reports whether bit i is set. Bits past the end are unset: the writer
// trims trailing zero bytes, so a short set is not a truncated one.
func (s ByteBitSet) Has(i int) bool {
	return i >= 0 && i/8 < len(s) && s[i/8]&(1<<(i%8)) != 0
}

// Set adds bit i, growing the set to reach it.
func (s *ByteBitSet) Set(i int) {
	if i < 0 {
		return
	}
	for len(*s) <= i/8 {
		*s = append(*s, 0)
	}
	(*s)[i/8] |= 1 << (i % 8)
}

// PublicKey is an encoded public key (ByteBufCodecs.PUBLIC_KEY): a byte array.
type PublicKey = pk.ByteArray

// RestBytes is the remainder of the packet (custom payloads).
type RestBytes = pk.PluginMessageData

// OptionalVarInt is ByteBufCodecs.OPTIONAL_VAR_INT: value + 1, or 0 for absent.
type OptionalVarInt = pk.VarInt

// Instant is a millisecond timestamp.
type Instant = pk.Long

// Empty is a packet or structure with no payload.
type Empty struct{}

func (Empty) ReadFrom(io.Reader) (int64, error) { return 0, nil }
func (Empty) WriteTo(io.Writer) (int64, error)  { return 0, nil }

// Box holds a value of a type that contains itself (a slot display with a
// remainder slot display) behind a pointer; nil reads and writes as the zero
// value.
type Box[T pk.FieldEncoder, P Ptr[T]] struct{ V *T }

func (b *Box[T, P]) ReadFrom(r io.Reader) (int64, error) {
	var v T
	n, err := P(&v).ReadFrom(r)
	b.V = &v
	return n, err
}

func (b Box[T, P]) WriteTo(w io.Writer) (int64, error) {
	if b.V == nil {
		var z T
		return z.WriteTo(w)
	}
	return (*b.V).WriteTo(w)
}

// Either is ByteBufCodecs.either: a boolean selects the left (true) or the
// right (false) value.
type Either[L pk.FieldEncoder, PL Ptr[L], R pk.FieldEncoder, PR Ptr[R]] struct {
	IsLeft bool
	Left   L
	Right  R
}

func (e *Either[L, PL, R, PR]) ReadFrom(r io.Reader) (n int64, err error) {
	var left pk.Boolean
	if n, err = left.ReadFrom(r); err != nil {
		return
	}
	e.IsLeft = bool(left)
	var m int64
	if e.IsLeft {
		m, err = PL(&e.Left).ReadFrom(r)
	} else {
		m, err = PR(&e.Right).ReadFrom(r)
	}
	return n + m, err
}

func (e Either[L, PL, R, PR]) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.Boolean(e.IsLeft).WriteTo(w); err != nil {
		return
	}
	var m int64
	if e.IsLeft {
		m, err = e.Left.WriteTo(w)
	} else {
		m, err = e.Right.WriteTo(w)
	}
	return n + m, err
}
