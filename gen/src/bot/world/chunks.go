package world

import (
	"fmt"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/data/packetid"
	"github.com/mj41/go-mc26/level"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

type World struct {
	c      *bot.Client
	p      *basic.Player
	events EventsListener

	Columns map[level.ChunkPos]*level.Chunk
}

func NewWorld(c *bot.Client, p *basic.Player, events EventsListener) (w *World) {
	w = &World{
		c: c, p: p,
		events:  events,
		Columns: make(map[level.ChunkPos]*level.Chunk),
	}
	c.Events.AddListener(
		bot.PacketHandler{Priority: 64, ID: packetid.ClientboundPlayLogin, F: w.onPlayerSpawn},
		bot.PacketHandler{Priority: 64, ID: packetid.ClientboundPlayRespawn, F: w.onPlayerSpawn},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayLevelChunkWithLight, F: w.handleLevelChunkWithLightPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayForgetLevelChunk, F: w.handleForgetLevelChunkPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayChunkBatchFinished, F: w.handleChunkBatchFinishedPacket},
	)
	return
}

// DefaultChunksPerTick is the chunk rate the client asks for after every chunk
// batch. Vanilla servers throttle chunk sending until the client acknowledges a
// batch, and each acknowledgement carries the rate the client is willing to
// receive; the vanilla client derives it from its own timing, a bot has no such
// limit.
const DefaultChunksPerTick = 64

// handleChunkBatchFinishedPacket answers ClientboundChunkBatchFinished with
// ServerboundChunkBatchReceived. Without the reply a vanilla server sends only
// the first batch of chunks (protocol 764+).
func (w *World) handleChunkBatchFinishedPacket(packet pk.Packet) error {
	var finished play.ChunkBatchFinished
	if err := packet.Scan(&finished); err != nil {
		return err
	}
	ack := play.ChunkBatchReceived{DesiredChunksPerTick: DefaultChunksPerTick}
	return w.c.Conn.WritePacket(pk.Marshal(ack.PacketID(), ack))
}

func (w *World) onPlayerSpawn(pk.Packet) error {
	// unload all chunks
	w.Columns = make(map[level.ChunkPos]*level.Chunk)
	return nil
}

func (w *World) handleLevelChunkWithLightPacket(packet pk.Packet) error {
	currentDimType := w.c.Registries.DimensionType.GetByID(int32(w.p.Spawn.DimensionType))
	if currentDimType == nil {
		return fmt.Errorf("dimension type %d not found", w.p.Spawn.DimensionType)
	}
	var p play.LevelChunkWithLight
	if err := packet.Scan(&p); err != nil {
		return err
	}
	// The section bytes are decoded with the dimension's height; the light
	// arrays are not kept.
	chunk := level.EmptyChunk(int(currentDimType.Height) / 16)
	heightmaps := make(map[int32][]uint64, len(p.ChunkData.Heightmaps))
	for _, e := range p.ChunkData.Heightmaps {
		longs := make([]uint64, len(e.Val))
		for i, v := range e.Val {
			longs[i] = uint64(v)
		}
		heightmaps[int32(e.Key)] = longs
	}
	chunk.SetHeightmapData(heightmaps)
	if err := chunk.PutSections(p.ChunkData.Buffer.V); err != nil {
		return err
	}
	chunk.BlockEntity = []level.BlockEntity(p.ChunkData.BlockEntitiesData)
	pos := level.ChunkPos{int32(p.X), int32(p.Z)}
	w.Columns[pos] = chunk
	if w.events.LoadChunk != nil {
		if err := w.events.LoadChunk(pos); err != nil {
			return err
		}
	}
	return nil
}

func (w *World) handleForgetLevelChunkPacket(packet pk.Packet) error {
	var forget play.ForgetLevelChunk
	if err := packet.Scan(&forget); err != nil {
		return err
	}
	pos := level.ChunkPos(forget.Pos)
	var err error
	if w.events.UnloadChunk != nil {
		err = w.events.UnloadChunk(pos)
	}
	delete(w.Columns, pos)
	return err
}
