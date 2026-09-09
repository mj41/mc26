package level

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mj41/go-mc26/save"
	"github.com/mj41/go-mc26/save/region"
)

// TestSaveChunk reads the region file a vanilla server of this version wrote
// (MC26_SAVE_REGION, set by the end-to-end run) and converts every chunk in it
// to a level.Chunk and back. It is the check that the saved shapes are this
// version's: they are generated from the reader and the writer Mojang parses
// and writes a chunk with, and nothing else here reads a real save file.
//
// It counts what it converted and fails when that collapses, so it cannot pass
// by finding nothing: a palette entry that decoded to an empty block id would
// once have gone unnoticed, since NBT leaves a key it does not know at zero.
func TestSaveChunk(t *testing.T) {
	path := os.Getenv("MC26_SAVE_REGION")
	if path == "" {
		t.Skip("MC26_SAVE_REGION is not set: this test wants the region files a server wrote")
	}
	// a directory means every region in it: three of a world's four regions
	// once passed while the fourth held the palette entry that did not read
	paths := []string{path}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		if paths, err = filepath.Glob(filepath.Join(path, "r.*.mca")); err != nil || len(paths) == 0 {
			t.Fatalf("no region files in %s", path)
		}
	}
	chunks, sections, palette := 0, 0, 0
	for _, path := range paths {
		c, s, p := checkRegion(t, path)
		chunks, sections, palette = chunks+c, sections+s, palette+p
	}
	if chunks == 0 || sections == 0 || palette == 0 {
		t.Fatalf("nothing was checked: %d chunks, %d sections, %d palette entries", chunks, sections, palette)
	}
	t.Logf("converted %d chunks, %d sections, %d palette entries from %d region files", chunks, sections, palette, len(paths))
}

func checkRegion(t *testing.T, path string) (chunks, sections, palette int) {
	t.Helper()
	r, err := region.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer r.Close()

	for x := 0; x < 32; x++ {
		for z := 0; z < 32; z++ {
			if !r.ExistSector(x, z) {
				continue
			}
			data, err := r.ReadSector(x, z)
			if err != nil {
				t.Fatalf("%s sector (%d, %d): %v", filepath.Base(path), x, z, err)
			}
			var c save.Chunk
			if err := c.Load(data); err != nil {
				t.Fatalf("%s sector (%d, %d): load: %v", filepath.Base(path), x, z, err)
			}
			if len(c.Sections) == 0 {
				continue
			}
			world := 0
			for _, s := range c.Sections {
				sections++
				if s.BlockStates == nil {
					continue // a section outside the generated world: light only
				}
				world++
				for i, id := range paletteBlockIDs(s.BlockStates) {
					if id == "" {
						t.Fatalf("%s chunk (%d, %d) section %d palette entry %d has no block id: the saved shape does not match this version", filepath.Base(path), x, z, s.Y, i)
					}
					palette++
				}
			}
			level, err := ChunkFromSave(&c)
			if err != nil {
				t.Fatalf("%s chunk (%d, %d): from save: %v", filepath.Base(path), x, z, err)
			}
			back := save.Chunk{YPos: c.YPos}
			if err := ChunkToSave(level, &back); err != nil {
				t.Fatalf("%s chunk (%d, %d): to save: %v", filepath.Base(path), x, z, err)
			}
			// the sections that carry block states come back; the light-only ones
			// above and below the world do not, as they are the server's to write
			if len(back.Sections) != world {
				t.Fatalf("%s chunk (%d, %d): %d sections of the world went in, %d came back", filepath.Base(path), x, z, world, len(back.Sections))
			}
			chunks++
		}
	}
	return chunks, sections, palette
}
