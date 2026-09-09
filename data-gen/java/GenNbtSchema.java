/**
 * GenNbtSchema — Extracts the NBT shape of the registry elements a server sends in the
 * configuration phase (RegistryDataLoader.SYNCHRONIZED_REGISTRIES) and of a few shared
 * structures (chat style and events, the chat type decoration), by interpreting their
 * DataFixerUpper codec chains from the jar's bytecode (java.lang.classfile, JDK 24+).
 *
 * Output: nbt_schema.json in the current directory:
 *   { "version": 1,
 *     "registries": {
 *       "minecraft:dimension_type": {
 *         "class": "net.minecraft.world.level.dimension.DimensionType", "field": "NETWORK_CODEC",
 *         "coverage": "full" | "partial",           // partial = contains an opaque node or a dispatch without cases
 *         "type": { "k": "struct", "name": "DimensionType", "java": "net/minecraft/…/DimensionType", "fields": [
 *                    {"name": "hasSkylight", "key": "has_skylight", "type": {"k": "prim", "t": "BOOL"}},
 *                    {"name": "skybox", "key": "skybox", "optional": true, "default": "OVERWORLD", "type": {"k": "enum", …}},
 *                    {"name": "monsterSettings", "inline": true, "type": {"k": "struct", …}},   // a MapCodec: its keys sit in the parent
 *                    … ] } },
 *       … },
 *     "types": { "net.minecraft.network.chat.Style": { …same shape… }, … } }
 *
 * Node kinds: prim(t: BOOL BYTE SHORT INT LONG FLOAT DOUBLE STRING IDENTIFIER UUID UUID_LENIENT
 * RGB_COLOR INT_ARRAY LONG_ARRAY BYTE_ARRAY) · struct(name, java, fields) · list(elem) · map(key, val) ·
 * enum(name, java, values, ids) — a StringRepresentable enum, ids are the serialized names ·
 * holder(registry, direct?) — an id string or the inline element · holderset(registry) — "#tag",
 * an id or a list of ids · resourcekey(registry) · registry(registry) — an id string · text — a
 * chat component · nbt — an arbitrary tag · either(java, left, right) — the class it was
 * built in, a name for a binding that types it · dispatch(name, key, cases[{id,
 * name, type}]) — a type-keyed union; cases are resolved when the key codec is an enum whose
 * constants carry their MapCodec, or (legacy: true, ComponentSerialization.createLegacyComponentMatcher)
 * when the class building it puts them in an id mapper, and then the key may be absent and the
 * first case whose keys are all present is meant, or when the class registered in the key's
 * registry (its bootstrap class and method, from the method reference BuiltInRegistries
 * registers it with: Registry.register calls, a forEach over a static map read by reflection)
 * · recursive(name, type) — Codec.recursive: the codec the function builds when
 * handed itself, which `ref(name)` inside stands for; also a static codec field that contains
 * itself through a combinator with no name of its own (BlockStateProvider's either), named after
 * the class · ref(name, of) — one instance of the enclosing node of that name and kind
 * (recursive, struct or dispatch) · unit · opaque(java) — a combinator the walker does not know.
 *
 * Static factories are interpreted from their bytecode with the arguments bound
 * (RecordCodecBuilder lambdas, codec-returning helpers, helpers returning a Products$Pn group
 * shared by several records), looked up along the superclass chain when called through a
 * subclass; StateHolder.codec is read as the id-and-properties struct every block dispatches
 * to. Nodes built while an outer field's placeholder was handed out are not cached: their ref
 * is right in that tree only. A MutableObject a factory fills after handing it to its lambdas
 * (26.1's CubicSpline.codec) is recursion by hand and read as Codec.recursive is;
 * KeyDispatchDataCodec (26.1) is the MapCodec it wraps; a shape-keeping combinator (xmap,
 * validate, stable, …) on one of Minecraft's own Codec classes is its receiver;
 * ExtraCodecs.retrieveContext reads no key (unit). Arithmetic on the stack is computed
 * (constants) or kept as one value, so Codec.intRange(MIN_Y * 2, …) stays in step.
 *
 * The text component (types: net.minecraft.network.chat.ComponentSerialization) is walked in
 * full although every other codec sees it as the `text` leaf: recursive Component = either a
 * string, a non-empty list of components, or a compound of the contents (a legacy dispatch on
 * "type": text, translatable, keybind, score, selector, nbt, object), "extra" and the style.
 * A `text` inside that description (the hover event's value, cached from the Style walk) is the
 * component itself.
 *
 * How: RecordCodecBuilder.create/mapCodec(lambda) interprets the lambda with an operand stack:
 * fieldOf/optionalFieldOf carry the key and optionality, forGetter the field name, group/and/apply
 * build the struct. Static codec fields and codec-returning static methods are resolved lazily, so
 * only the chains that flow into the requested fields are walked. Shares the class-file access,
 * enum reflection and JSON helpers of GenPacketSchema (compiled together by ExtractAll).
 */
