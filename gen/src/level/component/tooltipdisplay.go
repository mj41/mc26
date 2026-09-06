package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*TooltipDisplay)(nil)

type TooltipDisplay struct {
	HideTooltip      pk.Boolean
	HiddenComponents []pk.VarInt
}

func (TooltipDisplay) ID() string { return "minecraft:tooltip_display" }

func (t *TooltipDisplay) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{
		&t.HideTooltip,
		pk.Array(&t.HiddenComponents),
	}.ReadFrom(r)
}

func (t *TooltipDisplay) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		&t.HideTooltip,
		pk.Array(&t.HiddenComponents),
	}.WriteTo(w)
}
