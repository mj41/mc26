package basic

import (
	"time"

	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

const keepAliveDuration = time.Second * 20

func (p *Player) resetKeepAliveDeadline() {
	newDeadline := time.Now().Add(keepAliveDuration)
	p.c.Conn.Socket.SetDeadline(newDeadline)
}

func (p *Player) handleKeepAlivePacket(packet pk.Packet) error {
	var ka play.ClientboundKeepAlive
	if err := packet.Scan(&ka); err != nil {
		return Error{err}
	}

	p.resetKeepAliveDeadline()

	resp := play.ServerboundKeepAlive{ID: ka.ID}
	if err := p.c.Conn.WritePacket(pk.Marshal(resp.PacketID(), resp)); err != nil {
		return Error{err}
	}
	return nil
}
