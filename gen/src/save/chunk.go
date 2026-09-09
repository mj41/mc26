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
	var r io.Reader = bytes.NewReader(data[1:])

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
	if err != nil {
		return err
	}

	d := nbt.NewDecoder(r)
	_, err = d.Decode(c)
	return
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
	err := nbt.NewEncoder(w).Encode(c, "")
	return buff.Bytes(), err
}

// Entities is the NBT an entity of a saved chunk or a player file carries. It
// is not generated: Mojang reads an entity through a chain of
// readAdditionalSaveData methods, one per class in its hierarchy, which the
// save extractor does not follow yet.
type Entities struct {
	Pos, Motion  [3]float64
	Rotation     [3]float32
	FallDistance float32
	Fire, Air    int16

	OnGround       bool
	Invulnerable   bool
	PortalCooldown int32
	UUID           [4]int32

	CustomName        string
	CustomNameVisible bool
	Silent            bool
	NoGravity         bool
	Glowing           bool
	TicksFrozen       int32
	HasVisualFire     bool
	Tags              []string
}
