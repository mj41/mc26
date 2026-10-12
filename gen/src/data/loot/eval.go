package loot

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mj41/go-mc26/data/recipe"
	"github.com/mj41/go-mc26/data/tags"
)

// The loot tables played: what breaking a block gives with a tool, its
// enchantments and a chance, and what a mob killed leaves, by whom and how
// (the data pack's tables, tables.json, generated). A condition or a function
// the evaluator does not know stops it with an error naming it — never a
// guess; so does a property of an entity the table asks and the caller did not
// tell (a sheep's colour).

//go:embed tables.json
var tablesJSON []byte

// Stack is items of one kind.
type Stack struct {
	Item  string
	Count int
}

// Tool is what the player breaks with.
type Tool struct {
	Item         string         // "minecraft:shears", "" for a hand
	Enchantments map[string]int // "minecraft:fortune": 3
}

// Context is what a table is played against.
type Context struct {
	Tool  Tool
	Props map[string]string // the state of the block broken: half=upper
	Rand  *rand.Rand
	// Block says the block at an offset from the one broken (location checks:
	// the upper half of a tall grass); nil: not known, and such a table is not modelled.
	Block func(dx, dy, dz int) (name string, props map[string]string, ok bool)
	// ExplosionRadius is the radius of the explosion that broke the block (a creeper's 3); 0: none —
	// each drop then survives one time in the radius, each item of a stack likewise.
	ExplosionRadius float64

	// An entity's table (EntityDrops) is played against the entity killed and how it died:
	This           *Entity  // the entity killed (EntityDrops makes a plain one of its kind when nil)
	Attacker       *Entity  // who killed it: a player, a skeleton; nil: nobody (it fell, burned)
	Direct         *Entity  // what hit it, when not the attacker itself: an arrow, a fireball
	KilledByPlayer bool     // a player hurt it lately (the game's last_damage_player): the rare drops need it
	DamageTags     []string // the damage type's tags: "minecraft:is_projectile", "minecraft:is_lightning"
}

// Entity is an entity as a loot table asks of it.
type Entity struct {
	Type     string // "minecraft:zombie"
	OnFire   bool
	Baby     bool
	Vehicle  *Entity // what it rides; nil: nothing
	Mainhand Tool    // what it holds: a killer's weapon, whose looting and fire aspect count
	// Components are its components a table may ask: "minecraft:sheep/color": "white".
	Components map[string]string
	// Specific are its type-specific properties, kind/name: "sheep/sheared": "false",
	// "cube_mob/size": "1", "raider/is_captain": "true".
	Specific map[string]string
}

type tableSet struct {
	Tables     map[string]json.RawMessage `json:"tables"`   // the blocks': "minecraft:stone"
	Entities   map[string]json.RawMessage `json:"entities"` // the entities': "minecraft:zombie", "minecraft:sheep/white"
	Others     map[string]json.RawMessage `json:"others"`   // the others an entry names, by full name: "minecraft:gameplay/fishing/fish"
	Predicates map[string]json.RawMessage `json:"predicates"`
}

// byName finds a table by its full name, as a loot_table entry names it ("minecraft:entities/sheep/white").
func (ts tableSet) byName(name string) (json.RawMessage, bool) {
	if rest, ok := strings.CutPrefix(name, "minecraft:blocks/"); ok {
		t, ok := ts.Tables["minecraft:"+rest]
		return t, ok
	}
	if rest, ok := strings.CutPrefix(name, "minecraft:entities/"); ok {
		t, ok := ts.Entities["minecraft:"+rest]
		return t, ok
	}
	t, ok := ts.Others[name]
	return t, ok
}

var loaded = sync.OnceValues(func() (tableSet, error) {
	var t tableSet
	err := json.Unmarshal(tablesJSON, &t)
	return t, err
})

