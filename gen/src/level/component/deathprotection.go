package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*DeathProtection)(nil)

type DeathProtection struct {
	Effects []ItemConsumeEffect
}

func (DeathProtection) ID() string { return "minecraft:death_protection" }

func (d *DeathProtection) ReadFrom(r io.Reader) (int64, error) {
	return pk.Array(&d.Effects).ReadFrom(r)
}

func (d *DeathProtection) WriteTo(w io.Writer) (int64, error) {
	return pk.Array(&d.Effects).WriteTo(w)
}
