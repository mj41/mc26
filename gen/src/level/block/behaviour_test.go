package block

import "testing"

func TestBehaviour(t *testing.T) {
	full := []AABB{{0, 0, 0, 1, 1, 1}}
	same := func(a, b []AABB) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	stone := ToStateID[Stone{}]
	if got := CollisionShape(stone); !same(got, full) {
		t.Errorf("stone collides as %v, want the full block", got)
	}
	if got := CollisionShape(ToStateID[Air{}]); len(got) != 0 {
		t.Errorf("air collides as %v", got)
	}
	slab := ToStateID[OakSlab{Type: SlabTypeBottom}]
	if got := CollisionShape(slab); !same(got, []AABB{{0, 0, 0, 1, 0.5, 1}}) {
		t.Errorf("a bottom slab collides as %v", got)
	}
	if b := BehaviourOf(stone); b.DestroyTime != 1.5 || b.Friction != 0.6 {
		t.Errorf("stone: %+v", b)
	}
	if !RequiresCorrectTool(stone) || RequiresCorrectTool(ToStateID[Dirt{}]) {
		t.Errorf("stone needs a pickaxe for its drop, dirt nothing")
	}
	if BehaviourOf(ToStateID[Ice{}]).Friction <= 0.9 {
		t.Errorf("ice is not slippery")
	}
	if DestroySpeed(ToStateID[Bedrock{}]) != -1 {
		t.Errorf("bedrock breaks")
	}
	if f := FluidOf(ToStateID[Water{Level: 0}]); f == nil || !f.Source || f.Amount != 8 {
		t.Errorf("a water source: %+v", f)
	}
	if !Replaceable(ToStateID[Water{Level: 0}]) || Replaceable(stone) {
		t.Errorf("water is replaced by a placed block, stone is not")
	}
	if len(stateBlock) != len(StateList) {
		t.Errorf("%d states in the behaviour table, %d in the state list", len(stateBlock), len(StateList))
	}
}

// A block moved by its position: its shapes in the tables unmoved, the move
// as the game computes it (Mth.getSeed and BlockBehaviour.Properties.offsetType,
// the values printed by the game's own Mth).
func TestOffset(t *testing.T) {
	poppy, grass := ToStateID[Poppy{}], ToStateID[ShortGrass{}]
	if BehaviourOf(poppy).OffsetType == OffsetNone {
		t.Skip("data extracted before the offset type")
	}
	if got := OutlineShape(poppy); len(got) != 1 || got[0] != (AABB{0.3125, 0, 0.3125, 0.6875, 0.625, 0.6875}) {
		t.Errorf("a poppy's outline %v, want it centred", got)
	}
	if b := BehaviourOf(poppy); b.OffsetType != OffsetXZ || b.MaxHorizontalOffset != 0.25 || !b.OffsetShape {
		t.Errorf("poppy: %+v", b)
	}
	if b := BehaviourOf(grass); b.OffsetType != OffsetXYZ || b.MaxVerticalOffset != 0.2 || b.OffsetShape {
		t.Errorf("short grass: %+v", b)
	}
	for _, c := range []struct {
		x, z       int
		dx, dy, dz float64 // short grass's (xyz; a poppy's dx, dz are the same, its dy 0)
	}{
		{0, 0, -0.25, -0.20000000298023224, -0.25},
		{1, 0, 0.11666667461395264, -0.1333333333333333, -0.01666666567325592},
		{0, 1, -0.25, 0.0, 0.0833333432674408},
		{3, 7, 0.11666667461395264, -0.07999999642372124, 0.050000011920928955},
		{-5, 2, 0.21666666865348816, -0.06666666368643437, 0.016666680574417114},
		{-123, -456, -0.21666666492819786, -0.053333330949147495, 0.18333333730697632},
		{29999, -29999, -0.25, -0.12000000059604643, 0.016666680574417114},
		{100000, 7, -0.25, -0.16000000178813933, -0.11666665971279144},
	} {
		if dx, dy, dz := Offset(grass, c.x, c.z); dx != c.dx || dy != c.dy || dz != c.dz {
			t.Errorf("short grass at %d %d moved %v %v %v, want %v %v %v", c.x, c.z, dx, dy, dz, c.dx, c.dy, c.dz)
		}
		if dx, dy, dz := ShapeOffset(poppy, c.x, c.z); dx != c.dx || dy != 0 || dz != c.dz {
			t.Errorf("a poppy's shape at %d %d moved %v %v %v", c.x, c.z, dx, dy, dz)
		}
		if dx, dy, dz := ShapeOffset(grass, c.x, c.z); dx != 0 || dy != 0 || dz != 0 {
			t.Errorf("short grass's shape moved at %d %d", c.x, c.z)
		}
	}
	if dx, dy, dz := Offset(ToStateID[Stone{}], 1, 1); dx != 0 || dy != 0 || dz != 0 {
		t.Errorf("stone moved")
	}
}
