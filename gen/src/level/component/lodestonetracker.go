// lodestonetracker.go contains helper types for the LodestoneTracker data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

type GlobalPos struct {
	DimensionName pk.Identifier
	Location      pk.Position
}

func (g *GlobalPos) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&g.DimensionName, &g.Location}.ReadFrom(r)
}

func (g GlobalPos) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&g.DimensionName, &g.Location}.WriteTo(w)
}
