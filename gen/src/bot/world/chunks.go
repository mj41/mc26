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
		bot.PacketHandler{Priority: 64, ID: packetid.ClientboundLogin, F: w.onPlayerSpawn},
		bot.PacketHandler{Priority: 64, ID: packetid.ClientboundRespawn, F: w.onPlayerSpawn},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundLevelChunkWithLight, F: w.handleLevelChunkWithLightPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundForgetLevelChunk, F: w.handleForgetLevelChunkPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundChunkBatchFinished, F: w.handleChunkBatchFinishedPacket},
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
	currentDimType := w.c.Registries.DimensionType.GetByID(w.p.DimensionType)
	if currentDimType == nil {
		return fmt.Errorf("dimension type %d not found", w.p.DimensionType)
	}
	chunk := play.NewLevelChunkWithLight(int(currentDimType.Height) / 16)
	if err := packet.Scan(chunk); err != nil {
		return err
	}
	w.Columns[chunk.Pos] = chunk.Chunk
	if w.events.LoadChunk != nil {
		if err := w.events.LoadChunk(chunk.Pos); err != nil {
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
