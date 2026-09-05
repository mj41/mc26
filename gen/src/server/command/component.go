package command

import (
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
)

type Client interface {
	SendPacket(p pk.Packet)
}

// ClientJoin implement server.Component for Graph
func (g *Graph) ClientJoin(client Client) {
	client.SendPacket(pk.Marshal(
		packetid.ClientboundCommands, g,
	))
}
