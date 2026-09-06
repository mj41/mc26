package basic

import (
	"bytes"

	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
)

// handleLoginPacket keeps ClientboundLogin (play.Login) and its spawn info, and
// answers with the client brand and the client information.
func (p *Player) handleLoginPacket(packet pk.Packet) error {
	var login play.Login
	if err := packet.Scan(&login); err != nil {
		return Error{err}
	}
	p.Login = login
	p.Spawn = login.CommonPlayerSpawnInfo

	// minecraft:brand carries one string in the payload
	var brand bytes.Buffer
	if _, err := pk.String(p.Settings.Brand).WriteTo(&brand); err != nil {
		return Error{err}
	}
	payload := play.ServerboundCustomPayload{Channel: "minecraft:brand", Data: brand.Bytes()}
	if err := p.c.Conn.WritePacket(pk.Marshal(payload.PacketID(), payload)); err != nil {
		return Error{err}
	}

	info := play.ClientInformation{Information: types.ClientInformation{
		Language:             pk.String(p.Settings.Locale),
		ViewDistance:         pk.Byte(p.Settings.ViewDistance),
		ChatVisibility:       types.ChatVisiblity(p.Settings.ChatMode),
		ChatColors:           pk.Boolean(p.Settings.ChatColors),
		ModelCustomisation:   pk.UnsignedByte(p.Settings.DisplayedSkinParts),
		MainHand:             types.HumanoidArm(p.Settings.MainHand),
		TextFilteringEnabled: pk.Boolean(p.Settings.EnableTextFiltering),
		AllowsListing:        pk.Boolean(p.Settings.AllowListing),
		ParticleStatus:       types.ParticleStatus(p.Settings.ParticleStatus),
	}}
	if err := p.c.Conn.WritePacket(pk.Marshal(info.PacketID(), info)); err != nil {
		return Error{err}
	}

	p.resetKeepAliveDeadline()
	return nil
}

// handleRespawnPacket replaces the spawn info with ClientboundRespawn's
// (play.Respawn); the packet also says what the client keeps (attributes,
// metadata).
func (p *Player) handleRespawnPacket(packet pk.Packet) error {
	var respawn play.Respawn
	if err := packet.Scan(&respawn); err != nil {
		return Error{err}
	}
	p.Spawn = respawn.CommonPlayerSpawnInfo
	return nil
}
