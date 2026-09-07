// Package types holds the wire types shared by the generated protocol packages
// (protocol/play, protocol/login, …): the generic containers and primitives of
// package wire under their familiar names, plus the few structures that bridge
// to chat, level and the data components.
package types

import (
	"fmt"
	"io"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/level"
	"github.com/mj41/go-mc26/level/component"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/wire"
)

// The building blocks live in package wire (which depends on nothing above
// net/packet); the generated packets refer to them through these names. The
// records generated into wire (Vec3, GlobalPos, …) get theirs in wire_gen.go.
type (
	Ptr[T any]                                                                   = wire.Ptr[T]
	List[T pk.FieldEncoder, P wire.Ptr[T]]                                       = wire.List[T, P]
	Entry[K pk.FieldEncoder, V pk.FieldEncoder]                                  = wire.Entry[K, V]
	Map[K pk.FieldEncoder, PK wire.Ptr[K], V pk.FieldEncoder, PV wire.Ptr[V]]    = wire.Map[K, PK, V, PV]
	Holder[D pk.FieldEncoder, PD wire.Ptr[D]]                                    = wire.Holder[D, PD]
	EnumSet[E wire.EnumType]                                                     = wire.EnumSet[E]
	Box[T pk.FieldEncoder, P wire.Ptr[T]]                                        = wire.Box[T, P]
	Either[L pk.FieldEncoder, PL wire.Ptr[L], R pk.FieldEncoder, PR wire.Ptr[R]] = wire.Either[L, PL, R, PR]
	LenPrefixed[T pk.FieldEncoder, PT wire.Ptr[T]]                               = wire.LenPrefixed[T, PT]
	Counted[T pk.FieldEncoder, PT wire.Ptr[T]]                                   = wire.Counted[T, PT]
	IDSet                                                                        = wire.IDSet
	NBT                                                                          = wire.NBT
	OptionalNBT                                                                  = wire.OptionalNBT
	SectionPos                                                                   = wire.SectionPos
	LpVec3                                                                       = wire.LpVec3
	MessageSignature                                                             = wire.MessageSignature
	PublicKey                                                                    = wire.PublicKey
	RestBytes                                                                    = wire.RestBytes
	OptionalVarInt                                                               = wire.OptionalVarInt
	Instant                                                                      = wire.Instant
	ByteBitSet                                                                   = wire.ByteBitSet
	Empty                                                                        = wire.Empty
)

// Text is a chat component encoded as network NBT.
type Text = chat.Message

// ChatTypeBound is ChatType$Bound: a chat-type holder (registry id + 1, or 0
// followed by an inline definition) with sender and target names.
type ChatTypeBound = chat.Type

// ChunkPos is a chunk position packed into one long.
type ChunkPos = level.ChunkPos

// ItemStack is an item stack with its component patch (ItemStack.STREAM_CODEC;
// count 0 is the empty stack, also used for OPTIONAL_STREAM_CODEC).
type ItemStack struct{ component.SlotData }

// UntrustedItemStack is an ItemStack whose components each carry their length
// (ItemStack.OPTIONAL_UNTRUSTED_STREAM_CODEC). A client sends it, in the
// creative mode slot packet; a server never does.
type UntrustedItemStack struct{ component.UntrustedSlotData }

func (s *ItemStack) ReadFrom(r io.Reader) (int64, error) { return s.SlotData.ReadFrom(r) }
func (s ItemStack) WriteTo(w io.Writer) (int64, error)   { return (&s.SlotData).WriteTo(w) }

func (s *UntrustedItemStack) ReadFrom(r io.Reader) (int64, error) {
	return s.UntrustedSlotData.ReadFrom(r)
}
func (s UntrustedItemStack) WriteTo(w io.Writer) (int64, error) {
	return (&s.UntrustedSlotData).WriteTo(w)
}

// AddedComponent is one typed data component: the component type id and its
// value (TypedDataComponent on the wire).
type AddedComponent = component.Typed

// ComponentPatch is a DataComponentPatch: added components (type id + value) and
// removed component type ids.
type ComponentPatch = component.Patch

// EntityDataValue is one synched field of an entity in set_entity_data: its
// index (data/entitydata names them per class), the serializer id and the
// value, of the Go type NewEntityDataValue gives that serializer.
type EntityDataValue struct {
	Index      uint8
	Serializer int32
	Value      pk.Field
}

// EntityData is the value list of set_entity_data (SynchedEntityData.DataValue):
// index byte, serializer VarInt, value; terminated by an index of 0xff. A
// serializer the schema does not type stops the decoding with an error, since
// the value's length is unknown.
type EntityData []EntityDataValue

func (d *EntityData) ReadFrom(r io.Reader) (n int64, err error) {
	*d = (*d)[:0]
	for {
		var index pk.UnsignedByte
		m, err := index.ReadFrom(r)
		n += m
		if err != nil {
			return n, err
		}
		if index == 0xff {
			return n, nil
		}
		var serializer pk.VarInt
		if m, err = serializer.ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
		value, err := NewEntityDataValue(int32(serializer))
		if err != nil {
			return n, fmt.Errorf("entity data index %d: %w", index, err)
		}
		if m, err = value.ReadFrom(r); err != nil {
			return n + m, err
		}
		n += m
		*d = append(*d, EntityDataValue{Index: uint8(index), Serializer: int32(serializer), Value: value})
	}
}

func (d EntityData) WriteTo(w io.Writer) (n int64, err error) {
	for _, v := range d {
		m, err := pk.Tuple{pk.UnsignedByte(v.Index), pk.VarInt(v.Serializer), v.Value}.WriteTo(w)
		n += m
		if err != nil {
			return n, err
		}
	}
	m, err := pk.UnsignedByte(0xff).WriteTo(w)
	return n + m, err
}

// CountedOf pairs a Counted with the count another field holds; see wire.CountedOf.
func CountedOf[T pk.FieldEncoder, PT wire.Ptr[T]](s *Counted[T, PT], count int) pk.Field {
	return wire.CountedOf[T, PT](s, count)
}
