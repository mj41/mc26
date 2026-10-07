package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// scenarioVillage: the robot at a village of the survival world, trading with
// its villagers — real ones, with the trades the game gave them, which is what
// exercises the merchant's packets (offers, the selected trade, the result
// slot). It is put near the nearest village and given what a robot of a few
// days has much of (coal, wheat, sticks); it must find the villagers
// (village), trade (tradeall) and end with emeralds, the server objecting to
// nothing. Only when named (e2e --only village).
func scenarioVillage(o Options, bin string) error {
	const name = "Settler"
	srv, r, rcon, done, err := forestStart(o, bin, name, "village.log", false)
	if err != nil {
		return err
	}
	defer done()
	logFrom := serverLogSize(srv)
	resp, err := rcon.command("locate structure #minecraft:village")
	if err != nil {
		return err
	}
	m := regexp.MustCompile(`\[(-?\d+), [^,]+, (-?\d+)\]`).FindStringSubmatch(resp)
	if m == nil {
		return fmt.Errorf("no village: %q", resp)
	}
	o.Log("village: %s", strings.TrimSpace(resp))
	if resp, err := rcon.command("spreadplayers " + m[1] + " " + m[2] + " 0 8 false " + name); err != nil || !strings.Contains(resp, "Spread") {
		return fmt.Errorf("to the village at %s %s: %q %v", m[1], m[2], resp, err)
	}
	for _, c := range []string{"give " + name + " minecraft:coal 64", "give " + name + " minecraft:wheat 64", "give " + name + " minecraft:stick 64"} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	if _, err := r.ask("wait 60", 10*time.Second); err != nil {
		return err
	}
	ans, err := r.ask("village 3", 6*time.Minute)
	if err != nil {
		return fmt.Errorf("village: %w\n%s", err, tail(r.out.String(), 20))
	}
	o.Log("village: village 3: %s", ans)
	// a village just generated: its villagers take their jobs (claim their
	// workstations) in the first minutes of the day — tried again till then
	for try := 0; ; try++ {
		ans, err := r.ask("tradeall", 6*time.Minute)
		if err == nil && !strings.Contains(ans, "traded 0 times") {
			o.Log("village: tradeall: %s", ans)
			break
		}
		if try == 8 {
			return fmt.Errorf("tradeall: %q %v\n%s", ans, err, tail(r.out.String(), 20))
		}
		o.Log("village: tradeall: %q %v: again in half a minute", ans, err)
		if _, err := r.ask("wait 600", 40*time.Second); err != nil {
			return err
		}
	}
	// every offer the robot saw, for the log
	for _, l := range strings.Split(r.out.String(), "\n") {
		if strings.Contains(l, "trade: villager") {
			o.Log("village: %s", strings.TrimSpace(l))
		}
	}
	inv, err := rcon.command("data get entity " + name + " Inventory")
	if err != nil {
		return err
	}
	if n := itemCounts(inv)["minecraft:emerald"]; n == 0 {
		return fmt.Errorf("no emerald after trading: %v", itemCounts(inv))
	} else {
		o.Log("village: the server has %d emeralds in the robot's inventory", n)
	}
	if bad := complaints(serverLogFrom(srv, logFrom), name); len(bad) > 0 {
		return fmt.Errorf("the server objected:\n%s", strings.Join(bad, "\n"))
	}
	return nil
}
