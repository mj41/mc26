package basic

import (
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

// handlePingPacket answers ClientboundPing with a Pong carrying the same id.
func (p *Player) handlePingPacket(packet pk.Packet) error {
	var ping play.Ping
	if err := packet.Scan(&ping); err != nil {
		return Error{err}
	}
	pong := play.Pong{ID: ping.ID}
	if err := p.c.Conn.WritePacket(pk.Marshal(pong.PacketID(), pong)); err != nil {
		return Error{err}
	}
	return nil
}
