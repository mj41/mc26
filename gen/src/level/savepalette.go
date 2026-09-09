package level

import (
	"bytes"
	"fmt"

	"github.com/mj41/go-mc26/level/block"
	"github.com/mj41/go-mc26/nbt"
	"github.com/mj41/go-mc26/registry"
	"github.com/mj41/go-mc26/save"
)

// The block-state palette of a saved section, which is the one shape of a save
// format that differs between the versions this library builds for: from 26.3
// a state with no properties is written as its id alone and one with them as a
// compound, so the palette is an either; 26.1 and 26.2 always write the
// compound. Older versions replace this file (gen/src/_versions/<version>).

// readStatesPalette reads a saved block-state palette. An entry is either the
// block's id alone (the state has no properties, which is most of them) or a
// compound of the id and the properties, each as the string it is written as.
func readStatesPalette(palette []registry.Either[string, save.BlockState], data []int64) (paletteData *PaletteContainer[BlocksState], err error) {
	statePalette := make([]BlocksState, len(palette))
	for i, v := range palette {
		id, props := "", map[string]string(nil)
		switch {
		case v.Left != nil:
			id = *v.Left
		case v.Right != nil:
			id, props = v.Right.ID, v.Right.Properties
		default:
			return nil, fmt.Errorf("palette entry %d is neither an id nor a block state", i)
		}
		b, ok := defaultState(id)
		if !ok {
			return nil, fmt.Errorf("unknown block id: %v", id)
		}
		if len(props) > 0 {
			raw, err := nbt.Marshal(props)
			if err != nil {
				return nil, fmt.Errorf("block %s properties: %w", id, err)
			}
			if err := nbt.Unmarshal(raw, &b); err != nil {
				return nil, fmt.Errorf("unmarshal block properties fail: %v", err)
			}
		}
		s, ok := block.ToStateID[b]
		if !ok {
			return nil, fmt.Errorf("block %s with properties %v is not a state this version has (%#v)", id, props, b)
		}
		statePalette[i] = s
	}
	paletteData = NewStatesPaletteContainerWithData(16*16*16, unsigned(data), statePalette)
	return
}

func writeStatesPalette(paletteData *PaletteContainer[BlocksState]) (palette []registry.Either[string, save.BlockState], data []int64, err error) {
	rawPalette := paletteData.palette.export()
	palette = make([]registry.Either[string, save.BlockState], len(rawPalette))

	// The list is homogeneous, as every NBT list is: a bare id where no state has
	// properties, and the compound form throughout as soon as one does.
	ids := make([]string, len(rawPalette))
	props := make([]map[string]string, len(rawPalette))
	anyProps := false
	var buffer bytes.Buffer
	for i, v := range rawPalette {
		b := block.StateList[v]
		buffer.Reset()
		if err = nbt.NewEncoder(&buffer).Encode(b, ""); err != nil {
			return
		}
		p := map[string]string{}
		if _, err = nbt.NewDecoder(&buffer).Decode(&p); err != nil {
			return
		}
		ids[i], props[i] = b.ID(), p
		anyProps = anyProps || len(p) > 0
	}
	for i := range rawPalette {
		if !anyProps {
			id := ids[i]
			palette[i].Left = &id
			continue
		}
		palette[i].Right = &save.BlockState{ID: ids[i], Properties: props[i]}
	}

	data = signed(paletteData.data.Raw())
	return
}

// newStatesContainer is an empty block-state container of this version's shape.
func newStatesContainer() *save.PaletteContainer[[]registry.Either[string, save.BlockState]] {
	return &save.PaletteContainer[[]registry.Either[string, save.BlockState]]{}
}

// paletteBlockIDs is the block id of every entry of a saved palette, for the
// test that reads a real region file: which shape an entry has is this file's
// business, not the test's.
func paletteBlockIDs(c *save.PaletteContainer[[]registry.Either[string, save.BlockState]]) []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.Palette))
	for i, e := range c.Palette {
		switch {
		case e.Left != nil:
			out[i] = *e.Left
		case e.Right != nil:
			out[i] = e.Right.ID
		}
	}
	return out
}

// defaultState is the state a bare block id stands for: the one Mojang marks as
// the block's default. FromID gives the zero value of the block's struct, which
// for a block with properties is a state that may not exist.
func defaultState(id string) (block.Block, bool) {
	if s, ok := block.DefaultStateID[id]; ok && int(s) < len(block.StateList) {
		return block.StateList[s], true
	}
	b, ok := block.FromID[id]
	return b, ok
}
