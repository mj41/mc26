import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.material.FluidState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.VoxelShape;

import java.io.*;
import java.util.*;

/**
 * GenBlockBehaviour — Extracts what a client needs to move among, dig and place blocks, which
 * the block report leaves out because it is code (BlockBehaviour.Properties), not data.
 *
 * Output: block_behaviour.json in the current directory:
 *
 *   shapes — the distinct shapes, each a list of boxes [minX, minY, minZ, maxX, maxY, maxZ] in
 *            block units; shape 0 is empty, shape 1 the full block
 *   blocks — per block id, in registry order:
 *     first_state, states                 the block's state ids (consecutive)
 *     destroy_time, explosion_resistance, friction, speed_factor, jump_factor
 *     bounce_restitution                  how much of a fall it gives back (slime 1, beds 0.66); from
 *                                         26.2, absent before (26.1 bounces in SlimeBlock's own code)
 *     dynamic_shape                       the shape depends on more than the state (scaffolding…)
 *     offset                              the block is moved by its position (hasOffsetFunction:
 *                                         flowers, grass, bamboo…); then also
 *       offset_type                       "xz" or "xyz" (BlockBehaviour.OffsetType)
 *       max_horizontal_offset             the bound of the move sideways (at most 0.25)
 *       max_vertical_offset               "xyz" only: the most it moves down
 *       offset_shape                      the collision and outline shapes move with it (flowers,
 *                                         bamboo; short grass's do not)
 *                                         The move at x, z: seed = Mth.getSeed(x, 0, z);
 *                                         dx = clamp(((float)(seed & 15) / 15f - 0.5) * 0.5, ±max_h),
 *                                         dz the same of seed >> 8, dy (xyz) =
 *                                         ((float)(seed >> 4 & 15) / 15f - 1) * max_v
 *     collision, outline                  per state: an index into shapes (getCollisionShape,
 *                                         getShape), for an empty getter, not moved by an offset
 *     destroy_speed, requires_tool, replaceable, light, solid
 *                                         per state
 *     fluid                               per state: null or {fluid, amount, source, falling, height}
 *     sturdy                              per state: the faces that fully support what is against them
 *                                         (isFaceSturdy), a bit per Direction: down 1, up 2, north 4,
 *                                         south 8, west 16, east 32
 *
 * A per-state value is written once when every state of the block has it, as an array of one
 * value per state otherwise.
 *
 * How: reflection on the bootstrapped registries — the same calls the game makes, with
 * EmptyBlockGetter and BlockPos.ZERO where it would pass a level and a position; a shape that
 * moves with the block's offset is taken back by the origin's move (and checked against the
 * shape at other positions, taken back by theirs).
 */
public class GenBlockBehaviour {
    static final Map<String, Integer> shapeIndex = new HashMap<>();
    static final List<String> shapes = new ArrayList<>();
    /** Block.getBounceRestitution, from 26.2; null before. */
    static final java.lang.reflect.Method BOUNCE = method(Block.class, "getBounceRestitution");

    static java.lang.reflect.Method method(Class<?> c, String name) {
        try {
            return c.getMethod(name);
        } catch (NoSuchMethodException e) {
            return null;
        }
    }

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        shape(net.minecraft.world.phys.shapes.Shapes.empty());
        shape(net.minecraft.world.phys.shapes.Shapes.block());

