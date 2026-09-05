// writablebookcontent.go contains helper types for the WritableBookContent data component.
package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

type Page struct {
	Raw      pk.String
	Filtered pk.Option[pk.String, *pk.String]
}

// Wire (Filterable<String>): raw:String, filtered:Option<String>.
func (p *Page) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.Raw, &p.Filtered}.ReadFrom(r)
}

func (p Page) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&p.Raw, &p.Filtered}.WriteTo(w)
}
