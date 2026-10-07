// gen_blockbehaviour generates level/block/behaviour_gen.go from
// block_behaviour.json (GenBlockBehaviour: the collision and outline shapes of
// every state, and per block the destroy time, friction, speed and jump
// factors, …): the shape table and one entry per block, a per-state value
// written once where every state of the block agrees. The lookups are the
// hand-written level/block/behaviour.go.
package generate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type blockBehaviourJSON struct {
	Shapes [][][6]float64 `json:"shapes"`
	Blocks orderedBlocks  `json:"blocks"`
}

type blockBehaviourEntry struct {
	ID                  string
	FirstState          int             `json:"first_state"`
	States              int             `json:"states"`
	DestroyTime         float64         `json:"destroy_time"`
	ExplosionResistance float64         `json:"explosion_resistance"`
	Friction            float64         `json:"friction"`
	SpeedFactor         float64         `json:"speed_factor"`
	JumpFactor          float64         `json:"jump_factor"`
	BounceRestitution   float64         `json:"bounce_restitution"` // from 26.2; 0 when absent
	DynamicShape        bool            `json:"dynamic_shape"`
	Offset              bool            `json:"offset"`
	OffsetType          string          `json:"offset_type"` // "xz", "xyz"; absent in data extracted before it
	MaxHorizontalOffset float64         `json:"max_horizontal_offset"`
	MaxVerticalOffset   float64         `json:"max_vertical_offset"`
	OffsetShape         bool            `json:"offset_shape"`
	Collision           json.RawMessage `json:"collision"`
	Outline             json.RawMessage `json:"outline"`
	DestroySpeed        json.RawMessage `json:"destroy_speed"`
	RequiresTool        json.RawMessage `json:"requires_tool"`
	Replaceable         json.RawMessage `json:"replaceable"`
	Light               json.RawMessage `json:"light"`
	Solid               json.RawMessage `json:"solid"`
	Sturdy              json.RawMessage `json:"sturdy"`
	MapColor            json.RawMessage `json:"map_color"` // RGB, 0 for none
	Fluid               json.RawMessage `json:"fluid"`
}

// orderedBlocks keeps the blocks in the file's (registry) order.
type orderedBlocks []blockBehaviourEntry

func (o *orderedBlocks) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if _, err := dec.Token(); err != nil { // {
		return err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		var e blockBehaviourEntry
		if err := dec.Decode(&e); err != nil {
			return err
		}
		e.ID = t.(string)
		*o = append(*o, e)
	}
	_, err := dec.Token() // }
	return err
}

type fluidJSON struct {
	Fluid   string  `json:"fluid"`
	Amount  int     `json:"amount"`
	Source  bool    `json:"source"`
	Falling bool    `json:"falling"`
	Height  float64 `json:"height"`
}

