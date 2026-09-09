package level

import (
	"bytes"
	"fmt"

	"github.com/mj41/go-mc26/level/block"
	"github.com/mj41/go-mc26/nbt"
	"github.com/mj41/go-mc26/save"
)

// The block-state palette of a saved section. This version always writes an
// entry as a compound of the block id and its properties; from 26.3 a state
// with no properties is its id alone, which is why the common file (gen/src)
// reads an either and this one does not.

// readStatesPalette reads a saved block-state palette: every entry is a
// compound of the block id and the properties, each as the string it is
// written as.
func readStatesPalette(palette []save.BlockState, data []int64) (paletteData *PaletteContainer[BlocksState], err error) {
	statePalette := make([]BlocksState, len(palette))
	for i, v := range palette {
		b, ok := defaultState(v.Name)
		if !ok {
			return nil, fmt.Errorf("unknown block id: %v", v.Name)
		}
		if len(v.Properties) > 0 {
			raw, err := nbt.Marshal(v.Properties)
			if err != nil {
				return nil, fmt.Errorf("block %s properties: %w", v.Name, err)
			}
			if err := nbt.Unmarshal(raw, &b); err != nil {
				return nil, fmt.Errorf("unmarshal block properties fail: %v", err)
			}
		}
		s, ok := block.ToStateID[b]
		if !ok {
			return nil, fmt.Errorf("unknown block: %v", b)
		}
		statePalette[i] = s
	}
	paletteData = NewStatesPaletteContainerWithData(16*16*16, unsigned(data), statePalette)
	return
}

func writeStatesPalette(paletteData *PaletteContainer[BlocksState]) (palette []save.BlockState, data []int64, err error) {
	rawPalette := paletteData.palette.export()
	palette = make([]save.BlockState, len(rawPalette))

	var buffer bytes.Buffer
	for i, v := range rawPalette {
		b := block.StateList[v]
		buffer.Reset()
		if err = nbt.NewEncoder(&buffer).Encode(b, ""); err != nil {
			return
		}
		props := map[string]string{}
		if _, err = nbt.NewDecoder(&buffer).Decode(&props); err != nil {
			return
		}
		palette[i] = save.BlockState{Name: b.ID(), Properties: props}
	}

	data = signed(paletteData.data.Raw())
	return
}

// newStatesContainer is an empty block-state container of this version's shape.
func newStatesContainer() *save.PaletteContainer[[]save.BlockState] {
	return &save.PaletteContainer[[]save.BlockState]{}
}

// paletteBlockIDs is the block id of every entry of a saved palette, for the
// test that reads a real region file: which shape an entry has is this file's
// business, not the test's.
func paletteBlockIDs(c *save.PaletteContainer[[]save.BlockState]) []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.Palette))
	for i, e := range c.Palette {
		out[i] = e.Name
	}
	return out
}

// defaultState is the state a block id stands for before its properties are
// applied: the one Mojang marks as the block's default. FromID gives the zero
// value of the block's struct, which for a block with properties is a state
// that may not exist.
func defaultState(id string) (block.Block, bool) {
	if s, ok := block.DefaultStateID[id]; ok && int(s) < len(block.StateList) {
		return block.StateList[s], true
	}
	b, ok := block.FromID[id]
	return b, ok
}
