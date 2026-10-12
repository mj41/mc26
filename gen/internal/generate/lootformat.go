package generate

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The loot tables of 26.1 and 26.2 name what a condition or a function is in a key of its own — {"condition":
// "minecraft:match_tool", …}, {"function": "minecraft:set_count", …} — where 26.3's say "type", and the block's
// state a condition asks for is a block_state_property where 26.3's is a match_block. The evaluator (data/loot) plays
// the 26.3 shape; these tables are written in it, nothing else of them changed (the rest of the two shapes is the
// same: pools, entries, number providers, enchantment predicates, the tables' meaning).

// normalizeOldLoot rewrites one old-shape table into the evaluator's shape.
func normalizeOldLoot(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // (a number as it was written)
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(rewriteOldLoot(v))
}

func rewriteOldLoot(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			v[k] = rewriteOldLoot(x)
		}
		if f, ok := v["function"].(string); ok {
			delete(v, "function")
			v["type"] = f
		}
		if c, ok := v["condition"].(string); ok {
			delete(v, "condition")
			v["type"] = c
		}
		if v["type"] == "minecraft:entity_properties" {
			if p, ok := v["predicate"].(map[string]any); ok {
				v["predicate"] = entityPredicate(p)
			}
		}
		if v["type"] == "minecraft:damage_source_properties" {
			if p, ok := v["predicate"].(map[string]any); ok {
				for _, k := range []string{"direct_entity", "source_entity"} { // the entities of a damage source: entity predicates
					if e, ok := p[k].(map[string]any); ok {
						p[k] = entityPredicate(e)
					}
				}
			}
		}
		if v["type"] == "minecraft:tag" {
			if n, ok := v["name"].(string); ok {
				delete(v, "name")
				v["items"] = "#" + n
			}
		}
		if v["type"] == "minecraft:block_state_property" {
			v["type"] = "minecraft:match_block"
			if b, ok := v["block"]; ok {
				delete(v, "block")
				v["blocks"] = b
			}
			if p, ok := v["properties"]; ok {
				delete(v, "properties")
				v["state"] = p
			}
		}
		return v
	case []any:
		for i, x := range v {
			v[i] = rewriteOldLoot(x)
		}
	}
	return v
}

// entityPredicate names an entity predicate's parts as 26.2 and 26.3 do (a key already so named is left): the
// entity's type, its flags, equipment, components, the vehicle it rides (a predicate itself), and type_specific
// — {"type": "minecraft:sheep", …} — as minecraft:type_specific/sheep.
func entityPredicate(p map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		switch k {
		case "type":
			out["minecraft:entity_type"] = v
		case "flags", "equipment", "components":
			out["minecraft:"+k] = v
		case "vehicle":
			if inner, ok := v.(map[string]any); ok {
				v = entityPredicate(inner)
			}
			out["minecraft:vehicle"] = v
		case "type_specific":
			if m, ok := v.(map[string]any); ok {
				if t, ok := m["type"].(string); ok {
					rest := map[string]any{}
					for rk, rv := range m {
						if rk != "type" {
							rest[rk] = rv
						}
					}
					name := strings.TrimPrefix(t, "minecraft:")
					if name == "slime" || name == "magma_cube" {
						name = "cube_mob" // (26.2 tells the size of both by the one case)
					}
					out["minecraft:type_specific/"+name] = rest
					continue
				}
			}
			out[k] = v
		default:
			out[k] = v
		}
	}
	return out
}
