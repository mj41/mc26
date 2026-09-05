package save

import (
	"compress/gzip"
	"os"
	"testing"
)

func TestLevel(t *testing.T) {
	f, err := os.Open("testdata/level.dat")
	if err != nil {
		t.Fatal(err)
	}

	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}

	data, err := ReadLevel(r)
	if err != nil {
		t.Fatal(err)
	}

	//want := PlayerData{
	//	Pos:    [3]float64{-41.5, 65, -89.5},
	//	Motion: [3]float64{0, -0.0784000015258789, 0},
	//	Rotation: [2]float32{0,0},
	//}

	t.Logf("%+v", data)
	//if data != want {
	//	t.Errorf("player data parse error: get %v, want %v", data, want)
	//}
}

// TestLevel26 reads a level.dat written by a vanilla 26.2 server (DataVersion 4903),
// which moved difficulty into difficulty_settings and the spawn point into spawn.
func TestLevel26(t *testing.T) {
	f, err := os.Open("testdata/level-26.2.dat")
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
	if data.Data.DataVersion != 4903 || data.Data.Version.Name != "26.2" {
		t.Errorf("unexpected version: DataVersion=%d Name=%q", data.Data.DataVersion, data.Data.Version.Name)
	}
	if data.Data.DifficultySettings.Difficulty != "peaceful" {
		t.Errorf("difficulty_settings.difficulty = %q, want peaceful", data.Data.DifficultySettings.Difficulty)
	}
	if data.Data.Spawn.Dimension != "minecraft:overworld" || len(data.Data.Spawn.Pos) != 3 {
		t.Errorf("spawn = %+v", data.Data.Spawn)
	}
}

// TestLevel26Paper reads a level.dat written by Paper 26.2 (a world converted to
// the 26.1+ dimensions layout). Paper adds "Bukkit.Version", which the strict
// decoder used to reject as an unknown field.
func TestLevel26Paper(t *testing.T) {
	f, err := os.Open("testdata/level-26.2-paper.dat")
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
	if data.Data.DataVersion != 4903 || data.Data.Version.Name != "26.2" {
		t.Errorf("unexpected version: DataVersion=%d Name=%q", data.Data.DataVersion, data.Data.Version.Name)
	}
	if data.Data.BukkitVersion == "" {
		t.Errorf("Bukkit.Version missing; Paper writes it")
	}
}
