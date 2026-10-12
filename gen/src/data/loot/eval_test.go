package loot

import (
	"math/rand"
	"strings"
	"testing"
)

func drops(t *testing.T, block string, tool Tool, props map[string]string, seed int64, n int) map[string]int {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	total := map[string]int{}
	for range n {
		got, err := Drops(block, Context{Tool: tool, Props: props, Rand: rng})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			total[s.Item] += s.Count
		}
	}
	return total
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s: %.4f, want %.4f ± %.4f", what, got, want, tol)
	}
}

// Stone gives cobblestone, or itself with silk touch.
func TestStone(t *testing.T) {
	if d := drops(t, "minecraft:stone", Tool{}, nil, 1, 10); d["minecraft:cobblestone"] != 10 || len(d) != 1 {
		t.Errorf("stone: %v", d)
	}
	silk := Tool{Item: "minecraft:iron_pickaxe", Enchantments: map[string]int{"minecraft:silk_touch": 1}}
	if d := drops(t, "minecraft:stone", silk, nil, 1, 10); d["minecraft:stone"] != 10 || len(d) != 1 {
		t.Errorf("stone with silk touch: %v", d)
	}
}

// Oak leaves broken by hand: a sapling one in twenty, a stick (one or two) two in a hundred, an apple one
// in two hundred; shears give the leaves themselves. The rates are the data's: a statistical check,
// which one trace cannot make.
func TestOakLeaves(t *testing.T) {
	const n = 20000
	d := drops(t, "minecraft:oak_leaves", Tool{}, nil, 7, n)
	near(t, "saplings", float64(d["minecraft:oak_sapling"])/n, 0.05, 0.006)
	near(t, "sticks", float64(d["minecraft:stick"])/n, 0.02*1.5, 0.006)
	near(t, "apples", float64(d["minecraft:apple"])/n, 0.005, 0.003)
	if d["minecraft:oak_leaves"] != 0 {
		t.Errorf("leaves by hand gave leaves")
	}
	if d := drops(t, "minecraft:oak_leaves", Tool{Item: "minecraft:shears"}, nil, 7, 100); d["minecraft:oak_leaves"] != 100 || len(d) != 1 {
		t.Errorf("leaves with shears: %v", d)
	}
}

// Fortune on iron ore: ore_drops, a multiplier of 1, 1, 2, 3, 4 at level three (mean 2.2).
func TestFortuneOnIronOre(t *testing.T) {
	const n = 20000
	d := drops(t, "minecraft:iron_ore", Tool{Item: "minecraft:iron_pickaxe", Enchantments: map[string]int{"minecraft:fortune": 3}}, nil, 3, n)
	near(t, "raw iron per ore", float64(d["minecraft:raw_iron"])/n, 2.2, 0.05)
	if d := drops(t, "minecraft:iron_ore", Tool{}, nil, 3, 100); d["minecraft:raw_iron"] != 100 {
		t.Errorf("no fortune: %v", d)
	}
}

// Blocks broken by an explosion of radius 3 (a creeper's): a drop survives one time in three — stone gives
// cobblestone a third of the time; a gravel's flint or gravel likewise.
func TestExplosion(t *testing.T) {
	const n = 20000
	rng := rand.New(rand.NewSource(4))
	got := 0
	for range n {
		d, err := Drops("minecraft:stone", Context{Rand: rng, ExplosionRadius: 3})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range d {
			got += s.Count
		}
	}
	near(t, "cobblestone per stone blown up", float64(got)/n, 1.0/3, 0.015)
}

// A plain log, a number provider (the binomial of a berry bush is for a table that has it), a table without a case.
func TestOthers(t *testing.T) {
	if d := drops(t, "minecraft:oak_log", Tool{}, nil, 1, 5); d["minecraft:oak_log"] != 5 {
		t.Errorf("log: %v", d)
	}
	if _, err := Drops("minecraft:no_such_block", Context{Rand: rand.New(rand.NewSource(1))}); err == nil {
		t.Errorf("a block without a table gave drops")
	}
	if _, err := Drops("minecraft:stone", Context{}); err == nil || !strings.Contains(err.Error(), "chance") {
		t.Errorf("no source of chance: %v", err)
	}
}

