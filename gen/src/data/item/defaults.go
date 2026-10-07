package item

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/mj41/go-mc26/level/component"
)

// The default components of every item, as the game's item registry has them
// (Item.components()): the tool rules of a pickaxe, the food of bread, the
// attack damage of a sword. An item stack on the wire carries only its patch
// against these. The tables are generated (defaults_gen.go); this file
// decodes them with the decoder of a stack's patch.

var (
	defaultsOnce sync.Once
	defaults     [][]component.Typed
	defaultsErr  error
)

// DefaultComponents returns the default components of item id, each with its
// data_component_type id. A component the extraction could not write (one
// that names a registry only a server's data packs make, as a jukebox song)
// is not among them.
func DefaultComponents(id ID) ([]component.Typed, error) {
	defaultsOnce.Do(func() {
		defaults = make([][]component.Typed, len(defaultWire))
		for i, w := range defaultWire {
			raw, err := base64.StdEncoding.DecodeString(w)
			if err != nil {
				defaultsErr = fmt.Errorf("item %d: %w", i, err)
				return
			}
			var p component.Patch
			if _, err := p.ReadFrom(bytes.NewReader(raw)); err != nil {
				defaultsErr = fmt.Errorf("item %d: %w", i, err)
				return
			}
			defaults[i] = []component.Typed(p.Positive)
		}
	})
	if defaultsErr != nil {
		return nil, defaultsErr
	}
	if int(id) >= len(defaults) {
		return nil, fmt.Errorf("no item %d", id)
	}
	return defaults[id], nil
}

// DefaultComponent returns item id's default component of the type of T
// (*component.Tool, *component.Food, …), or nil when it has none.
func DefaultComponent[T component.DataComponent](id ID) T {
	var zero T
	cs, err := DefaultComponents(id)
	if err != nil {
		return zero
	}
	for _, c := range cs {
		if v, ok := c.Value.(T); ok {
			return v
		}
	}
	return zero
}
