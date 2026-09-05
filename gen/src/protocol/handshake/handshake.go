// Package handshake holds the single packet of the handshake protocol state.
//
// The handshake has no packetid constants (the state has exactly one packet,
// always id 0), so this package is written by hand from the packet schema:
// serverbound minecraft:intention, Java ClientIntentionPacket.
package handshake

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

// Intent is the protocol state a client asks the server to switch to
// (Java ClientIntent).
type Intent int32

const (
	IntentStatus   Intent = 1 // server list ping
	IntentLogin    Intent = 2 // join the game
	IntentTransfer Intent = 3 // join after a server transfer
)

func (i *Intent) ReadFrom(r io.Reader) (int64, error) { return (*pk.VarInt)(i).ReadFrom(r) }
func (i Intent) WriteTo(w io.Writer) (int64, error)   { return pk.VarInt(i).WriteTo(w) }

// IntentionID is the packet id of Intention: the only handshake packet.
const IntentionID int32 = 0x00

// Intention is serverbound minecraft:intention (0x00), Java ClientIntentionPacket.
type Intention struct {
	ProtocolVersion pk.VarInt
	HostName        pk.String // at most 255 characters
	Port            pk.UnsignedShort
	Intention       Intent
}

// PacketID returns the serverbound id of Intention.
func (Intention) PacketID() int32 { return IntentionID }

func (p *Intention) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.ProtocolVersion, &p.HostName, &p.Port, &p.Intention}.ReadFrom(r)
}

func (p Intention) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.ProtocolVersion, p.HostName, p.Port, p.Intention}.WriteTo(w)
}
