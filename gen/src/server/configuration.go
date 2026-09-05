package server

import (
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/configuration"
	"github.com/mj41/go-mc26/registry"
)

type ConfigHandler interface {
	AcceptConfig(conn *net.Conn) error
}

type Configurations struct {
	Registries registry.Registries
}

// AcceptConfig sends every registry as a registry_data packet and finishes the
// configuration phase. The registry encodes its own entry list (the body of
// configuration.RegistryData after the registry id).
func (c *Configurations) AcceptConfig(conn *net.Conn) error {
	err := c.Registries.EachRegistry(func(id string, reg pk.FieldEncoder) error {
		return conn.WritePacket(pk.Marshal(
			configuration.RegistryData{}.PacketID(),
			pk.Identifier(id),
			reg,
		))
	})
	if err != nil {
		return err
	}
	finish := configuration.ClientboundFinishConfiguration{}
	return conn.WritePacket(pk.Marshal(finish.PacketID(), finish))
}

type ConfigFailErr struct {
	reason chat.Message
}

func (c ConfigFailErr) Error() string {
	return "config error: " + c.reason.ClearString()
}
