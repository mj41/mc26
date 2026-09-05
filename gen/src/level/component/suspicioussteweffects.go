// suspicioussteweffects.go contains helper types for the SuspiciousStewEffects data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

type StewEffect struct {
	Effect   pk.VarInt
	Duration pk.VarInt
}

// Wire: effect:VarInt (mob effect id), duration:VarInt.
func (s *StewEffect) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&s.Effect, &s.Duration}.ReadFrom(r)
}

func (s StewEffect) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&s.Effect, &s.Duration}.WriteTo(w)
}
