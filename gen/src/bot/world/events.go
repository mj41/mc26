package world

import "github.com/mj41/go-mc26/level"

type EventsListener struct {
	LoadChunk   func(pos level.ChunkPos) error
	UnloadChunk func(pos level.ChunkPos) error
}
