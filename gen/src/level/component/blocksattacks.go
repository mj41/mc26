// blocksattacks.go contains helper types for the BlocksAttacks data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

// DamageReduction represents a damage reduction entry.
type DamageReduction struct {
	HorizontalBlockingAngle pk.Float
	Type                    pk.Option[IDSet, *IDSet]
	Base                    pk.Float
	Factor                  pk.Float
}

func (d *DamageReduction) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&d.HorizontalBlockingAngle, &d.Type, &d.Base, &d.Factor}.ReadFrom(r)
}

func (d DamageReduction) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&d.HorizontalBlockingAngle, &d.Type, &d.Base, &d.Factor}.WriteTo(w)
}

// ItemDamageFunction represents item damage parameters.
type ItemDamageFunction struct {
	Threshold pk.Float
	Base      pk.Float
	Factor    pk.Float
}

func (d *ItemDamageFunction) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&d.Threshold, &d.Base, &d.Factor}.ReadFrom(r)
}

func (d ItemDamageFunction) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&d.Threshold, &d.Base, &d.Factor}.WriteTo(w)
}
