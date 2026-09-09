package save

import (
	"io"

	"github.com/mj41/go-mc26/nbt"
)

// Level is a world's level.dat: the tag LevelStorageAccess.saveDataTag builds,
// whose Data is what PrimaryLevelData.createTag writes. The shape is generated
// from that writer (save_gen.go); these names are the ones a caller uses.
type Level = LevelStorageSourceLevelStorageAccess

// LevelData is the Data compound of level.dat. The world options and the
// dimensions are not in it since 26.1: they are the saved data
// data/minecraft/world_gen_settings.dat (WorldGenSettings).
type LevelData = PrimaryLevelData

// ReadLevel decodes level.dat from an uncompressed reader (the file is gzipped).
// A key the version does not write is kept out of the way rather than refused:
// a server fork adds its own.
func ReadLevel(r io.Reader) (data Level, err error) {
	_, err = nbt.NewDecoder(r).Decode(&data)
	return
}
