// kineticweapon.go contains helper types for the KineticWeapon data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

// KineticWeaponCondition represents a condition for kinetic weapon effects.
// Wire: {maxDurationTicks:VarInt, minSpeed:f32, minRelativeSpeed:f32}
type KineticWeaponCondition struct {
	MaxDurationTicks pk.VarInt
	MinSpeed         pk.Float
	MinRelativeSpeed pk.Float
}

func (c *KineticWeaponCondition) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&c.MaxDurationTicks, &c.MinSpeed, &c.MinRelativeSpeed}.ReadFrom(r)
}

func (c KineticWeaponCondition) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&c.MaxDurationTicks, &c.MinSpeed, &c.MinRelativeSpeed}.WriteTo(w)
}
