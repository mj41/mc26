// bees.go contains helper types for the Bees data component.
package component

import (
	"io"

	"github.com/mj41/go-mc26/nbt/dynbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

type BeeData struct {
	NBTData        dynbt.Value
	TicksInHive    pk.VarInt
	MinTicksInHive pk.VarInt
}

func (b *BeeData) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{
		pk.NBTField{V: &b.NBTData, AllowUnknownFields: true},
		&b.TicksInHive,
		&b.MinTicksInHive,
	}.ReadFrom(r)
}

func (b BeeData) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{
		pk.NBTField{V: &b.NBTData, AllowUnknownFields: true},
		&b.TicksInHive,
		&b.MinTicksInHive,
	}.WriteTo(w)
}
