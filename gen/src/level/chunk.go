package level

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/mj41/go-mc26/wire"
	"io"
	"math/bits"
	"strconv"

	"github.com/mj41/go-mc26/level/block"
	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/save"
)

// ChunkPos is a chunk position, x then z.
//
// On the wire it is one long, as ChunkPos.pack writes it: x in the low 32 bits
// and z in the high 32 bits, so the four bytes of z come first. Reading it as
// two ints in the order they arrive gives the halves the wrong way round, which
// is a chunk somewhere else entirely.
type ChunkPos [2]int32

func (c ChunkPos) WriteTo(w io.Writer) (int64, error) {
	return pk.Long(int64(uint32(c[0])) | int64(uint32(c[1]))<<32).WriteTo(w)
}

func (c *ChunkPos) ReadFrom(r io.Reader) (int64, error) {
	var v pk.Long
	n, err := v.ReadFrom(r)
	if err != nil {
		return n, err
	}
	*c = ChunkPos{int32(uint32(v)), int32(uint32(uint64(v) >> 32))}
	return n, nil
}

type Chunk struct {
	Sections    []Section
	HeightMaps  HeightMaps
	BlockEntity []BlockEntity
	Status      ChunkStatus
}

func EmptyChunk(secs int) *Chunk {
	sections := make([]Section, secs)
	for i := range sections {
		sections[i] = Section{
			BlockCount: 0,
			States:     NewStatesPaletteContainer(16*16*16, 0),
			Biomes:     NewBiomesPaletteContainer(4*4*4, 0),
		}
	}
	return &Chunk{
		Sections: sections,
		HeightMaps: HeightMaps{
			WorldSurfaceWG:         NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			WorldSurface:           NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			OceanFloorWG:           NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			OceanFloor:             NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			MotionBlocking:         NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			MotionBlockingNoLeaves: NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
		},
		Status: StatusEmpty,
	}
}

// ChunkFromSave convert save.Chunk to level.Chunk.
func ChunkFromSave(c *save.Chunk) (*Chunk, error) {
	secs := len(c.Sections)
	sections := make([]Section, secs)
	for _, v := range c.Sections {
		i := int32(v.Y) - c.YPos
		if i < 0 || i >= int32(secs) {
			return nil, fmt.Errorf("section Y value %d out of bounds", v.Y)
		}
		var err error
		sections[i].States, err = readStatesPalette(v.BlockStates.Palette, v.BlockStates.Data)
		if err != nil {
			return nil, err
		}
		sections[i].BlockCount = countNoneAirBlocks(&sections[i])
		sections[i].Biomes, err = readBiomesPalette(v.Biomes.Palette, v.Biomes.Data)
		if err != nil {
			return nil, err
		}
		sections[i].SkyLight = v.SkyLight
		sections[i].BlockLight = v.BlockLight
	}

	blockEntities := make([]BlockEntity, len(c.BlockEntities))
	for i, v := range c.BlockEntities {
		var tmp struct {
			ID string `nbt:"id"`
			X  int32  `nbt:"x"`
			Y  int32  `nbt:"y"`
			Z  int32  `nbt:"z"`
		}
		if err := v.Unmarshal(&tmp); err != nil {
			return nil, err
		}
		blockEntities[i].Tag.SetRaw(v) // NBT in 26.1 and 26.2, OptionalNBT from 26.3 on
		x, z := int(tmp.X-c.XPos<<4), int(tmp.Z-c.ZPos<<4)
		xz, ok := PackBlockEntityXZ(x, z)
		if !ok {
			return nil, errors.New("Packing a XZ(" + strconv.Itoa(x) + ", " + strconv.Itoa(z) + ") out of bound")
		}
		blockEntities[i].PackedXZ = xz
		blockEntities[i].Y = pk.Short(tmp.Y)
		blockEntities[i].Type = pk.VarInt(block.EntityTypes[tmp.ID])
	}

	bitsForHeight := bits.Len( /* chunk height in blocks */ uint(secs)*16 + 1)
	return &Chunk{
		Sections: sections,
		HeightMaps: HeightMaps{
			WorldSurface:           NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["WORLD_SURFACE_WG"]),
			WorldSurfaceWG:         NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["WORLD_SURFACE"]),
			OceanFloorWG:           NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["OCEAN_FLOOR_WG"]),
			OceanFloor:             NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["OCEAN_FLOOR"]),
			MotionBlocking:         NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["MOTION_BLOCKING"]),
			MotionBlockingNoLeaves: NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["MOTION_BLOCKING_NO_LEAVES"]),
		},
		BlockEntity: blockEntities,
		Status:      ChunkStatus(c.Status),
	}, nil
}

