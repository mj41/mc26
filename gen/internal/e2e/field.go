package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// scenarioField: the robot makes a wheat field with its own water, as the
// week's farm does — a pool two by two (two buckets of water in opposite
// corners: an endless source), two channels of ten filled from it, the rows
// across the field seed, water, seed, seed, water, seed — forty farmland,
// tilled and sown. The water for the pool is fetched from
// a pool put thirty blocks off. Only when named (e2e --only field).
func scenarioField(o Options, bin string) error {
	const name = "Settler"
	srv, r, rcon, done, err := forestStart(o, bin, name, "field.log", false)
	if err != nil {
		return err
	}
	defer done()
	_ = srv
	ans, err := r.ask("pos", 5*time.Second)
	if err != nil {
		return err
	}
	var fx, fy, fz float64
	if _, err := fmt.Sscanf(ans, "%f %f %f", &fx, &fy, &fz); err != nil {
		return fmt.Errorf("pos answered %q", ans)
	}
	x, y, z := int(fx), int(fy), int(fz)
	// water to fetch: a 2x2 pool sunk into the ground thirty blocks east,
	// as a pond is — no walk built to it (a walk at the robot's height was a
	// stone bridge in the sky wherever the land dropped): the robot finds its
	// own way over the land
	wx := x + 30
	pond := waterPond(wx, z, 2)
	for _, c := range append(pond, []string{
		"time set 1000",
		"give " + name + " minecraft:bucket 1",
		"give " + name + " minecraft:stone_hoe 1",
		"give " + name + " minecraft:wheat_seeds 40",
		"give " + name + " minecraft:dirt 64",
		"give " + name + " minecraft:cobblestone 64",
	}...) {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	o.Log("field: robot at %d %d %d, a pond to fetch water at %d %d (sunk into the ground)", x, y, z, wx, z)
	if _, err := r.ask("wait 40", 10*time.Second); err != nil {
		return err
	}
	ans, err = r.ask("farm 40", 20*time.Minute)
	if err != nil {
		return fmt.Errorf("farm: %w\n%s", err, tail(r.out.String(), 25))
	}
	o.Log("field: farm: %s", ans)
	m := regexp.MustCompile(`field at (-?\d+) (-?\d+) (-?\d+) tilled=(\d+) sown=(\d+)`).FindStringSubmatch(ans)
	if m == nil {
		return fmt.Errorf("farm answered %q", ans)
	}
	cx, _ := strconv.Atoi(m[1])
	cy, _ := strconv.Atoi(m[2])
	cz, _ := strconv.Atoi(m[3])
	is := func(x, y, z int, block string) bool {
		resp, err := rcon.command(fmt.Sprintf("execute if block %d %d %d %s", x, y, z, block))
		return err == nil && strings.Contains(resp, "passed")
	}
	// the pool (west of the channel) and the channel: all still water
	var dry []string
	for _, p := range [][2]int{{cx - 6, cz}, {cx - 5, cz}, {cx - 6, cz + 1}, {cx - 5, cz + 1}} {
		if !is(p[0], cy, p[1], "minecraft:water[level=0]") {
			dry = append(dry, fmt.Sprintf("pool %d %d %d", p[0], cy, p[1]))
		}
	}
	for _, dz := range fieldChannelRows {
		for dx := -4; dx < 6; dx++ {
			if !is(cx+dx, cy, cz+dz, "minecraft:water[level=0]") {
				dry = append(dry, fmt.Sprintf("channel %d %d %d", cx+dx, cy, cz+dz))
			}
		}
	}
	if len(dry) > 0 {
		return fmt.Errorf("not still water: %s\n%s", strings.Join(dry, ", "), tail(r.out.String(), 25))
	}
	// the rows: farmland, sown
	sown := 0
	for _, dz := range []int{1, 2, -1, 4} {
		for dx := -4; dx < 6; dx++ {
			if is(cx+dx, cy, cz+dz, "minecraft:farmland") && is(cx+dx, cy+1, cz+dz, "minecraft:wheat") {
				sown++
			}
		}
	}
	o.Log("field: pool and channels still water, %d of 40 sown", sown)
	if sown < 30 {
		return fmt.Errorf("only %d of 40 sown", sown)
	}
	// the trees in and round the field felled (crops want the sun): the logs
	// left within four blocks of it counted (the fill at the end of the test)
	resp, err := rcon.command(fmt.Sprintf("fill %d %d %d %d %d %d minecraft:air replace #minecraft:logs", cx-12, cy-1, cz-6, cx+11, cy+24, cz+9))
	if err != nil {
		return err
	}
	logs := 0
	if m := regexp.MustCompile(`(\d+)`).FindString(resp); m != "" && !strings.Contains(resp, "No blocks") {
		logs, _ = strconv.Atoi(m)
	}
	o.Log("field: %d logs left round the field (%s)", logs, strings.TrimSpace(resp))
	if logs > 6 {
		return fmt.Errorf("%d logs of trees left round the field", logs)
	}
	return nil
}

// fieldChannelRows are the field's channels (z from its centre): the robot's
// field is seed, water, seed, seed, water, seed across, each channel ten long
// from x-4 (the pool two by two at x-6, z and z+1).
var fieldChannelRows = []int{0, 3}

// scenarioFieldWild: no iron, so no bucket — the robot finds still water
// (water: a pond put twenty blocks off, in grass) and makes its field round
// it (farm): the grass at the water's level within four blocks tilled and
// sown. Only when named (e2e --only fieldwild).
func scenarioFieldWild(o Options, bin string) error {
	const name = "Settler"
	_, r, rcon, done, err := forestStart(o, bin, name, "fieldwild.log", false)
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
	// a pond three by three in a meadow nine by nine, twenty blocks east, a
	// grass walk to it at the robot's level
	px := x + 20
	for _, c := range []string{
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:air", x+2, y, z, px+5, y+3, z+1),
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:grass_block", x+2, y-1, z, px+5, y-1, z+1),
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:air", px-5, y, z-5, px+5, y+4, z+5),
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:dirt", px-5, y-3, z-5, px+5, y-2, z+5),
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:grass_block", px-5, y-1, z-5, px+5, y-1, z+5),
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:water", px-1, y-1, z-1, px+1, y-1, z+1),
		"time set 1000",
		"give " + name + " minecraft:stone_hoe 1",
		"give " + name + " minecraft:wheat_seeds 24",
	} {
		if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
			return fmt.Errorf("rcon %q: %q %v", c, resp, err)
		}
	}
	o.Log("fieldwild: robot at %d %d %d, a pond at %d %d %d", x, y, z, px, y-1, z)
	if _, err := r.ask("wait 40", 10*time.Second); err != nil {
		return err
	}
	ans, err = r.ask("water", 6*time.Minute)
	if err != nil {
		return fmt.Errorf("water: %w\n%s", err, tail(r.out.String(), 20))
	}
	o.Log("fieldwild: water: %s", ans)
	ans, err = r.ask("farm 24", 10*time.Minute)
	if err != nil {
		return fmt.Errorf("farm: %w\n%s", err, tail(r.out.String(), 25))
	}
	o.Log("fieldwild: farm: %s", ans)
	if !strings.Contains(ans, "round the water found") {
		return fmt.Errorf("farm made no field round the water: %q", ans)
	}
	sown := 0
	for dx := -5; dx <= 5; dx++ {
		for dz := -5; dz <= 5; dz++ {
			resp, err := rcon.command(fmt.Sprintf("execute if block %d %d %d minecraft:wheat", px+dx, y, z+dz))
			if err == nil && strings.Contains(resp, "passed") {
				sown++
			}
		}
	}
	o.Log("fieldwild: %d sown round the pond", sown)
	if sown < 16 {
		return fmt.Errorf("only %d sown round the pond", sown)
	}
	return nil
}

