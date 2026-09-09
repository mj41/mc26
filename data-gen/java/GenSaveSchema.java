/**
 * GenSaveSchema — Extracts the NBT shape of the save formats: a saved chunk, and whatever
 * else Minecraft reads with keyed accessors rather than with a codec.
 *
 * Everything else in this pipeline is described by a codec: a DataFixerUpper chain names its
 * keys, and GenNbtSchema interprets the chain. The save formats have none. Mojang reads them
 * key by key — `tag.getIntOr("xPos", 0)`, `tag.getListOrEmpty("sections")`, `input.getString(…)`
 * — so the description has to come from the reader itself. That is what this extractor does:
 * it walks the reading method and records every key it takes off the tag, with the tag type the
 * accessor implies, whether the key may be absent and the default used when it is.
 *
 * Output: save_schema.json in the current directory, the same shape as nbt_schema.json's
 * entries:
 *   { "version": 1,
 *     "formats": {
 *       "chunk": { "class": "net.minecraft.world.level.chunk.storage.SerializableChunkData",
 *                  "methods": "parse, write", "coverage": "full" | "partial",
 *                  "type": {"k": "struct", "name": "SerializableChunkData", "fields": [
 *                      {"name": "xPos", "key": "xPos", "optional": true, "default": "0",
 *                       "type": {"k": "prim", "t": "INT"}}, … ]} },
 *       … } }
 *
 * How: the method is interpreted with an operand stack whose values carry where they came from
 * — the tag the method was handed, a compound read at one of its keys, the elements of a list
 * read at one of its keys — so an accessor called on one of them adds its key to that struct
 * rather than to the outer one, and a loop over a list describes the list's element. The
 * accessors are CompoundTag's and ValueInput's, which name their tag types (getIntOr, getString,
 * getByteArray, getCompound, getList); `read(key, CODEC)` hands the key's shape to GenNbtSchema's
 * codec walker, so a part of a save format that does have a codec is described in full.
 *
 * A value read through something this walker cannot follow (a lambda mapped over an Optional, a
 * codec that comes from a method rather than a static field) is an `nbt` node: an arbitrary tag,
 * which is what a binding would keep raw anyway. Shares the class-file access and JSON helpers
 * of GenPacketSchema and the codec walker of GenNbtSchema (compiled together by ExtractAll).
 */
