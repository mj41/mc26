package bot_test

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/configuration"
	"github.com/mj41/go-mc26/protocol/handshake"
	"github.com/mj41/go-mc26/protocol/login"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/status"
)

// TestCaptureCheck reads a session recorded off the wire — every state, both
// directions — and checks that each packet decodes into its generated type and
// consumes the body exactly. A packet whose type is short of a field decodes
// without complaint and says nothing at all, so reading it is not the test:
// reading all of it is.
//
// The capture is written by `mc26 crosscheck`, which records through a proxy
// rather than from inside this library, so what a bot sends is checked here
// against the same description as what it receives.
func TestCaptureCheck(t *testing.T) {
	path := os.Getenv("MC26_CHECK_CAPTURE")
	if path == "" {
		t.Skip("MC26_CHECK_CAPTURE not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	defer f.Close()

	type captured struct {
		State string `json:"state"`
		Flow  string `json:"flow"`
		ID    int32  `json:"id"`
		Data  string `json:"data"`
	}
	var wrong, differed []string
	counts, ungenerated := map[string]int{}, map[string]int{}
	total := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var c captured
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("capture line: %v", err)
		}
		total++
		where := c.State + "/" + c.Flow
		counts[where]++
		data, err := hex.DecodeString(c.Data)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		v := newPacket(c.State, c.Flow, c.ID)
		if v == nil {
			ungenerated[fmt.Sprintf("%s/%d", where, c.ID)]++
			continue
		}
		name := fmt.Sprintf("%s %T", where, v)
		if err := (pk.Packet{ID: c.ID, Data: data}).ScanAll(v.(pk.FieldDecoder)); err != nil {
			wrong = append(wrong, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		var back bytes.Buffer
		if _, err := v.WriteTo(&back); err != nil {
			wrong = append(wrong, fmt.Sprintf("%s: encode: %v", name, err))
			continue
		}
		if !bytes.Equal(data, back.Bytes()) {
			// Not a fault by itself: a chat component arrives as a bare NBT string
			// and this library writes back the compound that means the same. The
			// byte-for-byte half of the check belongs to `mc26 crosscheck`, whose
			// decoder keeps NBT as it found it.
			differed = append(differed, fmt.Sprintf("%s (%d in, %d out)", name, len(data), back.Len()))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	if total == 0 {
		t.Fatalf("%s holds no packets", path)
	}

	seen := map[string]bool{}
	for _, w := range wrong {
		if seen[w] {
			continue
		}
		seen[w] = true
		t.Error(w)
	}
	for _, k := range sortedKeys(ungenerated) {
		t.Errorf("%s: %d packets of an id this library does not generate", k, ungenerated[k])
	}
	if len(differed) > 0 {
		t.Logf("%d packets encoded back differently, which text components legitimately do: %v",
			len(differed), differed[:min(6, len(differed))])
	}
	if len(wrong) == 0 && len(ungenerated) == 0 {
		t.Logf("ok: %d packets decoded into their generated types and consumed exactly (%v)", total, counts)
	}
}

// newPacket is the generated packet of an id, in the state and direction it
// travelled: the ids restart at zero in every state, so the state picks the
// package before the id picks the packet.
func newPacket(state, flow string, id int32) pk.Field {
	switch state + "/" + flow {
	case "handshake/serverbound":
		if id == handshake.IntentionID {
			return new(handshake.Intention)
		}
	case "status/clientbound":
		return status.NewClientbound(packetid.ClientboundPacketID(id))
	case "status/serverbound":
		return status.NewServerbound(packetid.ServerboundPacketID(id))
	case "login/clientbound":
		return login.NewClientbound(packetid.ClientboundPacketID(id))
	case "login/serverbound":
		return login.NewServerbound(packetid.ServerboundPacketID(id))
	case "configuration/clientbound":
		return configuration.NewClientbound(packetid.ClientboundPacketID(id))
	case "configuration/serverbound":
		return configuration.NewServerbound(packetid.ServerboundPacketID(id))
	case "play/clientbound":
		return play.NewClientbound(packetid.ClientboundPacketID(id))
	case "play/serverbound":
		return play.NewServerbound(packetid.ServerboundPacketID(id))
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
