// Package fixtures writes the small world the library's own tests read: a
// few chunks, a few entities, level.dat and one player file, cut from the
// world a vanilla server of the version wrote during the end-to-end run. The
// tests then read what this version's server writes, and refreshing them on
// a version bump is one command, like the JSON.
package fixtures

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mj41/go-mc26/nbt"
	"github.com/mj41/go-mc26/save/region"
)

type Options struct {
	Version string
	World   string // the world directory a server wrote (temp/e2e/<v>/server/world)
	Out     string // the fixture directory to (re)write (gen/src/save/testdata/<version>)
	Chunks  int    // chunks to keep of the region around the spawn, and entity chunks
	Log     func(format string, args ...any)
}

// Source is the file that says where the fixtures came from; a directory
// without it is not one this package wrote, and is left alone.
const Source = "SOURCE"

// levelHead is what of level.dat the note needs: the version and the spawn.
type levelHead struct {
	Data struct {
		DataVersion int32 `nbt:"DataVersion"`
		// "version" (the storage format) is an int the decoder would otherwise
		// match to Version, case-insensitively
		StorageVersion int32 `nbt:"version"`
		Version        struct {
			Name string `nbt:"Name"`
		} `nbt:"Version"`
		Spawn struct {
			Pos []int32 `nbt:"pos"`
		} `nbt:"spawn"`
	} `nbt:"Data"`
}

// Make writes the fixtures and returns the note it put in SOURCE.
func Make(o Options) (string, error) {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Chunks <= 0 {
		o.Chunks = 4
	}
	head, err := readLevel(filepath.Join(o.World, "level.dat"))
	if err != nil {
		return "", err
	}
	if len(head.Data.Spawn.Pos) != 3 {
		return "", fmt.Errorf("%s: no spawn in level.dat", o.World)
	}
	cx, cz := int(head.Data.Spawn.Pos[0])>>4, int(head.Data.Spawn.Pos[2])>>4

	// only a directory this command wrote (it has SOURCE) is replaced; the new
	// one is built beside it and swapped in whole, so a failure leaves nothing
	if _, err := os.Stat(o.Out); err == nil {
		if _, err := os.Stat(filepath.Join(o.Out, Source)); err != nil {
			return "", fmt.Errorf("%s exists and has no %s file: not a fixture directory this command wrote", o.Out, Source)
		}
	}
	tmp := o.Out + ".partial"
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, d := range []string{"region", "entities", filepath.Join("players", "data")} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			return "", err
		}
	}
	if err := copyFile(filepath.Join(o.World, "level.dat"), filepath.Join(tmp, "level.dat")); err != nil {
		return "", err
	}

	overworld := filepath.Join(o.World, "dimensions", "minecraft", "overworld")
	chunks, regionFile, err := cutRegion(filepath.Join(overworld, "region"), filepath.Join(tmp, "region"), cx, cz, o.Chunks)
	if err != nil {
		return "", err
	}
	entities, entityFiles, err := cutEntities(filepath.Join(overworld, "entities"), filepath.Join(tmp, "entities"), cx, cz, o.Chunks)
	if err != nil {
		return "", err
	}
	player, err := copyPlayer(filepath.Join(o.World, "players", "data"), filepath.Join(tmp, "players", "data"))
	if err != nil {
		return "", err
	}

	note := fmt.Sprintf("Written by `mc26 fixtures --version %s` from the world the end-to-end run's vanilla server wrote.\n"+
		"Minecraft %s (level.dat Version.Name %q, DataVersion %d).\n"+
		"region/%s: the %d chunks nearest the spawn (chunk %d, %d), each sector as the server wrote it.\n"+
		"entities/%s: %d entity chunks, the nearest to the spawn that hold entities.\n"+
		"players/data/%s: one player's file, the end-to-end bot.\n"+
		"level.dat: as written. Re-run the command after a version bump; do not edit by hand.\n",
		o.Version, o.Version, head.Data.Version.Name, head.Data.DataVersion,
		regionFile, chunks, cx, cz, strings.Join(entityFiles, ", "), entities, player)
	if err := os.WriteFile(filepath.Join(tmp, Source), []byte(note), 0o644); err != nil {
		return "", err
	}
	if err := os.RemoveAll(o.Out); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, o.Out); err != nil {
		return "", err
	}
	o.Log("fixtures: %d chunks, %d entity chunks, level.dat and %s → %s", chunks, entities, player, o.Out)
	return note, nil
}