// Every table of the pack can be played, or says what it cannot: no table panics, none is skipped.
func TestEveryTable(t *testing.T) {
	ts, err := loaded()
	if err != nil {
		t.Fatal(err)
	}
	unknown := map[string]int{}
	for block := range ts.Tables {
		_, err := Drops(block, Context{Rand: rand.New(rand.NewSource(1)), Props: map[string]string{}})
		if err != nil {
			unknown[err.Error()[strings.Index(err.Error(), "not modelled"):]]++
		}
	}
	t.Logf("%d tables, the ones not played: %v", len(ts.Tables), unknown)
}

func kills(t *testing.T, entity string, ctx Context, seed int64, n int) map[string]int {
	t.Helper()
	ctx.Rand = rand.New(rand.NewSource(seed))
	total := map[string]int{}
	for range n {
		got, err := EntityDrops(entity, ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			total[s.Item] += s.Count
		}
	}
	return total
}

// byPlayer is a kill by a player's blow, with what the player holds.
func byPlayer(weapon Tool) Context {
	return Context{Attacker: &Entity{Type: "minecraft:player", Mainhand: weapon}, KilledByPlayer: true, DamageTags: []string{"minecraft:is_player_attack"}}
}

// A zombie killed by a player twenty thousand times: rotten flesh 0–2 a kill (one on the mean), and one
// kill in forty a rare drop — an iron ingot, a carrot or a potato, a third each. Killed by nobody (it
// burned in the sun) it leaves the flesh, never a rare drop; with looting III, more of both.
func TestZombie(t *testing.T) {
	const n = 20000
	d := kills(t, "minecraft:zombie", byPlayer(Tool{}), 11, n)
	near(t, "rotten flesh per kill", float64(d["minecraft:rotten_flesh"])/n, 1.0, 0.03)
	for _, rare := range []string{"minecraft:iron_ingot", "minecraft:carrot", "minecraft:potato"} {
		near(t, rare+" per kill", float64(d[rare])/n, 0.025/3, 0.003)
	}
	if len(d) != 4 {
		t.Errorf("a zombie gave %v", d)
	}
	d = kills(t, "minecraft:zombie", Context{}, 11, n/4)
	near(t, "rotten flesh per kill, nobody's", float64(d["minecraft:rotten_flesh"])/(n/4), 1.0, 0.05)
	if len(d) != 1 {
		t.Errorf("a zombie killed by nobody gave %v", d)
	}
	// looting III: the flesh 1 + round(3 × 0..1) more (1.5 on the mean), the rare chance 0.035 + 2 × 0.01
	d = kills(t, "minecraft:zombie", byPlayer(Tool{Item: "minecraft:iron_sword", Enchantments: map[string]int{"minecraft:looting": 3}}), 11, n)
	near(t, "rotten flesh per kill, looting III", float64(d["minecraft:rotten_flesh"])/n, 2.5, 0.05)
	near(t, "rare drops per kill, looting III", float64(d["minecraft:iron_ingot"]+d["minecraft:carrot"]+d["minecraft:potato"])/n, 0.055, 0.006)
}

// A zombie burning (or killed by a fire-aspect sword) leaves its potato baked; a cow on fire, its beef cooked.
func TestSmeltedWhenOnFire(t *testing.T) {
	fire := byPlayer(Tool{Item: "minecraft:iron_sword", Enchantments: map[string]int{"minecraft:fire_aspect": 1}})
	d := kills(t, "minecraft:zombie", fire, 5, 5000)
	if d["minecraft:potato"] != 0 || d["minecraft:baked_potato"] == 0 {
		t.Errorf("a zombie killed with fire aspect: %v", d)
	}
	cow := byPlayer(Tool{})
	cow.This = &Entity{Type: "minecraft:cow", OnFire: true}
	d = kills(t, "minecraft:cow", cow, 5, 100)
	if d["minecraft:beef"] != 0 || d["minecraft:cooked_beef"] < 100 {
		t.Errorf("a cow on fire: %v", d)
	}
}

