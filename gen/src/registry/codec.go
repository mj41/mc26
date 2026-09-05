package registry

import (
	"io"
	"reflect"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

type Registries struct {
	ChatType        Registry[ChatType]       `registry:"minecraft:chat_type"`
	DamageType      Registry[DamageType]     `registry:"minecraft:damage_type"`
	DimensionType   Registry[Dimension]      `registry:"minecraft:dimension_type"`
	TrimMaterial    Registry[nbt.RawMessage] `registry:"minecraft:trim_material"`
	TrimPattern     Registry[nbt.RawMessage] `registry:"minecraft:trim_pattern"`
	WorldGenBiome   Registry[nbt.RawMessage] `registry:"minecraft:worldgen/biome"`
	Wolfvariant     Registry[nbt.RawMessage] `registry:"minecraft:wolf_variant"`
	PaintingVariant Registry[nbt.RawMessage] `registry:"minecraft:painting_variant"`
	BannerPattern   Registry[nbt.RawMessage] `registry:"minecraft:banner_pattern"`
	Enchantment     Registry[nbt.RawMessage] `registry:"minecraft:enchantment"`
	JukeboxSong     Registry[nbt.RawMessage] `registry:"minecraft:jukebox_song"`

	// ExtraRegistries stores registries not explicitly declared as struct fields.
	// Servers may send registry data for types added in newer versions (e.g.
	// minecraft:instrument, minecraft:entity_type). Instead of fataling, these
	// are captured as RawMessage registries for protocol compatibility.
	ExtraRegistries map[string]*Registry[nbt.RawMessage]
}

func NewNetworkCodec() Registries {
	return Registries{
		ChatType:        NewRegistry[ChatType](),
		DamageType:      NewRegistry[DamageType](),
		DimensionType:   NewRegistry[Dimension](),
		TrimMaterial:    NewRegistry[nbt.RawMessage](),
		TrimPattern:     NewRegistry[nbt.RawMessage](),
		WorldGenBiome:   NewRegistry[nbt.RawMessage](),
		Wolfvariant:     NewRegistry[nbt.RawMessage](),
		PaintingVariant: NewRegistry[nbt.RawMessage](),
		BannerPattern:   NewRegistry[nbt.RawMessage](),
		Enchantment:     NewRegistry[nbt.RawMessage](),
		JukeboxSong:     NewRegistry[nbt.RawMessage](),
		ExtraRegistries: make(map[string]*Registry[nbt.RawMessage]),
	}
}

type ChatType struct {
	Chat      chat.Decoration `nbt:"chat"`
	Narration chat.Decoration `nbt:"narration"`
}

type DamageType struct {
	MessageID        string  `nbt:"message_id"`
	Scaling          string  `nbt:"scaling"`
	Exhaustion       float32 `nbt:"exhaustion"`
	Effects          string  `nbt:"effects,omitempty"`
	DeathMessageType string  `nbt:"death_message_type,omitempty"`
}

type Dimension struct {
	FixedTime          int64   `nbt:"fixed_time,omitempty"`
	HasSkylight        bool    `nbt:"has_skylight"`
	HasCeiling         bool    `nbt:"has_ceiling"`
	Ultrawarm          bool    `nbt:"ultrawarm"`
	Natural            bool    `nbt:"natural"`
	CoordinateScale    float64 `nbt:"coordinate_scale"`
	BedWorks           bool    `nbt:"bed_works"`
	RespawnAnchorWorks byte    `nbt:"respawn_anchor_works"`
	MinY               int32   `nbt:"min_y"`
	Height             int32   `nbt:"height"`
	LogicalHeight      int32   `nbt:"logical_height"`
	InfiniteBurn       string  `nbt:"infiniburn"`
	Effects            string  `nbt:"effects"`
	AmbientLight       float64 `nbt:"ambient_light"`

	PiglinSafe                  byte           `nbt:"piglin_safe"`
	HasRaids                    byte           `nbt:"has_raids"`
	MonsterSpawnLightLevel      nbt.RawMessage `nbt:"monster_spawn_light_level"` // Tag_Int or {type:"minecraft:uniform", value:{min_inclusive: Tag_Int, max_inclusive: Tag_Int}}
	MonsterSpawnBlockLightLimit int32          `nbt:"monster_spawn_block_light_limit"`
}

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
