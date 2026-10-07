package block

// What a client needs to move among, dig and place blocks, which the block
// states do not say: the shapes an entity collides with and a cursor hits, the
// destroy time, friction and the like. The tables are generated from
// block_behaviour.json (behaviour_gen.go); this file looks them up.

// AABB is a box in block units, relative to the block's corner.
type AABB struct{ MinX, MinY, MinZ, MaxX, MaxY, MaxZ float64 }

// Behaviour is what a block's properties say about it, the same for all its
// states (BlockBehaviour.Properties).
type Behaviour struct {
	DestroyTime         float32 // seconds × 1.5 by hand for a block that needs no tool; -1 unbreakable
	ExplosionResistance float32
	Friction            float32 // 0.6 for most; ice 0.98
	SpeedFactor         float32 // soul sand 0.4
	JumpFactor          float32 // honey 0.5
	// BounceRestitution is how much of a fall the block gives back (slime 1,
	// beds 0.75), from 26.2; 0 in 26.1, where the slime block bounces by its
	// own code.
	BounceRestitution float32
	// DynamicShape: the collision shape depends on more than the state
	// (scaffolding, powder snow); the tables hold the shape for no entity.
	DynamicShape bool
	// Offset: the block is moved by its position (BlockState.hasOffsetFunction:
	// flowers, grass, bamboo).
	Offset bool
	// OffsetType says how: by up to MaxHorizontalOffset sideways, and for
	// OffsetXYZ by up to MaxVerticalOffset down (Offset gives the move at a
	// position). OffsetShape: the collision and outline shapes move with it
	// (flowers, bamboo, pointed dripstone; short grass's do not) — the tables
	// hold them unmoved, ShapeOffset says how far they move. All zero in data
	// extracted before them, whose tables hold the shapes as moved at x 0, z 0.
	OffsetType          OffsetType
	MaxHorizontalOffset float32
	MaxVerticalOffset   float32
	OffsetShape         bool
}

// OffsetType is how a block's position moves it (BlockBehaviour.OffsetType).
type OffsetType uint8

const (
	OffsetNone OffsetType = iota // not moved
	OffsetXZ                     // sideways: flowers, bamboo
	OffsetXYZ                    // sideways and down: short grass, ferns
)

// Offset returns how far the block of state s at x, z (any y) is moved
// (BlockState.getOffset); zero for a block that is not.
func Offset(s StateID, x, z int) (dx, dy, dz float64) {
	b := BehaviourOf(s)
	if b.OffsetType == OffsetNone {
		return 0, 0, 0
	}
	// Mth.getSeed(x, 0, z)
	seed := int64(int32(x)*3129871) ^ int64(int32(z))*116129781
	seed = seed*seed*42317861 + seed*11
	seed >>= 16
	maxH := float64(b.MaxHorizontalOffset)
	dx = min(max((float64(float32(seed&15)/15)-0.5)*0.5, -maxH), maxH)
	dz = min(max((float64(float32(seed>>8&15)/15)-0.5)*0.5, -maxH), maxH)
	if b.OffsetType == OffsetXYZ {
		dy = (float64(float32(seed>>4&15)/15) - 1) * float64(b.MaxVerticalOffset)
	}
	return dx, dy, dz
}

// ShapeOffset is Offset for a block whose shapes move with it (OffsetShape),
// zero for the rest: what to add to the boxes of CollisionShape and
// OutlineShape of the block at x, z.
func ShapeOffset(s StateID, x, z int) (dx, dy, dz float64) {
	if !BehaviourOf(s).OffsetShape {
		return 0, 0, 0
	}
	return Offset(s, x, z)
}

// Fluid is a block state's fluid (FluidState).
type Fluid struct {
	Name   string // minecraft:water, minecraft:flowing_lava, …
	Amount int    // 1–8; 8 is a full block
	Source bool   // a source block, not a flowing one
	// Falling: flowing down, from above (FlowingFluid.FALLING)
	Falling bool
	Height  float32 // the fluid's own height in the block, 0–1
}

