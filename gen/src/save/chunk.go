package save

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"

	"github.com/mj41/go-mc26/nbt"
)

// Chunk is a saved chunk: the shape is generated from the reader Mojang parses
// one with (save_gen.go, SerializableChunkData). This name is the one a caller
// uses; the generated one carries Mojang's.
type Chunk = SerializableChunkData

// Section is one 16-block-high slice of a saved chunk.
type Section = SerializableChunkDatasections

// PaletteContainer is a section's block states or biomes: the palette and the
// entries packed into longs, at the width the palette's size needs.
type PaletteContainer[T any] = PalettedContainerROPackedData[T]

// Load reads a chunk from a region file's sector: a compression byte, then the
// NBT of the chunk in that compression.
func (c *Chunk) Load(data []byte) (err error) {
	r, err := sectorReader(data)
	if err != nil {
		return err
	}
	_, err = nbt.NewDecoder(r).Decode(c)
	return
}

// sectorReader is the NBT of a region sector: a compression byte, then the
// data in that compression. An entity region's sectors are framed the same.
func sectorReader(data []byte) (r io.Reader, err error) {
	if len(data) == 0 {
		return nil, errors.New("empty sector")
	}
	r = bytes.NewReader(data[1:])
	switch data[0] {
	default:
		err = errors.New("unknown compression")
	case 1:
		r, err = gzip.NewReader(r)
	case 2:
		r, err = zlib.NewReader(r)
	case 3:
		// none compression
	}
	return r, err
}

// Data is the inverse of Load: the compression byte and the compressed NBT.
func (c *Chunk) Data(compressingType byte) ([]byte, error) {
	var buff bytes.Buffer

	buff.WriteByte(compressingType)
	var w io.Writer
	switch compressingType {
	default:
		return nil, errors.New("unknown compression")
	case 1:
		w = gzip.NewWriter(&buff)
	case 2:
		w = zlib.NewWriter(&buff)
	case 3:
		w = &buff
	}
	if err := nbt.NewEncoder(w).Encode(c, ""); err != nil {
		return nil, err
	}
	// the compressor holds the tail until it is closed
	if closer, ok := w.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return nil, err
		}
	}
	return buff.Bytes(), nil
}

// Entities is the NBT every entity carries, the keys of Entity.load and
// Entity.save; a specific type's keys are its own generated struct (Zombie,
// ItemFrame, …), one per entity type of this version (save_gen.go).
type Entities = Entity
