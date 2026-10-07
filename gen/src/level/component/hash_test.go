package component

import (
	"testing"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/wire"
)

// The values a vanilla client computes (HashOps.CRC32C_INSTANCE): createInt
// hashes the bytes 08, then the int little-endian.
func TestHash(t *testing.T) {
	if got, _ := Hash(&Damage{Value: 0}); got != hashBytes([]byte{8, 0, 0, 0, 0}) {
		t.Errorf("damage 0: %d", got)
	}
	a, _ := Hash(&Damage{Value: 10})
	b, _ := Hash(&RepairCost{Value: 10})
	if a != b {
		t.Errorf("two ints of the same value hash apart: %d %d", a, b)
	}
	if got, _ := Hash(&Unbreakable{}); got != hashBytes([]byte{2, 3}) {
		t.Errorf("unbreakable: %d", got)
	}
	// a text made in Go, not read from the server, is hashed when it is plain
	if got, ok := Hash(&CustomName{Value: chat.Text("x")}); !ok || got != hashString("x") {
		t.Errorf("a plain custom name: %d %v", got, ok)
	}
	if _, ok := Hash(&CustomName{Value: chat.Message{Text: "x", Bold: true}}); ok {
		t.Errorf("a styled text made in Go cannot be hashed exactly (an explicit false is lost)")
	}
}

func TestHashEnchantments(t *testing.T) {
	names := func(_ string, id int32) (string, bool) {
		return []string{"minecraft:efficiency", "minecraft:unbreaking"}[id], true
	}
	var e Enchantments
	e.Enchantments = append(e.Enchantments, wire.Entry[pk.VarInt, pk.VarInt]{Key: 0, Val: 3}, wire.Entry[pk.VarInt, pk.VarInt]{Key: 1, Val: 1})
	a, ok := HashWith(&e, names)
	if !ok {
		t.Fatal("enchantments not hashed")
	}
	// the order of the entries does not matter: HashOps sorts them
	e.Enchantments[0], e.Enchantments[1] = e.Enchantments[1], e.Enchantments[0]
	if b, _ := HashWith(&e, names); a != b {
		t.Errorf("the hash depends on the entries' order: %d %d", a, b)
	}
	if hashString("ab") != hashBytes([]byte{12, 2, 0, 0, 0, 'a', 0, 'b', 0}) {
		t.Errorf("a string is its length and its UTF-16 units, little-endian")
	}
}

// A list of mixed elements is written as compounds wrapping each under the
// empty key; ListTag unwraps them as it loads, in every list, so custom_data
// {l:[1,"a"]} hashes as the list of 1 and "a". A wrapped value is not text:
// a byte under "bold" stays a byte.
func TestHashCustomDataMixedList(t *testing.T) {
	data := []byte{
		nbt.TagList, 0, 1, 'l', nbt.TagCompound, 0, 0, 0, 2,
		nbt.TagInt, 0, 0, 0, 0, 0, 1, nbt.TagEnd, // {"":1}
		nbt.TagString, 0, 0, 0, 1, 'a', nbt.TagEnd, // {"":"a"}
		nbt.TagEnd,
	}
	got, ok := Hash(&CustomData{Value: wire.NBT{RawMessage: nbt.RawMessage{Type: nbt.TagCompound, Data: data}}})
	want := hashMap([][2]int32{{hashString("l"), hashList([]int32{hashInt(1), hashString("a")})}})
	if !ok || got != want {
		t.Errorf("custom_data {l:[1,\"a\"]}: %d %v, want %d", got, ok, want)
	}
	data = []byte{
		nbt.TagList, 0, 1, 'l', nbt.TagCompound, 0, 0, 0, 1,
		nbt.TagCompound, 0, 0, nbt.TagByte, 0, 4, 'b', 'o', 'l', 'd', 1, nbt.TagEnd, nbt.TagEnd, // {"":{bold:1b}}
		nbt.TagEnd,
	}
	got, ok = Hash(&CustomData{Value: wire.NBT{RawMessage: nbt.RawMessage{Type: nbt.TagCompound, Data: data}}})
	want = hashMap([][2]int32{{hashString("l"), hashList([]int32{hashMap([][2]int32{{hashString("bold"), hashByte(1)}})})}})
	if !ok || got != want {
		t.Errorf("custom_data {l:[{bold:1b}]}: %d %v, want %d", got, ok, want)
	}
}
