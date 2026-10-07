package registry

import (
	"fmt"
	"slices"

	"github.com/mj41/go-mc26/nbt"
)

type Registry[E any] struct {
	keys    map[string]int32
	values  []E
	indices map[*E]int32
	tags    map[string][]*E

	keepRaw bool
	raws    []nbt.RawMessage // the NBT of every entry, when keepRaw is set
}

func NewRegistry[E any]() Registry[E] {
	return Registry[E]{
		keys:    make(map[string]int32),
		values:  make([]E, 0, 256),
		indices: make(map[*E]int32),
		tags:    make(map[string][]*E),
	}
}

func (r *Registry[E]) Clear() {
	r.keys = make(map[string]int32)
	r.values = r.values[:0]
	r.indices = make(map[*E]int32)
	r.tags = make(map[string][]*E)
	r.raws = nil
}

// SetKeepRaw makes the registry keep the NBT of every entry the server sends,
// so Unexpected can report what the element type does not cover. It costs the
// memory of a second copy of the registry data and must be set before joining.
func (r *Registry[E]) SetKeepRaw(on bool) { r.keepRaw = on }

// Raw returns the NBT the server sent for an entry, when SetKeepRaw was on.
func (r *Registry[E]) Raw(key string) (nbt.RawMessage, bool) {
	id, ok := r.keys[key]
	if !ok || int(id) >= len(r.raws) {
		return nbt.RawMessage{}, false
	}
	return r.raws[id], true
}

// Unexpected re-reads every kept entry into the element type, refusing keys
// the type does not have, and returns one error per entry that carries
// something this build does not know: a registry element gained a field, or a
// generated tag is wrong. It returns nothing when SetKeepRaw was off, and
// nothing for a registry whose element is nbt.RawMessage.
func (r *Registry[E]) Unexpected() []error {
	var out []error
	names := make([]string, len(r.values))
	for name, id := range r.keys {
		if int(id) < len(names) {
			names[id] = name
		}
	}
	for id, raw := range r.raws {
		if raw.Type == nbt.TagEnd {
			continue // an entry the server sent without data
		}
		var v E
		if err := raw.UnmarshalDisallowUnknownField(&v); err != nil {
			name := ""
			if id < len(names) {
				name = names[id]
			}
			out = append(out, fmt.Errorf("%s: %w", name, err))
		}
	}
	return out
}

// KeyOf returns the name of the entry id (minecraft:sharpness).
func (r *Registry[E]) KeyOf(id int32) (string, bool) {
	for k, i := range r.keys {
		if i == id {
			return k, true
		}
	}
	return "", false
}

func (r *Registry[E]) Get(key string) (int32, *E) {
	id, ok := r.keys[key]
	if !ok {
		return -1, nil
	}
	return id, &r.values[id]
}

func (r *Registry[E]) GetByID(id int32) *E {
	if id >= 0 && id < int32(len(r.values)) {
		return &r.values[id]
	}
	return nil
}

func (r *Registry[E]) Put(key string, data E) (id int32, val *E) {
	id = int32(len(r.values))
	r.keys[key] = id
	r.values = append(r.values, data)
	val = &r.values[id]
	r.indices[val] = id
	return
}

// Tags

func (r *Registry[E]) Tag(tag string) []*E {
	return slices.Clone(r.tags[tag])
}

func (r *Registry[E]) ClearTags() {
	r.tags = make(map[string][]*E)
}

// func (r *Registry[E]) BindTags(tag string, ids []int32) error {
// 	values := make([]*E, len(ids))
// 	for i, id := range ids {
// 		if id < 0 || id >= int32(len(r.values)) {
// 			return errors.New("invalid id: " + strconv.Itoa(int(id)))
// 		}
// 		values[i] = &r.values[id]
// 	}
// 	r.tags[tag] = values
// 	return nil
// }
