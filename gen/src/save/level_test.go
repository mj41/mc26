package save

import (
	"compress/gzip"
	"os"
	"testing"
)

// TestLevel reads the level.dat a vanilla server of this version wrote
// (testdata/world, `mc26 fixtures`).
func TestLevel(t *testing.T) {
	skipWithoutFixtures(t)
	f, err := os.Open("testdata/world/level.dat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ReadLevel(r)
	if err != nil {
		t.Fatal(err)
	}
	if data.Data == nil {
		t.Fatal("no Data compound")
	}
	d := data.Data
	if d.DataVersion == 0 || d.Version == nil || d.Version.Name == "" {
		t.Errorf("no version: DataVersion=%d Version=%+v", d.DataVersion, d.Version)
	}
	if d.DifficultySettings.Difficulty == "" {
		t.Errorf("difficulty_settings.difficulty is empty")
	}
	if d.Spawn.Dimension != "minecraft:overworld" || len(d.Spawn.Pos) != 3 {
		t.Errorf("spawn = %+v", d.Spawn)
	}
	if d.LevelName == "" || len(d.ServerBrands) == 0 {
		t.Errorf("LevelName %q, ServerBrands %v", d.LevelName, d.ServerBrands)
	}
}