// scenarioFarmStart: the first day handed over — the tools, logs and
// torches day 1 makes — so the run goes straight to what comes after: its
// shelter, real water found (water), a field round it with seeds from the
// grass (farm, no iron yet), then three iron handed over and the field by
// home with its own water made at the harvest, its buckets from the water
// found. Only when named (e2e --only farmstart).
func scenarioFarmStart(o Options, bin string) error {
	const name = "Settler"
	_, r, rcon, done, err := forestStart(o, bin, name, "farmstart.log", true)
	if err != nil {
		return err
	}
	defer done()
	rc := func(cmds ...string) error {
		for _, c := range cmds {
			if resp, err := rcon.command(c); err != nil || rconFailed(resp) {
				return fmt.Errorf("rcon %q: %q %v", c, resp, err)
			}
		}
		return nil
	}
	// where the world's water is, for reading the run: the nearest river,
	// ocean, frozen ground
	for _, b := range []string{"#minecraft:is_river", "#minecraft:is_ocean", "minecraft:frozen_river", "minecraft:snowy_plains"} {
		if resp, err := rcon.command("locate biome " + b); err == nil {
			o.Log("farmstart: %s", strings.TrimSpace(resp))
		}
	}
	// what the first day makes
	if err := rc("time set 1000",
		"give "+name+" minecraft:stone_pickaxe 1", "give "+name+" minecraft:stone_axe 1",
		"give "+name+" minecraft:stone_sword 1", "give "+name+" minecraft:stone_hoe 1",
		"give "+name+" minecraft:oak_log 32", "give "+name+" minecraft:torch 16",
		"give "+name+" minecraft:stick 16", "give "+name+" minecraft:crafting_table 1",
		"give "+name+" minecraft:cobblestone 64", "give "+name+" minecraft:dirt 32"); err != nil {
		return err
	}
	if _, err := r.ask("wait 40", 10*time.Second); err != nil {
		return err
	}
	step := func(cmd string, limit time.Duration) (string, error) {
		for day := 0; ; day++ {
			if err := rc("time set 1000"); err != nil { // each step in the morning
				return "", err
			}
			start := time.Now()
			ans, err := r.ask(cmd, limit)
			if err != nil {
				return "", fmt.Errorf("%s: %w\n%s", cmd, err, tail(r.out.String(), 25))
			}
			o.Log("farmstart: %s: %s (%s)", cmd, ans, time.Since(start).Round(time.Second))
			// stopped by the dusk (a long way to the water): on the next morning
			if !strings.HasPrefix(ans, "stopped:") || day == 2 {
				return ans, nil
			}
		}
	}
	if _, err := step("shelter", 8*time.Minute); err != nil {
		return err
	}
	if _, err := step("water", 20*time.Minute); err != nil {
		return err
	}
	ans, err := step("farm 16", 12*time.Minute)
	if err != nil {
		return err
	}
	// no bucket: the field round the water found; iron picked up on the way
	// (a bucket made): the field by home, with its own water, at once
	if m := regexp.MustCompile(`sown=(\d+)`).FindStringSubmatch(ans); m == nil || m[1] == "0" {
		return fmt.Errorf("nothing sown: %q", ans)
	}
	o.Log("farmstart: the field %s", map[bool]string{true: "round the water found", false: "by home (it had iron)"}[strings.Contains(ans, "round the water found")])
	// iron for a bucket: the field by home, its water from the water found
	if err := rc("give " + name + " minecraft:iron_ingot 3"); err != nil {
		return err
	}
	if _, err := step("harvest", 15*time.Minute); err != nil {
		return err
	}
	// the field by home, as its memory has it (kept every ten seconds): pool
	// and channel still water
	if _, err := r.ask("wait 240", 20*time.Second); err != nil {
		return err
	}
	mem, err := os.ReadFile(filepath.Join(o.WorkDir, "farmstart.memory.json"))
	if err != nil {
		return err
	}
	var m struct {
		Field *struct{ X, Y, Z int } `json:"field"`
	}
	if err := json.Unmarshal(mem, &m); err != nil {
		return err
	}
	if m.Field == nil {
		return fmt.Errorf("no field by home after the harvest\n%s", tail(r.out.String(), 25))
	}
	cx, cy, cz := m.Field.X, m.Field.Y, m.Field.Z
	var dry []string
	cells := [][2]int{{cx - 6, cz}, {cx - 5, cz}, {cx - 6, cz + 1}, {cx - 5, cz + 1}}
	for _, dz := range fieldChannelRows {
		for dx := -4; dx < 6; dx++ {
			cells = append(cells, [2]int{cx + dx, cz + dz})
		}
	}
	for _, p := range cells {
		resp, err := rcon.command(fmt.Sprintf("execute if block %d %d %d minecraft:water[level=0]", p[0], cy, p[1]))
		if err != nil || !strings.Contains(resp, "passed") {
			dry = append(dry, fmt.Sprintf("%d %d %d", p[0], cy, p[1]))
		}
	}
	if len(dry) > 0 {
		return fmt.Errorf("the field by home at %d %d %d: not still water at %s\n%s", cx, cy, cz, strings.Join(dry, ", "), tail(r.out.String(), 25))
	}
	o.Log("farmstart: the field by home at %d %d %d has its pool and channels", cx, cy, cz)
	return nil
}

// waterPond makes an n×n pond of still water sunk into the ground at x, z, as
// a natural one is: a stone basin under the surface (motion_blocking_no_leaves:
// the ground, not the leaves), the water's top at the ground's level,
// nothing built above the land. The heightmap counts logs, so the trunks
// standing where the pond goes (its rim too) are taken out first: the
// heightmap is then the ground's, even where x, z was a trunk. A test's water
// this way is never a structure in the air.
func waterPond(x, z, n int) []string {
	at := fmt.Sprintf("execute positioned %d 0 %d positioned over motion_blocking_no_leaves run ", x, z)
	return []string{
		at + fmt.Sprintf("fill ~-1 ~-40 ~-1 ~%d ~30 ~%d minecraft:air replace #minecraft:logs", n, n),
		at + fmt.Sprintf("fill ~-1 ~-3 ~-1 ~%d ~-1 ~%d minecraft:stone", n, n),
		at + fmt.Sprintf("fill ~ ~-2 ~ ~%d ~-1 ~%d minecraft:water", n-1, n-1),
		at + fmt.Sprintf("fill ~ ~ ~ ~%d ~2 ~%d minecraft:air", n-1, n-1),
	}
}
