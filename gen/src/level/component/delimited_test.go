package component

import (
	"bytes"
	"testing"

	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/wire"
)

// TestDelimitedPatchWire pins the shape of the patch a client sends: two counts,
// then every added component preceded by its length in bytes. Writing it without
// the length is the same bytes minus one var int, which the server would read as
// the next component's type. The component here has an id this version does not
// know, which is the case the length exists for: its bytes are kept and written
// back untouched. The type is generated from DELIMITED_COMPONENT_PATCH, so this
// also checks that the generated form still carries the raw bytes.
func TestDelimitedPatchWire(t *testing.T) {
	p := DelimitedPatch{
		PositiveCount: 1,
		NegativeCount: 1,
		Positive:      wire.Counted[DelimitedTyped, *DelimitedTyped]{{Type: 4000, Raw: []byte{1, 2, 3}}},
		Negative:      wire.Counted[pk.VarInt, *pk.VarInt]{9},
	}
	var b bytes.Buffer
	if _, err := p.WriteTo(&b); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := []byte{1, 1, 0xa0, 0x1f, 3, 1, 2, 3, 9} // positive=1, negative=1, type=4000, len=3, body, removed id
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("wrote % x, want % x", b.Bytes(), want)
	}
	var back DelimitedPatch
	if _, err := back.ReadFrom(bytes.NewReader(b.Bytes())); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(back.Positive) != 1 || back.Positive[0].Type != 4000 || !bytes.Equal(back.Positive[0].Raw, []byte{1, 2, 3}) {
		t.Fatalf("the added components came back as %+v", back.Positive)
	}
	if len(back.Negative) != 1 || back.Negative[0] != 9 {
		t.Fatalf("the removed ids came back as %v", back.Negative)
	}
}

// TestUntrustedSlotDataEmpty checks that an empty stack is one var int, as the
// count being at most zero ends the value.
func TestUntrustedSlotDataEmpty(t *testing.T) {
	var b bytes.Buffer
	if _, err := (&UntrustedSlotData{}).WriteTo(&b); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(b.Bytes(), []byte{0}) {
		t.Fatalf("an empty stack wrote % x, want 00", b.Bytes())
	}
}
