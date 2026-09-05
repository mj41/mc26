package basic

import (
	"bytes"

	pk "github.com/mj41/go-mc26/net/packet"
)

func (p *Player) handleUpdateTags(packet pk.Packet) error {
	r := bytes.NewReader(packet.Data)

	var length pk.VarInt
	_, err := length.ReadFrom(r)
	if err != nil {
		return Error{err}
	}

	var registryID pk.Identifier
	for i := 0; i < int(length); i++ {
		_, err = registryID.ReadFrom(r)
		if err != nil {
			return Error{err}
		}

		registry := p.c.Registries.Registry(string(registryID))
		_, err = registry.ReadTagsFrom(r)
		if err != nil {
			return Error{err}
		}
	}
	return nil
}
