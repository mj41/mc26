package server

import (
	"github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/handshake"
)

// handshake reads the client's intention packet: the protocol version it
// speaks and the state (status or login) it wants to enter. The address and
// port the client connected to are ignored.
func (s *Server) handshake(conn *net.Conn) (protocol int32, intent handshake.Intent, err error) {
	var p pk.Packet
	if err = conn.ReadPacket(&p); err != nil {
		return 0, 0, err
	}
	var hs handshake.Intention
	if p.ID != hs.PacketID() {
		return 0, 0, wrongPacketErr{expect: hs.PacketID(), get: p.ID}
	}
	if err = p.Scan(&hs); err != nil {
		return 0, 0, err
	}
	return int32(hs.ProtocolVersion), hs.Intention, nil
}
