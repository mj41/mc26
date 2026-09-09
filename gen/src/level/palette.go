package level

import (
	"fmt"
	"github.com/mj41/go-mc26/wire"
	"strconv"

	"github.com/mj41/go-mc26/level/biome"
	"github.com/mj41/go-mc26/level/block"
	pk "github.com/mj41/go-mc26/net/packet"
)

type State interface {
	~int
}
type (
	BlocksState = block.StateID
	BiomesState = biome.Type
)

type PaletteContainer[T State] struct {
	bits    int
	config  paletteCfg[T]
	palette palette[T]
	data    *BitStorage
}

func NewStatesPaletteContainer(length int, defaultValue BlocksState) *PaletteContainer[BlocksState] {
	return &PaletteContainer[BlocksState]{
		bits:    0,
		config:  statesCfg{},
		palette: &singleValuePalette[BlocksState]{v: defaultValue},
		data:    NewBitStorage(0, length, nil),
	}
}

func NewStatesPaletteContainerWithData(length int, data []uint64, pat []BlocksState) *PaletteContainer[BlocksState] {
	var p palette[BlocksState]
	n := calcBitsPerValue(length, len(data))
	switch n {
	case 0:
		p = &singleValuePalette[BlocksState]{pat[0]}
	case 1, 2, 3, 4:
		n = 4
		p = &linearPalette[BlocksState]{
			values: pat,
			bits:   n,
		}
	case 5, 6, 7, 8:
		ids := make(map[BlocksState]int)
		for i, v := range pat {
			ids[v] = i
		}
		p = &hashPalette[BlocksState]{
			ids:    ids,
			values: pat,
			bits:   n,
		}
	default:
		p = &globalPalette[BlocksState]{}
	}
	return &PaletteContainer[BlocksState]{
		bits:    n,
		config:  statesCfg{},
		palette: p,
		data:    NewBitStorage(n, length, data),
	}
}

func NewBiomesPaletteContainer(length int, defaultValue BiomesState) *PaletteContainer[BiomesState] {
	return &PaletteContainer[BiomesState]{
		bits:    0,
		config:  biomesCfg{},
		palette: &singleValuePalette[BiomesState]{v: defaultValue},
		data:    NewBitStorage(0, length, nil),
	}
}

func NewBiomesPaletteContainerWithData(length int, data []uint64, pat []BiomesState) *PaletteContainer[BiomesState] {
	var p palette[BiomesState]
	n := calcBitsPerValue(length, len(data))
	switch n {
	case 0:
		p = &singleValuePalette[BiomesState]{pat[0]}
	case 1, 2, 3:
		p = &linearPalette[BiomesState]{
			values: pat,
			bits:   n,
		}
	default:
		p = &globalPalette[BiomesState]{}
	}
	return &PaletteContainer[BiomesState]{
		bits:    n,
		config:  biomesCfg{},
		palette: p,
		data:    NewBitStorage(n, length, data),
	}
}

func (p *PaletteContainer[T]) Get(i int) T {
	return p.palette.value(p.data.Get(i))
}

func (p *PaletteContainer[T]) Set(i int, v T) {
	if vv, ok := p.palette.id(v); ok {
		p.data.Set(i, vv)
	} else {
		length := p.data.Len()
		// resize
		newContainer := PaletteContainer[T]{
			bits:    vv,
			config:  p.config,
			palette: p.config.create(vv),
			data:    NewBitStorage(p.config.bits(vv), length, nil),
		}
		// copy
		for i := 0; i < length; i++ {
			newContainer.Set(i, p.Get(i))
		}

		if vv, ok := newContainer.palette.id(v); !ok {
			panic("not reachable")
		} else {
			newContainer.data.Set(i, vv)
		}
		*p = newContainer
	}
}

// Palette export the raw palette values for @maxsupermanhd.
// Others shouldn't call this because this might be removed
// after max doesn't need it anymore.
func (p *PaletteContainer[T]) Palette() []T {
	return p.palette.export()
}

