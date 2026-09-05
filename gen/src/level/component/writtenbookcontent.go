// writtenbookcontent.go contains helper types for the WrittenBookContent data component.
package component

import (
	"io"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/nbt/dynbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

type WrittenPage struct {
	Content chat.Message // anonymousNbt
	// filteredContent: anonOptionalNbt — optional NBT, we store as dynbt.Value
	FilteredContent dynbt.Value
}

func (p *WrittenPage) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = p.Content.ReadFrom(r)
	if err != nil {
		return
	}
	// anonOptionalNbt — read NBT that may be TagEnd
	n2, err := pk.NBTField{V: &p.FilteredContent, AllowUnknownFields: true}.ReadFrom(r)
	return n + n2, err
}

func (p WrittenPage) WriteTo(w io.Writer) (n int64, err error) {
	n, err = p.Content.WriteTo(w)
	if err != nil {
		return
	}
	n2, err := pk.NBTField{V: &p.FilteredContent, AllowUnknownFields: true}.WriteTo(w)
	return n + n2, err
}
