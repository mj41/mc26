package e2e

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mj41/mc26/gen/internal/extract"
	"github.com/mj41/mc26/gen/internal/mcver"
	"github.com/mj41/mc26/gen/internal/smoke"
)

// The ordinary world made by one version for all (worldFrom): the same seed generates slightly different land in each
// Minecraft version, so each version's robot would play another world. With
// it, the world is made once by that version's server — every chunk round
// the forest the robot starts in generated, saved — and each version plays
// a copy, upgraded as it loads it: the same blocks, the same start; past
// the generated chunks each version makes its own. A verify of the three
// versions then passes or fails on all three, as their code differs, not
// their land.

// worldRadius is how far round the forest, in chunks, the one version makes
// the world: 160 blocks each way, as far as the scenarios' robots go but the
// week's.
const worldRadius = 10

// worldsFrom is the version that makes the world by default: the oldest the
// library speaks (a server loads an older world, not a newer one).
const worldsFrom = "26.1"

// worldFrom is the version that makes the ordinary world version plays:
// MC26_WORLD_FROM, else worldsFrom; "" — each version its own — for "own" or
// a version older than it.
func worldFrom(version string) string {
	from := os.Getenv("MC26_WORLD_FROM")
	if from == "" {
		from = worldsFrom
	}
	if from == "own" || mcver.Less(version, from) {
		return ""
	}
	return from
}

// sharedWorld is the world the version from makes of the seed (made the
// first time, kept for the next scenarios), and the forest the robot starts
// in.
func sharedWorld(o Options, from, seed string) (string, [2]int, error) {
	var forest [2]int
	dir := filepath.Join(filepath.Dir(o.WorkDir), "worlds", fmt.Sprintf("%s-%s-r%d", from, seed, worldRadius))
	if b, err := os.ReadFile(filepath.Join(dir, "forest")); err == nil {
		if _, err := fmt.Sscanf(string(b), "%d %d", &forest[0], &forest[1]); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "world", "level.dat")); err == nil {
				return dir, forest, nil
			}
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", forest, err
	}
	jar, err := extract.ServerJar(filepath.Dir(o.JarPath), from, o.Log)
	if err != nil {
		return "", forest, err
	}
	o.Log("world: %s makes the world of seed %s for every version", from, seed)
	gen := filepath.Join(dir, "gen")
	srv := &smoke.Server{
		Version: from, JarPath: jar, WorkDir: gen,
		Port: o.Port, Runtime: o.Runtime, Log: o.Log,
		LevelType: "minecraft:normal", Seed: seed,
	}
	if err := servers.start(srv); err != nil {
		return "", forest, err
	}
	stopped := false
	defer func() {
		if !stopped {
			servers.stop(srv)
		}
	}()
	if err := srv.WaitReady(6 * time.Minute); err != nil {
		return "", forest, err
	}
	rc, err := dialRCON(srv.RCONAddr(), srv.RCONPassword)
	if err != nil {
		return "", forest, fmt.Errorf("rcon: %w", err)
	}
	defer rc.close()
	rc.timeout = 3 * time.Minute // forceload makes its chunks before it answers
	resp, err := rc.command("locate biome minecraft:forest")
	if err != nil {
		return "", forest, err
	}
	m := regexp.MustCompile(`\[(-?\d+), [^,]+, (-?\d+)\]`).FindStringSubmatch(resp)
	if m == nil {
		return "", forest, fmt.Errorf("no forest: %q", resp)
	}
	fmt.Sscanf(m[1]+" "+m[2], "%d %d", &forest[0], &forest[1])
	// every chunk round the forest, kept loaded till all are made: in squares
	// of 5 by 5, as forceload makes a square's chunks before it answers
	const square = 5
	cx, cz := forest[0]>>4, forest[1]>>4
	var corners [][2]int
	start := time.Now()
	for x0 := cx - worldRadius; x0 <= cx+worldRadius; x0 += square {
		for z0 := cz - worldRadius; z0 <= cz+worldRadius; z0 += square {
			x1, z1 := min(x0+square-1, cx+worldRadius), min(z0+square-1, cz+worldRadius)
			if resp, err := rc.command(fmt.Sprintf("forceload add %d %d %d %d", x0*16, z0*16, x1*16, z1*16)); err != nil || rconFailed(resp) {
				return "", forest, fmt.Errorf("forceload: %q %v", resp, err)
			}
			corners = append(corners, [2]int{x0 * 16, z0 * 16}, [2]int{x1 * 16, z1 * 16})
		}
		o.Log("world: chunks %d..%d along x made (%s)", x0, min(x0+square-1, cx+worldRadius), time.Since(start).Round(time.Second))
	}
	for deadline := time.Now().Add(15 * time.Minute); ; time.Sleep(2 * time.Second) {
		all := true
		for _, c := range corners {
			if resp, err := rc.command(fmt.Sprintf("execute if loaded %d 64 %d", c[0], c[1])); err != nil || !strings.Contains(resp, "passed") {
				all = false
				break
			}
		}
		if all {
			break
		}
		if time.Now().After(deadline) {
			return "", forest, fmt.Errorf("the world round %v not made within 15 minutes", forest)
		}
	}
	for _, c := range []string{"forceload remove all", "save-all flush"} {
		if resp, err := rc.command(c); err != nil || rconFailed(resp) {
			return "", forest, fmt.Errorf("%s: %q %v", c, resp, err)
		}
	}
	rc.close()
	servers.stop(srv) // saves the world as it stops
	stopped = true
	if err := copyDir(filepath.Join(gen, "world"), filepath.Join(dir, "world")); err != nil {
		return "", forest, err
	}
	if err := os.RemoveAll(gen); err != nil {
		return "", forest, err
	}
	if err := os.WriteFile(filepath.Join(dir, "forest"), []byte(fmt.Sprintf("%d %d\n", forest[0], forest[1])), 0o644); err != nil {
		return "", forest, err
	}
	o.Log("world: %d chunks round the forest at %d %d made by %s", (2*worldRadius+1)*(2*worldRadius+1), forest[0], forest[1], from)
	return dir, forest, nil
}

// copyDir copies a directory of regular files (a world) to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		switch {
		case fi.IsDir():
			return os.MkdirAll(to, 0o755)
		case !fi.Mode().IsRegular():
			return nil // a lock file's link, nothing of the world
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
