package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*CanBreak)(nil)

type CanBreak struct {
	Predicates []ItemBlockPredicate
}

func (CanBreak) ID() string { return "minecraft:can_break" }

func (c *CanBreak) ReadFrom(r io.Reader) (int64, error) {
	return pk.Array(&c.Predicates).ReadFrom(r)
}

func (c *CanBreak) WriteTo(w io.Writer) (int64, error) {
	return pk.Array(&c.Predicates).WriteTo(w)
}
