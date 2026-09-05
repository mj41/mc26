package play

// Hand-written packets of the play state: their Java readers branch on
// earlier fields or read into external structures, which the schema marks as
// partial. Each mirrors the wire format of the 26.2 class named in its comment.

import (
	"io"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/chat/sign"
	"github.com/mj41/go-mc26/data/packetid"
	"github.com/mj41/go-mc26/level"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/types"
	"github.com/mj41/go-mc26/yggdrasil/user"
)

// CustomPayload is clientbound/serverbound minecraft:custom_payload
// (ClientboundCustomPayloadPacket / ServerboundCustomPayloadPacket): a channel
// name followed by the rest of the packet.
type CustomPayload struct {
	Channel pk.Identifier
	Data    types.RestBytes
}

func (p *CustomPayload) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.Channel, &p.Data}.ReadFrom(r)
}
func (p CustomPayload) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.Channel, p.Data}.WriteTo(w)
}

// ClientboundCustomPayloadID and ServerboundCustomPayloadID are the ids of CustomPayload in each flow.
const (
	ClientboundCustomPayloadID = packetid.ClientboundCustomPayload
	ServerboundCustomPayloadID = packetid.ServerboundCustomPayload
)

// LevelChunkWithLight is clientbound minecraft:level_chunk_with_light
// (ClientboundLevelChunkWithLightPacket). The chunk body depends on the
// dimension height, so Chunk must be allocated with level.EmptyChunk(sections)
// before ReadFrom.
type LevelChunkWithLight struct {
	Pos   level.ChunkPos
	Chunk *level.Chunk
}

// NewLevelChunkWithLight prepares a packet able to decode a chunk of the given
// section count (dimension height / 16).
func NewLevelChunkWithLight(sections int) *LevelChunkWithLight {
	return &LevelChunkWithLight{Chunk: level.EmptyChunk(sections)}
}

func (LevelChunkWithLight) PacketID() packetid.ClientboundPacketID {
	return packetid.ClientboundLevelChunkWithLight
}

func (p *LevelChunkWithLight) ReadFrom(r io.Reader) (int64, error) {
	if p.Chunk == nil {
		return 0, io.ErrUnexpectedEOF
	}
	return pk.Tuple{&p.Pos, p.Chunk}.ReadFrom(r)
}

func (p LevelChunkWithLight) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.Pos, p.Chunk}.WriteTo(w)
}

// PlayerChat is clientbound minecraft:player_chat (ClientboundPlayerChatPacket).
type PlayerChat struct {
	GlobalIndex     pk.VarInt
	Sender          pk.UUID
	Index           pk.VarInt
	Signature       pk.Option[sign.Signature, *sign.Signature]
	Body            sign.PackedMessageBody
	UnsignedContent pk.Option[chat.Message, *chat.Message]
	Filter          sign.FilterMask
	ChatType        chat.Type
}

func (PlayerChat) PacketID() packetid.ClientboundPacketID { return packetid.ClientboundPlayerChat }

func (p *PlayerChat) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.GlobalIndex, &p.Sender, &p.Index, &p.Signature, &p.Body, &p.UnsignedContent, &p.Filter, &p.ChatType}.ReadFrom(r)
}

func (p PlayerChat) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.GlobalIndex, p.Sender, p.Index, p.Signature, &p.Body, p.UnsignedContent, &p.Filter, p.ChatType}.WriteTo(w)
}

// PlayerInfoAction is ClientboundPlayerInfoUpdatePacket$Action; the packet's
// Actions bit set says which parts every entry carries.
type PlayerInfoAction int

const (
	PlayerInfoAddPlayer PlayerInfoAction = iota
	PlayerInfoInitializeChat
	PlayerInfoUpdateGameMode
	PlayerInfoUpdateListed
	PlayerInfoUpdateLatency
	PlayerInfoUpdateDisplayName
	PlayerInfoUpdateListOrder
	PlayerInfoUpdateHat
)

// PlayerInfoActions is the packet's EnumSet<Action>: one byte, bit i = action i.
type PlayerInfoActions [1]byte

func (a PlayerInfoActions) Has(action PlayerInfoAction) bool { return a[0]&(1<<uint(action)) != 0 }
func (a *PlayerInfoActions) Set(action PlayerInfoAction)     { a[0] |= 1 << uint(action) }

// PlayerInfoEntry is one entry of PlayerInfoUpdate; only the parts whose action
// bit is set are read or written.
type PlayerInfoEntry struct {
	ID          pk.UUID
	Name        pk.String                                 // AddPlayer
	Properties  types.List[user.Property, *user.Property] // AddPlayer
	ChatSession pk.Option[sign.Session, *sign.Session]    // InitializeChat
	GameMode    pk.VarInt                                 // UpdateGameMode
	Listed      pk.Boolean                                // UpdateListed
	Latency     pk.VarInt                                 // UpdateLatency
	DisplayName pk.Option[chat.Message, *chat.Message]    // UpdateDisplayName
	ListOrder   pk.VarInt                                 // UpdateListOrder
	ShowHat     pk.Boolean                                // UpdateHat
}

func (e *PlayerInfoEntry) fields(actions PlayerInfoActions) pk.Tuple {
	var t pk.Tuple
	add := func(action PlayerInfoAction, p pk.Field) {
		if actions.Has(action) {
			t = append(t, p)
		}
	}
	t = append(t, &e.ID)
	add(PlayerInfoAddPlayer, pk.Tuple{&e.Name, &e.Properties})
	add(PlayerInfoInitializeChat, &e.ChatSession)
	add(PlayerInfoUpdateGameMode, &e.GameMode)
	add(PlayerInfoUpdateListed, &e.Listed)
	add(PlayerInfoUpdateLatency, &e.Latency)
	add(PlayerInfoUpdateDisplayName, &e.DisplayName)
	add(PlayerInfoUpdateListOrder, &e.ListOrder)
	add(PlayerInfoUpdateHat, &e.ShowHat)
	return t
}

// PlayerInfoUpdate is clientbound minecraft:player_info_update
// (ClientboundPlayerInfoUpdatePacket).
type PlayerInfoUpdate struct {
	Actions PlayerInfoActions
	Entries []PlayerInfoEntry
}

func (PlayerInfoUpdate) PacketID() packetid.ClientboundPacketID {
	return packetid.ClientboundPlayerInfoUpdate
}

func (p *PlayerInfoUpdate) ReadFrom(r io.Reader) (n int64, err error) {
	var count pk.VarInt
	m, err := io.ReadFull(r, p.Actions[:])
	n = int64(m)
	if err != nil {
		return
	}
	var m2 int64
	if m2, err = count.ReadFrom(r); err != nil {
		return n + m2, err
	}
	n += m2
	p.Entries = make([]PlayerInfoEntry, int(count))
	for i := range p.Entries {
		m2, err = p.Entries[i].fields(p.Actions).ReadFrom(r)
		n += m2
		if err != nil {
			return
		}
	}
	return
}

func (p PlayerInfoUpdate) WriteTo(w io.Writer) (n int64, err error) {
	m, err := w.Write(p.Actions[:])
	n = int64(m)
	if err != nil {
		return
	}
	var m2 int64
	if m2, err = pk.VarInt(len(p.Entries)).WriteTo(w); err != nil {
		return n + m2, err
	}
	n += m2
	for i := range p.Entries {
		m2, err = p.Entries[i].fields(p.Actions).WriteTo(w)
		n += m2
		if err != nil {
			return
		}
	}
	return
}