        StringBuilder blocks = new StringBuilder();
        int nBlocks = 0, nStates = 0;
        for (Block block : BuiltInRegistries.BLOCK) {
            List<BlockState> states = block.getStateDefinition().getPossibleStates();
            int first = Block.getId(states.get(0));
            for (int i = 0; i < states.size(); i++) {
                if (Block.getId(states.get(i)) != first + i) {
                    throw new IllegalStateException(BuiltInRegistries.BLOCK.getKey(block) + ": state ids are not consecutive");
                }
            }
            if (nBlocks > 0) blocks.append(",\n");
            blocks.append("    ").append(jsonStr(BuiltInRegistries.BLOCK.getKey(block).toString())).append(": {");
            blocks.append("\"first_state\": ").append(first);
            blocks.append(", \"states\": ").append(states.size());
            blocks.append(", \"destroy_time\": ").append(num(block.defaultDestroyTime()));
            blocks.append(", \"explosion_resistance\": ").append(num(block.getExplosionResistance()));
            blocks.append(", \"friction\": ").append(num(block.getFriction()));
            blocks.append(", \"speed_factor\": ").append(num(block.getSpeedFactor()));
            blocks.append(", \"jump_factor\": ").append(num(block.getJumpFactor()));
            if (BOUNCE != null) blocks.append(", \"bounce_restitution\": ").append(num((float) BOUNCE.invoke(block)));
            blocks.append(", \"dynamic_shape\": ").append(block.hasDynamicShape());
            blocks.append(", \"offset\": ").append(states.get(0).hasOffsetFunction());
            boolean offsetShape = false;
            if (states.get(0).hasOffsetFunction()) {
                // at the origin the seed is 0: the move is (-max_h, -max_v, -max_h)
                BlockState s0 = states.get(0);
                Vec3 o = s0.getOffset(BlockPos.ZERO);
                boolean xyz = o.y != 0;
                blocks.append(", \"offset_type\": \"").append(xyz ? "xyz" : "xz").append("\"");
                blocks.append(", \"max_horizontal_offset\": ").append(num((float) -o.x));
                if (xyz) blocks.append(", \"max_vertical_offset\": ").append(num((float) -o.y));
                // the move as documented above is the game's
                for (BlockPos p : PROBES) {
                    Vec3 want = s0.getOffset(p), got = offsetAt(p, (float) -o.x, xyz ? (float) -o.y : 0);
                    if (!want.equals(got)) {
                        throw new IllegalStateException(BuiltInRegistries.BLOCK.getKey(block) + ": offset at " + p + " is " + want + ", the formula gives " + got);
                    }
                }
                for (BlockState s : states) {
                    offsetShape |= follows(s, p -> s.getCollisionShape(EmptyBlockGetter.INSTANCE, p))
                        || follows(s, p -> s.getShape(EmptyBlockGetter.INSTANCE, p));
                }
                blocks.append(", \"offset_shape\": ").append(offsetShape);
            }

            List<String> collision = new ArrayList<>(), outline = new ArrayList<>(), destroySpeed = new ArrayList<>(),
                requiresTool = new ArrayList<>(), replaceable = new ArrayList<>(), light = new ArrayList<>(),
                solid = new ArrayList<>(), fluid = new ArrayList<>(), sturdy = new ArrayList<>(), mapColor = new ArrayList<>();
            boolean anyFluid = false;
            for (BlockState s : states) {
                collision.add(Integer.toString(indexOf(unmoved(s, p -> s.getCollisionShape(EmptyBlockGetter.INSTANCE, p)))));
                outline.add(Integer.toString(indexOf(unmoved(s, p -> s.getShape(EmptyBlockGetter.INSTANCE, p)))));
                destroySpeed.add(num(s.getDestroySpeed(EmptyBlockGetter.INSTANCE, BlockPos.ZERO)));
                requiresTool.add(Boolean.toString(s.requiresCorrectToolForDrops()));
                replaceable.add(Boolean.toString(s.canBeReplaced()));
                light.add(Integer.toString(s.getLightEmission()));
                solid.add(Boolean.toString(s.isSolid()));
                // the colour a map draws it in (MapColor.col, RGB; 0 for none)
                mapColor.add(Integer.toString(s.getMapColor(EmptyBlockGetter.INSTANCE, BlockPos.ZERO).col));
                int faces = 0;
                for (net.minecraft.core.Direction d : net.minecraft.core.Direction.values()) {
                    if (s.isFaceSturdy(EmptyBlockGetter.INSTANCE, BlockPos.ZERO, d)) faces |= 1 << d.ordinal();
                }
                sturdy.add(Integer.toString(faces));
                FluidState f = s.getFluidState();
                if (f.isEmpty()) {
                    fluid.add("null");
                } else {
                    anyFluid = true;
                    fluid.add("{\"fluid\": " + jsonStr(BuiltInRegistries.FLUID.getKey(f.getType()).toString())
                        + ", \"amount\": " + f.getAmount() + ", \"source\": " + f.isSource()
                        + ", \"falling\": " + (f.hasProperty(net.minecraft.world.level.material.FlowingFluid.FALLING) && f.getValue(net.minecraft.world.level.material.FlowingFluid.FALLING))
                        + ", \"height\": " + num(f.getOwnHeight()) + "}");
                }
            }
            blocks.append(", \"collision\": ").append(perState(collision));
            blocks.append(", \"outline\": ").append(perState(outline));
            blocks.append(", \"destroy_speed\": ").append(perState(destroySpeed));
            blocks.append(", \"requires_tool\": ").append(perState(requiresTool));
            blocks.append(", \"replaceable\": ").append(perState(replaceable));
            blocks.append(", \"light\": ").append(perState(light));
            blocks.append(", \"solid\": ").append(perState(solid));
            blocks.append(", \"sturdy\": ").append(perState(sturdy));
            blocks.append(", \"map_color\": ").append(perState(mapColor));
            if (anyFluid) blocks.append(", \"fluid\": ").append(perState(fluid));
            blocks.append("}");
            nBlocks++;
            nStates += states.size();
        }