// perState holds a value of every state of one block: one, when they agree.
type perState[T any] struct {
	one  T
	each []T
}

func (p *perState[T]) at(i int) T {
	if p.each == nil {
		return p.one
	}
	return p.each[i]
}

type blockBehaviour struct {
	id          string
	first, n    int
	Behaviour   Behaviour
	collision   perState[uint16]
	outline     perState[uint16]
	destroy     perState[float32]
	requireTool perState[bool]
	replaceable perState[bool]
	light       perState[uint8]
	solid       perState[bool]
	sturdy      perState[uint8]
	mapColor    perState[uint32]
	fluid       perState[*Fluid]
}

// stateBlock maps a state id to its block's index in behaviours.
var stateBlock []uint16

func init() {
	n := 0
	for i := range behaviours {
		n += behaviours[i].n
	}
	stateBlock = make([]uint16, n)
	for i := range behaviours {
		b := &behaviours[i]
		for s := b.first; s < b.first+b.n; s++ {
			stateBlock[s] = uint16(i)
		}
	}
}

func lookup(s StateID) (*blockBehaviour, int) {
	if s < 0 || int(s) >= len(stateBlock) {
		s = 0 // air
	}
	b := &behaviours[stateBlock[s]]
	return b, int(s) - b.first
}

// BehaviourOf returns what the block of state s is like.
func BehaviourOf(s StateID) Behaviour {
	b, _ := lookup(s)
	return b.Behaviour
}

// CollisionShape returns the boxes an entity collides with in state s, at the
// block's corner (BlockState.getCollisionShape); none for air, water, grass.
func CollisionShape(s StateID) []AABB {
	b, i := lookup(s)
	return shapes[b.collision.at(i)]
}

// OutlineShape returns the boxes a cursor hits in state s (BlockState.getShape):
// what a client aims at to dig or place against.
func OutlineShape(s StateID) []AABB {
	b, i := lookup(s)
	return shapes[b.outline.at(i)]
}

// DestroySpeed is the state's hardness (BlockState.getDestroySpeed); -1 cannot
// be broken.
func DestroySpeed(s StateID) float32 {
	b, i := lookup(s)
	return b.destroy.at(i)
}

// RequiresCorrectTool reports whether the state drops anything only when mined
// with the right tool (BlockState.requiresCorrectToolForDrops).
func RequiresCorrectTool(s StateID) bool {
	b, i := lookup(s)
	return b.requireTool.at(i)
}

// Replaceable reports whether placing a block into this state's space replaces
// it (BlockState.canBeReplaced: air, water, short grass).
func Replaceable(s StateID) bool {
	b, i := lookup(s)
	return b.replaceable.at(i)
}

// LightEmission is the light level the state gives off.
func LightEmission(s StateID) int {
	b, i := lookup(s)
	return int(b.light.at(i))
}

// Solid is BlockState.isSolid, the legacy "solid" flag some rules still use.
func Solid(s StateID) bool {
	b, i := lookup(s)
	return b.solid.at(i)
}

// SturdyFaces says which faces of the state fully support what is against
// them (BlockState.isFaceSturdy): a bit per Direction, down 1, up 2, north 4,
// south 8, west 16, east 32.
func SturdyFaces(s StateID) uint8 {
	b, i := lookup(s)
	return b.sturdy.at(i)
}

// MapColor returns the colour a map draws the state in (MapColor.col: RGB,
// 0 for none — air, glass), as the game's maps and a viewer of the robot's
// world draw it.
func MapColor(s StateID) uint32 {
	b, i := lookup(s)
	return b.mapColor.at(i)
}

// FluidOf returns the state's fluid, nil for none.
func FluidOf(s StateID) *Fluid {
	b, i := lookup(s)
	return b.fluid.at(i)
}