type paletteCfg[T State] interface {
	bits(int) int
	create(bits int) palette[T]
}

type statesCfg struct{}

// bits is the storage width for a palette byte: the table prims.json gives the
// chunk packet, generated into section_gen.go.
func (s statesCfg) bits(bits int) int { return PalettedContainerBlockStatesDataWidth(bits) }

func (s statesCfg) create(bits int) palette[BlocksState] {
	switch bits {
	case 0:
		return &singleValuePalette[BlocksState]{v: -1}
	case 1, 2, 3, 4:
		return &linearPalette[BlocksState]{bits: 4, values: make([]BlocksState, 0, 1<<4)}
	case 5, 6, 7, 8:
		return &hashPalette[BlocksState]{
			bits:   bits,
			ids:    make(map[BlocksState]int),
			values: make([]BlocksState, 0, 1<<bits),
		}
	default:
		return &globalPalette[BlocksState]{}
	}
}

type biomesCfg struct{}

func (b biomesCfg) bits(bits int) int { return PalettedContainerBiomesDataWidth(bits) }

func (b biomesCfg) create(bits int) palette[BiomesState] {
	switch bits {
	case 0:
		return &singleValuePalette[BiomesState]{v: -1}
	case 1, 2, 3:
		return &linearPalette[BiomesState]{bits: bits, values: make([]BiomesState, 0, 1<<bits)}
	default:
		return &globalPalette[BiomesState]{}
	}
}

type palette[T State] interface {
	// id return the index of state v in the palette and true if existed.
	// otherwise return the new bits for resize and false.
	id(v T) (int, bool)
	value(i int) T
	export() []T
}

type singleValuePalette[T State] struct {
	v T
}

func (s *singleValuePalette[T]) id(v T) (int, bool) {
	if s.v == v {
		return 0, true
	}
	// We have 2 values now. At least 1 bit is required.
	return 1, false
}

func (s *singleValuePalette[T]) value(i int) T {
	if i == 0 {
		return s.v
	}
	panic("singleValuePalette: " + strconv.Itoa(i) + " out of bounds")
}

func (s *singleValuePalette[T]) export() []T {
	return []T{s.v}
}

type linearPalette[T State] struct {
	values []T
	bits   int
}

func (l *linearPalette[T]) id(v T) (int, bool) {
	for i, t := range l.values {
		if t == v {
			return i, true
		}
	}
	if cap(l.values)-len(l.values) > 0 {
		l.values = append(l.values, v)
		return len(l.values) - 1, true
	}
	return l.bits + 1, false
}

func (l *linearPalette[T]) value(i int) T {
	if i >= 0 && i < len(l.values) {
		return l.values[i]
	}
	panic("linearPalette: " + strconv.Itoa(i) + " out of bounds")
}

func (l *linearPalette[T]) export() []T {
	return l.values
}

type hashPalette[T State] struct {
	ids    map[T]int
	values []T
	bits   int
}

func (h *hashPalette[T]) id(v T) (int, bool) {
	if i, ok := h.ids[v]; ok {
		return i, true
	}
	if cap(h.values)-len(h.values) > 0 {
		h.ids[v] = len(h.values)
		h.values = append(h.values, v)
		return len(h.values) - 1, true
	}
	return h.bits + 1, false
}

func (h *hashPalette[T]) value(i int) T {
	if i >= 0 && i < len(h.values) {
		return h.values[i]
	}
	panic("hashPalette: " + strconv.Itoa(i) + " out of bounds")
}

func (h *hashPalette[T]) export() []T {
	return h.values
}

type globalPalette[T State] struct{}

func (g *globalPalette[T]) id(v T) (int, bool) {
	return int(v), true
}

func (g *globalPalette[T]) value(i int) T {
	return T(i)
}

func (g *globalPalette[T]) export() []T {
	return []T{}
}

