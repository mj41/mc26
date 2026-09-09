// Package component holds the data components of an item stack: one generated
// type per component of this Minecraft version, the interface they share, and
// the wire types that carry them (wire_gen.go, generated from the schema's
// definitions of ITEM_STACK, COMPONENT_PATCH and TYPED_DATA_COMPONENT).
package component

import "github.com/mj41/go-mc26/wire"

// IDSet is a HolderSet on the wire (a tag name or a list of registry ids).
type IDSet = wire.IDSet

// SoundHolder is a sound event by registry id, or an inline SoundEvent.
type SoundHolder = wire.Holder[SoundEvent, *SoundEvent]
