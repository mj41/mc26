package registry

import (
	"bytes"
	"testing"

	"github.com/mj41/go-mc26/nbt"
)

// soundLike stands in for a generated element type (SoundEvent) so the test
// does not depend on one version's schema.
type soundLike struct {
	SoundID string  `nbt:"sound_id"`
	Range   float32 `nbt:"range,omitempty"`
}

type holderDoc struct {
	Ref    Holder[soundLike] `nbt:"ref"`
	Inline Holder[soundLike] `nbt:"inline"`
}

func roundTrip(t *testing.T, in, out any) {
	t.Helper()
	data, err := nbt.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := nbt.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestHolderRoundTrip(t *testing.T) {
	in := holderDoc{
		Ref:    Holder[soundLike]{ID: "minecraft:music_disc.cat"},
		Inline: Holder[soundLike]{Value: &soundLike{SoundID: "minecraft:custom", Range: 16}},
	}
	var out holderDoc
	roundTrip(t, in, &out)
	if out.Ref.ID != in.Ref.ID || out.Ref.Value != nil {
		t.Errorf("ref: got %+v", out.Ref)
	}
	if out.Inline.Value == nil || out.Inline.Value.SoundID != "minecraft:custom" || out.Inline.Value.Range != 16 {
		t.Errorf("inline: got %+v", out.Inline)
	}
}

type setDoc struct {
	Tag  HolderSet `nbt:"tag"`
	One  HolderSet `nbt:"one"`
	List HolderSet `nbt:"list"`
}

func TestHolderSetRoundTrip(t *testing.T) {
	in := setDoc{
		Tag:  HolderSet{Tag: "minecraft:infiniburn_overworld"},
		One:  HolderSet{IDs: []string{"minecraft:stone"}},
		List: HolderSet{IDs: []string{"minecraft:stone", "minecraft:dirt"}},
	}
	var out setDoc
	roundTrip(t, in, &out)
	if out.Tag.Tag != in.Tag.Tag || len(out.Tag.IDs) != 0 {
		t.Errorf("tag: got %+v", out.Tag)
	}
	if out.One.Tag != "" || len(out.One.IDs) != 1 || out.One.IDs[0] != "minecraft:stone" {
		t.Errorf("one: got %+v", out.One)
	}
	if len(out.List.IDs) != 2 || out.List.IDs[1] != "minecraft:dirt" {
		t.Errorf("list: got %+v", out.List)
	}
}

func TestColor(t *testing.T) {
	var c Color
	// from an int
	data, _ := nbt.Marshal(struct {
		C int32 `nbt:"c"`
	}{0x3f76e4})
	var doc struct {
		C Color `nbt:"c"`
	}
	if err := nbt.Unmarshal(data, &doc); err != nil || doc.C != 0x3f76e4 {
		t.Fatalf("from int: %v %x", err, doc.C)
	}
	// from a string, and back
	c = 0x3f76e4
	roundTrip(t, struct {
		C Color `nbt:"c"`
	}{c}, &doc)
	if doc.C != c {
		t.Errorf("round trip: got %x", doc.C)
	}
	var buf bytes.Buffer
	if err := c.MarshalNBT(&buf); err != nil || buf.String() != "\x00\x07#3f76e4" {
		t.Errorf("marshal: %v %q", err, buf.String())
	}
}
