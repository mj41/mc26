package registry

import (
	"io"
	"reflect"

	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

// The Registries struct and its element types are generated from the jar's
// codecs (registries_gen.go, elements_gen.go); this file holds the lookups.

type RegistryCodec interface {
	pk.FieldDecoder
	pk.FieldEncoder
	ReadTagsFrom(r io.Reader) (int64, error)
}

// EachRegistry calls fn for each registry in the Registries struct,
// including both typed struct fields and ExtraRegistries entries.
// The callback receives the registry ID (e.g. "minecraft:chat_type")
// and the registry as a FieldEncoder (WriteTo).
func (c *Registries) EachRegistry(fn func(id string, reg pk.FieldEncoder) error) error {
	codecVal := reflect.ValueOf(c).Elem()
	codecTyp := codecVal.Type()
	numField := codecVal.NumField()
	for i := 0; i < numField; i++ {
		registryID, ok := codecTyp.Field(i).Tag.Lookup("registry")
		if !ok {
			continue
		}
		reg := codecVal.Field(i).Addr().Interface().(pk.FieldEncoder)
		if err := fn(registryID, reg); err != nil {
			return err
		}
	}
	for id, reg := range c.ExtraRegistries {
		if err := fn(id, reg); err != nil {
			return err
		}
	}
	return nil
}

func (c *Registries) Registry(id string) RegistryCodec {
	codecVal := reflect.ValueOf(c).Elem()
	codecTyp := codecVal.Type()
	numField := codecVal.NumField()
	for i := 0; i < numField; i++ {
		registryID, ok := codecTyp.Field(i).Tag.Lookup("registry")
		if !ok {
			continue
		}
		if registryID == id {
			return codecVal.Field(i).Addr().Interface().(RegistryCodec)
		}
	}
	// Unknown registry — create a RawMessage sink so we don't fatal.
	// This handles registries added in newer MC versions (e.g. instrument,
	// entity_type, consume_effect_type) that aren't struct fields yet.
	if c.ExtraRegistries == nil {
		c.ExtraRegistries = make(map[string]*Registry[nbt.RawMessage])
	}
	reg, ok := c.ExtraRegistries[id]
	if !ok {
		r := NewRegistry[nbt.RawMessage]()
		reg = &r
		c.ExtraRegistries[id] = reg
	}
	return reg
}
