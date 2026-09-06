// Package basic provides some basic packet handler which client needs.
//
// # [Player]
//
// The [Player] is attached to a [Client] by calling [NewPlayer] before the client joins a server.
//
// There is 4 kinds of clientbound packet is handled by this package.
//   - LoginPacket, kept as [Player.Login]; its spawn info as [Player.Spawn].
//   - KeepAlivePacket, for avoid the client to be kicked by the server.
//   - PlayerPosition, is only received when server teleporting the player.
//   - Respawn, which replaces [Player.Spawn].
//
// # [EventsListener]
//
// Handles some basic event you probably need.
//   - GameStart
//   - Disconnect
//   - HealthChange
//   - Death
package basic

import (
	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
)

type Player struct {
	c        *bot.Client
	Settings Settings

	// Login is the last play.Login the server sent: the entity id, the level
	// names, the view and simulation distances, the game rules the client is
	// told about (hardcore, reduced debug info, the death screen, …).
	Login play.Login
	// Spawn is the spawn info of the dimension the player is in, from Login and
	// then from every Respawn: the dimension type and name, the game mode, the
	// last death location, the sea level.
	Spawn types.CommonPlayerSpawnInfo
}

// NewPlayer create a new Player manager.
func NewPlayer(c *bot.Client, settings Settings, events EventsListener) *Player {
	p := &Player{c: c, Settings: settings}
	c.Events.AddListener(
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundLogin, F: p.handleLoginPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundKeepAlive, F: p.handleKeepAlivePacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundRespawn, F: p.handleRespawnPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPing, F: p.handlePingPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundCookieRequest, F: p.handleCookieRequestPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundStoreCookie, F: p.handleStoreCookiePacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundUpdateTags, F: p.handleUpdateTags},
	)
	events.attach(p)
	return p
}

// Respawn is used to send a respawn packet to the server.
// Typically, you should call this method when the player is dead (in the [Death] event handler).
func (p *Player) Respawn() error {
	cmd := play.ClientCommand{Action: types.ClientCommandActionPerformRespawn}
	if err := p.c.Conn.WritePacket(pk.Marshal(cmd.PacketID(), cmd)); err != nil {
		return Error{err}
	}
	return nil
}

// AcceptTeleportation is used to send a teleport confirmation packet to the server.
// Typically, you should call this method when received a ClientboundPlayerPosition packet (in the [Teleported] event handler).
func (p *Player) AcceptTeleportation(teleportID pk.VarInt) error {
	ack := play.AcceptTeleportation{ID: teleportID}
	if err := p.c.Conn.WritePacket(pk.Marshal(ack.PacketID(), ack)); err != nil {
		return Error{err}
	}
	return nil
}

type Error struct {
	Err error
}

func (e Error) Error() string {
	return "bot/basic: " + e.Err.Error()
}

func (e Error) Unwrap() error {
	return e.Err
}