func readStatesPalette(palette []save.BlockState, data []uint64) (paletteData *PaletteContainer[BlocksState], err error) {
	statePalette := make([]BlocksState, len(palette))
	for i, v := range palette {
		b, ok := block.FromID[v.Name]
		if !ok {
			return nil, fmt.Errorf("unknown block id: %v", v.Name)
		}
		if v.Properties.Data != nil {
			if err := v.Properties.Unmarshal(&b); err != nil {
				return nil, fmt.Errorf("unmarshal block properties fail: %v", err)
			}
		}
		s, ok := block.ToStateID[b]
		if !ok {
			return nil, fmt.Errorf("unknown block: %v", b)
		}
		statePalette[i] = s
	}
	paletteData = NewStatesPaletteContainerWithData(16*16*16, data, statePalette)
	return
}

func readBiomesPalette(palette []save.BiomeState, data []uint64) (*PaletteContainer[BiomesState], error) {
	biomesRawPalette := make([]BiomesState, len(palette))
	for i, v := range palette {
		err := biomesRawPalette[i].UnmarshalText([]byte(v))
		if err != nil {
			return nil, err
		}
	}
	return NewBiomesPaletteContainerWithData(4*4*4, data, biomesRawPalette), nil
}

func countNoneAirBlocks(sec *Section) (blockCount int16) {
	for i := 0; i < 16*16*16; i++ {
		b := sec.GetBlock(i)
		if !block.IsAir(b) {
			blockCount++
		}
	}
	return
}

// ChunkToSave convert level.Chunk to save.Chunk
func ChunkToSave(c *Chunk, dst *save.Chunk) (err error) {
	secs := len(c.Sections)
	sections := make([]save.Section, secs)
	for i, v := range c.Sections {
		s := &sections[i]
		states := &s.BlockStates
		biomes := &s.Biomes
		s.Y = int8(int32(i) + dst.YPos)
		states.Palette, states.Data, err = writeStatesPalette(v.States)
		if err != nil {
			return
		}
		biomes.Palette, biomes.Data, err = writeBiomesPalette(v.Biomes)
		if err != nil {
			return
		}
		s.SkyLight = v.SkyLight
		s.BlockLight = v.BlockLight
	}
	dst.Sections = sections
	if dst.Heightmaps == nil {
		dst.Heightmaps = make(map[string][]uint64)
	}
	dst.Heightmaps["WORLD_SURFACE_WG"] = c.HeightMaps.WorldSurfaceWG.Raw()
	dst.Heightmaps["WORLD_SURFACE"] = c.HeightMaps.WorldSurface.Raw()
	dst.Heightmaps["OCEAN_FLOOR_WG"] = c.HeightMaps.OceanFloorWG.Raw()
	dst.Heightmaps["OCEAN_FLOOR"] = c.HeightMaps.OceanFloor.Raw()
	dst.Heightmaps["MOTION_BLOCKING"] = c.HeightMaps.MotionBlocking.Raw()
	dst.Heightmaps["MOTION_BLOCKING_NO_LEAVES"] = c.HeightMaps.MotionBlockingNoLeaves.Raw()
	dst.Status = string(c.Status)
	return
}

func writeStatesPalette(paletteData *PaletteContainer[BlocksState]) (palette []save.BlockState, data []uint64, err error) {
	rawPalette := paletteData.palette.export()
	palette = make([]save.BlockState, len(rawPalette))

	var buffer bytes.Buffer
	for i, v := range rawPalette {
		b := block.StateList[v]
		palette[i].Name = b.ID()

		buffer.Reset()
		err = nbt.NewEncoder(&buffer).Encode(b, "")
		if err != nil {
			return
		}
		_, err = nbt.NewDecoder(&buffer).Decode(&palette[i].Properties)
		if err != nil {
			return
		}
	}

	data = make([]uint64, len(paletteData.data.Raw()))
	copy(data, paletteData.data.Raw())
	return
}

func writeBiomesPalette(paletteData *PaletteContainer[BiomesState]) (palette []save.BiomeState, data []uint64, err error) {
	rawPalette := paletteData.palette.export()
	palette = make([]save.BiomeState, len(rawPalette))

	var biomeID []byte
	for i, v := range rawPalette {
		biomeID, err = v.MarshalText()
		if err != nil {
			return
		}
		palette[i] = save.BiomeState(biomeID)
	}

	data = make([]uint64, len(paletteData.data.Raw()))
	copy(data, paletteData.data.Raw())
	return
}

// HeightmapData returns the packed heightmaps by type id, the form of a
// level_chunk_with_light packet: 0 world_surface_wg, 1 world_surface,
// 2 ocean_floor_wg, 3 ocean_floor, 4 motion_blocking, 5 motion_blocking_no_leaves.
func (c *Chunk) HeightmapData() map[int32][]uint64 {
	out := map[int32][]uint64{}
	for i, bs := range []*BitStorage{c.HeightMaps.WorldSurfaceWG, c.HeightMaps.WorldSurface, c.HeightMaps.OceanFloorWG,
		c.HeightMaps.OceanFloor, c.HeightMaps.MotionBlocking, c.HeightMaps.MotionBlockingNoLeaves} {
		if bs != nil {
			out[int32(i)] = bs.Raw()
		}
	}
	return out
}

