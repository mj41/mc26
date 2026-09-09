package save

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestPlayerData reads the player file of the end-to-end bot, as the server
// of this version saved it (testdata/world, `mc26 fixtures`).
func TestPlayerData(t *testing.T) {
	skipWithoutFixtures(t)
	files, _ := filepath.Glob("testdata/world/players/data/*.dat")
	if len(files) == 0 {
		t.Fatal("no player file under testdata/world")
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ReadPlayerData(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Pos) != 3 || len(p.Rotation) != 2 {
		t.Errorf("Pos %v, Rotation %v", p.Pos, p.Rotation)
	}
	if p.Dimension == "" {
		t.Errorf("no Dimension")
	}
}
