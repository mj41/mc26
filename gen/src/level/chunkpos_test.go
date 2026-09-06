package level

import (
	"bytes"
	"testing"
)

// TestChunkPosWire pins the wire form ChunkPos.pack gives it: one long with x
// in the low 32 bits and z in the high 32, so z's four bytes go first. Reading
// the halves the other way round names a different chunk and nothing says so.
func TestChunkPosWire(t *testing.T) {
	var b bytes.Buffer
	if _, err := (ChunkPos{1, 2}).WriteTo(&b); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := []byte{0, 0, 0, 2, 0, 0, 0, 1}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("wrote % x, want % x", b.Bytes(), want)
	}
	for _, c := range []ChunkPos{{0, 0}, {1, 2}, {-1, 5}, {7, -3}, {-2147483648, 2147483647}} {
		var buf bytes.Buffer
		if _, err := c.WriteTo(&buf); err != nil {
			t.Fatalf("write %v: %v", c, err)
		}
		var back ChunkPos
		if _, err := back.ReadFrom(&buf); err != nil {
			t.Fatalf("read %v: %v", c, err)
		}
		if back != c {
			t.Errorf("round trip of %v gave %v", c, back)
		}
	}
}