// The chunk packet's form of a container is generated from prims.json into
// section_gen.go (PalettedContainerBlockStates, PalettedContainerBiomes): the
// palette byte, the single value or the palette it selects, and the packed
// longs. A container is built from it and written back as it.

// StatesFromWire builds a block state container from the packet's form.
func StatesFromWire(w *PalettedContainerBlockStates) (*PaletteContainer[BlocksState], error) {
	bits := int(w.Bits)
	var p palette[BlocksState]
	switch {
	case bits == 0:
		p = &singleValuePalette[BlocksState]{v: BlocksState(w.Single)}
	case bits <= 4:
		p = &linearPalette[BlocksState]{bits: 4, values: statesOf(w.Palette)}
	case bits <= 8:
		values := statesOf(w.Palette)
		ids := make(map[BlocksState]int, len(values))
		for i, v := range values {
			ids[v] = i
		}
		p = &hashPalette[BlocksState]{bits: bits, ids: ids, values: values}
	default:
		p = &globalPalette[BlocksState]{}
	}
	return containerFromWire[BlocksState](statesCfg{}, p, PalettedContainerBlockStatesDataWidth(bits), 16*16*16, w.Data)
}

// BiomesFromWire builds a biome container from the packet's form.
func BiomesFromWire(w *PalettedContainerBiomes) (*PaletteContainer[BiomesState], error) {
	bits := int(w.Bits)
	var p palette[BiomesState]
	switch {
	case bits == 0:
		p = &singleValuePalette[BiomesState]{v: BiomesState(w.Single)}
	case bits <= 3:
		p = &linearPalette[BiomesState]{bits: bits, values: biomesOf(w.Palette)}
	default:
		p = &globalPalette[BiomesState]{}
	}
	return containerFromWire[BiomesState](biomesCfg{}, p, PalettedContainerBiomesDataWidth(bits), 4*4*4, w.Data)
}

func containerFromWire[T State](cfg paletteCfg[T], p palette[T], width, length int, data wire.Packed) (*PaletteContainer[T], error) {
	if want := wire.PackedLongs(length, width); len(data) != want {
		return nil, fmt.Errorf("a container of %d values at %d bits has %d longs, not %d", length, width, want, len(data))
	}
	c := &PaletteContainer[T]{bits: width, config: cfg, palette: p, data: NewBitStorage(width, length, data)}
	return c, c.data.Fix(width)
}

// StatesToWire is the packet's form of a block state container.
func StatesToWire(p *PaletteContainer[BlocksState]) PalettedContainerBlockStates {
	w := PalettedContainerBlockStates{Bits: pk.Byte(p.bits), Data: p.data.Raw()}
	switch p.palette.(type) {
	case *singleValuePalette[BlocksState]:
		w.Single = pk.VarInt(p.palette.export()[0])
	case *linearPalette[BlocksState], *hashPalette[BlocksState]:
		for _, v := range p.palette.export() {
			w.Palette = append(w.Palette, pk.VarInt(v))
		}
	}
	return w
}

// BiomesToWire is the packet's form of a biome container.
func BiomesToWire(p *PaletteContainer[BiomesState]) PalettedContainerBiomes {
	w := PalettedContainerBiomes{Bits: pk.Byte(p.bits), Data: p.data.Raw()}
	switch p.palette.(type) {
	case *singleValuePalette[BiomesState]:
		w.Single = pk.VarInt(p.palette.export()[0])
	case *linearPalette[BiomesState]:
		for _, v := range p.palette.export() {
			w.Palette = append(w.Palette, pk.VarInt(v))
		}
	}
	return w
}

func statesOf(ids wire.List[pk.VarInt, *pk.VarInt]) []BlocksState {
	out := make([]BlocksState, len(ids))
	for i, v := range ids {
		out[i] = BlocksState(v)
	}
	return out
}

func biomesOf(ids wire.List[pk.VarInt, *pk.VarInt]) []BiomesState {
	out := make([]BiomesState, len(ids))
	for i, v := range ids {
		out[i] = BiomesState(v)
	}
	return out
}
