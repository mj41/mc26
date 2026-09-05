// enchantments.go contains helper types for the Enchantments data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

type EnchantmentEntry struct {
	ID    pk.VarInt
	Level pk.VarInt
}

// Wire: enchantment:VarInt (registry id), level:VarInt.
func (e *EnchantmentEntry) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&e.ID, &e.Level}.ReadFrom(r)
}

func (e EnchantmentEntry) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&e.ID, &e.Level}.WriteTo(w)
}
