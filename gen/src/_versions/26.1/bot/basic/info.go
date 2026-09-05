package basic

import (
	"bytes"

	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
)

// WorldInfo content player info in server.
type WorldInfo struct {
	DimensionType       int32
	DimensionNames      []string // Identifiers for all worlds on the server.
	DimensionName       string   // Name of the world being spawned into.
	HashedSeed          int64    // First 8 bytes of the SHA-256 hash of the world's seed. Used client side for biome noise
	MaxPlayers          int32    // Was once used by the client to draw the player list, but now is ignored.
	ViewDistance        int32    // Render distance (2-32).
	SimulationDistance  int32    // The distance that the client will process specific things, such as entities.
	ReducedDebugInfo    bool     // If true, a vanilla client shows reduced information on the debug screen. For servers in development, this should almost always be false.
	EnableRespawnScreen bool     // Set to false when the doImmediateRespawn gamerule is true.
	IsDebug             bool     // True if the world is a debug mode world; debug mode worlds cannot be modified and have predefined blocks.
	IsFlat              bool     // True if the world is a superflat world; flat worlds have different void fog and a horizon at y=0 instead of y=63.
	DoLimitCrafting     bool     // Whether players can only craft recipes they have already unlocked. Currently unused by the client.
	HasDeathLocation    bool     // True if DeathDimension/DeathLocation are set (player died before and has not respawned).
	DeathDimension      string   // Dimension the player died in.
	DeathLocation       pk.Position
	PortalCooldown      int32 // Ticks until the player can use a portal again.
	SeaLevel            int32 // Sea level of the current dimension (1.21.2+).
	EnforcesSecureChat  bool  // Whether the server requires signed chat (login packet only).
	OnlineMode          bool  // Not sent by 26.1; always false.
}

type PlayerInfo struct {
	EID          int32 // The player's Entity ID (EID).
	Hardcore     bool  // Is hardcore
	Gamemode     byte  // Gamemode. 0: Survival, 1: Creative, 2: Adventure, 3: Spectator.
	PrevGamemode int8  // Previous Gamemode
}

// globalPos is the wire form of a GlobalPos: dimension identifier + packed block position.
// applySpawnInfo copies a CommonPlayerSpawnInfo (sent by Login and Respawn)
// into the player's world info.
func (p *Player) applySpawnInfo(info types.CommonPlayerSpawnInfo) {
	p.WorldInfo.DimensionType = int32(info.DimensionType)
	p.DimensionName = string(info.Dimension)
	p.HashedSeed = int64(info.Seed)
	p.Gamemode = byte(info.GameType)
	p.PrevGamemode = int8(info.PreviousGameType)
	p.IsDebug = bool(info.IsDebug)
	p.IsFlat = bool(info.IsFlat)
	p.HasDeathLocation = bool(info.LastDeathLocation.Has)
	if info.LastDeathLocation.Has {
		p.DeathDimension = string(info.LastDeathLocation.Val.Dimension)
		p.DeathLocation = info.LastDeathLocation.Val.Pos
	} else {
		p.DeathDimension = ""
		p.DeathLocation = pk.Position{}
	}
	p.PortalCooldown = int32(info.PortalCooldown)
	p.SeaLevel = int32(info.SeaLevel)
}

// handleLoginPacket applies ClientboundLogin (play.Login) and answers with the
// client brand and the client information.
func (p *Player) handleLoginPacket(packet pk.Packet) error {
	var login play.Login
	if err := packet.Scan(&login); err != nil {
		return Error{err}
	}
	p.EID = int32(login.PlayerID)
	p.Hardcore = bool(login.Hardcore)
	p.DimensionNames = p.DimensionNames[:0]
	for _, level := range login.Levels {
		p.DimensionNames = append(p.DimensionNames, string(level))
	}
	p.MaxPlayers = int32(login.MaxPlayers)
	p.ViewDistance = int32(login.ChunkRadius)
	p.SimulationDistance = int32(login.SimulationDistance)
	p.ReducedDebugInfo = bool(login.ReducedDebugInfo)
	p.EnableRespawnScreen = bool(login.ShowDeathScreen)
	p.DoLimitCrafting = bool(login.DoLimitedCrafting)
	p.applySpawnInfo(login.CommonPlayerSpawnInfo)
	p.EnforcesSecureChat = bool(login.EnforcesSecureChat)

	// minecraft:brand carries one string in the payload
	var brand bytes.Buffer
	if _, err := pk.String(p.Settings.Brand).WriteTo(&brand); err != nil {
		return Error{err}
	}
	payload := play.CustomPayload{Channel: "minecraft:brand", Data: brand.Bytes()}
	if err := p.c.Conn.WritePacket(pk.Marshal(play.ServerboundCustomPayloadID, payload)); err != nil {
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

// handleRespawnPacket applies ClientboundRespawn (play.Respawn): the spawn info
// followed by the data-to-keep flags (0x01 keep attributes, 0x02 keep metadata).
func (p *Player) handleRespawnPacket(packet pk.Packet) error {
	var respawn play.Respawn
	if err := packet.Scan(&respawn); err != nil {
		return Error{err}
	}
	p.applySpawnInfo(respawn.CommonPlayerSpawnInfo)
	return nil
}
