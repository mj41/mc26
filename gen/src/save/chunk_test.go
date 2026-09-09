package save

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mj41/go-mc26/nbt"
	"github.com/mj41/go-mc26/save/region"
)

// The world under testdata/world is cut from what a vanilla server of this
// version wrote (`mc26 fixtures`, see testdata/world/SOURCE): a few chunks
// around the spawn, a few entity chunks, level.dat and one player file. The
// end-to-end run reads whole worlds; these tests read this one offline.

// skipWithoutFixtures skips a test when the library was built before the
// end-to-end run of its version wrote the fixture world (a new version's
// first build): the next build has it.
func skipWithoutFixtures(t testing.TB) {
	t.Helper()
	if _, err := os.Stat(filepath.Join("testdata", "world", "SOURCE")); err != nil {
		t.Skip("no fixture world for this version yet: the end-to-end run writes it (`mc26 fixtures`)")
	}
}

func fixtureFiles(t testing.TB, pattern string) []string {
	t.Helper()
	skipWithoutFixtures(t)
	files, err := filepath.Glob(filepath.Join("testdata", "world", pattern))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixture matches %s", pattern)
	}
	return files
}

// eachSector calls fn with every chunk of every region file the pattern names.
func eachSector(t testing.TB, pattern string, fn func(file string, x, z int, data []byte)) int {
	t.Helper()
	n := 0
	for _, file := range fixtureFiles(t, pattern) {
		r, err := region.OpenReadOnly(file)
		if err != nil {
			t.Fatal(err)
		}
		for x := 0; x < 32; x++ {
			for z := 0; z < 32; z++ {
				if !r.ExistSector(x, z) {
					continue
				}
				data, err := r.ReadSector(x, z)
				if err != nil {
					t.Fatalf("%s (%d, %d): %v", filepath.Base(file), x, z, err)
				}
				fn(filepath.Base(file), x, z, data)
				n++
			}
		}
		r.Close()
	}
	if n == 0 {
		t.Fatalf("no chunk in the fixtures matching %s", pattern)
	}
	return n
}

// TestChunks loads every chunk of the fixture region, expects the world in
// it (sections with block states) and writes each back to the same shape.
func TestChunks(t *testing.T) {
	sections := 0
	n := eachSector(t, filepath.Join("region", "r.*.mca"), func(file string, x, z int, data []byte) {
		var c Chunk
		if err := c.Load(data); err != nil {
			t.Fatalf("%s (%d, %d): %v", file, x, z, err)
		}
		if len(c.Sections) == 0 {
			t.Fatalf("%s (%d, %d): no sections", file, x, z)
		}
		for _, s := range c.Sections {
			if s.BlockStates != nil {
				sections++
			}
		}
		back, err := c.Data(2)
		if err != nil {
			t.Fatalf("%s (%d, %d): encode: %v", file, x, z, err)
		}
		var again Chunk
		if err := again.Load(back); err != nil {
			t.Fatalf("%s (%d, %d): reload: %v", file, x, z, err)
		}
		if len(again.Sections) != len(c.Sections) {
			t.Fatalf("%s (%d, %d): %d sections went out, %d came back", file, x, z, len(c.Sections), len(again.Sections))
		}
	})
	if sections == 0 {
		t.Fatal("no section holds block states")
	}
	t.Logf("%d chunks, %d sections with block states", n, sections)
}

// TestEntities reads the fixture entity regions: each chunk is an
// EntityStorage whose Entities each name their type.
func TestEntities(t *testing.T) {
	entities := 0
	eachSector(t, filepath.Join("entities", "r.*.mca"), func(file string, x, z int, data []byte) {
		var store EntityStorage
		if err := loadSector(data, &store); err != nil {
			t.Fatalf("%s (%d, %d): %v", file, x, z, err)
		}
		if len(store.Position) != 2 {
			t.Fatalf("%s (%d, %d): Position %v", file, x, z, store.Position)
		}
		for i, raw := range store.Entities {
			var e struct {
				ID string `nbt:"id"`
			}
			if err := raw.Unmarshal(&e); err != nil {
				t.Fatalf("%s (%d, %d) entity %d: %v", file, x, z, i, err)
			}
			if e.ID == "" {
				t.Fatalf("%s (%d, %d) entity %d has no id", file, x, z, i)
			}
			entities++
		}
	})
	if entities == 0 {
		t.Fatal("no entity in the fixtures")
	}
	t.Logf("%d entities", entities)
}

// loadSector decodes a region sector (compression byte, then NBT) into v.
func loadSector(data []byte, v any) error {
	r, err := sectorReader(data)
	if err != nil {
		return err
	}
	_, err = nbt.NewDecoder(r).Decode(v)
	return err
}

func BenchmarkChunkLoad(b *testing.B) {
	var sectors [][]byte
	eachSector(b, filepath.Join("region", "r.*.mca"), func(_ string, _, _ int, data []byte) { sectors = append(sectors, data) })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var c Chunk
		if err := c.Load(sectors[i%len(sectors)]); err != nil {
			b.Fatal(err)
		}
	}
}
