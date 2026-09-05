package component

import (
	"io"

	"github.com/mj41/go-mc26/nbt/dynbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*AttributeModifiers)(nil)

type AttributeModifier struct {
	TypeID    pk.VarInt
	Name      pk.String
	Value     pk.Double
	Operation pk.VarInt
	Slot      pk.VarInt
}

type AttributeModifiers struct {
	Attributes  []AttributeModifier
	DisplayType pk.VarInt
	// DisplayData stores the override NBT if DisplayType == 2
	DisplayData dynbt.Value
}

// ID implements DataComponent.
func (AttributeModifiers) ID() string {
	return "minecraft:attribute_modifiers"
}

// ReadFrom implements DataComponent.
func (a *AttributeModifiers) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = pk.Array(&a.Attributes).ReadFrom(r)
	if err != nil {
		return
	}
	n2, err := a.DisplayType.ReadFrom(r)
	n += n2
	if err != nil {
		return
	}
	if a.DisplayType == 2 {
		n2, err = pk.NBTField{V: &a.DisplayData, AllowUnknownFields: true}.ReadFrom(r)
		n += n2
	}
	return
}

// WriteTo implements DataComponent.
func (a *AttributeModifiers) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Array(&a.Attributes).WriteTo(w)
	if err != nil {
		return
	}
	n2, err := a.DisplayType.WriteTo(w)
	n += n2
	if err != nil {
		return
	}
	if a.DisplayType == 2 {
		n2, err = pk.NBTField{V: &a.DisplayData, AllowUnknownFields: true}.WriteTo(w)
		n += n2
	}
	return
}
