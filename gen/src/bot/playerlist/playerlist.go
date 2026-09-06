// Package playerlist contains a PlayerList struct that used to manage player information.
//
// The [PlayerList] contains a list of [PlayerInfo] which is received from server when client join.
// The playerlist contains every players' information of name, display name, uuid, gamemode, latency, public key, etc.
// And can be used to render the "TAB List". Other packages may also require playerlist to work,
// for example, the bot/msg package.
package playerlist

import (
	"github.com/google/uuid"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/chat/sign"
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
	"github.com/mj41/go-mc26/yggdrasil/user"
)

type PlayerList struct {
	PlayerInfos map[uuid.UUID]*PlayerInfo
}

func New(c *bot.Client) *PlayerList {
	pl := PlayerList{
		PlayerInfos: make(map[uuid.UUID]*PlayerInfo),
	}
	c.Events.AddListener(
		bot.PacketHandler{
			Priority: 64, ID: packetid.ClientboundPlayerInfoUpdate,
			F: pl.handlePlayerInfoUpdatePacket,
		},
		bot.PacketHandler{
			Priority: 64, ID: packetid.ClientboundPlayerInfoRemove,
			F: pl.handlePlayerInfoRemovePacket,
		},
	)
	return &pl
}

// handlePlayerInfoUpdatePacket applies play.PlayerInfoUpdate: every entry
// carries only the parts selected by the packet's action bits.
func (pl *PlayerList) handlePlayerInfoUpdatePacket(p pk.Packet) error {
	var update play.PlayerInfoUpdate
	if err := p.Scan(&update); err != nil {
		return err
	}
	actions := update.Actions
	for i := range update.Entries {
		e := &update.Entries[i]
		id := uuid.UUID(e.ProfileID)
		player, ok := pl.PlayerInfos[id]
		if !ok { // create new player info if not exist
			player = new(PlayerInfo)
			pl.PlayerInfos[id] = player
		}
		if actions.Has(types.PlayerInfoUpdateActionAddPlayer) {
			player.GameProfile = GameProfile{
				ID:         id,
				Name:       string(e.Name),
				Properties: []user.Property(e.Properties),
			}
		}
		if actions.Has(types.PlayerInfoUpdateActionInitializeChat) {
			if e.ChatSession.Has {
				d := e.ChatSession.Val
				key, err := user.ParsePublicKey(int64(d.ProfilePublicKey.ExpiresAt), d.ProfilePublicKey.Key, d.ProfilePublicKey.KeySignature)
				if err != nil {
					return err
				}
				player.ChatSession = &sign.Session{SessionID: uuid.UUID(d.SessionID), PublicKey: key}
				player.ChatSession.InitValidate()
			} else {
				player.ChatSession = nil
			}
		}
		if actions.Has(types.PlayerInfoUpdateActionUpdateGameMode) {
			player.Gamemode = int32(e.GameMode)
		}
		if actions.Has(types.PlayerInfoUpdateActionUpdateListed) {
			player.Listed = bool(e.Listed)
		}
		if actions.Has(types.PlayerInfoUpdateActionUpdateLatency) {
			player.Latency = int32(e.Latency)
		}
		if actions.Has(types.PlayerInfoUpdateActionUpdateDisplayName) {
			if e.DisplayName.Has {
				name := e.DisplayName.Val
				player.DisplayName = &name
			} else {
				player.DisplayName = nil
			}
		}
		if actions.Has(types.PlayerInfoUpdateActionUpdateListOrder) {
			player.ListOrder = int32(e.ListOrder)
		}
		if actions.Has(types.PlayerInfoUpdateActionUpdateHat) {
			player.ShowHat = bool(e.ShowHat)
		}
	}
	return nil
}

func (pl *PlayerList) handlePlayerInfoRemovePacket(p pk.Packet) error {
	var remove play.PlayerInfoRemove
	if err := p.Scan(&remove); err != nil {
		return err
	}
	for _, id := range remove.ProfileIds {
		delete(pl.PlayerInfos, uuid.UUID(id))
	}
	return nil
}

type PlayerInfo struct {
	GameProfile
	ChatSession *sign.Session
	Gamemode    int32
	Latency     int32
	Listed      bool
	DisplayName *chat.Message
	ListOrder   int32 // tab-list sort priority (26.x)
	ShowHat     bool  // whether the hat layer is shown in the tab list (26.x)
}

type GameProfile struct {
	ID         uuid.UUID
	Name       string
	Properties []user.Property
}