// SetHeightmapData installs packed heightmaps by type id (see HeightmapData).
func (c *Chunk) SetHeightmapData(data map[int32][]uint64) {
	bitsForHeight := bits.Len(uint(len(c.Sections))*16 + 1)
	for typ, raw := range data {
		bs := NewBitStorage(bitsForHeight, 16*16, raw)
		switch typ {
		case 0:
			c.HeightMaps.WorldSurfaceWG = bs
		case 1:
			c.HeightMaps.WorldSurface = bs
		case 2:
			c.HeightMaps.OceanFloorWG = bs
		case 3:
			c.HeightMaps.OceanFloor = bs
		case 4:
			c.HeightMaps.MotionBlocking = bs
		case 5:
			c.HeightMaps.MotionBlockingNoLeaves = bs
		}
	}
}

// WireSections is the chunk's sections in the chunk packet's form (the
// generated LevelChunkSection), bottom section first.
func (c *Chunk) WireSections() []LevelChunkSection {
	out := make([]LevelChunkSection, len(c.Sections))
	for i := range c.Sections {
		out[i] = c.Sections[i].ToWire()
	}
	return out
}

// PutSections fills the chunk from the sections of a chunk packet, which has
// as many as the dimension has (its height in blocks / 16).
func (c *Chunk) PutSections(secs []LevelChunkSection) error {
	if len(secs) != len(c.Sections) {
		return fmt.Errorf("a chunk packet with %d sections for a dimension of %d", len(secs), len(c.Sections))
	}
	for i := range secs {
		if err := c.Sections[i].FromWire(&secs[i]); err != nil {
			return err
		}
	}
	return nil
}

type HeightMaps struct {
	WorldSurfaceWG         *BitStorage // test = NOT_AIR
	WorldSurface           *BitStorage // test = NOT_AIR
	OceanFloorWG           *BitStorage // test = MATERIAL_MOTION_BLOCKING
	OceanFloor             *BitStorage // test = MATERIAL_MOTION_BLOCKING
	MotionBlocking         *BitStorage // test = BlocksMotion or isFluid
	MotionBlockingNoLeaves *BitStorage // test = BlocksMotion or isFluid
}

// BlockEntity is a block entity as the chunk packet carries it (generated into
// package wire from the schema): the position packed into a byte and a short,
// the block entity type's registry id and its NBT.
type BlockEntity = wire.LevelChunkPacketDataBlockEntityInfo

// BlockEntityXZ unpacks the x and z of a block entity within its chunk.
func BlockEntityXZ(b BlockEntity) (X, Z int) {
	return int((uint8(b.PackedXZ) >> 4) & 0xF), int(uint8(b.PackedXZ) & 0xF)
}

// PackBlockEntityXZ packs an x and z within a chunk into the byte the packet
// carries; false when either is out of range.
func PackBlockEntityXZ(X, Z int) (pk.Byte, bool) {
	if X > 0xF || Z > 0xF || X < 0 || Z < 0 {
		return 0, false
	}
	return pk.Byte(X<<4 | Z), true
}

type Section struct {
	BlockCount int16
	// FluidCount is the number of non-empty fluid states in the section (26.1+, protocol 775+).
	// It is only informational for clients; 0 is accepted by vanilla.
	FluidCount int16
	States     *PaletteContainer[BlocksState]
	Biomes     *PaletteContainer[BiomesState]
	// Half a byte per light value.
	// Could be nil if not exist
	SkyLight   []byte // len() == 2048
	BlockLight []byte // len() == 2048
}

func (s *Section) GetBlock(i int) BlocksState {
	return s.States.Get(i)
}

func (s *Section) SetBlock(i int, v BlocksState) {
	if !block.IsAir(s.States.Get(i)) {
		s.BlockCount--
	}
	if !block.IsAir(v) {
		s.BlockCount++
	}
	s.States.Set(i, v)
}

// ToWire is the section in the chunk packet's form: the block and fluid
// counts, then the two containers (LevelChunkSection, generated from prims.json).
func (s *Section) ToWire() LevelChunkSection {
	return LevelChunkSection{
		NonEmptyBlockCount: pk.Short(s.BlockCount),
		FluidCount:         pk.Short(s.FluidCount),
		States:             StatesToWire(s.States),
		Biomes:             BiomesToWire(s.Biomes),
	}
}

// FromWire fills the section from the chunk packet's form.
func (s *Section) FromWire(w *LevelChunkSection) (err error) {
	s.BlockCount = int16(w.NonEmptyBlockCount)
	s.FluidCount = int16(w.FluidCount)
	if s.States, err = StatesFromWire(&w.States); err != nil {
		return err
	}
	s.Biomes, err = BiomesFromWire(&w.Biomes)
	return err
}
