package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*PotionContents)(nil)

type PotionContents struct {
	PotionID      pk.Option[pk.VarInt, *pk.VarInt]
	CustomColor   pk.Option[pk.Int, *pk.Int]
	CustomEffects []ItemPotionEffect
	CustomName    pk.Option[pk.String, *pk.String]
}

func (PotionContents) ID() string { return "minecraft:potion_contents" }

func (p *PotionContents) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{
		&p.PotionID,
		&p.CustomColor,
		pk.Array(&p.CustomEffects),
		&p.CustomName,
	}.ReadFrom(r)
}

func (p *PotionContents) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		&p.PotionID,
		&p.CustomColor,
		pk.Array(&p.CustomEffects),
		&p.CustomName,
	}.WriteTo(w)
}