import java.lang.classfile.*;
import java.lang.classfile.instruction.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public class GenSaveSchema {

    /**
     * The formats to describe: the name in the output, the class, then the methods to walk —
     * the reader first, then the writer. A key the reader does not read is still part of the
     * format when the writer writes it (a saved chunk's `yPos`: 26.3 takes the lowest section
     * from the level's height rather than from the tag, but every chunk on disk has the key),
     * so both sides are walked into one description.
     */
    static final String[][] FORMATS = {
        {"chunk", "net/minecraft/world/level/chunk/storage/SerializableChunkData", "parse", "write"},
        {"entities", "net/minecraft/world/level/chunk/storage/EntityStorage", "storeEntities"},
        // level.dat: the tag LevelStorageAccess.saveDataTag builds, Data being what
        // PrimaryLevelData.createTag builds; the reader takes it through the Dynamic API,
        // and reads nothing the writer does not write
        {"level", "net/minecraft/world/level/storage/LevelStorageSource$LevelStorageAccess", "saveDataTag"},
    };

    /** The class an interface call resolves in, for a format walked from an interface's caller. */
    static final Map<String, String> RESOLVE = Map.of("level", "net/minecraft/world/level/storage/PrimaryLevelData");

    /**
     * An entity's save data is read by Entity.load, which reads the keys every entity has and
     * then calls readAdditionalSaveData, which each class of the hierarchy overrides and chains
     * to its superclass's; the writer is Entity.save the same way. So an entity format is the
     * walk of load and save with the entity's own class as the one virtual calls resolve in,
     * and every helper handed the tag followed. A player's file is the walk of ServerPlayer's
     * load and saveWithoutId, the writer PlayerDataStorage calls (the file carries no entity
     * id); every entity type's is the walk of the class its EntityType is declared with.
     */
    static final String[] ENTITY_METHODS = {"load", "save"};
    static final String ENTITY = "net/minecraft/world/entity/Entity";

    /**
     * Every entity type's class, by the type's id. EntityType.getBaseClass names Entity for
     * all of them; the generic argument of the static field each type is held in
     * (EntityType&lt;Zombie&gt;) names the class, as GenEntityData reads it.
     */
    static Map<String, String> entityClasses() throws Exception {
        Object registry = Class.forName("net.minecraft.core.registries.BuiltInRegistries").getField("ENTITY_TYPE").get(null);
        java.lang.reflect.Method getKey = Class.forName("net.minecraft.core.Registry").getMethod("getKey", Object.class);
        Class<?> entityTypeClass = Class.forName("net.minecraft.world.entity.EntityType");
        Map<String, String> out = new TreeMap<>();
        for (String holder : new String[] {"net.minecraft.world.entity.EntityTypes", "net.minecraft.world.entity.EntityType"}) {
            Class<?> hc;
            try { hc = Class.forName(holder); } catch (ClassNotFoundException e) { continue; }
            for (java.lang.reflect.Field tf : hc.getDeclaredFields()) {
                if (!java.lang.reflect.Modifier.isStatic(tf.getModifiers()) || !entityTypeClass.isAssignableFrom(tf.getType())) continue;
                if (!(tf.getGenericType() instanceof java.lang.reflect.ParameterizedType pt) || pt.getActualTypeArguments().length != 1) continue;
                java.lang.reflect.Type arg = pt.getActualTypeArguments()[0];
                Class<?> base = arg instanceof Class<?> c ? c : arg instanceof java.lang.reflect.ParameterizedType ap && ap.getRawType() instanceof Class<?> c2 ? c2 : null;
                if (base == null) continue;
                tf.setAccessible(true);
                Object type = tf.get(null);
                out.putIfAbsent(String.valueOf(getKey.invoke(registry, type)), base.getName().replace('.', '/'));
            }
        }
        return out;
    }

    /** The class a format's virtual calls resolve in: the entity's own, for a chain. */
    static String formatClass = null;

    /** The classes whose keyed accessors describe a saved value: the tag itself, and the
     *  input and output views an entity reads and writes through. */
    static boolean isTagInput(String owner) {
        return owner.equals("net/minecraft/nbt/CompoundTag")
            || owner.equals("net/minecraft/world/level/storage/ValueInput")
            || owner.equals("net/minecraft/world/level/storage/ValueOutput");
    }

    /** The tag a writer's accessor writes, by its name; null for one that writes no scalar. */
    static String primOfPut(String accessor) {
        return switch (accessor) {
            case "putBoolean" -> "BOOL";
            case "putByte" -> "BYTE";
            case "putShort" -> "SHORT";
            case "putInt" -> "INT";
            case "putLong" -> "LONG";
            case "putFloat" -> "FLOAT";
            case "putDouble" -> "DOUBLE";
            case "putString" -> "STRING";
            case "putByteArray" -> "BYTE_ARRAY";
            case "putIntArray" -> "INT_ARRAY";
            case "putLongArray" -> "LONG_ARRAY";
            default -> null;
        };
    }

    /** The tag an accessor reads, by its name; null for one that reads no value of its own. */
    static String primOf(String accessor) {
        return switch (accessor) {
            case "getBoolean", "getBooleanOr" -> "BOOL";
            case "getByte", "getByteOr" -> "BYTE";
            case "getShort", "getShortOr" -> "SHORT";
            case "getInt", "getIntOr" -> "INT";
            case "getLong", "getLongOr" -> "LONG";
            case "getFloat", "getFloatOr" -> "FLOAT";
            case "getDouble", "getDoubleOr" -> "DOUBLE";
            case "getString", "getStringOr" -> "STRING";
            case "getByteArray" -> "BYTE_ARRAY";
            case "getIntArray" -> "INT_ARRAY";
            case "getLongArray" -> "LONG_ARRAY";
            default -> null;
        };
    }

    static Map<String, Object> node(String kind, Object... kv) { return GenPacketSchema.node(kind, kv); }
    static Map<String, Object> prim(String t) { return GenPacketSchema.prim(t); }

    // ---- the values on the interpreted stack -------------------------------------

    sealed interface SV permits TagV, ListV, ConstV, CodecV, NodeV, LambdaSV, OptV, PrimSV, OtherV {}
    /**
     * The tag handed to the method, or one read at a key: its fields go into `struct`, and
     * `field` is the entry it was read at (null for the tag the method was handed), so a codec
     * applied to it later can say what that key really holds.
     */
    record TagV(Map<String, Object> struct, Map<String, Object> field) implements SV {}
    /** A list read at a key: every element is a tag whose fields go into `elem`. */
    record ListV(Map<String, Object> elem) implements SV {}
    record ConstV(Object v) implements SV {}
    /** A static codec field, which the key it is read with describes. */
    record CodecV(String owner, String field) implements SV {}
    /** A codec already resolved to its node (one held in a record component). */
    record NodeV(Map<String, Object> n) implements SV {}
    /** java.util.Optional of something: unwrapped by get, orElse and their kin. */
    record OptV(SV inner) implements SV {}
    /** A lambda, with whatever it captured: a codec among them describes what it parses. */
    record LambdaSV(List<SV> captured, String implOwner, String implName) implements SV {}
    /** A scalar tag a writer made (IntTag.valueOf): what a list it is added to holds. */
    record PrimSV(String t) implements SV {}
    record OtherV(String what) implements SV {}

    static SV pop(Deque<SV> st) { return st.isEmpty() ? new OtherV("underflow") : st.pop(); }

    /**
     * Adds one field to a struct, or returns the one already there for that key. A key read
     * twice keeps the richer description: `getString("Status")` in front of a presence test
     * and `read("Status", ChunkStatus.CODEC)` for the value are the same key, and the codec is
     * what says what it holds.
     */
    @SuppressWarnings("unchecked")
    static Map<String, Object> put(Map<String, Object> struct, String key, Map<String, Object> type, boolean optional, Object dflt) {
        List<Map<String, Object>> fields = (List<Map<String, Object>>) struct.get("fields");
        for (Map<String, Object> f : fields) {
            if (!key.equals(f.get("key"))) continue;
            if ("prim".equals(castNode(f.get("type")).get("k")) && !"prim".equals(type.get("k"))) {
                f.put("type", type);
                f.remove("default");
            }
            return f;
        }
        Map<String, Object> f = new LinkedHashMap<>();
        f.put("name", key);
        f.put("key", key);
        if (optional) f.put("optional", true);
        if (dflt != null) f.put("default", String.valueOf(dflt));
        f.put("type", type);
        fields.add(f);
        return f;
    }

    static Map<String, Object> struct(String java) {
        Map<String, Object> s = node("struct", "name", GenPacketSchema.shortName(java).replace("$", ""), "java", java);
        s.put("fields", new ArrayList<Map<String, Object>>());
        return s;
    }

    /**
     * The keys one reader takes off the tag it is handed. The tag is whichever parameter of the
     * method is a CompoundTag or a ValueInput; a reader with none is not one of these.
     */
    /** The class whose reader is being walked: nested compounds and lists are named after it. */
    static String rootOwner = null;
    /** The struct a writer's own tag fills, when the method being walked is a writer. */
    static Map<String, Object> writerRoot = null;

    /** How deep a helper handed the tag is followed. */
    static int depth = 0;

    static Map<String, Object> walk(String owner, String method, Map<String, Object> root) {
        rootOwner = owner;
        MethodModel target = null;
        for (MethodModel m : GenPacketSchema.classModel(owner).methods()) {
            if (!m.methodName().stringValue().equals(method)) continue;
            String d = m.methodType().stringValue();
            if (d.contains("CompoundTag;") || d.contains("ValueInput;") || d.contains("ValueOutput;") || m.code().isPresent() && buildsTag(m)) { target = m; break; }
        }
        if (target == null) return null;
        String desc = target.methodType().stringValue();
        boolean isStatic = (target.flags().flagsMask() & java.lang.reflect.Modifier.STATIC) != 0;

        // a writer handed no tag builds the one it returns or stores: its first new
        // CompoundTag is the root
        String sig = desc.substring(0, desc.indexOf(')'));
        writerRoot = !sig.contains("CompoundTag;") && !sig.contains("ValueInput;") && !sig.contains("ValueOutput;") ? root : null;
        Map<Integer, SV> locals = new HashMap<>();
        List<Integer> slots = GenPacketSchema.paramSlots(desc, isStatic);
        List<String> params = paramTypes(desc);
        for (int i = 0; i < params.size() && i < slots.size(); i++) {
            if (params.get(i).endsWith("CompoundTag;") || params.get(i).endsWith("ValueInput;") || params.get(i).endsWith("ValueOutput;")) {
                locals.put(slots.get(i), new TagV(root, null));
            }
        }
        run(target, locals);
        return root;
    }

    /** Interprets one method with some of its locals bound. */
    static SV run(MethodModel target, Map<Integer, SV> locals) { return run(target, locals, false); }

    /** Interprets one method; answers the value it returns, when that is a tag it made. */
    static SV run(MethodModel target, Map<Integer, SV> locals, boolean builder) {
        boolean firstNew = builder || writerRoot != null && depth == 0;
        SV returned = null;
        Deque<SV> st = new ArrayDeque<>();
        for (CodeElement el : target.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            switch (el) {
                case ConstantInstruction ci -> st.push(new ConstV(ci.constantValue()));
                case LoadInstruction li -> st.push(locals.getOrDefault(li.slot(), new OtherV("local")));
                case StoreInstruction si -> locals.put(si.slot(), pop(st));
                case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> {
                    String t = fi.typeSymbol().descriptorString();
                    st.push(t.endsWith("Codec;") ? new CodecV(fi.owner().asInternalName(), fi.name().stringValue()) : new OtherV("static"));
                }
                case FieldInstruction fi when fi.opcode() == Opcode.GETFIELD -> { pop(st); st.push(new OtherV("field")); }
                case FieldInstruction fi -> { pop(st); if (fi.opcode() == Opcode.PUTFIELD) pop(st); }
                case InvokeInstruction ii -> invoke(ii, st);
                case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!st.isEmpty()) st.push(st.peek()); }
                case StackInstruction si when si.opcode() == Opcode.POP -> pop(st);
                case NewObjectInstruction no -> {
                    // `new CompoundTag()` in a writer: the first one is the tag it returns
                    // `new CompoundTag()` in a writer: the first one is the tag it returns; another
                    // is a compound it puts at a key, named by that key when it is put; a
                    // `new ListTag()` is a list whose elements are what gets added
                    String cls = no.className().asInternalName();
                    if (cls.equals("net/minecraft/nbt/CompoundTag") && firstNew) {
                        firstNew = false;
                        st.push(new TagV(writerRoot, null));
                    } else if (cls.equals("net/minecraft/nbt/CompoundTag")) st.push(new TagV(struct(rootOwner + "$?"), null));
                    else if (cls.equals("net/minecraft/nbt/ListTag")) st.push(new ListV(node("nbt")));
                    else st.push(new OtherV("new"));
                }
                case TypeCheckInstruction tc when tc.opcode() == Opcode.CHECKCAST -> { }   // keeps the value
                // a branch takes its operands; the walk is linear, so a ternary (`!stable()`
                // as putBoolean's value) pushes both arms, and the goto between them drops
                // the first so the value under it stays where the call expects it
                case BranchInstruction bi -> {
                    switch (bi.opcode()) {
                        case GOTO, GOTO_W -> { if (!st.isEmpty()) st.pop(); }
                        case IF_ICMPEQ, IF_ICMPNE, IF_ICMPLT, IF_ICMPGE, IF_ICMPGT, IF_ICMPLE, IF_ACMPEQ, IF_ACMPNE -> { pop(st); pop(st); }
                        default -> pop(st);
                    }
                }
                case LookupSwitchInstruction ls -> pop(st);
                case TableSwitchInstruction ts -> pop(st);
                case ReturnInstruction ri when ri.opcode() == Opcode.ARETURN -> { SV v = pop(st); if (v instanceof TagV || v instanceof ListV) returned = v; }
                case InvokeDynamicInstruction idi -> {
                    List<SV> captured = new ArrayList<>();
                    for (int i = 0; i < GenPacketSchema.arity(idi.typeSymbol().descriptorString()); i++) captured.add(0, pop(st));
                    GenPacketSchema.LambdaV impl = GenPacketSchema.lambdaOf(idi);
                    st.push(new LambdaSV(captured, impl.owner(), impl.name()));
                }
                case Instruction any -> {
                    // anything else consumes what it consumes and produces something we do not
                    // follow; the accessors we care about are called on values in locals
                    if (any instanceof ArrayLoadInstruction || any instanceof OperatorInstruction) { pop(st); pop(st); st.push(new OtherV("op")); }
                }
                default -> { }
            }
        }
        return returned;
    }

    static void invoke(InvokeInstruction ii, Deque<SV> st) {
        String owner = ii.owner().asInternalName(), name = ii.name().stringValue(), desc = ii.typeSymbol().descriptorString();
        boolean isStatic = ii.opcode() == Opcode.INVOKESTATIC;
        int n = GenPacketSchema.arity(desc);
        List<SV> args = new ArrayList<>();
        for (int i = 0; i < n; i++) args.add(0, pop(st));
        SV recv = isStatic ? null : pop(st);
        String ret = desc.substring(desc.indexOf(')') + 1);

        // a scalar tag made to be added to a list: IntTag.valueOf(i)
        if (isStatic && name.equals("valueOf") && owner.startsWith("net/minecraft/nbt/") && ret.endsWith("Tag;")) {
            String t = primOfTag(owner);
            st.push(t != null ? new PrimSV(t) : new OtherV("tag"));
            return;
        }
        if (owner.equals("java/util/Objects") && name.equals("requireNonNull")) { st.push(args.isEmpty() ? new OtherV("null") : args.get(0)); return; }
        // `brands.stream().map(StringTag::valueOf).forEach(list::add)`: the tags the stream
        // makes are the list's elements
        if (owner.equals("java/util/stream/Stream")) {
            if (name.equals("map") && !args.isEmpty() && args.get(0) instanceof LambdaSV l && l.implName().equals("valueOf") && primOfTag(l.implOwner()) != null) {
                st.push(new PrimSV(primOfTag(l.implOwner())));
                return;
            }
            if (name.equals("forEach") && recv instanceof PrimSV p && !args.isEmpty() && args.get(0) instanceof LambdaSV l) {
                for (SV c : l.captured()) if (c instanceof ListV lv) setElem(lv, prim(p.t()));
                return;
            }
            if (!ret.equals("V")) st.push(recv instanceof PrimSV ? recv : new OtherV("stream"));
            return;
        }
        // Optional: the value inside is what the caller goes on with
        if (owner.equals("java/util/Optional")) {
            switch (name) {
                case "get", "orElseThrow", "orElse", "orElseGet" -> { st.push(recv instanceof OptV o ? o.inner() : new OtherV("optional")); return; }
                case "isPresent", "isEmpty" -> { st.push(new OtherV("bool")); return; }
                case "map", "flatMap" -> {
                    // `getCompound("block_states").map(tag -> codec.parse(tag))`: the codec the
                    // lambda carries is what that key holds, which the accessor could not say
                    if (recv instanceof OptV o && o.inner() instanceof TagV t && t.field() != null
                            && !args.isEmpty() && args.get(0) instanceof LambdaSV l) {
                        for (SV c : l.captured()) {
                            Map<String, Object> parsed = c instanceof CodecV cv ? codec(cv) : c instanceof NodeV nv ? nv.n() : null;
                            if (parsed != null && !"nbt".equals(parsed.get("k"))) { t.field().put("type", parsed); break; }
                        }
                    }
                    st.push(new OtherV("mapped"));
                    return;
                }
                case "filter", "ifPresent", "ifPresentOrElse", "stream" -> { st.push(new OtherV("mapped")); return; }
                case "ofNullable", "of" -> { st.push(new OptV(args.isEmpty() ? new OtherV("of") : args.get(0))); return; }
                default -> { if (!ret.equals("V")) st.push(new OtherV("optional." + name)); return; }
            }
        }
        // a list read at a key: its elements are tags of the element struct; a list written at
        // a key hands out each element to fill (ValueOutputList.addChild)
        if (recv instanceof ListV l) {
            if (name.equals("add") && !args.isEmpty()) {
                if (args.get(0) instanceof PrimSV p) setElem(l, prim(p.t()));
                else if (args.get(0) instanceof TagV t) setElem(l, t.struct());
                if (!ret.equals("V")) st.push(new OtherV("bool"));
                return;
            }
            if (name.equals("getCompound") || name.equals("get") || name.equals("next")) { st.push(new OptV(new TagV(l.elem(), null))); return; }
            if (name.equals("addChild")) { st.push(new TagV(l.elem(), null)); return; }
            if (name.equals("compoundStream") || name.equals("stream") || name.equals("iterator")) { st.push(recv); return; }
            if (!ret.equals("V")) { st.push(new OtherV("list." + name)); return; }
            return;
        }
        // tag.store(MapCodec, value): the map codec's keys sit in this compound
        if (recv instanceof TagV tag && isTagInput(owner) && name.equals("store") && args.size() == 2 && !(args.get(0) instanceof ConstV)) {
            Map<String, Object> mc = codecNode(args.get(0));
            if ("struct".equals(mc.get("k"))) {
                Map<String, Object> f = new LinkedHashMap<>();
                f.put("name", String.valueOf(mc.get("name")));
                f.put("inline", true);
                f.put("type", mc);
                ((List<Map<String, Object>>) tag.struct().get("fields")).add(f);
            }
            return;
        }
        if (recv instanceof TagV tag && isTagInput(owner) && !args.isEmpty() && args.get(0) instanceof ConstV c && c.v() instanceof String key) {
            Object dflt = name.endsWith("Or") && args.size() > 1 && args.get(1) instanceof ConstV d ? d.v() : null;
            String t = primOf(name);
            if (t != null) {
                // the Or forms have the default with them; the plain ones answer an Optional
                boolean optional = true;
                if (t.equals("BOOL") && dflt instanceof Integer i) dflt = i != 0;
                if (t.equals("BYTE_ARRAY") || t.equals("INT_ARRAY") || t.equals("LONG_ARRAY")) dflt = null;
                put(tag.struct(), key, prim(t), optional, dflt);
                st.push(name.endsWith("Or") ? new OtherV("value") : new OptV(new OtherV("value")));
                return;
            }
            String pt = primOfPut(name);
            if (pt != null) {
                // a key the writer always writes is a key of the format, present; and the
                // tag it writes is the tag on disk — a reader's getIntOr takes any numeric
                // tag, so "Air" written with putShort is a short however it is read
                Map<String, Object> f = put(tag.struct(), key, prim(pt), false, null);
                if ("prim".equals(castNode(f.get("type")).get("k"))) f.put("type", prim(pt));
                return;
            }
            switch (name) {
                case "store", "storeNullable" -> {
                    put(tag.struct(), key, codecNode(args.size() > 1 ? args.get(1) : null), name.endsWith("Nullable"), null);
                    return;
                }
                case "put" -> {
                    SV v = args.size() > 1 ? args.get(1) : null;
                    if (v instanceof TagV t2 && String.valueOf(t2.struct().get("java")).endsWith("$?")) {
                        // a compound the writer made for this key is named by it
                        t2.struct().put("java", rootOwner + "$" + key);
                        t2.struct().put("name", GenPacketSchema.shortName(rootOwner + "$" + key).replace("$", ""));
                    }
                    Map<String, Object> vt = v instanceof TagV t2 ? t2.struct() : v instanceof ListV lv ? node("list", "elem", lv.elem()) : node("nbt");
                    // a writer's put is unconditional unless guarded, which the walk cannot see;
                    // the reader's accessor decides, and a key only the writer writes is optional
                    put(tag.struct(), key, vt, true, null);
                    return;
                }
                case "getCompound", "getCompoundOrEmpty", "child", "childOrEmpty" -> {
                    Map<String, Object> f = put(tag.struct(), key, struct(rootOwner + "$" + key), true, null);
                    SV child = new TagV(castNode(f.get("type")), f);
                    st.push(name.endsWith("OrEmpty") ? child : new OptV(child));
                    return;
                }
                case "getList", "getListOrEmpty", "childrenList", "childrenListOrEmpty" -> {
                    Map<String, Object> elem = struct(rootOwner + "$" + key);
                    Map<String, Object> f = put(tag.struct(), key, node("list", "elem", elem), true, null);
                    SV list = new ListV(castNode(castNode(f.get("type")).get("elem")));
                    st.push(name.endsWith("OrEmpty") ? list : new OptV(list));
                    return;
                }
                case "list", "listOrEmpty" -> {
                    Map<String, Object> e = codecNode(args.size() > 1 ? args.get(1) : null);
                    put(tag.struct(), key, node("list", "elem", e), true, null);
                    st.push(new OtherV("typed list"));
                    return;
                }
                case "read" -> {
                    Map<String, Object> e = codecNode(args.size() > 1 ? args.get(1) : null);
                    put(tag.struct(), key, e, true, null);
                    st.push(new OptV(new OtherV("read")));
                    return;
                }
                case "contains" -> { st.push(new OtherV("bool")); return; }
                default -> { }
            }
        }
        // A helper handed the tag writes into it too — NbtUtils.addCurrentDataVersion(tag)
        // puts DataVersion there — and gives it back, so it is followed with the tag bound and
        // the tag goes on being the tag.
        TagV passed = null;
        for (SV a : args) if (a instanceof TagV t) { passed = t; break; }
        boolean virtual = ii.opcode() == Opcode.INVOKEVIRTUAL || ii.opcode() == Opcode.INVOKEINTERFACE;
        if (passed != null && owner.startsWith("net/minecraft/") && depth < 12) {
            inline(owner, name, desc, isStatic, virtual, args);
            if (ret.endsWith("CompoundTag;") || ret.endsWith("ValueOutput;")) { st.push(passed); return; }
        }
        // a helper that builds the compound it returns (WorldData.createTag): walked with a
        // struct of its own as the tag it makes, which is what the caller then puts somewhere
        if (passed == null && (ret.endsWith("CompoundTag;") || ret.endsWith("ListTag;")) && owner.startsWith("net/minecraft/") && depth < 12) {
            MethodModel target = resolve(owner, name, desc, virtual);
            if (target != null && buildsTag(target)) {
                String cls = target.parent().map(c -> c.thisClass().asInternalName()).orElse(owner);
                boolean compound = ret.endsWith("CompoundTag;");
                Map<String, Object> built = compound ? struct(cls) : null;
                Map<String, Object> savedRoot = writerRoot;
                String savedOwner = rootOwner;
                writerRoot = built;
                rootOwner = cls;
                SV made;
                depth++;
                try {
                    made = run(target, new HashMap<>(), compound);
                } finally {
                    depth--;
                    writerRoot = savedRoot;
                    rootOwner = savedOwner;
                }
                st.push(made != null ? made : compound ? new TagV(built, null) : new OtherV("built"));
                return;
            }
        }
        // `factory.blockStatesContainerCodec()`: a codec a record holds. Where the record is
        // built says what it is, so the component is followed to the expression bound to it.
        if (!isStatic && args.isEmpty() && ret.endsWith("Codec;")) {
            Map<String, Object> held = componentCodec(owner, name);
            if (held != null) { st.push(new NodeV(held)); return; }
        }
        if (!ret.equals("V")) st.push(new OtherV(GenPacketSchema.shortName(owner) + "." + name));
    }

    /**
     * The node of the codec a record component holds. A record's accessor is its component, and
     * the component is bound where the record is built: the class's own static factory is
     * interpreted until it constructs the record, and the argument in that position is the
     * codec — a static field, or a static call the codec walker can interpret with it.
     */
    static final Map<String, Map<String, Object>> componentCodecs = new HashMap<>();
    static Map<String, Object> componentCodec(String owner, String accessor) {
        String key = owner + "." + accessor;
        if (componentCodecs.containsKey(key)) return componentCodecs.get(key);
        componentCodecs.put(key, null);   // a component that reaches itself stays unresolved
        Map<String, Object> out = componentCodec0(owner, accessor);
        componentCodecs.put(key, out);
        return out;
    }

    static Map<String, Object> componentCodec0(String owner, String accessor) {
        ClassModel cm = GenPacketSchema.classModel(owner);
        if (cm == null) return null;
        List<String> comps = new ArrayList<>();
        for (var f : cm.fields()) {
            if ((f.flags().flagsMask() & java.lang.reflect.Modifier.STATIC) == 0) comps.add(f.fieldName().stringValue());
        }
        int idx = comps.indexOf(accessor);
        if (idx < 0) return null;
        String ctorDesc = null;
        for (MethodModel m : cm.methods()) {
            if (m.methodName().stringValue().equals("<init>") && GenPacketSchema.arity(m.methodType().stringValue()) == comps.size()) {
                ctorDesc = m.methodType().stringValue();
            }
        }
        if (ctorDesc == null) return null;
        for (MethodModel m : cm.methods()) {
            if ((m.flags().flagsMask() & java.lang.reflect.Modifier.STATIC) == 0) continue;
            if (!m.methodType().stringValue().endsWith(")L" + owner + ";")) continue;
            GenNbtSchema.V v = ctorArg(m, owner, ctorDesc, idx);
            if (v == null) continue;
            try {
                Map<String, Object> n = GenNbtSchema.nodeOf(v, 0);
                if (n != null && !"opaque".equals(n.get("k"))) return n;
            } catch (Throwable t) {
                return null;
            }
        }
        return null;
    }

    /** The argument in position idx of the constructor call this factory ends in. */
    static GenNbtSchema.V ctorArg(MethodModel m, String owner, String ctorDesc, int idx) {
        Deque<GenNbtSchema.V> st = new ArrayDeque<>();
        Map<Integer, GenNbtSchema.V> locals = new HashMap<>();
        for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            switch (el) {
                case ConstantInstruction ci -> st.push(new GenNbtSchema.ConstV(ci.constantValue()));
                case LoadInstruction li -> st.push(locals.getOrDefault(li.slot(), new GenNbtSchema.OtherV("local")));
                case StoreInstruction si -> locals.put(si.slot(), st.isEmpty() ? new GenNbtSchema.OtherV("empty") : st.pop());
                case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> {
                    String o = fi.owner().asInternalName(), fn = fi.name().stringValue();
                    if (fi.typeSymbol().descriptorString().endsWith("Codec;")) st.push(new GenNbtSchema.RefV(o, fn));
                    else if (o.endsWith("core/registries/Registries")) st.push(new GenNbtSchema.KeyV(GenNbtSchema.registryId(fn)));
                    else st.push(new GenNbtSchema.OtherV(fn));
                }
                case NewObjectInstruction no -> st.push(new GenNbtSchema.OtherV("new"));
                case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!st.isEmpty()) st.push(st.peek()); }
                case InvokeInstruction ii -> {
                    String d = ii.typeSymbol().descriptorString();
                    int n = GenPacketSchema.arity(d);
                    List<GenNbtSchema.V> args = new ArrayList<>();
                    for (int i = 0; i < n && !st.isEmpty(); i++) args.add(0, st.pop());
                    boolean stat = ii.opcode() == Opcode.INVOKESTATIC;
                    GenNbtSchema.V recvV = stat || st.isEmpty() ? null : st.pop();
                    if (ii.name().stringValue().equals("<init>") && ii.owner().asInternalName().equals(owner) && d.equals(ctorDesc)) {
                        return idx < args.size() ? args.get(idx) : null;
                    }
                    String ret = d.substring(d.indexOf(')') + 1);
                    if (ret.equals("V")) break;
                    String mn = ii.name().stringValue();
                    // registry.holderByNameCodec() on the registry a key names: an element of it
                    if (recvV instanceof GenNbtSchema.KeyV k && ret.endsWith("Codec;")) {
                        st.push(new GenNbtSchema.CodecV(mn.startsWith("holder")
                            ? node("holder", "registry", k.registry())
                            : node("registry", "registry", k.registry())));
                        break;
                    }
                    // a lookup of the registry a key names keeps the key
                    if (recvV instanceof GenNbtSchema.KeyV || (!args.isEmpty() && args.get(0) instanceof GenNbtSchema.KeyV)) {
                        GenNbtSchema.V k2 = recvV instanceof GenNbtSchema.KeyV kv ? kv : args.get(0);
                        if (ret.contains("Registry") || ret.contains("Lookup") || ret.contains("HolderGetter")) { st.push(k2); break; }
                    }
                    st.push(stat && ret.endsWith("Codec;")
                        ? new GenNbtSchema.CallV(ii.owner().asInternalName(), ii.name().stringValue(), d, args)
                        : new GenNbtSchema.OtherV(ii.name().stringValue()));
                }
                default -> { }
            }
        }
        return null;
    }

    /** The node of a codec value on the stack, or an arbitrary tag when it is not one. */
    static Map<String, Object> codecNode(SV v) {
        if (v instanceof CodecV cv) return codec(cv);
        if (v instanceof NodeV nv) return nv.n();
        return node("nbt");
    }

    /**
     * Walks a helper the tag was handed, so what it writes into the tag is part of the format.
     * The tag arguments are bound to the helper's parameters; everything else is unknown there.
     */
    static void inline(String owner, String name, String desc, boolean isStatic, boolean virtual, List<SV> args) {
        // a virtual call on the entity being described resolves in its own class (Entity.load
        // calling this.readAdditionalSaveData), a super call in the class named
        MethodModel target = resolve(owner, name, desc, virtual);
        if (target == null) return;
        List<Integer> slots = GenPacketSchema.paramSlots(desc, isStatic);
        Map<Integer, SV> locals = new HashMap<>();
        for (int i = 0; i < args.size() && i < slots.size(); i++) if (args.get(i) instanceof TagV) locals.put(slots.get(i), args.get(i));
        if (locals.isEmpty()) return;
        depth++;
        try {
            run(target, locals);
        } finally {
            depth--;
        }
    }

    /** The method a call runs, with the class being described resolving a virtual call. */
    static MethodModel resolve(String owner, String name, String desc, boolean virtual) {
        MethodModel target = null;
        String at = virtual && formatClass != null && isSubclass(formatClass, owner) ? formatClass : owner;
        for (String cls = at; cls != null && target == null; ) {
            ClassModel cm = GenPacketSchema.classModel(cls);
            if (cm == null) break;
            target = declared(cm, name, desc);
            // a default method of an interface the class implements
            // (InventoryCarrier.readInventoryFromTag, called on the villager)
            if (target == null) target = defaultMethod(cm, name, desc, new HashSet<>());
            cls = cm.superclass().map(c -> c.asInternalName()).orElse(null);
        }
        return target;
    }

    /** Sets what every element of a list is. */
    static void setElem(ListV l, Map<String, Object> elem) {
        l.elem().clear();
        l.elem().putAll(elem);
    }

    /** The scalar a tag class holds: IntTag is an INT. */
    static String primOfTag(String owner) {
        return switch (GenPacketSchema.shortName(owner)) {
            case "ByteTag" -> "BYTE";
            case "ShortTag" -> "SHORT";
            case "IntTag" -> "INT";
            case "LongTag" -> "LONG";
            case "FloatTag" -> "FLOAT";
            case "DoubleTag" -> "DOUBLE";
            case "StringTag" -> "STRING";
            case "ByteArrayTag" -> "BYTE_ARRAY";
            case "IntArrayTag" -> "INT_ARRAY";
            case "LongArrayTag" -> "LONG_ARRAY";
            default -> null;
        };
    }

    /** Whether a method creates a CompoundTag or a ListTag of its own: a writer handed no tag. */
    static boolean buildsTag(MethodModel m) {
        for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            if (el instanceof NewObjectInstruction no && no.className().asInternalName().matches("net/minecraft/nbt/(Compound|List)Tag")) return true;
        }
        return false;
    }

    static MethodModel declared(ClassModel cm, String name, String desc) {
        for (MethodModel m : cm.methods()) {
            if (m.methodName().stringValue().equals(name) && m.methodType().stringValue().equals(desc) && m.code().isPresent()) return m;
        }
        return null;
    }

    /** A default method with that name in the interfaces of cm, transitively. */
    static MethodModel defaultMethod(ClassModel cm, String name, String desc, Set<String> seen) {
        for (var i : cm.interfaces()) {
            String in = i.asInternalName();
            if (!seen.add(in)) continue;
            ClassModel icm = GenPacketSchema.classModel(in);
            if (icm == null) continue;
            MethodModel m = declared(icm, name, desc);
            if (m == null) m = defaultMethod(icm, name, desc, seen);
            if (m != null) return m;
        }
        return null;
    }

    /** Whether cls is owner or extends it. */
    static boolean isSubclass(String cls, String owner) {
        for (String c = cls; c != null; ) {
            if (c.equals(owner)) return true;
            ClassModel cm = GenPacketSchema.classModel(c);
            if (cm == null) return false;
            for (var i : cm.interfaces()) if (isSubclass(i.asInternalName(), owner)) return true;
            c = cm.superclass().map(x -> x.asInternalName()).orElse(null);
        }
        return false;
    }

    /** The node of a static codec field, through GenNbtSchema's codec walker. */
    static Map<String, Object> codec(CodecV cv) {
        try {
            Map<String, Object> n = GenNbtSchema.codecField(cv.owner(), cv.field(), 0);
            return n != null ? n : node("nbt");
        } catch (Throwable t) {
            return node("nbt");
        }
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> castNode(Object o) { return (Map<String, Object>) o; }

    /** The parameter descriptors of a method descriptor, in order. */
    static List<String> paramTypes(String desc) {
        List<String> out = new ArrayList<>();
        int i = 1;
        while (i < desc.length() && desc.charAt(i) != ')') {
            int start = i;
            while (desc.charAt(i) == '[') i++;
            if (desc.charAt(i) == 'L') { while (desc.charAt(i) != ';') i++; }
            i++;
            out.add(desc.substring(start, i));
        }
        return out;
    }

    /**
     * A compound or a list whose elements nothing was read from is an arbitrary tag: the reader
     * kept it whole (it hands it to a data fixer, or stores it as it is), so an empty struct
     * would claim it has no keys rather than that they were not read here.
     */
    @SuppressWarnings("unchecked")
    static void collapseEmpty(Map<String, Object> n) {
        if (!(n.get("fields") instanceof List<?> fs)) return;
        for (Object o : (List<Object>) fs) {
            Map<String, Object> f = castNode(o);
            Map<String, Object> t = castNode(f.get("type"));
            if ("struct".equals(t.get("k"))) {
                collapseEmpty(t);
                if (((List<?>) t.get("fields")).isEmpty()) f.put("type", node("nbt"));
            } else if ("list".equals(t.get("k")) && t.get("elem") instanceof Map<?, ?> em) {
                Map<String, Object> e = castNode(em);
                if ("struct".equals(e.get("k"))) {
                    collapseEmpty(e);
                    if (((List<?>) e.get("fields")).isEmpty()) t.put("elem", node("nbt"));
                }
            }
        }
    }

    static boolean hasHole(Object n) {
        if (n instanceof Map<?, ?> m) {
            if ("opaque".equals(m.get("k"))) return true;
            for (Object v : m.values()) if (hasHole(v)) return true;
        } else if (n instanceof List<?> l) {
            for (Object v : l) if (hasHole(v)) return true;
        }
        return false;
    }

    public static void main(String[] args) throws Exception {
        net.minecraft.SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();

        List<String[]> formats = new ArrayList<>(Arrays.asList(FORMATS));
        List<String[]> chains = new ArrayList<>();
        chains.add(new String[] {"entity", ENTITY});
        for (var e : entityClasses().entrySet()) chains.add(new String[] {"entity/" + e.getKey(), e.getValue()});
        StringBuilder sb = new StringBuilder("{\n  \"version\": 1,\n  \"formats\": {\n");
        int written = 0, keys = 0;
        for (String[] c : chains) {
            String[] r = new String[2 + ENTITY_METHODS.length];
            r[0] = c[0]; r[1] = c[1];
            System.arraycopy(ENTITY_METHODS, 0, r, 2, ENTITY_METHODS.length);
            formats.add(r);
        }
        // a player is read like any entity and written by PlayerDataStorage.save,
        // which calls saveWithoutId: the file carries no entity id
        formats.add(new String[] {"player", "net/minecraft/server/level/ServerPlayer", "load", "saveWithoutId"});
        for (String[] r : formats) {
            if (GenPacketSchema.classModel(r[1]) == null) {
                System.err.println("GenSaveSchema: no class " + r[1] + " (format " + r[0] + " skipped)");
                continue;
            }
            Map<String, Object> type = struct(r[1]);
            boolean any = false;
            boolean chain = isSubclass(r[1], ENTITY);
            formatClass = chain ? r[1] : RESOLVE.get(r[0]);
            for (int i = 2; i < r.length; i++) {
                // an entity's load and save are Entity's, walked with the entity's class in
                // charge of the virtual calls
                String owner = chain ? ENTITY : r[1];
                if (walk(owner, r[i], type) != null) any = true;
                else System.err.println("GenSaveSchema: " + owner + "." + r[i] + " takes no tag (skipped)");
            }
            formatClass = null;
            if (!any) {
                System.err.println("GenSaveSchema: nothing walked for format " + r[0] + " (skipped)");
                continue;
            }
            collapseEmpty(type);
            if (written > 0) sb.append(",\n");
            sb.append("    ").append(GenPacketSchema.json(r[0])).append(": {");
            sb.append("\"class\": ").append(GenPacketSchema.json(r[1].replace('/', '.'))).append(", ");
            sb.append("\"methods\": ").append(GenPacketSchema.json(String.join(", ", Arrays.copyOfRange(r, 2, r.length)))).append(", ");
            sb.append("\"coverage\": ").append(GenPacketSchema.json(hasHole(type) ? "partial" : "full"));
            sb.append(", \"type\": ").append(GenPacketSchema.jsonNode(type)).append("}");
            written++;
            keys += ((List<?>) type.get("fields")).size();
        }
        sb.append("\n  }\n}\n");
        Files.writeString(Path.of("save_schema.json"), sb.toString(), StandardCharsets.UTF_8);
        System.err.printf("GenSaveSchema: %d save formats (%d top-level keys) written to save_schema.json%n", written, keys);
    }
}
