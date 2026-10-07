// Package generate turns the JSON of one Minecraft version (an mc26-data
// checkout or a fresh extraction) into the generated packages of the go-mc26
// library: packet ids and packet structs, registries, items, blocks and block
// states, entities, block entities, data components, biomes, translations and
// the version constants.
//
// Every generator is a function of (jsonDir, outRoot); the hand-crafted inputs
// and templates are read from the assets directory (gen/hand-crafted,
// gen/templates).
package generate

import (
	"fmt"
	"time"
)

// Config tells Run where to read and write.
type Config struct {
	JSONDir   string // the extracted data of one version: registries.json, packets.json, lang/, …
	OutRoot   string // the library tree to write the generated packages into
	AssetsDir string // directory holding hand-crafted/ and templates/ (the gen/ directory)
	Log       func(format string, args ...any)
}

// Module is the import path of the library the generated code belongs to.
const Module = "github.com/mj41/go-mc26"

// BuildInfo is what version.go records about the build's inputs.
type BuildInfo struct {
	DataSource string // e.g. "mc26-data v0.262.0"
	Generator  string // e.g. "mc26 1a2b3c4d5e6f"
}

// Build is recorded by the next Run.
var Build BuildInfo

// Generator is one code generator.
type Generator struct {
	Name string
	Fn   func(jsonDir, outRoot string) error
}

// Generators lists every generator in the order they run.
var Generators = []Generator{
	{"version", genVersion},
	{"packetid", genPacketID},
	{"soundid", genSoundID},
	{"item", genItem},
	{"blocks", genBlocks},
	{"entity", genEntity},
	{"component", genComponent},
	{"componentwire", genComponentWire},
	{"blockentities", genBlockEntities},
	{"registryid", genRegistryID},
	{"biome", genBiome},
	{"chunkstatus", genChunkStatus},
	{"lang", genLang},
	{"packets", genPackets},
	{"nbt", genNBT},
	{"save", genSave},
	{"constants", genConstants},
	{"blockbehaviour", genBlockBehaviour},
	{"itemdefaults", genItemDefaults},
	{"loot", genLoot},
	{"componenthash", genComponentHash},
	{"recipes", genRecipes},
	{"rpc", genRPC},
}

// Run executes every generator.
func Run(cfg Config) error {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	assetsDir = cfg.AssetsDir
	var v versionJSON
	if err := readJSON(cfg.JSONDir+"/version.json", &v); err != nil {
		return err
	}
	jsonVersion = v.ID
	logf = cfg.Log
	for _, g := range Generators {
		start := time.Now()
		if err := g.Fn(cfg.JSONDir, cfg.OutRoot); err != nil {
			return fmt.Errorf("%s: %w", g.Name, err)
		}
		cfg.Log("  %s: done (%s)", g.Name, time.Since(start).Round(time.Millisecond))
	}
	return nil
}
