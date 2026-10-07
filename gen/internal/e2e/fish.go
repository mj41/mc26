package e2e

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// scenarioFish: the robot's first food, fishing. It starts in a forest by
// day with a pool of still water (four by four, in stone, open to the sky)
// a few blocks off, a rod, a furnace and coal; "fish 3" catches three cod or
// salmon and cooks them. Only when named (e2e --only fish).
func scenarioFish(o Options, bin string) error {
	const name = "Settler"
	_, r, rcon, done, err := forestStart(o, bin, name, "fish.log", false)
	if err != nil {
		return err
	}
	defer done()
	ans, err := r.ask("pos", 5*time.Second)
	if err != nil {
		return err
	}
	var fx, fy, fz float64
	if _, err := fmt.Sscanf(ans, "%f %f %f", &fx, &fy, &fz); err != nil {
		return fmt.Errorf("pos answered %q", ans)
	}
	x, y, z := int(fx), int(fy), int(fz)
	// a pond four east, four by four, sunk into the ground (nothing built in
	// the air)
	px := x + 4
	for _, c := range append(waterPond(px, z-1, 4), []string{
		"time set 1000",
		"weather clear",
		"give " + name + " minecraft:fishing_rod 1",
		"give " + name + " minecraft:furnace 1",
		"give " + name + " minecraft:coal 4",
	}...) {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	o.Log("fish: robot at %d %d %d, a pond at %d %d", x, y, z, px, z)
	if _, err := r.ask("wait 40", 10*time.Second); err != nil {
		return err
	}
	ans, err = r.ask("fish 3", 12*time.Minute)
	if err != nil {
		return fmt.Errorf("fish: %w\n%s", err, tail(r.out.String(), 25))
	}
	o.Log("fish: %s", ans)
	m := regexp.MustCompile(`caught=(\d+) cooked=(\d+)`).FindStringSubmatch(ans)
	if m == nil {
		return fmt.Errorf("fish answered %q", ans)
	}
	if caught, _ := strconv.Atoi(m[1]); caught < 3 {
		return fmt.Errorf("caught %d of 3\n%s", caught, tail(r.out.String(), 25))
	}
	resp, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	inv := itemCounts(resp)
	cooked := inv["minecraft:cooked_cod"] + inv["minecraft:cooked_salmon"]
	o.Log("fish: the server has %d cooked fish: %v", cooked, inv)
	if cooked < 3 {
		return fmt.Errorf("the server has %d cooked fish, not 3: %v", cooked, inv)
	}
	return nil
}
