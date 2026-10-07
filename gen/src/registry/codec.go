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
	SetKeepRaw(on bool)
	Unexpected() []error
	KeyOf(id int32) (string, bool)
}

// KeepRaw makes every registry keep the NBT of the entries the server sends,
// so Unexpected can report what this build does not cover. Set it before
// joining; it costs a second copy of the registry data.
func (c *Registries) KeepRaw(on bool) {
	c.eachCodec(func(_ string, reg RegistryCodec) { reg.SetKeepRaw(on) })
}

// Unexpected re-reads every registry entry the server sent into its element
// type, refusing keys the type does not have, and returns one error per entry
// that carries something this build does not know — an element that gained a
// field, or a wrong tag in the generated types. It needs KeepRaw(true) before
// the join and reports nothing for a registry decoded as raw NBT.
func (c *Registries) Unexpected() map[string][]error {
	out := map[string][]error{}
	c.eachCodec(func(id string, reg RegistryCodec) {
		if errs := reg.Unexpected(); len(errs) > 0 {
			out[id] = errs
		}
	})
	return out
}

// eachCodec calls fn for every registry, typed field or extra.
func (c *Registries) eachCodec(fn func(id string, reg RegistryCodec)) {
	codecVal := reflect.ValueOf(c).Elem()
	codecTyp := codecVal.Type()
	for i := 0; i < codecVal.NumField(); i++ {
		id, ok := codecTyp.Field(i).Tag.Lookup("registry")
		if !ok {
			continue
		}
		if reg, ok := codecVal.Field(i).Addr().Interface().(RegistryCodec); ok {
			fn(id, reg)
		}
	}
	for id, reg := range c.ExtraRegistries {
		fn(id, reg)
	}
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