// A sheep leaves its mutton and, unshorn, one wool of its colour (the colour's own table); a sheep whose colour
// is not told is not modelled, and says so.
func TestSheep(t *testing.T) {
	ctx := byPlayer(Tool{})
	ctx.This = &Entity{Type: "minecraft:sheep", Components: map[string]string{"minecraft:sheep/color": "lime"}, Specific: map[string]string{"sheep/sheared": "false"}}
	d := kills(t, "minecraft:sheep", ctx, 3, 100)
	if d["minecraft:lime_wool"] != 100 || d["minecraft:mutton"] < 100 || len(d) != 2 {
		t.Errorf("a lime sheep: %v", d)
	}
	ctx.This.Specific["sheep/sheared"] = "true"
	if d := kills(t, "minecraft:sheep", ctx, 3, 100); d["minecraft:lime_wool"] != 0 {
		t.Errorf("a shorn sheep: %v", d)
	}
	_, err := EntityDrops("minecraft:sheep", Context{Rand: rand.New(rand.NewSource(1))})
	if err == nil || !strings.Contains(err.Error(), "minecraft:sheep/color") {
		t.Errorf("a sheep of no colour told: %v", err)
	}
}

// A creeper killed by a skeleton's arrow leaves a music disc, one of the tag's twelve; by a player, none.
func TestCreeperBySkeleton(t *testing.T) {
	ctx := Context{Attacker: &Entity{Type: "minecraft:skeleton"}, Direct: &Entity{Type: "minecraft:arrow"}, DamageTags: []string{"minecraft:is_projectile"}}
	d := kills(t, "minecraft:creeper", ctx, 2, 600)
	discs := 0
	for it, n := range d {
		if strings.HasPrefix(it, "minecraft:music_disc_") {
			discs += n
		}
	}
	if discs != 600 || len(d) != 13 {
		t.Errorf("creepers killed by skeletons: %v", d)
	}
	for it := range kills(t, "minecraft:creeper", byPlayer(Tool{}), 2, 200) {
		if strings.HasPrefix(it, "minecraft:music_disc_") {
			t.Errorf("a creeper killed by a player left %s", it)
		}
	}
}

// Every entity table of the pack plays, told what the tables ask of the entity (a colour, a size, a
// captain), killed by a player and by a skeleton's arrow: none is left out, none fails.
func TestEveryEntityTable(t *testing.T) {
	names := Entities()
	if len(names) < 100 {
		t.Fatalf("%d entity tables", len(names))
	}
	this := func(name string) *Entity {
		return &Entity{Type: name, Components: map[string]string{"minecraft:sheep/color": "white"},
			Specific: map[string]string{"sheep/sheared": "false", "cube_mob/size": "2", "raider/is_captain": "true"}}
	}
	for _, name := range names {
		for _, ctx := range []Context{byPlayer(Tool{Item: "minecraft:iron_sword", Enchantments: map[string]int{"minecraft:looting": 2}}),
			{Attacker: &Entity{Type: "minecraft:skeleton"}, Direct: &Entity{Type: "minecraft:arrow"}, DamageTags: []string{"minecraft:is_projectile"}}} {
			ctx.This = this(name)
			ctx.Rand = rand.New(rand.NewSource(1))
			for range 20 {
				if _, err := EntityDrops(name, ctx); err != nil {
					t.Errorf("%v", err)
					break
				}
			}
		}
	}
	if _, err := EntityDrops("minecraft:no_such_mob", Context{Rand: rand.New(rand.NewSource(1))}); err == nil {
		t.Errorf("a mob without a table gave drops")
	}
}

// A condition or a function the evaluator does not know stops it, naming it.
func TestUnknownStopsWithItsName(t *testing.T) {
	ts, err := loaded()
	if err != nil {
		t.Fatal(err)
	}
	e := &eval{ts: ts, ctx: Context{Rand: rand.New(rand.NewSource(1)), This: &Entity{}}, entity: true}
	if _, err := e.cond([]byte(`{"type":"minecraft:weather_check","raining":true}`)); err == nil || !strings.Contains(err.Error(), "weather_check") {
		t.Errorf("an unknown condition: %v", err)
	}
	s := Stack{Item: "minecraft:stick", Count: 1}
	if err := e.functions([]byte(`[{"function":"x","type":"minecraft:set_lore"}]`), &s); err == nil || !strings.Contains(err.Error(), "set_lore") {
		t.Errorf("an unknown function: %v", err)
	}
	if _, err := e.cond([]byte(`{"type":"minecraft:entity_properties","entity":"this","predicate":{"minecraft:flags":{"is_sneaking":true}}}`)); err == nil || !strings.Contains(err.Error(), "is_sneaking") {
		t.Errorf("an unknown flag: %v", err)
	}
}
