package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Walled arenas: boxes on the flat world, side by side along x, each shut in
// so a bot in one cannot walk, dig or build its way into another. sharedArenaWorld
// (world.go) makes the world holding them once and keeps it.

// arenaRCON is what the build needs of a server's console.
type arenaRCON interface {
	command(string) (string, error)
}

// arenaHalf is half the side of an arena's box: it spans ox±32 along x and
// ±32 along z, from the floor (y 0) to the roof (y 128).
const arenaHalf = 32

// arenaRoof is the roof's height.
const arenaRoof = 128

// arenaFillMax is the most blocks a fill may change.
const arenaFillMax = 32768

// arenaFills is a box of blocks as fill commands, split along y so none is
// over the limit.
func arenaFills(x0, y0, z0, x1, y1, z1 int, block string) []string {
	area := (x1 - x0 + 1) * (z1 - z0 + 1)
	layers := max(1, arenaFillMax/area)
	var out []string
	for y := y0; y <= y1; y += layers {
		out = append(out, fmt.Sprintf("fill %d %d %d %d %d %d %s", x0, y, z0, x1, min(y+layers-1, y1), z1, block))
	}
	return out
}

// arenaCommands are the console commands that build arena k, centred on ox
// along x and on 0 along z: the box's chunks kept loaded, bedrock floor and
// bedrock walls up to the ground (a dig cannot tunnel out), barrier walls
// above it and a barrier roof (a bot cannot climb out, light still comes
// in), the stage's start area, no stray entity, the chunks let go. Nothing
// here touches the clock or the weather: the arenas share them.
func arenaCommands(k, ox int) []string {
	x0, x1, z0, z1 := ox-arenaHalf, ox+arenaHalf, -arenaHalf, arenaHalf
	box := fmt.Sprintf("%d %d %d %d", x0, z0, x1, z1)
	out := []string{"forceload add " + box}
	out = append(out, arenaFills(x0, 0, z0, x1, 0, z1, "minecraft:bedrock")...)
	type wall struct{ x0, z0, x1, z1 int }
	for _, w := range []wall{{x0, z0, x1, z0}, {x0, z1, x1, z1}, {x0, z0, x0, z1}, {x1, z0, x1, z1}} {
		out = append(out, arenaFills(w.x0, 1, w.z0, w.x1, flatGround, w.z1, "minecraft:bedrock")...)
		out = append(out, arenaFills(w.x0, flatGround+1, w.z0, w.x1, arenaRoof-1, w.z1, "minecraft:barrier")...)
	}
	out = append(out, arenaFills(x0, arenaRoof, z0, x1, arenaRoof, z1, "minecraft:barrier")...)
	for _, c := range flatAreaAt(ox) {
		out = append(out, flatShift(c, ox, ""))
	}
	out = append(out, fmt.Sprintf("kill @e[type=!minecraft:player,x=%d,y=0,z=%d,dx=%d,dy=%d,dz=%d]",
		x0, z0, 2*arenaHalf, arenaRoof, 2*arenaHalf))
	return append(out, "forceload remove "+box)
}

// arenaRecipeHash names the recipe of n arenas: a short hash of every
// command, so changing it makes a new cached world.
func arenaRecipeHash(n int) string {
	h := sha256.New()
	for k := 0; k < n; k++ {
		fmt.Fprintln(h, strings.Join(arenaCommands(k, (k+1)*flatApart), "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// buildArenaWorld runs the n arenas' commands, saves, and stops the server
// (stop saves the world as it stops).
func buildArenaWorld(n int, rc arenaRCON, stop func() error) error {
	for k := 0; k < n; k++ {
		for _, c := range arenaCommands(k, (k+1)*flatApart) {
			if resp, err := rc.command(c); err != nil || rconFailed(resp) {
				return fmt.Errorf("arena %d, %s: %q %v", k, c, resp, err)
			}
		}
	}
	if resp, err := rc.command("save-all flush"); err != nil || rconFailed(resp) {
		return fmt.Errorf("save-all flush: %q %v", resp, err)
	}
	return stop()
}

// arenaMarker says the cached world was built to the end.
const arenaMarker = "arenas"

// arenaWorld is the cached directory of the world, made by build the first
// time (build fills the directory); a marker written last says it is whole.
func arenaWorld(workdir, from, recipeHash string, n int, build func(dir string) error) (string, error) {
	dir := filepath.Join(filepath.Dir(workdir), "worlds", fmt.Sprintf("%s-arenas-%s-n%d", from, recipeHash, n))
	if _, err := os.Stat(filepath.Join(dir, arenaMarker)); err == nil {
		return dir, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := build(dir); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, arenaMarker), []byte(recipeHash+"\n"), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}
