package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*CanPlaceOn)(nil)

type CanPlaceOn struct {
	Predicates []ItemBlockPredicate
}

func (CanPlaceOn) ID() string { return "minecraft:can_place_on" }

func (c *CanPlaceOn) ReadFrom(r io.Reader) (int64, error) {
	return pk.Array(&c.Predicates).ReadFrom(r)
}

func (c *CanPlaceOn) WriteTo(w io.Writer) (int64, error) {
	return pk.Array(&c.Predicates).WriteTo(w)
}