import java.lang.classfile.*;
import java.lang.classfile.instruction.*;
import java.lang.constant.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public class GenNbtSchema {
    static final int MAX_DEPTH = 40;   // a dialog's inputs inside a chat type's click event nest 25 deep; cycles are guarded by refs, not by this

    /** Shared structures written to the "types" section: owner class (internal name) and codec field. */
    static final String[][] TYPES = {
        {"net/minecraft/network/chat/Style$Serializer", "MAP_CODEC"},
        {"net/minecraft/network/chat/ClickEvent", "CODEC"},
        {"net/minecraft/network/chat/HoverEvent", "CODEC"},
        {"net/minecraft/network/chat/ChatTypeDecoration", "CODEC"},
        {"net/minecraft/network/chat/ComponentSerialization", "CODEC"},
        {"net/minecraft/world/level/storage/LevelData$RespawnData", "MAP_CODEC"},
        {"net/minecraft/world/level/WorldDataConfiguration", "MAP_CODEC"},
        {"net/minecraft/world/level/DataPackConfig", "CODEC"},
        {"net/minecraft/world/level/levelgen/WorldOptions", "CODEC"},
        {"net/minecraft/world/level/levelgen/WorldDimensions", "CODEC"},
        {"net/minecraft/world/item/ItemStack", "CODEC"},
    };

    static final String REGISTRY_DATA_LOADER = "net/minecraft/resources/RegistryDataLoader";
    static final String REGISTRY_DATA = "net/minecraft/resources/RegistryDataLoader$RegistryData";

    // ---- values on the simulated operand stack ------------------------------------

    sealed interface V permits CodecV, RefV, CallV, LambdaV, ConstV, KeyV, ClassV, OtherV, CellV {}
    /**
     * A MutableObject a codec factory fills with the codec it builds after handing it to the
     * lambdas inside (26.1's CubicSpline.codec, recursion before Codec.recursive): a get
     * before the set is a ref to the result, which is then a recursive node of that name.
     */
    static final class CellV implements V {
        final String name;
        Map<String, Object> value;
        boolean used;
        CellV(String name) { this.name = name; }
        @Override public String toString() { return "cell:" + name; }
    }
    record CodecV(Map<String, Object> n) implements V {}
    record RefV(String owner, String field) implements V {}                                  // a static codec field, resolved on demand
    record CallV(String owner, String name, String desc, List<V> args) implements V {}          // a codec-returning static method, resolved on demand
    record LambdaV(String owner, String name, String desc, boolean ctor, List<V> captured) implements V {}
    record ConstV(Object v) implements V {}
    record KeyV(String registry) implements V {}                                                // Registries.X → "minecraft:x"
    record ClassV(String internal) implements V {}
    record OtherV(String what) implements V {}

    static Map<String, Object> node(String kind, Object... kv) { return GenPacketSchema.node(kind, kv); }
    static Map<String, Object> prim(String t) { return GenPacketSchema.prim(t); }
    static Map<String, Object> opaque(String java) { return GenPacketSchema.opaque(java); }
    static String shortName(String internal) { return GenPacketSchema.shortName(internal); }
    static ClassModel classModel(String internal) { return GenPacketSchema.classModel(internal); }

    // ---- well-known codec constants -------------------------------------------------

    static final Map<String, Map<String, Object>> KNOWN_FIELDS = new HashMap<>();
    static void known(String owner, String field, Map<String, Object> n) { KNOWN_FIELDS.put(owner + "." + field, n); }
    static {
        String codec = "com/mojang/serialization/Codec";
        for (String p : new String[]{"BOOL", "BYTE", "SHORT", "INT", "LONG", "FLOAT", "DOUBLE", "STRING"}) known(codec, p, prim(p));
        known(codec, "BYTE_BUFFER", prim("BYTE_ARRAY"));
        known(codec, "INT_STREAM", prim("INT_ARRAY"));
        known(codec, "LONG_STREAM", prim("LONG_ARRAY"));
        known(codec, "PASSTHROUGH", node("nbt"));
        known(codec, "EMPTY", node("unit"));
        String extra = "net/minecraft/util/ExtraCodecs";
        known(extra, "STRING_RGB_COLOR", prim("RGB_COLOR"));   // "#rrggbb" with an int alternative
        known(extra, "RGB_COLOR_CODEC", prim("INT"));
        known(extra, "ARGB_COLOR_CODEC", prim("INT"));
        known(extra, "POSITIVE_INT", prim("INT"));
        known(extra, "NON_NEGATIVE_INT", prim("INT"));
        known(extra, "POSITIVE_FLOAT", prim("FLOAT"));
        known(extra, "NON_NEGATIVE_FLOAT", prim("FLOAT"));
        known(extra, "UUID", prim("UUID"));
        known(extra, "NON_EMPTY_STRING", prim("STRING"));
        known(extra, "PLAYER_NAME", prim("STRING"));
        known(extra, "TAG_OR_ELEMENT_ID", prim("STRING"));
        known(extra, "INSTANT_ISO8601", prim("STRING"));
        known(extra, "BASE64_STRING", prim("STRING"));
        known(extra, "JSON", node("nbt"));
        known(extra, "VECTOR3F", node("list", "elem", prim("FLOAT")));
        known(extra, "VECTOR4F", node("list", "elem", prim("FLOAT")));
        known(extra, "QUATERNIONF", node("list", "elem", prim("FLOAT")));
        known(extra, "AXISANGLE4F", node("list", "elem", prim("FLOAT")));
        known(extra, "MATRIX4F", node("list", "elem", prim("FLOAT")));
        known("net/minecraft/resources/Identifier", "CODEC", prim("IDENTIFIER"));
        known("net/minecraft/resources/ResourceLocation", "CODEC", prim("IDENTIFIER"));
        known("net/minecraft/core/UUIDUtil", "CODEC", prim("UUID"));
        known("net/minecraft/core/UUIDUtil", "STRING_CODEC", prim("UUID_LENIENT"));
        known("net/minecraft/core/UUIDUtil", "LENIENT_CODEC", prim("UUID_LENIENT"));
        known("net/minecraft/core/UUIDUtil", "AUTHLIB_CODEC", prim("UUID_LENIENT"));
        known("net/minecraft/core/BlockPos", "CODEC", prim("INT_ARRAY"));
        known("net/minecraft/world/phys/Vec3", "CODEC", node("list", "elem", prim("DOUBLE")));
        known("net/minecraft/nbt/CompoundTag", "CODEC", node("nbt"));
        known("net/minecraft/nbt/TagParser", "AS_CODEC", node("nbt"));
        known("net/minecraft/nbt/TagParser", "LENIENT_CODEC", node("nbt"));
        for (String f : new String[]{"CODEC", "FLAT_CODEC", "TRUSTED_CODEC", "TRUSTED_FLAT_CODEC", "FLAT_TRUSTED_CODEC", "TRUSTED_CONTEXT_FREE_CODEC"})
            known("net/minecraft/network/chat/ComponentSerialization", f, node("text"));
        known("net/minecraft/core/component/DataComponentMap", "CODEC", node("nbt"));
        known("net/minecraft/core/component/DataComponentPatch", "CODEC", node("nbt"));
        known("net/minecraft/world/item/component/CustomData", "CODEC", node("nbt"));
    }

    static final Map<String, Map<String, Object>> fieldCache = new HashMap<>();
    static final Map<String, String> registryIds = new HashMap<>();

    // ---- entry point ----------------------------------------------------------------

    record Entry(String key, String className, String field, Map<String, Object> type) {}

    public static void main(String[] args) throws Exception {
        // Enum reflection (serialized names) initialises MC classes that touch the registries.
        net.minecraft.SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();

        List<Entry> registries = synchronizedRegistries();
        List<Entry> types = new ArrayList<>();
        for (String[] t : TYPES) {
            if (classModel(t[0]) == null) { System.err.println("GenNbtSchema: no class " + t[0] + " (type skipped)"); continue; }
            types.add(new Entry(t[0].replace('/', '.'), t[0].replace('/', '.'), t[1], expandedField(t[0], t[1])));
        }

        StringBuilder sb = new StringBuilder("{\n  \"version\": 1,\n  \"registries\": {\n");
        writeEntries(sb, registries);
        sb.append("  },\n  \"types\": {\n");
        writeEntries(sb, types);
        sb.append("  }\n}\n");
        Files.writeString(Path.of("nbt_schema.json"), sb.toString(), StandardCharsets.UTF_8);
        long full = registries.stream().filter(e -> !hasHole(e.type)).count();
        long fullT = types.stream().filter(e -> !hasHole(e.type)).count();
        System.err.printf("GenNbtSchema: %d synchronized registries (%d fully typed) + %d shared types (%d fully typed) written to nbt_schema.json%n",
            registries.size(), full, types.size(), fullT);
    }

    static void writeEntries(StringBuilder sb, List<Entry> entries) {
        for (int i = 0; i < entries.size(); i++) {
            Entry e = entries.get(i);
            sb.append("    ").append(GenPacketSchema.json(e.key)).append(": {");
            if (e.className != null) sb.append("\"class\": ").append(GenPacketSchema.json(e.className)).append(", ");
            if (e.field != null) sb.append("\"field\": ").append(GenPacketSchema.json(e.field)).append(", ");
            sb.append("\"coverage\": ").append(GenPacketSchema.json(hasHole(e.type) ? "partial" : "full"));
            sb.append(", \"type\": ").append(GenPacketSchema.jsonNode(e.type)).append("}");
            sb.append(i + 1 < entries.size() ? ",\n" : "\n");
        }
    }

    /**
     * The (registry key, codec) pairs of RegistryDataLoader.SYNCHRONIZED_REGISTRIES: every
     * new RegistryData(key, codec, …) between the previous list's PUTSTATIC and this one.
     */
    static List<Entry> synchronizedRegistries() {
        List<Entry> out = new ArrayList<>();
        ClassModel cm = classModel(REGISTRY_DATA_LOADER);
        if (cm == null) { System.err.println("GenNbtSchema: no " + REGISTRY_DATA_LOADER); return out; }
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            Deque<V> stack = new ArrayDeque<>();
            Map<Integer, V> locals = new HashMap<>();
            List<V[]> pending = new ArrayList<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) {
                    if (fi.name().stringValue().equals("SYNCHRONIZED_REGISTRIES")) {
                        for (V[] p : pending) {
                            String key = p[0] instanceof KeyV k ? k.registry() : String.valueOf(p[0]);
                            String cls = p[1] instanceof RefV r ? r.owner().replace('/', '.') : null;
                            String field = p[1] instanceof RefV r ? r.field() : null;
                            out.add(new Entry(key, cls, field, nodeOf(p[1], 0)));
                        }
                        break;
                    }
                    pending.clear();
                    stack.clear();
                    continue;
                }
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESPECIAL
                        && ii.owner().asInternalName().equals(REGISTRY_DATA) && ii.name().stringValue().equals("<init>")) {
                    List<V> a = popArgs(stack, GenPacketSchema.arity(ii.typeSymbol().descriptorString()));
                    if (!stack.isEmpty()) stack.pop();   // the receiver (new/dup)
                    if (a.size() >= 2) pending.add(new V[]{a.get(0), a.get(1)});
                    continue;
                }
                step(el, stack, locals, REGISTRY_DATA_LOADER, 0);
            }
            break;
        }
        return out;
    }

    // ---- static codec fields (lazy) -------------------------------------------------

    /** The target being described in full although it is a leaf everywhere else (ComponentSerialization.CODEC). */
    static String expandTarget = null;

    /**
     * A target's node. A codec the walker otherwise keeps as a leaf — the text component, which
     * every other codec uses as `text` — is walked in full when it is the target itself: the leaf
     * is suspended for the walk, a reference to the field from inside it (a component's `extra`,
     * a translation's `with`) is a `ref` to the recursive codec, and the leaf is put back after.
     */
    static Map<String, Object> expandedField(String owner, String field) {
        String key = owner + "." + field;
        Map<String, Object> leaf = KNOWN_FIELDS.get(key);
        if (leaf == null) return codecField(owner, field, 0);
        KNOWN_FIELDS.remove(key);
        expandTarget = key;
        fieldCache.put(key, node("ref", "name", key, "of", "recursive"));
        Map<String, Object> result;
        try {
            result = codecFieldWalk(owner, field, 0);
        } finally {
            expandTarget = null;
            KNOWN_FIELDS.put(key, leaf);
            fieldCache.put(key, leaf);
        }
        String rname = recursiveName(result);
        if (rname != null) renameRefs(result, key, rname);
        return result;
    }

    /** The name of the outermost recursive codec in a node, or null. */
    static String recursiveName(Object n) {
        if (n instanceof Map<?, ?> m) {
            if ("recursive".equals(m.get("k"))) return String.valueOf(m.get("name"));
            for (Object v : m.values()) { String r = recursiveName(v); if (r != null) return r; }
        } else if (n instanceof List<?> l) {
            for (Object v : l) { String r = recursiveName(v); if (r != null) return r; }
        }
        return null;
    }

    @SuppressWarnings("unchecked")
    static void renameRefs(Object n, String from, String to) {
        if (n instanceof Map<?, ?> m0) {
            Map<String, Object> m = (Map<String, Object>) m0;
            if ("ref".equals(m.get("k")) && from.equals(m.get("name"))) m.put("name", to);
            for (Object v : m.values()) renameRefs(v, from, to);
        } else if (n instanceof List<?> l) {
            for (Object v : l) renameRefs(v, from, to);
        }
    }

    static final Map<String, List<Object>> idMapperCache = new HashMap<>();

    /**
     * The cases of a legacy component matcher: every id the class that builds it puts in its
     * id mapper — ComponentSerialization.bootstrap: "text" → PlainTextContents.MAP_CODEC,
     * "translatable" → TranslatableContents.MAP_CODEC, …; DataSources.<clinit>: "entity",
     * "block", "storage" — each as a struct of that map codec's fields. A class builds one
     * mapper, so every put in it belongs to that mapper.
     */
    static List<Object> idMapperCases(String owner, int depth) {
        if (idMapperCache.containsKey(owner)) return idMapperCache.get(owner);
        List<Object> cases = new ArrayList<>();
        idMapperCache.put(owner, cases);   // a matcher built inside the scan refers to the same mapper
        ClassModel cm = classModel(owner);
        if (cm == null) return cases;
        for (MethodModel m : cm.methods()) {
            Deque<V> stack = new ArrayDeque<>();
            Map<Integer, V> locals = new HashMap<>();
            String lastOwner = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) { stack.clear(); continue; }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETSTATIC) lastOwner = fi.owner().asInternalName();
                if (el instanceof InvokeInstruction ii && ii.name().stringValue().equals("put") && ii.owner().asInternalName().endsWith("LateBoundIdMapper")) {
                    List<V> a = popArgs(stack, 2);
                    if (!stack.isEmpty()) stack.pop();   // the mapper
                    String id = strOf(arg(a, 0));
                    Map<String, Object> t = nodeOf(arg(a, 1), depth + 1);
                    if (id == null) continue;
                    if ("field".equals(t.get("k"))) {
                        Map<String, Object> f = new LinkedHashMap<>(t);
                        f.remove("k");
                        String java = lastOwner != null ? lastOwner : owner;
                        t = node("struct", "name", shortName(java), "java", java, "fields", new ArrayList<>(List.of(f)));
                    }
                    Map<String, Object> c = new LinkedHashMap<>();
                    c.put("k", "case"); c.put("id", id); c.put("type", t);
                    cases.add(c);
                    stack.push(new OtherV("put"));
                    continue;
                }
                step(el, stack, locals, owner, depth);
            }
        }
        return cases;
    }

    /** The node of a static codec field, interpreting the owner's <clinit> up to its PUTSTATIC. */
    static Map<String, Object> codecField(String owner, String field, int depth) {
        String key = owner + "." + field;
        Map<String, Object> known = KNOWN_FIELDS.get(key);
        if (known != null) return known;
        if (fieldCache.containsKey(key)) {
            Map<String, Object> cached = fieldCache.get(key);
            if (guards.containsKey(key) && guards.get(key) == cached) { recursed.add(key); handouts++; ownHandouts.merge(key, 1, Integer::sum); }
            return cached;
        }
        if (depth > MAX_DEPTH) return opaque("depth:" + key);
        // A codec that refers to itself (a test environment made of test environments) gets
        // this node while it is being built; once built, the node becomes a ref to it by name.
        Map<String, Object> guard = opaque("recursive:" + key);
        fieldCache.put(key, guard);
        guards.put(key, guard);
        int before = handouts, ownBefore = ownHandouts.getOrDefault(key, 0);
        Map<String, Object> result;
        try {
            result = codecFieldWalk(owner, field, depth);
        } finally {
            guards.remove(key);
        }
        // A node holding a ref to a field still being built outside this one (a block state
        // provider's case, met while the provider codec itself is) is right only in that tree:
        // cached, its ref would dangle in the next. It is computed again there instead.
        boolean outer = handouts - before > ownHandouts.getOrDefault(key, 0) - ownBefore;
        String k = String.valueOf(result.get("k"));
        if (result.get("name") != null && (k.equals("struct") || k.equals("dispatch") || k.equals("recursive"))) {
            recursed.remove(key);
            guard.clear();
            guard.put("k", "ref");
            guard.put("name", result.get("name"));
            guard.put("of", k);
        } else if (recursed.remove(key)) {
            // the cycle goes through a combinator with no name of its own (a block state
            // provider is a block state or one of the providers, and some providers hold
            // providers): the field becomes a recursive node named after its class, like
            // Codec.recursive's, and the use inside it a ref to that name
            String rname = shortName(owner) + (field.equals("CODEC") || field.equals("DIRECT_CODEC") ? "" : "." + field);
            result = node("recursive", "name", rname, "type", result);
            guard.clear();
            guard.put("k", "ref");
            guard.put("name", rname);
            guard.put("of", "recursive");
        }
        if (outer) fieldCache.remove(key);
        else if (fieldCache.containsKey(key)) fieldCache.put(key, result);
        return result;
    }

    static int handouts = 0;                                                   // placeholders handed out so far
    static final Map<String, Integer> ownHandouts = new HashMap<>();           // … per field

    static final Map<String, Map<String, Object>> guards = new HashMap<>();   // the placeholder of each field being built
    static final Set<String> recursed = new HashSet<>();                       // fields whose placeholder was handed out

    static boolean hasField(String cls, String field) {
        ClassModel cm = classModel(cls);
        if (cm == null) return false;
        for (FieldModel f : cm.fields()) if (f.fieldName().stringValue().equals(field)) return true;
        return false;
    }

    static Map<String, Object> codecFieldWalk(String owner, String field, int depth) {
        String key = owner + "." + field;
        int refsBefore = refsEmitted;
        Map<String, Object> result = opaque("no-clinit:" + key);
        ClassModel cm = classModel(owner);
        if (cm != null) {
            for (MethodModel m : cm.methods()) {
                if (!m.methodName().stringValue().equals("<clinit>")) continue;
                Deque<V> stack = new ArrayDeque<>();
                Map<Integer, V> locals = new HashMap<>();
                for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                    if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC && fi.owner().asInternalName().equals(owner)) {
                        V top = stack.isEmpty() ? null : stack.pop();
                        if (fi.name().stringValue().equals(field)) {
                            result = top == null ? opaque("not-a-codec:" + key) : nodeOf(top, depth + 1);
                            break;
                        }
                        stack.clear();
                        continue;
                    }
                    step(el, stack, locals, owner, depth);
                }
                break;
            }
        }
        // getstatic ServerLinksDialog.WIDTH_CODEC names the class it was written in, and the
        // field is a constant of an interface it implements: the declaring class assigns it.
        if ("opaque".equals(result.get("k")) && String.valueOf(result.get("java")).startsWith("no-clinit:") && cm != null) {
            Deque<String> supers = new ArrayDeque<>();
            cm.superclass().ifPresent(sc -> supers.add(sc.asInternalName()));
            for (var ie : cm.interfaces()) supers.add(ie.asInternalName());
            Set<String> seen = new HashSet<>();
            while (!supers.isEmpty()) {
                String sup = supers.poll();
                if (!sup.startsWith("net/minecraft/") || !seen.add(sup)) continue;
                if (hasField(sup, field)) { result = codecField(sup, field, depth + 1); break; }
                ClassModel sm = classModel(sup);
                if (sm == null) continue;
                sm.superclass().ifPresent(sc -> supers.add(sc.asInternalName()));
                for (var ie : sm.interfaces()) supers.add(ie.asInternalName());
            }
        }
        // A field first met inside the scan of its own registry's cases (a dialog list holds
        // dialogs) got a ref where the dispatch belongs; that answer is right there and wrong
        // as the field's own, so it is not kept — the next use computes the dispatch in full.
        if (refsEmitted != refsBefore) fieldCache.remove(key);
        else fieldCache.put(key, result);
        return result;
    }

    /** What reading a cell gives: a ref before it is set, then its value, recursive when the ref was handed out. */
    static Map<String, Object> cellValue(CellV cell) {
        if (cell.value == null) { cell.used = true; return node("ref", "name", cell.name, "of", "recursive"); }
        return cell.used ? node("recursive", "name", cell.name, "type", cell.value) : cell.value;
    }

    /** Resolves a stack value to a node (static fields, factory calls and codec lambdas on demand). */
    static Map<String, Object> nodeOf(V v, int depth) {
        if (v instanceof CodecV c) return c.n();
        if (v instanceof CellV cell) return cellValue(cell);
        // ref::getValue on the cell: the same as reading it
        if (v instanceof LambdaV l && l.owner().endsWith("mutable/MutableObject") && !l.captured().isEmpty() && l.captured().get(0) instanceof CellV cell) return cellValue(cell);
        if (v instanceof RefV r) return codecField(r.owner(), r.field(), depth + 1);
        if (v instanceof CallV c) {
            Map<String, Object> n = interpretMethod(c.owner(), c.name(), c.desc(), c.args(), depth + 1);
            return n != null ? n : opaque(shortName(c.owner()) + "." + c.name());
        }
        if (v instanceof LambdaV l) {
            if (l.ctor()) return opaque("ctor:" + shortName(l.owner()));
            // a bound method reference on a codec (MAP_CODEC::codec, CODEC::listOf): the receiver is the first capture
            if (l.owner().startsWith("com/mojang/serialization/") && !l.captured().isEmpty()) {
                Map<String, Object> base = nodeOf(l.captured().get(0), depth + 1);
                return l.name().equals("listOf") ? node("list", "elem", base) : base;
            }
            Map<String, Object> n = interpretMethod(l.owner(), l.name(), l.desc(), l.captured(), depth + 1);
            return n != null ? n : opaque("lambda:" + shortName(l.owner()) + "." + l.name());
        }
        if (v == null) return opaque("null");
        return opaque(v.toString());
    }

    /**
     * Interprets a method body with its parameters bound to args (a codec factory, or the
     * lambda of RecordCodecBuilder.create with its captured values followed by the Instance)
     * and returns the node it returns, null when the method is unknown or returns no codec.
     */
    static Map<String, Object> interpretMethod(String owner, String name, String desc, List<V> args, int depth) {
        if (depth > MAX_DEPTH) return opaque("depth:" + shortName(owner) + "." + name);
        // an inherited static factory is called through the subclass (AnyOfPredicate.codec is
        // CombiningPredicate.codec, DualNoiseProvider.noiseProviderCodec is NoiseProvider's):
        // the method is looked for up the superclass chain, and its body reads as its own class
        for (String cls = owner; cls != null; ) {
            ClassModel cm = classModel(cls);
            if (cm == null) return null;
            if (cls.endsWith("world/level/block/state/StateHolder") && name.equals("codec")) return stateHolderCodec(owner, cls, args, depth);
            Map<String, Object> n = interpretMethodIn(cm, cls, name, desc, args, depth);
            if (n != null) return n;
            if (hasMethod(cm, name, desc)) return null;
            cls = cm.superclass().map(c -> c.asInternalName()).orElse(null);
        }
        return null;
    }

    /**
     * StateHolder.codec(byName, defaultState, stateDefinition), called as BlockState.codec or
     * FluidState.codec: byName.dispatch("id", state -> owner, owner -> its state definition's
     * properties codec) — per block a MapCodec.unit when the block has one state, else
     * "properties" (lenient optional) holding each property's serialized value under its name
     * (Property.codec is a Codec.STRING mapping). The cases differ only by which property
     * names may appear, so the shape is one struct: id, then a string-to-string map.
     */
    static Map<String, Object> stateHolderCodec(String self, String holder, List<V> args, int depth) {
        // The two keys are the first strings the codec pushes, in order: they were
        // "Name" and "Properties" until 26.2 and are "id" and "properties" from 26.3,
        // so reading them is the difference between describing a saved block state and
        // describing the one a previous version had.
        List<String> keys = stringConstants(holder, "codec", 2);
        String idKey = keys.size() > 0 ? keys.get(0) : "id";
        String propsKey = keys.size() > 1 ? keys.get(1) : "properties";
        List<Object> fields = new ArrayList<>();
        fields.add(fieldOf(field(nodeOf(arg(args, 0), depth), idKey, false, null)));
        fields.add(fieldOf(field(node("map", "key", prim("STRING"), "val", prim("STRING")), propsKey, true, null)));
        return node("struct", "name", shortName(self), "java", self, "fields", fields);   // java: the state class, since BlockState and FluidState share the holder
    }

    /**
     * The first `want` string constants a method pushes, in order, looking in its lambdas
     * too when the method itself has none: a key a codec names is a constant in the
     * bytecode, and reading it is how a renamed key is noticed rather than assumed.
     */
    static List<String> stringConstants(String owner, String method, int want) {
        List<String> out = new ArrayList<>();
        ClassModel cm = classModel(owner);
        if (cm == null) return out;
        for (String prefix : List.of(method, "lambda$" + method)) {
            for (MethodModel m : cm.methods()) {
                String n = m.methodName().stringValue();
                if (!(n.equals(prefix) || n.startsWith(prefix + "$"))) continue;
                for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                    if (el instanceof ConstantInstruction ci && ci.constantValue() instanceof String v && !v.isEmpty()) {
                        if (!out.contains(v)) out.add(v);
                        if (out.size() >= want) return out;
                    }
                }
            }
        }
        return out;
    }

    static boolean hasMethod(ClassModel cm, String name, String desc) {
        for (MethodModel m : cm.methods()) if (m.methodName().stringValue().equals(name) && m.methodType().stringValue().equals(desc)) return true;
        return false;
    }

    static Map<String, Object> interpretMethodIn(ClassModel cm, String owner, String name, String desc, List<V> args, int depth) {
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(name) || !m.methodType().stringValue().equals(desc)) continue;
            boolean isStatic = (m.flags().flagsMask() & 0x0008) != 0;
            Map<Integer, V> locals = new HashMap<>();
            int slot = isStatic ? 0 : 1, ai = 0;
            if (!isStatic) locals.put(0, new OtherV("this"));
            for (int i = 1; desc.charAt(i) != ')'; ) {
                char c = desc.charAt(i);
                if (c == '[') { i++; continue; }
                int width = (c == 'J' || c == 'D') ? 2 : 1;
                i = c == 'L' ? desc.indexOf(';', i) + 1 : i + 1;
                locals.put(slot, ai < args.size() ? args.get(ai) : new OtherV("param:" + ai));
                slot += width;
                ai++;
            }
            Deque<V> stack = new ArrayDeque<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof ReturnInstruction ri && ri.opcode() == Opcode.ARETURN) {
                    V top = stack.isEmpty() ? null : stack.pop();
                    if (top instanceof OtherV || top instanceof ConstV || top == null) return null;
                    return nodeOf(top, depth + 1);
                }
                step(el, stack, locals, owner, depth);
            }
            return null;
        }
        return null;
    }

    // ---- the interpreter ------------------------------------------------------------

    static void step(CodeElement el, Deque<V> stack, Map<Integer, V> locals, String self, int depth) {
        switch (el) {
            case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> {
                String o = fi.owner().asInternalName(), n = fi.name().stringValue(), d = fi.typeSymbol().descriptorString();
                if (o.endsWith("core/registries/Registries") && d.contains("ResourceKey")) stack.push(new KeyV(registryId(n)));
                else if (o.endsWith("core/registries/BuiltInRegistries")) stack.push(new OtherV("builtin:" + builtinRegistryId(n)));
                else if (d.contains("ResourceKey")) stack.push(new KeyV("minecraft:" + n.toLowerCase(Locale.ROOT)));
                else if (d.contains("Codec") && !d.contains("StreamCodec")) stack.push(new RefV(o, n));
                else stack.push(new OtherV(shortName(o) + "." + n));
            }
            case InvokeDynamicInstruction idi -> {
                List<V> captured = popArgs(stack, GenPacketSchema.arity(idi.typeSymbol().descriptorString()));
                stack.push(lambdaOf(idi, captured));
            }
            case ConstantInstruction ci -> {
                Object v = ci.constantValue();
                if (v instanceof ClassDesc cd) stack.push(new ClassV(cd.descriptorString().replaceAll("^L|;$", "")));
                else stack.push(new ConstV(v));
            }
            case InvokeInstruction ii -> invoke(ii, stack, locals, self, depth);
            case NewObjectInstruction no when no.className().asInternalName().equals("org/apache/commons/lang3/mutable/MutableObject") -> stack.push(new CellV(shortName(self)));
            case NewObjectInstruction no -> stack.push(new OtherV("new:" + no.className().asInternalName()));
            case LoadInstruction li -> stack.push(locals.getOrDefault(li.slot(), new OtherV("local:" + li.slot())));
            case StoreInstruction st -> { if (!stack.isEmpty()) locals.put(st.slot(), stack.pop()); }
            case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!stack.isEmpty()) stack.push(stack.peek()); }
            case StackInstruction si when si.opcode() == Opcode.POP -> { if (!stack.isEmpty()) stack.pop(); }
            // arithmetic keeps the stack in step (Codec.intRange(DimensionType.MIN_Y * 2, …)):
            // two operands become one value, a constant when both were
            case OperatorInstruction op -> {
                Opcode oc = op.opcode();
                boolean unary = oc == Opcode.INEG || oc == Opcode.LNEG || oc == Opcode.FNEG || oc == Opcode.DNEG || oc == Opcode.ARRAYLENGTH;
                V b = stack.isEmpty() ? null : stack.pop();
                V a = unary ? null : (stack.isEmpty() ? null : stack.pop());
                stack.push(arith(oc, a, b));
            }
            default -> { }
        }
    }

    static V arith(Opcode oc, V a, V b) {
        if (a instanceof ConstV ca && b instanceof ConstV cb && ca.v() instanceof Number x && cb.v() instanceof Number y) {
            switch (oc) {
                case IADD: return new ConstV(x.intValue() + y.intValue());
                case ISUB: return new ConstV(x.intValue() - y.intValue());
                case IMUL: return new ConstV(x.intValue() * y.intValue());
                case LADD: return new ConstV(x.longValue() + y.longValue());
                case LSUB: return new ConstV(x.longValue() - y.longValue());
                case LMUL: return new ConstV(x.longValue() * y.longValue());
                default: break;
            }
        }
        if (oc == Opcode.INEG && b instanceof ConstV cb && cb.v() instanceof Integer i) return new ConstV(-i);
        return new OtherV("arith:" + oc.name().toLowerCase(Locale.ROOT));
    }

    static LambdaV lambdaOf(InvokeDynamicInstruction idi, List<V> captured) {
        for (ConstantDesc cd : idi.bootstrapArgs()) {
            if (cd instanceof DirectMethodHandleDesc dmh) {
                return new LambdaV(dmh.owner().descriptorString().replaceAll("^L|;$", ""), dmh.methodName(), dmh.lookupDescriptor(),
                    dmh.kind() == DirectMethodHandleDesc.Kind.CONSTRUCTOR, captured);
            }
        }
        return new LambdaV("?", "?", "", false, captured);
    }

    static List<V> popArgs(Deque<V> stack, int n) {
        List<V> args = new ArrayList<>();
        for (int i = 0; i < n; i++) args.add(0, stack.isEmpty() ? new OtherV("underflow") : stack.pop());
        return args;
    }

    static V arg(List<V> a, int i) { return i < a.size() ? a.get(i) : null; }
    static String keyOf(V v) { return v instanceof KeyV k ? k.registry() : "?"; }
    static String strOf(V v) { return v instanceof ConstV c && c.v() instanceof String s ? s : null; }
    static boolean isCodec(String ret) { return ret.contains("Codec") && !ret.contains("StreamCodec"); }

    /** Combinators whose result reads and writes exactly what the receiver does. */
    static final Set<String> SHAPE_KEEPING = Set.of("codec", "xmap", "flatXmap", "comapFlatMap", "flatComapMap", "validate", "stable",
        "promotePartial", "orElse", "orElseGet", "setPartial", "withLifecycle", "lenient", "compressed", "fieldOfDefault",
        "assumeMapUnsafe", "forRest", "mapResult", "dependent");

    static void invoke(InvokeInstruction ii, Deque<V> stack, Map<Integer, V> locals, String self, int depth) {
        String owner = ii.owner().asInternalName(), name = ii.name().stringValue(), desc = ii.typeSymbol().descriptorString();
        boolean isStatic = ii.opcode() == Opcode.INVOKESTATIC;
        List<V> args = popArgs(stack, GenPacketSchema.arity(desc));
        V recv = isStatic ? null : (stack.isEmpty() ? new OtherV("underflow") : stack.pop());
        String ret = desc.substring(desc.indexOf(')') + 1);
        String o = shortName(owner);

        if (recv instanceof CellV cell) {
            switch (name) {
                case "setValue", "set" -> { cell.value = nodeOf(arg(args, 0), depth); return; }
                case "getValue", "get" -> { stack.push(new CodecV(cellValue(cell))); return; }
                default -> { if (!ret.equals("V")) stack.push(new OtherV(o + "." + name)); return; }
            }
        }

        // --- JDK: boxing and constant containers used as defaults ---------------------
        if (owner.startsWith("java/lang/") && name.equals("valueOf") && args.size() == 1) { stack.push(args.get(0)); return; }
        if (owner.equals("java/util/Optional")) {
            if (name.equals("empty")) { stack.push(new ConstV(null)); return; }
            if (name.equals("of") || name.equals("ofNullable")) { stack.push(arg(args, 0)); return; }
        }
        if ((owner.equals("java/util/List") || owner.equals("java/util/Set") || owner.endsWith("ImmutableList") || owner.endsWith("ImmutableSet")) && name.equals("of")) { stack.push(new ConstV("[]")); return; }
        if ((owner.equals("java/util/Map") || owner.endsWith("ImmutableMap")) && name.equals("of")) { stack.push(new ConstV("{}")); return; }
        if (owner.endsWith("core/HolderSet") && name.equals("empty")) { stack.push(new ConstV("[]")); return; }

        // --- DataFixerUpper combinators ----------------------------------------------
        if (owner.startsWith("com/mojang/serialization/") || owner.startsWith("com/mojang/datafixers/")) {
            switch (name) {
                case "fieldOf" -> { stack.push(new CodecV(field(nodeOf(recv, depth), strOf(arg(args, 0)), false, null))); return; }
                case "optionalFieldOf", "lenientOptionalFieldOf", "strictOptionalFieldOf" -> {
                    stack.push(new CodecV(field(nodeOf(recv, depth), strOf(arg(args, 0)), true, args.size() >= 2 ? arg(args, 1) : null))); return;
                }
                case "forGetter" -> { stack.push(new CodecV(named(nodeOf(recv, depth), arg(args, 0)))); return; }
                case "group" -> {
                    List<Object> fields = new ArrayList<>();
                    for (V a : args) addToGroup(fields, nodeOf(a, depth));
                    stack.push(new CodecV(node("group", "fields", fields))); return;
                }
                case "and" -> {
                    Map<String, Object> g = nodeOf(recv, depth);
                    List<Object> fields = new ArrayList<>(g.get("fields") instanceof List<?> l ? castList(l) : List.of());
                    for (V a : args) addToGroup(fields, nodeOf(a, depth));
                    stack.push(new CodecV(node("group", "fields", fields))); return;
                }
                case "apply" -> { stack.push(new CodecV(structOf(nodeOf(recv, depth), arg(args, args.size() - 1), self))); return; }
                case "create", "mapCodec" -> {   // RecordCodecBuilder.create(instance -> …) / mapCodec(…)
                    if (owner.endsWith("RecordCodecBuilder") && arg(args, 0) instanceof LambdaV l) {
                        List<V> bound = new ArrayList<>(l.captured());
                        bound.add(new OtherV("instance"));
                        Map<String, Object> n = interpretMethod(l.owner(), l.name(), l.desc(), bound, depth + 1);
                        stack.push(new CodecV(n != null ? n : opaque("RecordCodecBuilder." + name + ":" + shortName(l.owner()) + "." + l.name()))); return;
                    }
                    stack.push(new CodecV(opaque(o + "." + name))); return;
                }
                case "codec", "xmap", "flatXmap", "comapFlatMap", "flatComapMap", "validate", "stable", "promotePartial",
                     "orElse", "orElseGet", "setPartial", "withLifecycle", "lenient", "compressed", "fieldOfDefault",
                     "assumeMapUnsafe", "forRest", "mapResult", "dependent" -> { stack.push(recv == null ? new CodecV(opaque(o + "." + name)) : recv); return; }
                case "listOf", "sizeLimitedListOf" -> { stack.push(new CodecV(node("list", "elem", nodeOf(recv, depth)))); return; }
                case "list" -> { stack.push(new CodecV(node("list", "elem", nodeOf(arg(args, 0), depth)))); return; }
                case "unboundedMap", "simpleMap", "dispatchedMap" -> {
                    stack.push(new CodecV(node("map", "key", nodeOf(arg(args, 0), depth), "val", name.equals("dispatchedMap") ? node("nbt") : nodeOf(arg(args, 1), depth)))); return;
                }
                case "either", "xor", "mapEither" -> { stack.push(new CodecV(either(nodeOf(arg(args, 0), depth), nodeOf(arg(args, 1), depth), self))); return; }
                case "withAlternative" -> {
                    Map<String, Object> left = nodeOf(arg(args, 0), depth);
                    Map<String, Object> n = new LinkedHashMap<>(left);
                    n.put("alt", summary(nodeOf(arg(args, 1), depth)));
                    stack.push(new CodecV(n)); return;
                }
                case "dispatch", "dispatchStable", "partialDispatch", "dispatchMap" -> { stack.push(new CodecV(dispatch(nodeOf(recv, depth), args, self, depth))); return; }
                case "intRange", "longRange" -> { stack.push(new CodecV(prim(name.equals("intRange") ? "INT" : "LONG"))); return; }
                case "floatRange", "doubleRange" -> { stack.push(new CodecV(prim(name.equals("floatRange") ? "FLOAT" : "DOUBLE"))); return; }
                case "sizeLimitedString", "string" -> { stack.push(new CodecV(prim("STRING"))); return; }
                case "unit", "unitCodec", "point" -> { stack.push(new CodecV(node("unit"))); return; }
                case "lazyInitialized" -> { stack.push(new CodecV(nodeOf(arg(args, 0), depth))); return; }
                case "recursive" -> { stack.push(new CodecV(recursive(args, depth))); return; }
                default -> {
                    if (isCodec(ret) || ret.contains("App;") || ret.contains("Products$")) { stack.push(new CodecV(opaque("dfu:" + o + "." + name))); return; }
                    if (!ret.equals("V")) stack.push(new OtherV(o + "." + name));
                    return;
                }
            }
        }

        // --- Minecraft's codec helpers -------------------------------------------------
        if (owner.endsWith("util/ExtraCodecs")) {
            switch (name) {
                case "catchDecoderException", "nonEmptyList", "optionalEmptyMap", "overrideLifecycle", "orCompressed", "validate",
                     "nonEmptyHolderSet", "sizeLimitedMap", "lazyInitialized", "nonEmptyMap" -> { stack.push(new CodecV(nodeOf(arg(args, 0), depth))); return; }
                case "compactListCodec" -> { stack.push(new CodecV(node("list", "elem", nodeOf(arg(args, 0), depth)))); return; }
                case "converter" -> { stack.push(new CodecV(node("nbt"))); return; }   // an NBT tag in another ops
                // retrieveContext(getter): decodes from the ops' context, reads no key and writes
                // none (ExtraCodecs$1ContextRetrievalCodec.keys is Stream.empty())
                case "retrieveContext" -> { stack.push(new CodecV(node("unit"))); return; }
                case "intRange" -> { stack.push(new CodecV(prim("INT"))); return; }
                case "floatRange" -> { stack.push(new CodecV(prim("FLOAT"))); return; }
                case "idResolverCodec", "stringResolverCodec" -> { stack.push(new CodecV(prim("STRING"))); return; }
                case "optionalAlwaysPresentFieldOf" -> { stack.push(new CodecV(field(nodeOf(arg(args, 0), depth), strOf(arg(args, 1)), true, arg(args, 2)))); return; }
                case "xor", "either" -> { stack.push(new CodecV(either(nodeOf(arg(args, 0), depth), nodeOf(arg(args, 1), depth), self))); return; }
                case "recursive" -> { stack.push(new CodecV(recursive(args, depth))); return; }
                // any other codec factory of ExtraCodecs is interpreted lazily, like Minecraft's own (gameProfileCodec)
                default -> { if (isCodec(ret)) { stack.push(new CallV(owner, name, desc, args)); return; } if (!ret.equals("V")) stack.push(new OtherV(o + "." + name)); return; }
            }
        }
        // registry references (net/minecraft/resources in 26.2, net/minecraft/core/registries/codec since 26.3)
        if (o.equals("RegistryFileCodec") && name.equals("create")) {
            stack.push(new CodecV(node("holder", "registry", keyOf(arg(args, 0)), "direct", nodeOf(arg(args, 1), depth)))); return;
        }
        if (o.equals("RegistryFixedCodec") && name.equals("create")) { stack.push(new CodecV(node("holder", "registry", keyOf(arg(args, 0))))); return; }
        if (o.equals("RegistryCodecs") || o.equals("HolderSetCodec")) {
            switch (name) {
                case "homogeneousList", "holderSet", "create" -> { stack.push(new CodecV(node("holderset", "registry", keyOf(arg(args, 0))))); return; }
                case "holder" -> {
                    if (args.size() >= 2 && !(arg(args, 1) instanceof ConstV)) { stack.push(new CodecV(node("holder", "registry", keyOf(arg(args, 0)), "direct", nodeOf(arg(args, 1), depth)))); return; }
                    stack.push(new CodecV(node("holder", "registry", keyOf(arg(args, 0))))); return;
                }
                default -> { stack.push(new CodecV(opaque("RegistryCodecs." + name))); return; }
            }
        }
        if (owner.endsWith("resources/ResourceKey") && name.equals("codec")) { stack.push(new CodecV(node("resourcekey", "registry", keyOf(arg(args, 0))))); return; }
        if ((owner.endsWith("core/Registry") || owner.endsWith("core/DefaultedRegistry") || owner.endsWith("core/DefaultedMappedRegistry") || owner.endsWith("core/MappedRegistry"))
                && (name.equals("byNameCodec") || name.equals("holderByNameCodec"))) {
            String reg = recv instanceof OtherV ov && ov.what().startsWith("builtin:") ? ov.what().substring(8) : "?";
            stack.push(new CodecV(node(name.equals("byNameCodec") ? "registry" : "holder", "registry", reg))); return;
        }
        if (owner.endsWith("util/StringRepresentable") && (name.equals("fromEnum") || name.equals("fromValues") || name.equals("fromEnumWithMapping"))) {
            V sup = arg(args, 0);
            stack.push(new CodecV(sup instanceof LambdaV l ? enumNode(l.owner()) : opaque("StringRepresentable." + name))); return;
        }
        if (owner.endsWith("util/StringRepresentable$EnumCodec") || owner.endsWith("util/StringRepresentable")) {
            // an enum codec is a Codec: its fieldOf is the same keyed field as any other's
            if (name.equals("fieldOf") && recv != null) { stack.push(new CodecV(field(nodeOf(recv, depth), strOf(arg(args, 0)), false, null))); return; }
            if ((name.equals("optionalFieldOf") || name.equals("lenientOptionalFieldOf") || name.equals("strictOptionalFieldOf")) && recv != null) {
                stack.push(new CodecV(field(nodeOf(recv, depth), strOf(arg(args, 0)), true, args.size() >= 2 ? arg(args, 1) : null))); return;
            }
            if ((name.equals("listOf") || name.equals("sizeLimitedListOf")) && recv != null) { stack.push(new CodecV(node("list", "elem", nodeOf(recv, depth)))); return; }
            if (isCodec(ret)) { stack.push(recv != null ? recv : new CodecV(opaque(o + "." + name))); return; }
        }
        if (owner.endsWith("network/chat/ComponentSerialization") && name.equals("createLegacyComponentMatcher")) {
            // (mapper, ComponentContents::codec, "type"): a dispatch on the "type" key whose cases the
            // mapper's bootstrap registered; when the key is absent the first case whose keys are all
            // present is meant (legacy), which is what makes {"text": "…"} a text component
            String contents = "net/minecraft/network/chat/ComponentContents";
            Map<String, Object> d = node("dispatch", "name", shortName(contents), "java", contents, "key", strOf(arg(args, 2)), "keyType", prim("STRING"));
            d.put("legacy", true);
            d.put("cases", idMapperCases(self, depth));
            stack.push(new CodecV(d)); return;
        }
        if (owner.endsWith("network/chat/ComponentSerialization") && isCodec(ret) && expandTarget == null) { stack.push(new CodecV(node("text"))); return; }
        // KeyDispatchDataCodec (26.1): a record around a MapCodec — of(codec) wraps, codec() unwraps
        if (owner.endsWith("util/KeyDispatchDataCodec")) {
            if (name.equals("of")) { stack.push(new CodecV(nodeOf(arg(args, 0), depth))); return; }
            if (name.equals("codec") && recv != null) { stack.push(recv); return; }
        }
        if (owner.endsWith("world/flag/FeatureFlagRegistry") && name.equals("codec")) { stack.push(new CodecV(node("list", "elem", prim("IDENTIFIER")))); return; }
        // a DataFixerUpper combinator that keeps the shape, called on one of Minecraft's own
        // Codec classes (RegistryFileCodec.xmap): the receiver, when it is a codec
        if (recv != null && !(recv instanceof OtherV) && !(recv instanceof ConstV) && isCodec(ret) && SHAPE_KEEPING.contains(name)) { stack.push(recv); return; }

        // --- anything else returning a codec, or a group of fields (a Products$Pn a helper
        // builds for several records: NoiseProvider.noiseProviderCodec): a static Minecraft
        // factory is interpreted lazily
        if (isCodec(ret) || ret.contains("datafixers/Products$")) {
            if (isStatic && owner.startsWith("net/minecraft/")) { stack.push(new CallV(owner, name, desc, args)); return; }
            stack.push(new CodecV(opaque(o + "." + name)));
            return;
        }
        if (!ret.equals("V")) stack.push(new OtherV(o + "." + name));
    }

    /**
     * Codec.recursive(name, self -> codec): the codec the function builds when handed a
     * reference to itself, wrapped as a named recursive node; a ref inside it names the node.
     */
    static Map<String, Object> recursive(List<V> args, int depth) {
        String rname = strOf(arg(args, 0));
        if (rname != null && arg(args, 1) instanceof LambdaV l) {
            List<V> bound = new ArrayList<>(l.captured());
            bound.add(new CodecV(node("ref", "name", rname, "of", "recursive")));
            Map<String, Object> body = interpretMethod(l.owner(), l.name(), l.desc(), bound, depth + 1);
            if (body != null) return node("recursive", "name", rname, "type", body);
        }
        return opaque("Codec.recursive");
    }

    // ---- node builders --------------------------------------------------------------

    /** A keyed field of a record codec (fieldOf / optionalFieldOf). */
    static Map<String, Object> field(Map<String, Object> type, String key, boolean optional, V dflt) {
        Map<String, Object> f = new LinkedHashMap<>();
        f.put("k", "field");
        f.put("name", key == null ? null : camel(key));
        f.put("key", key);
        if (optional) f.put("optional", true);
        String d = defaultOf(dflt, type);
        if (d != null) f.put("default", d);
        f.put("type", type);
        return f;
    }

    /** either(left, right), with the class it was built in (VerticalAnchor: xor of three keyed fields) as `java`. */
    static Map<String, Object> either(Map<String, Object> left, Map<String, Object> right, String self) {
        return node("either", "java", self, "left", left, "right", right);
    }

    /** A keyed field as a struct's member (the `k` dropped, as structOf does). */
    static Map<String, Object> fieldOf(Map<String, Object> f) {
        Map<String, Object> m = new LinkedHashMap<>(f);
        m.remove("k");
        return m;
    }

    /** forGetter(getter): names the field after a method reference; wraps an inline MapCodec (a struct) as an inline field. */
    static Map<String, Object> named(Map<String, Object> n, V getter) {
        String getterName = getter instanceof LambdaV l && !l.name().startsWith("lambda$") && !l.ctor() ? l.name() : null;
        if ("field".equals(n.get("k"))) {
            Map<String, Object> f = new LinkedHashMap<>(n);
            if (getterName != null) f.put("name", getterName);
            return f;
        }
        Map<String, Object> f = new LinkedHashMap<>();
        f.put("k", "field");
        f.put("name", getterName != null ? getterName : "struct".equals(n.get("k")) ? lowerFirst(shortName(String.valueOf(n.get("name"))).replace("$", "")) : "inline");
        f.put("inline", true);
        f.put("type", n);
        return f;
    }

    /** A member of a group: a group handed to and() (a helper's Products$Pn) contributes its fields. */
    static void addToGroup(List<Object> fields, Map<String, Object> n) {
        if ("group".equals(n.get("k")) && n.get("fields") instanceof List<?> l) fields.addAll(castList(l));
        else fields.add(n);
    }

    /** Products$Pn.apply(instance, ctor): the struct of a group, named after the constructor reference (or the class being built). */
    static Map<String, Object> structOf(Map<String, Object> group, V ctor, String self) {
        String java = self;
        if (ctor instanceof LambdaV l && l.ctor()) java = l.owner();
        else if (ctor instanceof LambdaV l && !l.name().startsWith("lambda$") && !l.owner().equals("?")) java = l.owner();
        List<Object> fields = new ArrayList<>();
        Object fs = group.get("fields");
        if (fs instanceof List<?> l) {
            for (Object f : l) {
                if (f instanceof Map<?, ?> m && "field".equals(m.get("k"))) {
                    Object t = m.get("type");
                    if (t instanceof Map<?, ?> tm && "unit".equals(tm.get("k"))) continue;   // MapCodec.unit(...).forGetter: no key
                    Map<String, Object> fm = new LinkedHashMap<>(castMap(m));
                    fm.remove("k");
                    fields.add(fm);
                } else if (f instanceof Map<?, ?> m) {
                    fields.add(named(castMap(m), null));   // a bare MapCodec in group(): inline
                }
            }
        } else {
            return opaque("apply-without-group");
        }
        return node("struct", "name", shortName(java), "java", java, "fields", fields);
    }

    /** codec.dispatch([key,] toType, codecOf): cases from an enum whose constants carry their MapCodec. */
    static Map<String, Object> dispatch(Map<String, Object> keyCodec, List<V> args, String self, int depth) {
        String key = args.size() == 3 ? strOf(arg(args, 0)) : "type";
        if (key == null) key = "type";
        Map<String, Object> n = node("dispatch", "name", shortName(self), "java", self, "key", key, "keyType", keyCodec);
        if ("enum".equals(keyCodec.get("k"))) {
            List<Object> cases = enumCases(String.valueOf(keyCodec.get("java")), depth);
            if (cases != null) n.put("cases", cases);
        } else if ("registry".equals(keyCodec.get("k")) || "holder".equals(keyCodec.get("k"))) {
            String registryId = String.valueOf(keyCodec.get("registry"));
            // a dispatch on a registry whose cases are being read (a clamped int provider's
            // source is an int provider): a reference to the registry's dispatch
            if (registryCasesBuilding.contains(registryId)) { refsEmitted++; return node("ref", "name", buildingDispatch.get(registryId), "of", "dispatch"); }
            buildingDispatch.put(registryId, shortName(self));
            List<Object> cases = registryCases(registryId, depth);
            if (cases != null && !cases.isEmpty()) n.put("cases", cases);
        }
        return n;
    }

    static final Map<String, List<Object>> registryCasesCache = new HashMap<>();
    static final Set<String> registryCasesBuilding = new HashSet<>();
    static final Map<String, String> buildingDispatch = new HashMap<>();   // registry id → the name of the dispatch whose cases are being read
    static int refsEmitted = 0;   // refs handed out while a registry's cases were being read

    /**
     * The cases of a dispatch keyed by a registry (int_provider_type, dialog_type,
     * worldgen/block_state_provider_type): what the registry's bootstrap class registers —
     * Registry.register(registry, "constant", ConstantInt.CODEC), in its bootstrap(Registry)
     * or its <clinit>, directly or through a static register helper of its own — each a
     * struct of that map codec's fields under the id "minecraft:<name>". The bootstrap class
     * is read from BuiltInRegistries (GenPacketSchema.registryBootstrapByLocation).
     */
    static List<Object> registryCases(String registryId, int depth) {
        if (registryCasesCache.containsKey(registryId)) return registryCasesCache.get(registryId);
        List<Object> cases = new ArrayList<>();
        String loc = registryId.startsWith("minecraft:") ? registryId.substring(10) : registryId;
        String cls = GenPacketSchema.registryBootstrapByLocation(loc);
        ClassModel cm = cls == null ? null : classModel(cls);
        if (cm == null) { registryCasesCache.put(registryId, cases); return cases; }
        registryCasesBuilding.add(registryId);
        int before = handouts;
        try {
            scanRegistrations(cm, cls, GenPacketSchema.registryBootstrapMethodByLocation(loc), registryId, cases, depth);
        } finally {
            registryCasesBuilding.remove(registryId);
        }
        // cases holding a ref to a field being built outside (see codecField) are this tree's
        if (handouts == before) registryCasesCache.put(registryId, cases);
        return cases;
    }

    static void scanRegistrations(ClassModel cm, String cls, String bootstrap, String registryId, List<Object> cases, int depth) {
        for (MethodModel m : cm.methods()) {
            String mn = m.methodName().stringValue();
            if (!mn.equals(bootstrap) && !mn.equals("<clinit>")) continue;
            Deque<V> stack = new ArrayDeque<>();
            Map<Integer, V> locals = new HashMap<>();
            String mapField = null;   // the last static Map read, the one a forEach that follows walks
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) { stack.clear(); continue; }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETSTATIC && fi.typeSymbol().descriptorString().equals("Ljava/util/Map;")) {
                    mapField = fi.owner().asInternalName() + "." + fi.name().stringValue();
                }
                // registrations driven by a map's forEach (ActionTypes registers every static
                // action from StaticAction.WRAPPED_CODECS): the map's entries, when they can be
                // read; otherwise the dispatch stays without cases rather than with some of them
                if (el instanceof InvokeInstruction ii && ii.name().stringValue().equals("forEach")) {
                    List<V> a = popArgs(stack, GenPacketSchema.arity(ii.typeSymbol().descriptorString()));
                    if (!stack.isEmpty()) stack.pop();
                    List<Object> fromMap = mapField == null || !(arg(a, 0) instanceof LambdaV l) ? null : mapForEachCases(mapField, l, depth);
                    if (fromMap == null) { cases.clear(); return; }
                    cases.addAll(fromMap);
                    continue;
                }
                // Registry.register(registry, Identifier.withDefaultNamespace("item"), codec): the id is the string
                if (el instanceof InvokeInstruction ii && ii.owner().asInternalName().endsWith("resources/Identifier")
                        && (ii.name().stringValue().equals("withDefaultNamespace") || ii.name().stringValue().equals("parse"))) {
                    V a = stack.isEmpty() ? null : stack.pop();
                    stack.push(a instanceof ConstV ? a : new OtherV("identifier"));
                    continue;
                }
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESPECIAL && ii.name().stringValue().equals("<init>")) {
                    // new IntProviderType(codec): the object stands for the codec it was built from
                    List<V> a = popArgs(stack, GenPacketSchema.arity(ii.typeSymbol().descriptorString()));
                    if (!stack.isEmpty()) stack.pop();   // the dup'd reference
                    V codec = null;
                    for (V v : a) if (v instanceof RefV || v instanceof CodecV || v instanceof CallV || v instanceof LambdaV) codec = v;
                    if (codec != null && !stack.isEmpty() && stack.peek() instanceof OtherV top && top.what().startsWith("new:")) { stack.pop(); stack.push(codec); }
                    continue;
                }
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESTATIC && ii.name().stringValue().startsWith("register")) {
                    List<V> a = popArgs(stack, GenPacketSchema.arity(ii.typeSymbol().descriptorString()));
                    String ret = ii.typeSymbol().descriptorString();
                    ret = ret.substring(ret.indexOf(')') + 1);
                    String id = null;
                    int nameAt = -1;
                    for (int i = 0; i < a.size(); i++) if (a.get(i) instanceof ConstV c && c.v() instanceof String str) { id = str; nameAt = i; break; }
                    V value = nameAt >= 0 && nameAt + 1 < a.size() ? a.get(a.size() - 1) : null;
                    if (id != null && value != null && !(value instanceof OtherV) && !(value instanceof ConstV)) {
                        Map<String, Object> t = nodeOf(value, depth + 1);
                        if ("field".equals(t.get("k"))) {
                            Map<String, Object> f = new LinkedHashMap<>(t);
                            f.remove("k");
                            t = node("struct", "name", GenPacketSchema.camel(id), "fields", new ArrayList<>(List.of(f)));
                        }
                        Map<String, Object> c = new LinkedHashMap<>();
                        c.put("k", "case"); c.put("id", id.contains(":") ? id : "minecraft:" + id); c.put("type", t);
                        cases.add(c);
                    }
                    if (!ret.equals("V")) stack.push(new OtherV("registered:" + id));
                    continue;
                }
                step(el, stack, locals, cls, depth);
            }
        }
    }

    /**
     * map.forEach((key, codec) -> Registry.register(registry, Identifier.withDefaultNamespace(key.getSerializedName()), codec)):
     * the cases such a loop registers. The map is read by reflection, so its keys are the ones
     * the builder kept (StaticAction.WRAPPED_CODECS holds the click actions a server may send,
     * isAllowedFromServer). The id of each is the key's serialized name, as the lambda says;
     * the codec is the one the key's enum constant carries (the builder wraps
     * key.valueCodec() in an xmap, which changes the Java type and not the bytes). Null when
     * the lambda is not of that shape or the map cannot be read.
     */
    static List<Object> mapForEachCases(String mapField, LambdaV consumer, int depth) {
        ClassModel cm = classModel(consumer.owner());
        if (cm == null) return null;
        boolean bySerializedName = false, registers = false;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(consumer.name()) || !m.methodType().stringValue().equals(consumer.desc())) continue;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeInstruction ii && ii.name().stringValue().equals("getSerializedName")) bySerializedName = true;
                if (el instanceof InvokeInstruction ii && ii.name().stringValue().equals("register")) registers = true;
            }
        }
        if (!bySerializedName || !registers) return null;
        int dot = mapField.lastIndexOf('.');
        String owner = mapField.substring(0, dot), fieldName = mapField.substring(dot + 1);
        Map<String, String> keys = new LinkedHashMap<>();   // constant name → serialized name
        String enumClass = null;
        try {
            Class<?> c = GenPacketSchema.loadClass(owner);
            var f = c.getDeclaredField(fieldName);
            f.setAccessible(true);
            Class<?> sr = Class.forName("net.minecraft.util.StringRepresentable");
            var getName = sr.getMethod("getSerializedName");
            for (Object k : ((Map<?, ?>) f.get(null)).keySet()) {
                if (!(k instanceof Enum<?> e)) return null;
                enumClass = e.getDeclaringClass().getName().replace('.', '/');
                keys.put(e.name(), String.valueOf(getName.invoke(k)));
            }
        } catch (Throwable t) {
            return null;
        }
        if (enumClass == null) return null;
        List<Object> all = enumCases(enumClass, depth);
        if (all == null) return null;
        Map<String, Map<String, Object>> byName = new HashMap<>();
        for (Object o : all) if (o instanceof Map<?, ?> m) byName.put(String.valueOf(m.get("name")), castMap(m));
        List<Object> cases = new ArrayList<>();
        for (var e : keys.entrySet()) {
            Map<String, Object> c = byName.get(e.getKey());
            if (c == null) return null;
            cases.add(node("case", "id", "minecraft:" + e.getValue(), "name", e.getKey(), "type", c.get("type")));
        }
        return cases;
    }

    /** The MapCodec each constant of an enum passes to its constructor (ClickEvent.Action, HoverEvent.Action): [{id, name, type}]. */
    static List<Object> enumCases(String enumClass, int depth) {
        ClassModel cm = classModel(enumClass);
        if (cm == null) return null;
        List<Object> cases = new ArrayList<>();
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            Deque<V> stack = new ArrayDeque<>();
            Map<Integer, V> locals = new HashMap<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESPECIAL && ii.name().stringValue().equals("<init>")
                        && (ii.owner().asInternalName().equals(enumClass) || ii.owner().asInternalName().startsWith(enumClass + "$"))) {
                    List<V> a = popArgs(stack, GenPacketSchema.arity(ii.typeSymbol().descriptorString()));
                    if (!stack.isEmpty()) stack.pop();
                    List<String> strings = new ArrayList<>();
                    V codec = null;
                    for (V v : a) {
                        if (v instanceof ConstV c && c.v() instanceof String s) strings.add(s);
                        else if (codec == null && (v instanceof RefV || v instanceof CodecV || v instanceof CallV)) codec = v;
                    }
                    if (strings.isEmpty()) continue;
                    String name = strings.get(0);
                    String id = strings.size() >= 2 ? strings.get(1) : name.toLowerCase(Locale.ROOT);
                    if (codec != null) cases.add(node("case", "id", id, "name", name, "type", nodeOf(codec, depth + 1)));
                    continue;
                }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) { stack.clear(); continue; }
                step(el, stack, locals, enumClass, depth);
            }
            break;
        }
        return cases.isEmpty() ? null : cases;
    }

    /** A StringRepresentable enum: constant names and their serialized names. */
    static Map<String, Object> enumNode(String enumClass) {
        List<String> values = GenPacketSchema.enumValues(enumClass);
        if (values == null) return opaque("enum:" + enumClass);
        List<String> ids = new ArrayList<>();
        try {
            Class<?> c = GenPacketSchema.loadClass(enumClass);
            Class<?> sr = Class.forName("net.minecraft.util.StringRepresentable");
            var getName = sr.getMethod("getSerializedName");
            for (Object o : c.getEnumConstants()) ids.add(String.valueOf(getName.invoke(o)));
        } catch (Throwable t) {
            ids.clear();
        }
        if (ids.size() != values.size()) {
            ids.clear();
            Map<String, String> fromInit = enumIdsFromClinit(enumClass);
            for (String v : values) ids.add(fromInit.getOrDefault(v, v.toLowerCase(Locale.ROOT)));
        }
        return node("enum", "name", shortName(enumClass), "java", enumClass, "values", values, "ids", ids);
    }

    /** constant name → serialized name, from the second string each constant's constructor receives. */
    static Map<String, String> enumIdsFromClinit(String enumClass) {
        Map<String, String> out = new HashMap<>();
        ClassModel cm = classModel(enumClass);
        if (cm == null) return out;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            List<String> strings = new ArrayList<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof ConstantInstruction ci && ci.constantValue() instanceof String s) strings.add(s);
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) {
                    if (strings.size() >= 2 && strings.get(0).equals(fi.name().stringValue())) out.put(strings.get(0), strings.get(1));
                    strings.clear();
                }
            }
            break;
        }
        return out;
    }

    static String defaultOf(V v, Map<String, Object> type) {
        if (v == null) return null;
        if (v instanceof ConstV c) {
            if (c.v() == null) return null;
            if (c.v() instanceof Integer i && "prim".equals(type.get("k")) && "BOOL".equals(type.get("t"))) return i != 0 ? "true" : "false";
            return String.valueOf(c.v());
        }
        if (v instanceof OtherV o) {
            String w = o.what();
            int dot = w.lastIndexOf('.');
            return dot >= 0 ? w.substring(dot + 1) : w;   // Skybox.OVERWORLD → OVERWORLD
        }
        return null;
    }

    static String summary(Map<String, Object> n) {
        String k = String.valueOf(n.get("k"));
        return switch (k) {
            case "prim" -> String.valueOf(n.get("t"));
            case "struct", "enum", "dispatch" -> k + ":" + n.get("name");
            case "opaque" -> "opaque:" + n.get("java");
            default -> k;
        };
    }

    static String camel(String key) {
        StringBuilder sb = new StringBuilder();
        boolean up = false;
        for (char c : key.toCharArray()) {
            if (c == '_' || c == '-' || c == '/' || c == ':' || c == '.') { up = true; continue; }
            sb.append(up ? Character.toUpperCase(c) : c);
            up = false;
        }
        return sb.toString();
    }
    static String lowerFirst(String s) { return s.isEmpty() ? s : Character.toLowerCase(s.charAt(0)) + s.substring(1); }

    @SuppressWarnings("unchecked") static Map<String, Object> castMap(Map<?, ?> m) { return (Map<String, Object>) m; }
    @SuppressWarnings("unchecked") static List<Object> castList(List<?> l) { return (List<Object>) l; }

    /** Registries.X → its id ("minecraft:worldgen/biome"), by reflection on the ResourceKey. */
    static String registryId(String field) {
        return registryIds.computeIfAbsent("Registries." + field, k -> {
            try {
                Object key = Class.forName("net.minecraft.core.registries.Registries").getField(field).get(null);
                return String.valueOf(identifierOf(key));
            } catch (Throwable t) {
                return "minecraft:" + field.toLowerCase(Locale.ROOT);
            }
        });
    }
    /** BuiltInRegistries.X → the registry's id. */
    static String builtinRegistryId(String field) {
        return registryIds.computeIfAbsent("BuiltInRegistries." + field, k -> {
            try {
                Object reg = Class.forName("net.minecraft.core.registries.BuiltInRegistries").getField(field).get(null);
                Object key = reg.getClass().getMethod("key").invoke(reg);
                return String.valueOf(identifierOf(key));
            } catch (Throwable t) {
                return "minecraft:" + field.toLowerCase(Locale.ROOT);
            }
        });
    }
    static Object identifierOf(Object resourceKey) throws Exception {
        for (String m : new String[]{"identifier", "location"}) {
            try { return resourceKey.getClass().getMethod(m).invoke(resourceKey); } catch (NoSuchMethodException ignored) { }
        }
        return resourceKey;
    }

    // ---- coverage -------------------------------------------------------------------

    @SuppressWarnings("unchecked")
    static boolean hasHole(Map<String, Object> n) {
        String k = String.valueOf(n.get("k"));
        // either and ref are described on both sides and by name; what is not described is an
        // opaque node or a dispatch whose cases the walker could not find
        if (k.equals("opaque")) return true;
        if (k.equals("dispatch") && n.get("cases") == null) return true;
        for (Object v : n.values()) {
            if (v instanceof Map<?, ?> m && m.containsKey("k") && hasHole((Map<String, Object>) m)) return true;
            if (v instanceof List<?> l) for (Object o : l) {
                if (o instanceof Map<?, ?> m) {
                    Map<String, Object> mm = (Map<String, Object>) m;
                    Object t = mm.get("type");
                    if (t instanceof Map<?, ?> tm && hasHole((Map<String, Object>) tm)) return true;
                    if (mm.containsKey("k") && hasHole(mm)) return true;
                }
            }
        }
        return false;
    }
}