// Drops plays the block's loot table: the stacks it gives. A block without a
// table gives nothing the data knows of: an error.
func Drops(block string, ctx Context) ([]Stack, error) {
	ts, err := loaded()
	if err != nil {
		return nil, err
	}
	raw, ok := ts.Tables[block]
	if !ok {
		return nil, fmt.Errorf("loot: %s: no loot table", block)
	}
	if ctx.Rand == nil {
		return nil, fmt.Errorf("loot: %s: no source of chance", block)
	}
	ctx.This, ctx.Attacker, ctx.Direct = nil, nil, nil // (a block's table asks of no entity)
	e := &eval{ts: ts, ctx: ctx, block: block}
	out, err := e.table(raw)
	if err != nil {
		return nil, fmt.Errorf("loot: %s: not modelled: %w", block, err)
	}
	return out, nil
}

// Entities returns the names of the entities that have a loot table ("minecraft:zombie",
// "minecraft:sheep/white" — a sheep's wool, which its own table names), sorted.
func Entities() []string {
	ts, err := loaded()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ts.Entities))
	for n := range ts.Entities {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EntityDrops plays the loot table of an entity killed ("minecraft:zombie"): the stacks it
// leaves, by who killed it (ctx.Attacker, ctx.KilledByPlayer), with what (the attacker's
// Mainhand: looting, fire aspect) and how it was (ctx.This: on fire, its colour). An entity
// without a table leaves nothing the data knows of: an error.
func EntityDrops(entity string, ctx Context) ([]Stack, error) {
	ts, err := loaded()
	if err != nil {
		return nil, err
	}
	raw, ok := ts.Entities[entity]
	if !ok {
		return nil, fmt.Errorf("loot: %s: no loot table", entity)
	}
	if ctx.Rand == nil {
		return nil, fmt.Errorf("loot: %s: no source of chance", entity)
	}
	if ctx.This == nil {
		ctx.This = &Entity{Type: entity}
	}
	e := &eval{ts: ts, ctx: ctx, block: entity, entity: true}
	out, err := e.table(raw)
	if err != nil {
		return nil, fmt.Errorf("loot: %s: not modelled: %w", entity, err)
	}
	return out, nil
}

type eval struct {
	ts     tableSet
	ctx    Context
	block  string // the block broken, or the entity killed
	entity bool   // an entity's table: the conditions on entities and the damage are played
	depth  int    // tables inside tables (loot_table entries)
}

// table plays a table's pools.
func (e *eval) table(raw json.RawMessage) ([]Stack, error) {
	var t struct {
		Pools []json.RawMessage `json:"pools"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	var out []Stack
	for _, p := range t.Pools {
		got, err := e.pool(p)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// attackerLevel is the killer's level of the enchantment (its main hand's): what looting asks.
func (e *eval) attackerLevel(enchantment string) int {
	if e.ctx.Attacker == nil {
		return 0
	}
	return e.ctx.Attacker.Mainhand.Enchantments[enchantment]
}

func (e *eval) pool(raw json.RawMessage) ([]Stack, error) {
	var p struct {
		Rolls      json.RawMessage   `json:"rolls"`
		BonusRolls json.RawMessage   `json:"bonus_rolls"`
		Condition  json.RawMessage   `json:"condition"`
		Conditions []json.RawMessage `json:"conditions"`
		Entries    []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if ok, err := e.all(p.Condition, p.Conditions); err != nil || !ok {
		return nil, err
	}
	if len(p.BonusRolls) > 0 {
		if n, err := e.number(p.BonusRolls); err != nil || n != 0 {
			return nil, fmt.Errorf("bonus rolls (luck)")
		}
	}
	rolls := 1
	if len(p.Rolls) > 0 {
		n, err := e.number(p.Rolls)
		if err != nil {
			return nil, err
		}
		rolls = int(n)
	}
	var out []Stack
	for range rolls {
		got, err := e.pick(p.Entries)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// entry is one entry of a pool or of alternatives.
type entry struct {
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Items      string            `json:"items"`  // a tag entry's tag: "#minecraft:creeper_drop_music_discs"
	Expand     bool              `json:"expand"` // a tag entry: each item an entry of its own, of the tag's weight
	Value      json.RawMessage   `json:"value"`  // a loot_table entry's table: its name, or the table itself
	Weight     int               `json:"weight"`
	Condition  json.RawMessage   `json:"condition"`
	Conditions []json.RawMessage `json:"conditions"`
	Modifier   json.RawMessage   `json:"modifier"`
	Functions  json.RawMessage   `json:"functions"`
	Children   []json.RawMessage `json:"children"`
}

// pick chooses one of the entries whose conditions hold, by weight, and plays it.
func (e *eval) pick(raws []json.RawMessage) ([]Stack, error) {
	var ok []entry
	total := 0
	for _, raw := range raws {
		var en entry
		if err := json.Unmarshal(raw, &en); err != nil {
			return nil, err
		}
		pass, err := e.all(en.Condition, en.Conditions)
		if err != nil {
			return nil, err
		}
		if !pass {
			continue
		}
		if en.Weight == 0 {
			en.Weight = 1
		}
		if strings.TrimPrefix(en.Type, "minecraft:") == "tag" && en.Expand {
			items := tags.Item(strings.TrimPrefix(en.Items, "#"))
			if len(items) == 0 {
				return nil, fmt.Errorf("the item tag %s (empty or not in the pack)", en.Items)
			}
			for _, it := range items {
				one := en
				one.Type, one.Name = "minecraft:item", it
				ok = append(ok, one)
				total += en.Weight
			}
			continue
		}
		ok = append(ok, en)
		total += en.Weight
	}
	if len(ok) == 0 {
		return nil, nil
	}
	chosen := ok[0]
	if len(ok) > 1 {
		r := e.ctx.Rand.Intn(total)
		for _, en := range ok {
			if r < en.Weight {
				chosen = en
				break
			}
			r -= en.Weight
		}
	}
	return e.play(chosen)
}

func (e *eval) play(en entry) ([]Stack, error) {
	switch strings.TrimPrefix(en.Type, "minecraft:") {
	case "item":
		s := Stack{Item: en.Name, Count: 1}
		for _, mods := range []json.RawMessage{en.Modifier, en.Functions} {
			if err := e.functions(mods, &s); err != nil {
				return nil, err
			}
		}
		if s.Count <= 0 {
			return nil, nil
		}
		return []Stack{s}, nil
	case "alternatives": // the first child whose conditions hold
		for _, raw := range en.Children {
			var ch entry
			if err := json.Unmarshal(raw, &ch); err != nil {
				return nil, err
			}
			pass, err := e.all(ch.Condition, ch.Conditions)
			if err != nil {
				return nil, err
			}
			if pass {
				return e.play(ch)
			}
		}
		return nil, nil
	case "empty":
		return nil, nil
	case "tag": // (not expanded: every item of the tag)
		var out []Stack
		for _, it := range tags.Item(strings.TrimPrefix(en.Items, "#")) {
			one := en
			one.Type, one.Name = "minecraft:item", it
			got, err := e.play(one)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
		}
		return out, nil
	case "loot_table": // another table played, the entry's functions on each of its stacks
		raw := en.Value
		var name string
		if json.Unmarshal(raw, &name) == nil {
			t, ok := e.ts.byName(name)
			if !ok {
				return nil, fmt.Errorf("the loot table %s (not in the data)", name)
			}
			raw = t
		}
		if e.depth > 8 {
			return nil, fmt.Errorf("loot tables nested too deep (%s)", en.Value)
		}
		e.depth++
		got, err := e.table(raw)
		e.depth--
		if err != nil {
			return nil, err
		}
		var out []Stack
		for _, s := range got {
			for _, mods := range []json.RawMessage{en.Modifier, en.Functions} {
				if err := e.functions(mods, &s); err != nil {
					return nil, err
				}
			}
			if s.Count > 0 {
				out = append(out, s)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("the entry type %s", en.Type)
}

// functions applies a list (or one) of item functions to the stack.
func (e *eval) functions(raw json.RawMessage, s *Stack) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	for _, f := range list {
		var fn struct {
			Type       string            `json:"type"`
			Condition  json.RawMessage   `json:"condition"`
			Conditions []json.RawMessage `json:"conditions"`
			Count      json.RawMessage   `json:"count"`
			Add        bool              `json:"add"`
			Enchant    string            `json:"enchantment"`
			Formula    string            `json:"formula"`
			Parameters struct {
				BonusMultiplier int     `json:"bonusMultiplier"`
				Extra           int     `json:"extra"`
				Probability     float64 `json:"probability"`
			} `json:"parameters"`
			Limit json.RawMessage `json:"limit"` // limit_count's {min, max}; enchanted_count_increase's most
		}
		if err := json.Unmarshal(f, &fn); err != nil {
			return err
		}
		if ok, err := e.all(fn.Condition, fn.Conditions); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		switch strings.TrimPrefix(fn.Type, "minecraft:") {
		case "explosion_decay": // each item kept one time in the radius (none: nothing explodes)
			if r := e.ctx.ExplosionRadius; r > 0 {
				kept := 0
				for range s.Count {
					if e.ctx.Rand.Float64() <= 1/r {
						kept++
					}
				}
				s.Count = kept
			}
		case "copy_components", "copy_state": // the item's components and states: not its count
		case "set_count":
			n, err := e.number(fn.Count)
			if err != nil {
				return err
			}
			if fn.Add {
				s.Count += int(n)
			} else {
				s.Count = int(n)
			}
		case "limit_count":
			var limit struct {
				Min *float64 `json:"min"`
				Max *float64 `json:"max"`
			}
			if err := json.Unmarshal(fn.Limit, &limit); err != nil {
				return err
			}
			if limit.Min != nil {
				s.Count = max(s.Count, int(*limit.Min))
			}
			if limit.Max != nil {
				s.Count = min(s.Count, int(*limit.Max))
			}
		case "enchanted_count_increase": // looting: the level times a number more, rounded (none without the enchantment)
			level := e.attackerLevel(fn.Enchant)
			if !e.entity || level == 0 {
				break
			}
			f, err := e.float(fn.Count)
			if err != nil {
				return err
			}
			s.Count += int(math.Floor(float64(level)*f + 0.5))
			var limit int
			if len(fn.Limit) > 0 {
				if err := json.Unmarshal(fn.Limit, &limit); err != nil {
					return err
				}
			}
			if limit > 0 {
				s.Count = min(s.Count, limit)
			}
		case "furnace_smelt": // what a furnace makes of it (meat cooked when the mob burned); unchanged when nothing
			if out, ok := smelted()[s.Item]; ok {
				s.Item = out
			}
		case "set_potion", "set_ominous_bottle_amplifier": // the item's components (its potion): not its kind, not its count
		case "apply_bonus":
			level := e.ctx.Tool.Enchantments[fn.Enchant]
			switch strings.TrimPrefix(fn.Formula, "minecraft:") {
			case "ore_drops": // OreDrops: a bonus of up to level times, none when the roll is below zero
				if level > 0 {
					s.Count *= max(e.ctx.Rand.Intn(level+2)-1, 0) + 1
				}
			case "uniform_bonus_count":
				s.Count += e.ctx.Rand.Intn(fn.Parameters.BonusMultiplier*level + 1)
			case "binomial_with_bonus_count":
				for range level + fn.Parameters.Extra {
					if e.ctx.Rand.Float64() < fn.Parameters.Probability {
						s.Count++
					}
				}
			default:
				return fmt.Errorf("the bonus formula %s", fn.Formula)
			}
		default:
			return fmt.Errorf("the function %s", fn.Type)
		}
	}
	return nil
}

// number plays a number provider as a whole number (the game's getInt): a constant, uniform
// {min,max} (each whole number between, both included), binomial {n,p}.
func (e *eval) number(raw json.RawMessage) (float64, error) {
	return e.provide(raw, false)
}

// float plays a number provider as a fraction (the game's getFloat): uniform is any number between.
func (e *eval) float(raw json.RawMessage) (float64, error) {
	return e.provide(raw, true)
}

func (e *eval) provide(raw json.RawMessage, fraction bool) (float64, error) {
	var c float64
	if json.Unmarshal(raw, &c) == nil {
		return c, nil
	}
	var n struct {
		Type  string  `json:"type"`
		Value float64 `json:"value"`
		Min   float64 `json:"min"`
		Max   float64 `json:"max"`
		N     int     `json:"n"`
		P     float64 `json:"p"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	switch strings.TrimPrefix(n.Type, "minecraft:") {
	case "constant":
		return n.Value, nil
	case "uniform":
		if fraction {
			if n.Max <= n.Min {
				return n.Min, nil
			}
			return n.Min + e.ctx.Rand.Float64()*(n.Max-n.Min), nil
		}
		lo, hi := math.Floor(n.Min+0.5), math.Floor(n.Max+0.5)
		if hi <= lo {
			return lo, nil
		}
		return lo + float64(e.ctx.Rand.Intn(int(hi-lo)+1)), nil
	case "binomial":
		k := 0
		for range n.N {
			if e.ctx.Rand.Float64() < n.P {
				k++
			}
		}
		return float64(k), nil
	}
	return 0, fmt.Errorf("the number provider %s", n.Type)
}

// levelValue plays a level-based value at the level: a number, linear {base, per_level_above_first}.
func levelValue(raw json.RawMessage, level int) (float64, error) {
	var c float64
	if json.Unmarshal(raw, &c) == nil {
		return c, nil
	}
	var v struct {
		Type     string  `json:"type"`
		Base     float64 `json:"base"`
		PerLevel float64 `json:"per_level_above_first"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	switch strings.TrimPrefix(v.Type, "minecraft:") {
	case "linear":
		return v.Base + v.PerLevel*float64(level-1), nil
	}
	return 0, fmt.Errorf("the level-based value %s", v.Type)
}

// smelted is what a furnace makes of each item (the pack's smelting recipes).
var smelted = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	for _, r := range recipe.All() {
		if r.Kind != "minecraft:smelting" || len(r.Ingredients) != 1 {
			continue
		}
		for _, in := range r.Ingredients[0] {
			items := []string{in}
			if tag, isTag := strings.CutPrefix(in, "#"); isTag {
				items = tags.Item(tag)
			}
			for _, it := range items {
				if _, seen := out[it]; !seen {
					out[it] = r.Result
				}
			}
		}
	}
	return out
})

// all reports whether every condition holds (one in condition, several in conditions).
func (e *eval) all(one json.RawMessage, many []json.RawMessage) (bool, error) {
	conds := many
	if len(one) > 0 && string(one) != "null" {
		conds = append([]json.RawMessage{one}, many...)
	}
	for _, c := range conds {
		ok, err := e.cond(c)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// cond plays a condition: a named predicate (a string) or an object.
func (e *eval) cond(raw json.RawMessage) (bool, error) {
	var ref string
	if json.Unmarshal(raw, &ref) == nil {
		p, ok := e.ts.Predicates[ref]
		if !ok {
			return false, fmt.Errorf("the predicate %s", ref)
		}
		return e.cond(p)
	}
	var c struct {
		Type      string            `json:"type"`
		Terms     []json.RawMessage `json:"terms"`
		Term      json.RawMessage   `json:"term"`
		Chances   []float64         `json:"chances"`
		Enchant   string            `json:"enchantment"`
		Chance    json.RawMessage   `json:"chance"`
		Blocks    json.RawMessage   `json:"blocks"`
		State     map[string]string `json:"state"`
		Predicate json.RawMessage   `json:"predicate"`
		OffsetY   int               `json:"offsetY"`
		OffsetX   int               `json:"offsetX"`
		OffsetZ   int               `json:"offsetZ"`
		Entity    string            `json:"entity"`
		// random_chance_with_enchanted_bonus
		Unenchanted float64         `json:"unenchanted_chance"`
		Enchanted   json.RawMessage `json:"enchanted_chance"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return false, err
	}
	switch strings.TrimPrefix(c.Type, "minecraft:") {
	case "survives_explosion": // one time in the radius (none: nothing explodes)
		if r := e.ctx.ExplosionRadius; r > 0 {
			return e.ctx.Rand.Float64() <= 1/r, nil
		}
		return true, nil
	case "any_of":
		for _, t := range c.Terms {
			ok, err := e.cond(t)
			if err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	case "all_of":
		return e.all(nil, c.Terms)
	case "inverted":
		ok, err := e.cond(c.Term)
		return !ok, err
	case "table_bonus":
		level := e.ctx.Tool.Enchantments[c.Enchant]
		return e.ctx.Rand.Float64() < c.Chances[min(level, len(c.Chances)-1)], nil
	case "random_chance":
		p, err := e.float(c.Chance)
		return e.ctx.Rand.Float64() < p, err
	case "random_chance_with_enchanted_bonus": // looting: the chance at the killer's level, or the plain one
		p := c.Unenchanted
		if level := e.attackerLevel(c.Enchant); e.entity && level > 0 {
			var err error
			if p, err = levelValue(c.Enchanted, level); err != nil {
				return false, err
			}
		}
		return e.ctx.Rand.Float64() < p, nil
	case "killed_by_player":
		if !e.entity {
			return false, fmt.Errorf("killed_by_player (no entity killed)")
		}
		return e.ctx.KilledByPlayer, nil
	case "damage_source_properties":
		if !e.entity {
			return false, fmt.Errorf("the damage source (no entity killed)")
		}
		return e.matchDamage(c.Predicate)
	case "match_block":
		return e.matchBlock(c.Blocks, c.State, e.block, e.ctx.Props)
	case "match_tool":
		return e.matchTool(c.Predicate)
	case "entity_properties":
		if !e.entity {
			if string(c.Predicate) == "{}" {
				return true, nil // (no property asked of the player)
			}
			return false, fmt.Errorf("an entity predicate %s", c.Predicate)
		}
		var who *Entity
		switch c.Entity {
		case "this":
			who = e.ctx.This
		case "attacker":
			who = e.ctx.Attacker
		case "direct_attacker":
			who = e.ctx.Direct
			if who == nil {
				who = e.ctx.Attacker
			}
		default:
			return false, fmt.Errorf("the entity %q of an entity predicate", c.Entity)
		}
		return e.matchEntity(c.Predicate, who)
	case "location_check":
		if e.ctx.Block == nil {
			return false, fmt.Errorf("a location check (the blocks round it are not known)")
		}
		var p struct {
			Block struct {
				Blocks json.RawMessage   `json:"blocks"`
				State  map[string]string `json:"state"`
			} `json:"block"`
		}
		if err := json.Unmarshal(c.Predicate, &p); err != nil {
			return false, err
		}
		name, props, ok := e.ctx.Block(c.OffsetX, c.OffsetY, c.OffsetZ)
		if !ok {
			return false, nil
		}
		return e.matchBlock(p.Block.Blocks, p.Block.State, name, props)
	}
	return false, fmt.Errorf("the condition %s", c.Type)
}

// matchBlock: the block is one of the named (a name or a list) and its state has the properties.
func (e *eval) matchBlock(blocks json.RawMessage, state map[string]string, name string, props map[string]string) (bool, error) {
	var one string
	var list []string
	switch {
	case json.Unmarshal(blocks, &one) == nil:
		list = []string{one}
	case json.Unmarshal(blocks, &list) != nil:
		return false, fmt.Errorf("a block set %s", blocks)
	}
	found := false
	for _, b := range list {
		if strings.HasPrefix(b, "#") {
			found = found || tags.BlockHas(strings.TrimPrefix(b, "#"), name)
		} else {
			found = found || b == name
		}
	}
	if !found {
		return false, nil
	}
	for k, v := range state {
		if props[k] != v {
			return false, nil
		}
	}
	return true, nil
}

// matchTool: the tool in hand matches the item predicate.
func (e *eval) matchTool(raw json.RawMessage) (bool, error) {
	return e.matchItem(raw, e.ctx.Tool)
}

// matchItem: the item is named (or in a tag), and it has the enchantments the predicate asks
// (a name, or a tag: "#minecraft:smelts_loot"; none named: any).
func (e *eval) matchItem(raw json.RawMessage, tool Tool) (bool, error) {
	var p struct {
		Items      json.RawMessage `json:"items"`
		Predicates struct {
			Enchantments []struct {
				Enchantments string          `json:"enchantments"`
				Levels       json.RawMessage `json:"levels"`
			} `json:"minecraft:enchantments"`
		} `json:"predicates"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, err
	}
	if len(p.Items) > 0 {
		var one string
		var list []string
		switch {
		case json.Unmarshal(p.Items, &one) == nil:
			list = []string{one}
		case json.Unmarshal(p.Items, &list) != nil:
			return false, fmt.Errorf("a tool item set %s", p.Items)
		}
		found := false
		for _, it := range list {
			if tag, isTag := strings.CutPrefix(it, "#"); isTag {
				found = found || tags.ItemHas(tag, tool.Item)
			} else {
				found = found || it == tool.Item
			}
		}
		if !found {
			return false, nil
		}
	}
	for _, want := range p.Predicates.Enchantments {
		lo, hi := 1, math.MaxInt
		if len(want.Levels) > 0 {
			var exact int
			var r struct {
				Min *int `json:"min"`
				Max *int `json:"max"`
			}
			switch {
			case json.Unmarshal(want.Levels, &exact) == nil:
				lo, hi = exact, exact
			case json.Unmarshal(want.Levels, &r) == nil:
				if r.Min != nil {
					lo = max(*r.Min, 1)
				}
				if r.Max != nil {
					hi = *r.Max
				}
			default:
				return false, fmt.Errorf("enchantment levels %s", want.Levels)
			}
		}
		found := false
		for name, level := range tool.Enchantments {
			if level < lo || level > hi {
				continue
			}
			if tag, isTag := strings.CutPrefix(want.Enchantments, "#"); isTag {
				found = found || tags.EnchantmentHas(tag, name)
			} else {
				found = found || want.Enchantments == "" || want.Enchantments == name
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

// matchEntity: the entity (nil: none — no match) is what the entity predicate asks: its kind
// (a name or a tag), its flags (on fire, a baby), what it rides, what it holds, its components
// and type-specific properties. A property the caller did not tell is an error naming it.
func (e *eval) matchEntity(raw json.RawMessage, who *Entity) (bool, error) {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, err
	}
	if who == nil {
		return false, nil
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	// the kind first (as the game does: a player is asked no frog's variant), then the rest in the
	// same order every time — the same error for the same table
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "minecraft:entity_type") != (keys[j] == "minecraft:entity_type") {
			return keys[i] == "minecraft:entity_type"
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		v := p[k]
		var ok bool
		var err error
		switch {
		case k == "minecraft:entity_type":
			ok, err = matchNames(v, who.Type, tags.EntityTypeHas)
		case k == "minecraft:flags":
			var flags map[string]bool
			if err := json.Unmarshal(v, &flags); err != nil {
				return false, err
			}
			ok = true
			for f, want := range flags {
				switch f {
				case "is_on_fire":
					ok = ok && who.OnFire == want
				case "is_baby":
					ok = ok && who.Baby == want
				default:
					return false, fmt.Errorf("the entity flag %s", f)
				}
			}
		case k == "minecraft:vehicle":
			ok, err = e.matchEntity(v, who.Vehicle)
		case k == "minecraft:equipment":
			var slots map[string]json.RawMessage
			if err := json.Unmarshal(v, &slots); err != nil {
				return false, err
			}
			ok = true
			for slot, item := range slots {
				if slot != "mainhand" {
					return false, fmt.Errorf("the equipment slot %s", slot)
				}
				m, err := e.matchItem(item, who.Mainhand)
				if err != nil {
					return false, err
				}
				ok = ok && m
			}
		case k == "minecraft:components":
			var comps map[string]json.RawMessage
			if err := json.Unmarshal(v, &comps); err != nil {
				return false, err
			}
			ok = true
			for name, want := range comps {
				have, told := who.Components[name]
				if !told {
					return false, fmt.Errorf("the %s's component %s (not told)", who.Type, name)
				}
				m, err := matchValue(want, have)
				if err != nil {
					return false, err
				}
				ok = ok && m
			}
		case strings.HasPrefix(k, "minecraft:type_specific/"):
			kind := strings.TrimPrefix(k, "minecraft:type_specific/")
			var props map[string]json.RawMessage
			if err := json.Unmarshal(v, &props); err != nil {
				return false, err
			}
			ok = true
			for name, want := range props {
				have, told := who.Specific[kind+"/"+name]
				if !told {
					return false, fmt.Errorf("the %s's %s/%s (not told)", who.Type, kind, name)
				}
				m, err := matchValue(want, have)
				if err != nil {
					return false, err
				}
				ok = ok && m
			}
		default:
			return false, fmt.Errorf("the entity predicate %s", k)
		}
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// matchDamage: how the entity died is what the damage source predicate asks: the damage's tags, what
// hit it (direct_entity: the arrow, or the attacker itself) and who caused it (source_entity).
func (e *eval) matchDamage(raw json.RawMessage) (bool, error) {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, err
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := p[k]
		var ok bool
		var err error
		switch k {
		case "tags":
			var list []struct {
				ID       string `json:"id"`
				Expected bool   `json:"expected"`
			}
			if err := json.Unmarshal(v, &list); err != nil {
				return false, err
			}
			ok = true
			for _, t := range list {
				has := false
				for _, d := range e.ctx.DamageTags {
					has = has || d == strings.TrimPrefix(t.ID, "#")
				}
				ok = ok && has == t.Expected
			}
		case "direct_entity":
			direct := e.ctx.Direct
			if direct == nil {
				direct = e.ctx.Attacker
			}
			ok, err = e.matchEntity(v, direct)
		case "source_entity":
			ok, err = e.matchEntity(v, e.ctx.Attacker)
		default:
			return false, fmt.Errorf("the damage source predicate %s", k)
		}
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// matchNames: the name is the one asked, one of a list, or in a tag ("#minecraft:skeletons").
func matchNames(raw json.RawMessage, name string, inTag func(tag, name string) bool) (bool, error) {
	var one string
	var list []string
	switch {
	case json.Unmarshal(raw, &one) == nil:
		list = []string{one}
	case json.Unmarshal(raw, &list) != nil:
		return false, fmt.Errorf("a set of names %s", raw)
	}
	for _, n := range list {
		if tag, isTag := strings.CutPrefix(n, "#"); isTag {
			if inTag(tag, name) {
				return true, nil
			}
		} else if n == name {
			return true, nil
		}
	}
	return false, nil
}

// matchValue: a property as told (a string) is the value asked: a string, a boolean, a number or a
// range {min, max}.
func matchValue(want json.RawMessage, have string) (bool, error) {
	var s string
	if json.Unmarshal(want, &s) == nil {
		return s == have, nil
	}
	var b bool
	if json.Unmarshal(want, &b) == nil {
		return strconv.FormatBool(b) == have, nil
	}
	var r struct {
		Min *float64 `json:"min"`
		Max *float64 `json:"max"`
	}
	var n float64
	exact := json.Unmarshal(want, &n) == nil
	if !exact && json.Unmarshal(want, &r) != nil {
		return false, fmt.Errorf("a property value %s", want)
	}
	h, err := strconv.ParseFloat(have, 64)
	if err != nil {
		return false, fmt.Errorf("the property %q is not a number (asked %s)", have, want)
	}
	if exact {
		return h == n, nil
	}
	return (r.Min == nil || h >= *r.Min) && (r.Max == nil || h <= *r.Max), nil
}
