package handshake

import (
	"bytes"
	"testing"

	pk "github.com/mj41/go-mc26/net/packet"
)

func TestIntentionRoundTrip(t *testing.T) {
	in := Intention{ProtocolVersion: 776, HostName: "localhost", Port: 25565, Intention: IntentLogin}
	p := pk.Marshal(in.PacketID(), in)
	if p.ID != IntentionID {
		t.Fatalf("packet id %d, want %d", p.ID, IntentionID)
	}
	var out Intention
	if err := p.Scan(&out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip: got %+v, want %+v", out, in)
	}
	var buf bytes.Buffer
	if _, err := in.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	// VarInt(776)=2 bytes, String(9)=1+9, UnsignedShort=2, VarInt(2)=1
	if buf.Len() != 2+10+2+1 {
		t.Fatalf("encoded %d bytes, want 15", buf.Len())
	}
}