        try (PrintWriter pw = new PrintWriter(new FileWriter("block_behaviour.json"))) {
            pw.println("{");
            pw.println("  \"shapes\": [");
            for (int i = 0; i < shapes.size(); i++) {
                pw.print("    " + shapes.get(i));
                pw.println(i < shapes.size() - 1 ? "," : "");
            }
            pw.println("  ],");
            pw.println("  \"blocks\": {");
            pw.println(blocks);
            pw.println("  }");
            pw.println("}");
        }
        System.out.printf("GenBlockBehaviour: wrote block_behaviour.json (%d blocks, %d states, %d distinct shapes)%n",
            nBlocks, nStates, shapes.size());
    }

    /** Positions whose moves differ from the origin's and from each other's, to tell a shape that
     * follows the move from one that does not, and to check the formula. */
    static final BlockPos[] PROBES = {new BlockPos(1, 0, 0), new BlockPos(0, 0, 1), new BlockPos(3, 0, 7),
        new BlockPos(-5, 0, 2), new BlockPos(17, 0, -9), new BlockPos(-123, 0, -456), new BlockPos(1000, 0, 31),
        new BlockPos(29999, 0, -29999)};

    /** offsetAt is the move at p as the header documents it (BlockBehaviour.Properties.offsetType). */
    static Vec3 offsetAt(BlockPos p, float maxH, float maxV) {
        long seed = net.minecraft.util.Mth.getSeed(p.getX(), 0, p.getZ());
        double x = net.minecraft.util.Mth.clamp(((double) ((float) (seed & 15L) / 15.0F) - 0.5) * 0.5, -maxH, maxH);
        double z = net.minecraft.util.Mth.clamp(((double) ((float) (seed >> 8 & 15L) / 15.0F) - 0.5) * 0.5, -maxH, maxH);
        double y = maxV == 0 ? 0 : ((double) ((float) (seed >> 4 & 15L) / 15.0F) - 1.0) * (double) maxV;
        return new Vec3(x, y, z);
    }

    /** follows reports whether the shape f gives moves with the state's offset: it differs at a
     * position whose move differs from the origin's. */
    static boolean follows(BlockState s, java.util.function.Function<BlockPos, VoxelShape> f) {
        if (!s.hasOffsetFunction()) return false;
        String at0 = key(f.apply(BlockPos.ZERO));
        for (BlockPos p : PROBES) {
            if (!key(f.apply(p)).equals(at0)) return true;
        }
        return false;
    }

    /** unmoved is the shape f gives with no offset, as the table writes it: at the origin, less
     * the origin's move, when the shape follows it; checked to be the same from every probe
     * position, less its move. */
    static String unmoved(BlockState s, java.util.function.Function<BlockPos, VoxelShape> f) {
        VoxelShape at0 = f.apply(BlockPos.ZERO);
        if (!follows(s, f)) return key(at0);
        String want = key(at0, s.getOffset(BlockPos.ZERO));
        for (BlockPos p : PROBES) {
            String got = key(f.apply(p), s.getOffset(p));
            if (!got.equals(want)) {
                throw new IllegalStateException(s + ": the shape at " + p + " moved back is " + got + ", at the origin " + want);
            }
        }
        return want;
    }

    /** snap takes the last bits of rounding off a coordinate moved back: a shape's coordinates
     * are sixteenths and their halves, a fine binary grid. */
    static double snap(double v) {
        double r = Math.rint(v * 1048576.0) / 1048576.0;
        return Math.abs(r - v) < 1e-9 ? r : v;
    }

    static int shape(VoxelShape s) {
        return indexOf(key(s));
    }

    /** indexOf returns the index of a shape in the table, adding it when new. */
    static int indexOf(String key) {
        return shapeIndex.computeIfAbsent(key, k -> {
            shapes.add(k);
            return shapes.size() - 1;
        });
    }

    /** key is a shape as the table writes it: its boxes. */
    static String key(VoxelShape s) {
        return key(s, Vec3.ZERO);
    }

    /** key is a shape as the table writes it, every box moved back by o. */
    static String key(VoxelShape s, Vec3 o) {
        StringBuilder sb = new StringBuilder("[");
        List<AABB> boxes = s.toAabbs();
        for (int i = 0; i < boxes.size(); i++) {
            AABB b = boxes.get(i);
            double minX = b.minX, minY = b.minY, minZ = b.minZ, maxX = b.maxX, maxY = b.maxY, maxZ = b.maxZ;
            if (!o.equals(Vec3.ZERO)) {
                minX = snap(minX - o.x); minY = snap(minY - o.y); minZ = snap(minZ - o.z);
                maxX = snap(maxX - o.x); maxY = snap(maxY - o.y); maxZ = snap(maxZ - o.z);
            }
            if (i > 0) sb.append(", ");
            sb.append("[").append(num(minX)).append(", ").append(num(minY)).append(", ").append(num(minZ))
                .append(", ").append(num(maxX)).append(", ").append(num(maxY)).append(", ").append(num(maxZ)).append("]");
        }
        return sb.append("]").toString();
    }

    /** perState writes one value when all are equal, the array of them otherwise. */
    static String perState(List<String> values) {
        if (new HashSet<>(values).size() == 1) return values.get(0);
        return "[" + String.join(", ", values) + "]";
    }

    /** num writes a number as JSON: shortest round-trip form, no NaN or infinities. */
    static String num(double v) {
        if (Double.isNaN(v) || Double.isInfinite(v)) throw new IllegalArgumentException("not a JSON number: " + v);
        if (v == Math.rint(v) && Math.abs(v) < 1e15) return Long.toString((long) v);
        return Double.toString(v);
    }

    static String num(float v) {
        // the float's own shortest form (0.6, not 0.6000000238418579)
        if (Float.isNaN(v) || Float.isInfinite(v)) throw new IllegalArgumentException("not a JSON number: " + v);
        if (v == Math.rint(v) && Math.abs(v) < 1e7) return Long.toString((long) v);
        return Float.toString(v);
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
