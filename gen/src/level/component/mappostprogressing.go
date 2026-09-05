package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*MapPostProcessing)(nil)

type MapPostProcessing int32

const (
	MapLock MapPostProcessing = iota
	MapScale
)

// ID implements DataComponent.
func (MapPostProcessing) ID() string {
	return "minecraft:map_post_processing"
}

// ReadFrom implements DataComponent.
func (m *MapPostProcessing) ReadFrom(r io.Reader) (n int64, err error) {
	return (*pk.VarInt)(m).ReadFrom(r)
}

// WriteTo implements DataComponent.
func (m *MapPostProcessing) WriteTo(w io.Writer) (n int64, err error) {
	return (*pk.VarInt)(m).WriteTo(w)
}
