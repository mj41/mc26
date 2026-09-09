package save

import (
	"io"

	"github.com/mj41/go-mc26/nbt"
)

// PlayerData is a player's file under playerdata/: the shape is generated from
// the save chain of ServerPlayer (save_gen.go), every key its readers take off
// the tag and its writers put in, from Entity.load up through the classes a
// player is made of.
type PlayerData = ServerPlayer

// ReadPlayerData reads one player file (the caller opens and gunzips it).
func ReadPlayerData(r io.Reader) (data PlayerData, err error) {
	_, err = nbt.NewDecoder(r).Decode(&data)
	return
}
