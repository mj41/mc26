package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// scenarioAutofishBite: the fishing example reels on a bite, end to end. The "autofish" scenario only checks the
// login, and the example once matched the splash sound by a stale registry id and never reeled (it was 609, it is
// 644 in 26.3); only a real bite on a real server tells. So: the bot joins (the example casts at game start, with
// nothing in its hand), the harness gives it a rod (Lure III: a bite in seconds, not half a minute) and a pond ahead,
// and the example's watchdog casts it 45 s on; the splash must come, and the example answer "gra~" (it reels, the
// line it prints), and the server must have got the catch in its inventory. Only when named
// (e2e --only autofishbite): about two minutes, and a pond sunk far from the spawn the other scenarios use.
func scenarioAutofishBite(o Options, bin string, srv *smoke.Server) error {
	const name = "Fisher"
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "autofish"), "-address", srv.Addr(), "-name", name)
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	if err := waitFor(out, 40*time.Second, "Login success", "Game start"); err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out.String(), 15))
	}
	rcon, err := dialRCON(srv.RCONAddr(), srv.RCONPassword)
	if err != nil {
		return fmt.Errorf("rcon: %w", err)
	}
	defer rcon.close()
	do := func(c string) error {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
		return nil
	}
	// far from the spawn the other scenarios use: the bot first, the land it stands on after
	const far = 2000
	// the chunks round the pond are loaded (forceload: generated and kept) before anything is built in them: a fill in
	// a chunk not loaded is refused ("That position is not loaded"). The bot is not needed there to load them: the
	// example is a bare client that sends no movement, so the server never lets it fall (it hangs where it was put,
	// OnGround 0b for good): it is put on the ground by the surface, not waited for.
	if err := do(fmt.Sprintf("forceload add %d %d %d %d", far-16, far-16, far+16, far+32)); err != nil {
		return err
	}
	defer rcon.command(fmt.Sprintf("forceload remove %d %d %d %d", far-16, far-16, far+16, far+32))
	for _, c := range []string{fmt.Sprintf("execute if loaded %d 0 %d", far-5, far+2), fmt.Sprintf("execute if loaded %d 0 %d", far+5, far+17)} {
		if err := pollRcon(rcon.command, 90*time.Second, c, "passed"); err != nil {
			return fmt.Errorf("the pond's chunks: %w", err)
		}
	}
	// a pond ahead (south: the bot faces yaw 0), nine wide and fourteen long from three blocks away, two deep, its
	// surface the ground's: a cast at the level lands in it. Every command is relative to the surface over the spot
	// (the first air above the highest block), so no height of the world is assumed.
	at := fmt.Sprintf("execute positioned %d 0 %d positioned over motion_blocking_no_leaves run ", far, far)
	for _, c := range []string{
		at + "fill ~-5 ~-4 ~2 ~5 ~-1 ~17 minecraft:stone",
		at + "fill ~-4 ~-3 ~3 ~4 ~-1 ~16 minecraft:water",
		at + "fill ~-5 ~ ~2 ~5 ~3 ~17 minecraft:air",
		at + fmt.Sprintf("tp %s ~0.5 ~ ~0.5 0 0", name), // on the ground by the pond, facing south
		"time set 1000",
		"weather clear",
	} {
		if err := do(c); err != nil {
			return err
		}
	}
	rod := fmt.Sprintf(`give %s minecraft:fishing_rod[minecraft:enchantments={"minecraft:lure":3}]`, name)
	if err := do(rod); err != nil {
		return fmt.Errorf("a Lure III rod: %w", err)
	}
	o.Log("autofishbite: %s on the ground at %d %d facing a pond, a Lure III rod given; waiting for the example to cast (its watchdog, 45 s) and reel", name, far, far)
	if err := waitFor(out, 4*time.Minute, "gra~"); err != nil {
		return fmt.Errorf("the example never reeled on a bite: %v\n%s", err, tail(out.String(), 20))
	}
	time.Sleep(3 * time.Second) // (the catch is handed over with the reel)
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	inv := itemCounts(resp)
	n := 0
	for item, c := range inv {
		if item != "minecraft:fishing_rod" {
			n += c
		}
	}
	o.Log("autofishbite: reeled; the server's inventory: %v", inv)
	if n == 0 {
		return fmt.Errorf("the example reeled (gra~) but the server gave nothing: %v\n%s", inv, tail(out.String(), 20))
	}
	return nil
}

// pollRcon runs a server command till its answer holds want, at most limit: what the harness waits for in the
// server's world (a chunk loaded, a player landed) instead of a sleep that a slow machine outlasts.
func pollRcon(cmd rconCmd, limit time.Duration, command, want string) error {
	deadline := time.Now().Add(limit)
	var last string
	var lastErr error
	for {
		last, lastErr = cmd(command)
		if lastErr == nil && strings.Contains(last, want) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%q still answers %q (%v) after %s, want it to hold %q", command, last, lastErr, limit, want)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
