package level

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"strconv"

	"github.com/mj41/go-mc26/level/block"
	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/save"
)

type ChunkPos [2]int32

func (c ChunkPos) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Int(c[0]).WriteTo(w)
	if err != nil {
		return
	}
	n1, err := pk.Int(c[1]).WriteTo(w)
	return n + n1, err
}

func (c *ChunkPos) ReadFrom(r io.Reader) (n int64, err error) {
	var x, z pk.Int
	if n, err = x.ReadFrom(r); err != nil {
		return n, err
	}
	var n1 int64
	if n1, err = z.ReadFrom(r); err != nil {
		return n + n1, err
	}
	*c = ChunkPos{int32(x), int32(z)}
	return n + n1, nil
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
		blockEntities[i].Data = v
		if x, z := int(tmp.X-c.XPos<<4), int(tmp.Z-c.ZPos<<4); !blockEntities[i].PackXZ(x, z) {
			return nil, errors.New("Packing a XZ(" + strconv.Itoa(x) + ", " + strconv.Itoa(z) + ") out of bound")
		}
		blockEntities[i].Y = int16(tmp.Y)
		blockEntities[i].Type = block.EntityTypes[tmp.ID]
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

func (c *Chunk) WriteTo(w io.Writer) (int64, error) {
	data, err := c.Data()
	if err != nil {
		return 0, err
	}
	light := lightData{
		SkyLightMask:   make(pk.BitSet, (16*16*16-1)>>6+1),
		BlockLightMask: make(pk.BitSet, (16*16*16-1)>>6+1),
		SkyLight:       []pk.ByteArray{},
		BlockLight:     []pk.ByteArray{},
	}
	for i, v := range c.Sections {
		if v.SkyLight != nil {
			light.SkyLightMask.Set(i, true)
			light.SkyLight = append(light.SkyLight, v.SkyLight)
		}
		if v.BlockLight != nil {
			light.BlockLightMask.Set(i, true)
			light.BlockLight = append(light.BlockLight, v.BlockLight)
		}
	}

	// Protocol 774+: heightmaps are serialized as VarInt-typed array entries
	var hmEntries []heightMapEntry
	if bs := c.HeightMaps.WorldSurfaceWG; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 0, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.WorldSurface; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 1, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.OceanFloorWG; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 2, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.OceanFloor; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 3, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.MotionBlocking; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 4, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.MotionBlockingNoLeaves; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 5, Data: bs.Raw()})
	}

	return pk.Tuple{
		pk.Array(hmEntries),
		pk.ByteArray(data),
		pk.Array(c.BlockEntity),
		&light,
	}.WriteTo(w)
}

