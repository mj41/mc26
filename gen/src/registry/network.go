package registry

import (
	"io"

	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

func (reg *Registry[E]) WriteTo(w io.Writer) (int64, error) {
	length := pk.VarInt(len(reg.values))
	n, err := length.WriteTo(w)
	if err != nil {
		return n, err
	}

	// Build reverse map: index → key name.
	names := make([]string, len(reg.values))
	for name, idx := range reg.keys {
		names[idx] = name
	}

	for i := range reg.values {
		key := pk.Identifier(names[i])
		n1, err := key.WriteTo(w)
		n += n1
		if err != nil {
			return n, err
		}

		hasData := pk.Boolean(true)
		n2, err := hasData.WriteTo(w)
		n += n2
		if err != nil {
			return n, err
		}

		n3, err := pk.NBTField{V: &reg.values[i]}.WriteTo(w)
		n += n3
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (reg *Registry[E]) ReadFrom(r io.Reader) (int64, error) {
	var length pk.VarInt
	n, err := length.ReadFrom(r)
	if err != nil {
		return n, err
	}

	reg.Clear()

	var key pk.Identifier
	var hasData pk.Boolean
	for i := 0; i < int(length); i++ {
		var data E
		var n1, n2, n3 int64

		n1, err = key.ReadFrom(r)
		if err != nil {
			return n + n1, err
		}

		n2, err = hasData.ReadFrom(r)
		if err != nil {
			return n + n1 + n2, err
		}

		var raw nbt.RawMessage
		if hasData {
			if reg.keepRaw {
				// Keep the entry's NBT and decode from it, so Unexpected can
				// re-read it strictly later.
				n3, err = pk.NBTField{V: &raw}.ReadFrom(r)
				if err != nil {
					return n + n1 + n2 + n3, err
				}
				if err = raw.Unmarshal(&data); err != nil {
					return n + n1 + n2 + n3, err
				}
			} else {
				n3, err = pk.NBTField{V: &data, AllowUnknownFields: true}.ReadFrom(r)
				if err != nil {
					return n + n1 + n2 + n3, err
				}
			}
		}

		// Always register the entry (even without data) so that numeric
		// IDs match the server's ordering. Tags reference entries by index,
		// and skipping data-less entries would cause ID mismatches.
		reg.Put(string(key), data)
		if reg.keepRaw {
			reg.raws = append(reg.raws, raw)
		}

		n += n1 + n2 + n3
	}
	return n, nil
}

func (reg *Registry[E]) ReadTagsFrom(r io.Reader) (int64, error) {
	var count pk.VarInt
	n, err := count.ReadFrom(r)
	if err != nil {
		return n, err
	}

	var tag pk.Identifier
	var length pk.VarInt
	for i := 0; i < int(count); i++ {
		var n1, n2, n3 int64

		n1, err = tag.ReadFrom(r)
		if err != nil {
			return n + n1, err
		}

		n2, err = length.ReadFrom(r)
		if err != nil {
			return n + n1 + n2, err
		}

		n += n1 + n2
		values := make([]*E, length)

		var id pk.VarInt
		for i := 0; i < int(length); i++ {
			n3, err = id.ReadFrom(r)
			if err != nil {
				return n + n3, err
			}

			if id >= 0 && int(id) < len(reg.values) {
				values[i] = &reg.values[id]
			}
			// Tags may reference entries from "known packs" registries
			// (e.g. minecraft:block, minecraft:item) that the client
			// doesn't have populated via RegistryData. Skip gracefully.
			n += n3
		}

		reg.tags[string(tag)] = values
	}
	return n, nil
}