func genBlockBehaviour(jsonDir, outRoot string) error {
	var data blockBehaviourJSON
	if err := readJSON(filepath.Join(jsonDir, "block_behaviour.json"), &data); err != nil {
		return fmt.Errorf("genBlockBehaviour: %w (re-run extraction; the data must include GenBlockBehaviour's output)", err)
	}
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_blockbehaviour.go", "block_behaviour.json"))
	sb.WriteString("\npackage block\n\n")

	sb.WriteString("// shapes holds every distinct shape; 0 is empty, 1 the full block.\nvar shapes = [][]AABB{\n")
	for _, shape := range data.Shapes {
		sb.WriteString("\t{")
		for i, b := range shape {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "{%s, %s, %s, %s, %s, %s}", flt(b[0]), flt(b[1]), flt(b[2]), flt(b[3]), flt(b[4]), flt(b[5]))
		}
		sb.WriteString("},\n")
	}
	sb.WriteString("}\n\n")

	sb.WriteString("var behaviours = [...]blockBehaviour{\n")
	next := 0
	for _, b := range data.Blocks {
		if b.FirstState != next {
			return fmt.Errorf("genBlockBehaviour: %s starts at state %d, want %d", b.ID, b.FirstState, next)
		}
		next += b.States
		fmt.Fprintf(&sb, "\t{id: %q, first: %d, n: %d,\n", b.ID, b.FirstState, b.States)
		offset := ""
		switch b.OffsetType {
		case "":
		case "xz", "xyz":
			offset = fmt.Sprintf(", OffsetType: Offset%s, MaxHorizontalOffset: %s, MaxVerticalOffset: %s, OffsetShape: %v",
				strings.ToUpper(b.OffsetType), flt(b.MaxHorizontalOffset), flt(b.MaxVerticalOffset), b.OffsetShape)
		default:
			return fmt.Errorf("genBlockBehaviour: %s: offset_type %q", b.ID, b.OffsetType)
		}
		fmt.Fprintf(&sb, "\t\tBehaviour: Behaviour{DestroyTime: %s, ExplosionResistance: %s, Friction: %s, SpeedFactor: %s, JumpFactor: %s, BounceRestitution: %s, DynamicShape: %v, Offset: %v%s},\n",
			flt(b.DestroyTime), flt(b.ExplosionResistance), flt(b.Friction), flt(b.SpeedFactor), flt(b.JumpFactor), flt(b.BounceRestitution), b.DynamicShape, b.Offset, offset)
		fields := []struct {
			name, typ string
			raw       json.RawMessage
		}{
			{"collision", "uint16", b.Collision}, {"outline", "uint16", b.Outline}, {"destroy", "float32", b.DestroySpeed},
			{"requireTool", "bool", b.RequiresTool}, {"replaceable", "bool", b.Replaceable}, {"light", "uint8", b.Light},
			{"solid", "bool", b.Solid},
			{"sturdy", "uint8", b.Sturdy},
			{"mapColor", "uint32", b.MapColor},
		}
		for _, f := range fields {
			if len(f.raw) == 0 {
				f.raw = json.RawMessage("0") // data extracted before the field (map_color)
			}
			lit, err := perStateLit(f.typ, f.raw, b.States, func(v json.RawMessage) (string, error) { return scalarLit(f.typ, v) })
			if err != nil {
				return fmt.Errorf("genBlockBehaviour: %s %s: %w", b.ID, f.name, err)
			}
			fmt.Fprintf(&sb, "\t\t%s: %s,\n", f.name, lit)
		}
		if len(b.Fluid) > 0 {
			lit, err := perStateLit("*Fluid", b.Fluid, b.States, fluidLit)
			if err != nil {
				return fmt.Errorf("genBlockBehaviour: %s fluid: %w", b.ID, err)
			}
			fmt.Fprintf(&sb, "\t\tfluid: %s,\n", lit)
		}
		sb.WriteString("\t},\n")
	}
	sb.WriteString("}\n")
	if err := writeGo(filepath.Join(outRoot, "level", "block", "behaviour_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genBlockBehaviour: %w", err)
	}
	logf("genBlockBehaviour: %d blocks, %d states, %d shapes", len(data.Blocks), next, len(data.Shapes))
	return nil
}

// perStateLit writes perState[typ]{one: v} for a single value, or
// perState[typ]{each: []typ{…}} for an array of one value per state.
func perStateLit(typ string, raw json.RawMessage, states int, lit func(json.RawMessage) (string, error)) (string, error) {
	var each []json.RawMessage
	if err := json.Unmarshal(raw, &each); err != nil || strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		v, err := lit(raw)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("perState[%s]{one: %s}", typ, v), nil
	}
	if len(each) != states {
		return "", fmt.Errorf("%d values for %d states", len(each), states)
	}
	vals := make([]string, len(each))
	for i, e := range each {
		v, err := lit(e)
		if err != nil {
			return "", err
		}
		vals[i] = v
	}
	return fmt.Sprintf("perState[%s]{each: []%s{%s}}", typ, typ, strings.Join(vals, ", ")), nil
}

func scalarLit(typ string, raw json.RawMessage) (string, error) {
	s := strings.TrimSpace(string(raw))
	switch typ {
	case "bool":
		if s != "true" && s != "false" {
			return "", fmt.Errorf("not a bool: %s", s)
		}
		return s, nil
	case "uint8", "uint16":
		if _, err := strconv.ParseUint(s, 10, 16); err != nil {
			return "", err
		}
		return s, nil
	case "uint32":
		if _, err := strconv.ParseUint(s, 10, 32); err != nil {
			return "", err
		}
		return s, nil
	default: // float32
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return "", err
		}
		return flt(f), nil
	}
}

func fluidLit(raw json.RawMessage) (string, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return "nil", nil
	}
	var f fluidJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", err
	}
	return fmt.Sprintf("&Fluid{Name: %q, Amount: %d, Source: %v, Falling: %v, Height: %s}", f.Fluid, f.Amount, f.Source, f.Falling, flt(f.Height)), nil
}

// flt writes a number in its shortest form that reads back the same.
func flt(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