func (c *Chunk) ReadFrom(r io.Reader) (int64, error) {
	var (
		hmEntries []heightMapEntry
		data      pk.ByteArray
	)

	n, err := pk.Tuple{
		pk.Array(&hmEntries),
		&data,
		pk.Array(&c.BlockEntity),
		&lightData{
			SkyLightMask:   make(pk.BitSet, (16*16*16-1)>>6+1),
			BlockLightMask: make(pk.BitSet, (16*16*16-1)>>6+1),
			SkyLight:       []pk.ByteArray{},
			BlockLight:     []pk.ByteArray{},
		},
	}.ReadFrom(r)
	if err != nil {
		return n, err
	}

	if os.Getenv("GOMC_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[GOMC_DEBUG] Chunk ReadFrom: %d heightmap entries, data=%d bytes, %d block entities\n",
			len(hmEntries), len(data), len(c.BlockEntity))
		for i, entry := range hmEntries {
			fmt.Fprintf(os.Stderr, "[GOMC_DEBUG]   heightmap[%d]: type=%d, longs=%d\n", i, entry.Type, len(entry.Data))
		}
		if len(data) > 20 {
			fmt.Fprintf(os.Stderr, "[GOMC_DEBUG]   data first 20 bytes: %x\n", data[:20])
		}
	}

	bitsForHeight := bits.Len( /* chunk height in blocks */ uint(len(c.Sections))*16 + 1)
	for _, entry := range hmEntries {
		bs := NewBitStorage(bitsForHeight, 16*16, entry.Data)
		switch entry.Type {
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

	err = c.PutData(data)
	return n, err
}

func (c *Chunk) Data() ([]byte, error) {
	var buff bytes.Buffer
	for i := range c.Sections {
		_, err := c.Sections[i].WriteTo(&buff)
		if err != nil {
			return nil, err
		}
	}
	return buff.Bytes(), nil
}

func (c *Chunk) PutData(data []byte) error {
	r := bytes.NewReader(data)
	for i := range c.Sections {
		_, err := c.Sections[i].ReadFrom(r)
		if err != nil {
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

// heightMapEntry is a single heightmap in the protocol 774+ chunk format.
// Each entry has a type enum and a VarInt-prefixed array of int64 (packed data).
type heightMapEntry struct {
	Type int32    // 0=world_surface_wg, 1=world_surface, 2=ocean_floor_wg, 3=ocean_floor, 4=motion_blocking, 5=motion_blocking_no_leaves
	Data []uint64 // packed heightmap data
}

func (e heightMapEntry) WriteTo(w io.Writer) (int64, error) {
	longs := make([]pk.Long, len(e.Data))
	for i, v := range e.Data {
		longs[i] = pk.Long(v)
	}
	return pk.Tuple{
		pk.VarInt(e.Type),
		pk.Array(longs),
	}.WriteTo(w)
}

func (e *heightMapEntry) ReadFrom(r io.Reader) (int64, error) {
	var longs []pk.Long
	n, err := pk.Tuple{
		(*pk.VarInt)(&e.Type),
		pk.Array(&longs),
	}.ReadFrom(r)
	if err == nil {
		e.Data = make([]uint64, len(longs))
		for i, v := range longs {
			e.Data[i] = uint64(v)
		}
	}
	return n, err
}

type BlockEntity struct {
	XZ   int8
	Y    int16
	Type block.EntityType
	Data nbt.RawMessage
}

func (b BlockEntity) UnpackXZ() (X, Z int) {
	return int((uint8(b.XZ) >> 4) & 0xF), int(uint8(b.XZ) & 0xF)
}

func (b *BlockEntity) PackXZ(X, Z int) bool {
	if X > 0xF || Z > 0xF || X < 0 || Z < 0 {
		return false
	}
	b.XZ = int8(X<<4 | Z)
	return true
}

func (b BlockEntity) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{
		pk.Byte(b.XZ),
		pk.Short(b.Y),
		pk.VarInt(b.Type),
		pk.NBT(b.Data),
	}.WriteTo(w)
}

func (b *BlockEntity) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{
		(*pk.Byte)(&b.XZ),
		(*pk.Short)(&b.Y),
		(*pk.VarInt)(&b.Type),
		pk.NBT(&b.Data),
	}.ReadFrom(r)
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

// WriteTo encodes the section in the 26.1+ network layout:
// blockCount:Short, fluidCount:Short, states:PalettedContainer, biomes:PalettedContainer.
func (s *Section) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		pk.Short(s.BlockCount),
		pk.Short(s.FluidCount),
		s.States,
		s.Biomes,
	}.WriteTo(w)
}

func (s *Section) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{
		(*pk.Short)(&s.BlockCount),
		(*pk.Short)(&s.FluidCount),
		s.States,
		s.Biomes,
	}.ReadFrom(r)
}

type lightData struct {
	SkyLightMask   pk.BitSet
	BlockLightMask pk.BitSet
	SkyLight       []pk.ByteArray
	BlockLight     []pk.ByteArray
}

func bitSetRev(set pk.BitSet) pk.BitSet {
	rev := make(pk.BitSet, len(set))
	for i := range rev {
		rev[i] = ^set[i]
	}
	return rev
}

func (l *lightData) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		l.SkyLightMask,
		l.BlockLightMask,
		bitSetRev(l.SkyLightMask),
		bitSetRev(l.BlockLightMask),
		pk.Array(l.SkyLight),
		pk.Array(l.BlockLight),
	}.WriteTo(w)
}

func (l *lightData) ReadFrom(r io.Reader) (int64, error) {
	var RevSkyLightMask, RevBlockLightMask pk.BitSet
	return pk.Tuple{
		&l.SkyLightMask,
		&l.BlockLightMask,
		&RevSkyLightMask,
		&RevBlockLightMask,
		pk.Array(&l.SkyLight),
		pk.Array(&l.BlockLight),
	}.ReadFrom(r)
}
