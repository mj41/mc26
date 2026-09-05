// blockstate.go contains helper types for the BlockState data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

type BlockStateProperty struct {
	Name  pk.String
	Value pk.String
}

// Wire: name:String, value:String.
func (b *BlockStateProperty) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&b.Name, &b.Value}.ReadFrom(r)
}

func (b BlockStateProperty) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&b.Name, &b.Value}.WriteTo(w)
}
