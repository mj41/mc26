package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*Consumable)(nil)

type Consumable struct {
	ConsumeSeconds pk.Float
	Animation      pk.VarInt
	Sound          SoundHolder
	MakesParticles pk.Boolean
	Effects        []ItemConsumeEffect
}

func (Consumable) ID() string { return "minecraft:consumable" }

func (c *Consumable) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{
		&c.ConsumeSeconds,
		&c.Animation,
		&c.Sound,
		&c.MakesParticles,
		pk.Array(&c.Effects),
	}.ReadFrom(r)
}

func (c *Consumable) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		&c.ConsumeSeconds,
		&c.Animation,
		&c.Sound,
		&c.MakesParticles,
		pk.Array(&c.Effects),
	}.WriteTo(w)
}
