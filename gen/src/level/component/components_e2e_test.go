package component

import (
	"bytes"
	"testing"

	pk "github.com/mj41/go-mc26/net/packet"
)

// TestArrayElementCodecs guards against element types that pk.Array cannot decode:
// pk.Ary panics at runtime when an element does not implement FieldDecoder, and that
// only shows up when a server actually sends the component (e.g. an enchanted item).
func TestArrayElementCodecs(t *testing.T) {
	elems := []interface {
		pk.FieldEncoder
	}{
		EnchantmentEntry{ID: 1, Level: 2},
		BlockStateProperty{Name: "facing", Value: "north"},
		StewEffect{Effect: 3, Duration: 4},
		Page{Raw: "hi"},
		ItemStackTemplate{ItemID: 1, Count: 2},
	}
	for _, e := range elems {
		var buf bytes.Buffer
		if _, err := e.WriteTo(&buf); err != nil {
			t.Fatalf("%T: WriteTo: %v", e, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("%T: nothing written", e)
		}
	}
	// pointer types must decode
	var (
		ee EnchantmentEntry
		bp BlockStateProperty
		se StewEffect
		pg Page
		it ItemStackTemplate
	)
	for _, d := range []pk.FieldDecoder{&ee, &bp, &se, &pg, &it} {
		var buf bytes.Buffer
		enc := d.(pk.FieldEncoder)
		if _, err := enc.WriteTo(&buf); err != nil {
			t.Fatalf("%T: WriteTo: %v", d, err)
		}
		if _, err := d.ReadFrom(&buf); err != nil {
			t.Fatalf("%T: ReadFrom: %v", d, err)
		}
	}
	// Round trip through the generated array component.
	src := Enchantments{Enchantments: []EnchantmentEntry{{ID: 5, Level: 3}, {ID: 7, Level: 1}}}
	var buf bytes.Buffer
	if _, err := src.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	var dst Enchantments
	if _, err := dst.ReadFrom(&buf); err != nil {
		t.Fatal(err)
	}
	if len(dst.Enchantments) != 2 || dst.Enchantments[1].ID != 7 {
		t.Fatalf("round trip mismatch: %+v", dst)
	}
}