func readLevel(path string) (head levelHead, err error) {
	f, err := os.Open(path)
	if err != nil {
		return head, err
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return head, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := nbt.NewDecoder(r).Decode(&head); err != nil {
		return head, fmt.Errorf("%s: %w", path, err)
	}
	return head, nil
}

// sector is one chunk of a region file, with its distance to the spawn.
type sector struct {
	file   string
	cx, cz int
	data   []byte
	dist   int
}

// sectors lists the chunks of every region file under dir; empty ones (an
// entity region reserves its sector before it holds an entity) are skipped.
func sectors(dir string, cx, cz int) ([]sector, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "r.*.mca"))
	if len(files) == 0 {
		return nil, fmt.Errorf("no region files under %s", dir)
	}
	sort.Strings(files)
	var out []sector
	for _, file := range files {
		var rx, rz int
		if _, err := fmt.Sscanf(filepath.Base(file), "r.%d.%d.mca", &rx, &rz); err != nil {
			continue
		}
		// 26.1 leaves an entity region file of zero length: no header, no chunk
		if fi, err := os.Stat(file); err != nil || fi.Size() < 8192 {
			continue
		}
		r, err := region.OpenReadOnly(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for x := 0; x < 32; x++ {
			for z := 0; z < 32; z++ {
				if !r.ExistSector(x, z) {
					continue
				}
				data, err := r.ReadSector(x, z)
				if err != nil || len(data) < 2 {
					continue
				}
				acx, acz := rx*32+x, rz*32+z
				dx, dz := acx-cx, acz-cz
				out = append(out, sector{file: filepath.Base(file), cx: acx, cz: acz, data: data, dist: dx*dx + dz*dz})
			}
		}
		r.Close()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].dist < out[j].dist })
	return out, nil
}

// cutRegion writes the n chunks nearest the spawn into region files of the
// same names under out, and returns how many and the file they went to.
func cutRegion(dir, out string, cx, cz, n int) (int, string, error) {
	all, err := sectors(dir, cx, cz)
	if err != nil {
		return 0, "", err
	}
	if len(all) == 0 {
		return 0, "", fmt.Errorf("no chunks under %s", dir)
	}
	// one file: the region the spawn is in holds the nearest chunks
	file := all[0].file
	var keep []sector
	for _, s := range all {
		if s.file == file && len(keep) < n {
			keep = append(keep, s)
		}
	}
	if err := writeRegion(filepath.Join(out, file), keep); err != nil {
		return 0, "", err
	}
	return len(keep), file, nil
}

// cutEntities keeps the n entity chunks nearest the spawn, whichever entity
// region files they are in.
func cutEntities(dir, out string, cx, cz, n int) (int, []string, error) {
	all, err := sectors(dir, cx, cz)
	if err != nil {
		return 0, nil, err
	}
	if len(all) == 0 {
		return 0, nil, fmt.Errorf("no entity chunks under %s: the end-to-end world holds entities near the spawn", dir)
	}
	if len(all) > n {
		all = all[:n]
	}
	byFile := map[string][]sector{}
	for _, s := range all {
		byFile[s.file] = append(byFile[s.file], s)
	}
	var files []string
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		if err := writeRegion(filepath.Join(out, file), byFile[file]); err != nil {
			return 0, nil, err
		}
	}
	return len(all), files, nil
}

func writeRegion(path string, keep []sector) error {
	r, err := region.Create(path)
	if err != nil {
		return err
	}
	for _, s := range keep {
		x, z := region.In(s.cx, s.cz)
		if err := r.WriteSector(x, z, s.data); err != nil {
			r.Close()
			return fmt.Errorf("%s chunk (%d, %d): %w", path, s.cx, s.cz, err)
		}
	}
	return r.Close()
}

// copyPlayer copies the first player file by name (not its _old backup).
func copyPlayer(dir, out string) (string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.dat"))
	sort.Strings(files)
	if len(files) == 0 {
		return "", fmt.Errorf("no player file under %s: the end-to-end run saves while its bot is online", dir)
	}
	name := filepath.Base(files[0])
	return name, copyFile(files[0], filepath.Join(out, name))
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}
