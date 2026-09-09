/**
 * GenPacketSchema — Extracts the wire layout of every network packet from the
 * unobfuscated server jar as a typed codec tree, by reading bytecode with
 * java.lang.classfile (JDK 24+) and reflecting on the loaded classes.
 *
 * Output: packet_schema.json in the current directory:
 *   { "version": 2,
 *     "packets": {
 *       "<flow>/<name>": {                       // "serverbound/minecraft:interact"
 *         "state": "play", "class": "net.minecraft...ServerboundInteractPacket",
 *         "coverage": "full" | "partial",       // partial = contains opaque/dispatch/conditional nodes
 *         "type": { "k": "struct", "name": "ServerboundInteractPacket", "fields": [
 *                    {"name": "entityId", "type": {"k": "prim", "t": "VAR_INT"}},
 *                    {"name": "hand",     "type": {"k": "enum", "name": "InteractionHand", "values": ["MAIN_HAND","OFF_HAND"]}},
 *                    ... ] },
 *         "tokens": ["VAR_INT", "enum:InteractionHand", ...]   // flat form for packetdiff
 *     } },
 *     "structs": { "LevelChunkSection.read": { ...same shape... } } }
 *
 * Node kinds: prim(t) · string(max?) · struct(name, fields) · list(elem, max?) ·
 * optional(elem) · map(key, val) · enum(name, values) · registry(registry) ·
 * holder(registry, direct?) · holderset(registry) · resourcekey(registry) · nbt ·
 * either(left, right) · dispatch(key) · unit · opaque(java) — opaque marks what still
 * needs a hand rule in the generator.
 *
 * How: for a STREAM_CODEC built in <clinit> the instructions between the previous
 * PUTSTATIC and this one are interpreted with an operand stack (codec constants
 * push nodes, getter method references carry field names, combinators pop by
 * arity and push the combined node). For Packet.codec(write, read) / StreamCodec.of
 * the read lambda is walked as a reader: buffer read calls push nodes, PUTFIELD
 * and record components name the fields, nested X.read(buf) / new X(buf) recurse.
 */
import java.lang.classfile.*;
import java.lang.classfile.attribute.*;
import java.lang.classfile.instruction.*;
import java.lang.constant.*;
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public class GenPacketSchema {
    static final int MAX_DEPTH = 10;
    static final String[][] TYPES_CLASSES = {
        {"net.minecraft.network.protocol.handshake.HandshakePacketTypes", "handshake"},
        {"net.minecraft.network.protocol.status.StatusPacketTypes", "status"},
        {"net.minecraft.network.protocol.login.LoginPacketTypes", "login"},
        {"net.minecraft.network.protocol.configuration.ConfigurationPacketTypes", "configuration"},
        {"net.minecraft.network.protocol.game.GamePacketTypes", "play"},
        {"net.minecraft.network.protocol.common.CommonPacketTypes", "common"},
        {"net.minecraft.network.protocol.cookie.CookiePacketTypes", "cookie"},
        {"net.minecraft.network.protocol.ping.PingPacketTypes", "ping"},
    };
    /** Shared wire structures reached through byte blobs or shared by many packets. */
    static final String[][] STRUCTS = {
        {"net/minecraft/world/level/chunk/LevelChunkSection", "read"},
        {"net/minecraft/world/level/chunk/PalettedContainer", "read"},
        {"net/minecraft/network/protocol/game/ClientboundLevelChunkPacketData", "<init>"},
        {"net/minecraft/network/protocol/game/ClientboundLightUpdatePacketData", "<init>"},
        {"net/minecraft/network/protocol/game/CommonPlayerSpawnInfo", "<init>"},
        {"net/minecraft/network/protocol/game/ClientboundPlayerInfoUpdatePacket$Entry", "<init>"},
        {"net/minecraft/world/item/ItemStack", "STREAM_CODEC"},
        {"net/minecraft/world/item/ItemStack", "OPTIONAL_STREAM_CODEC"},
        {"net/minecraft/core/component/DataComponentPatch", "STREAM_CODEC"},
        {"net/minecraft/network/chat/ComponentSerialization", "STREAM_CODEC"},
        {"net/minecraft/network/chat/ChatType$Bound", "STREAM_CODEC"},
        {"net/minecraft/network/chat/RemoteChatSession$Data", "<init>"},
        {"net/minecraft/network/chat/SignedMessageBody$Packed", "<init>"},
        {"net/minecraft/network/chat/LastSeenMessages$Update", "<init>"},
        {"net/minecraft/core/RegistrySynchronization$PackedRegistryEntry", "STREAM_CODEC"},
        {"net/minecraft/world/entity/EntityType", "STREAM_CODEC"},
    };

    // ---- node model -------------------------------------------------------------

    static Map<String, Object> node(String kind, Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("k", kind);
        for (int i = 0; i + 1 < kv.length; i += 2) if (kv[i + 1] != null) m.put((String) kv[i], kv[i + 1]);
        return m;
    }
    static Map<String, Object> prim(String t) { return node("prim", "t", t); }
    static Map<String, Object> field(String name, Map<String, Object> type) {
        Map<String, Object> f = new LinkedHashMap<>();
        f.put("name", name); f.put("type", type);
        return f;
    }
    /**
     * Readers whose wire form only the writer shows: ServerboundCustomQueryAnswerPacket reads
     * "whatever is left" and discards it, but clients write writeNullable(payload).
     */
    /**
     * Fields a constructor reads as raw bytes whose contents prims.json describes: the chunk
     * data is a byte array to the packet and a run of chunk sections to whoever reads it, and
     * the section reader's count comes from outside the packet, which the bytecode of the
     * packet cannot say. The primitive named here is defined from that reader's bytecode.
     */
    static final Map<String, String> FIELD_PRIMS = Map.of(
        "ClientboundLevelChunkPacketData.buffer", "CHUNK_SECTIONS"
    );

    /** Applies FIELD_PRIMS to every struct of a finished tree, by struct name and field name. */
    @SuppressWarnings("unchecked")
    static void pinFields(Object tree) {
        if (tree instanceof Map<?, ?> m0) {
            Map<String, Object> m = (Map<String, Object>) m0;
            if ("struct".equals(m.get("k")) && m.get("fields") instanceof List<?> fs) {
                for (Object o : fs) {
                    if (!(o instanceof Map<?, ?> f0)) continue;
                    Map<String, Object> f = (Map<String, Object>) f0;
                    String pinned = FIELD_PRIMS.get(m.get("name") + "." + f.get("name"));
                    if (pinned != null) f.put("type", prim(pinned));
                }
            }
            for (Object v : m.values()) pinFields(v);
        } else if (tree instanceof List<?> l) {
            for (Object v : l) pinFields(v);
        }
    }

    static final Map<String, Map<String, Object>> READER_RULES = Map.ofEntries(
        Map.entry("net/minecraft/network/protocol/login/ServerboundCustomQueryAnswerPacket.readPayload", node("optional", "elem", prim("REST_BYTES"))),
        // entity data: index bytes terminated by 0xff, each with a serializer id and the serializer's value (GenEntityData types them)
        Map.entry("net/minecraft/network/protocol/game/ClientboundSetEntityDataPacket.unpack", prim("ENTITY_DATA")),
        // one VarInt, 0 for empty, else the value + 1 (the anonymous serializers of EntityDataSerializers)
        Map.entry("net/minecraft/network/syncher/EntityDataSerializers$2.decode", prim("OPTIONAL_VAR_INT")),
        Map.entry("net/minecraft/network/syncher/EntityDataSerializers$3.decode", prim("OPTIONAL_VAR_INT"))
    );
    /** Naming hints for values a reader produces without storing them in a field: the read method's noun. */
    static final Map<Map<String, Object>, String> hintOf = new IdentityHashMap<>();
    static int guardExpansions = 0;
    static boolean inKnownRegion = false;   // the reader is inside a branch region whose condition is known
    static Map<String, Object> opaque(String java) { return node("opaque", "java", java); }

    /** Values on the simulated operand stack. */
    sealed interface Value permits CodecV, LambdaV, ConstV, KeyV, FnV, ClassV, ArrayV, EnumFnV, RegistryElemV, MaskedV, PassedV, OffsetV, BitsV, NewV, OtherV {}
    record CodecV(Map<String, Object> n) implements Value {}
    record LambdaV(String owner, String name, String desc) implements Value {}
    record ConstV(Object v) implements Value {}
    record KeyV(String registry) implements Value {}          // Registries.X resource key
    record FnV(String kind, Object arg) implements Value {}     // ByteBufCodecs.list() etc.
    record ClassV(String internal) implements Value {}          // ldc Class
    record ArrayV(Value size) implements Value {}               // new X[size]: the size is what the reader knows
    record EnumFnV(Map<String, Object> key, String enumClass, String field) implements Value {}   // type.constructor
    record RegistryElemV(String registry, Map<String, Object> id) implements Value {}   // registry.byId(read)
    record MaskedV(Map<String, Object> of, int mask) implements Value {}   // value & mask
    // A value the caller read and passed in. A branch may test it, and that is the whole point
    // of binding it — but it is not a read of this reader, so it is no field and no value of
    // its own here.
    record PassedV(Map<String, Object> of, String sub) implements Value {
        PassedV(Map<String, Object> of) { this(of, null); }
    }
    /** One named bit field of a packed integer read as `of`, offset counted from the low bit. */
    record BitsV(Map<String, Object> of, String name, int offset, int width, boolean signed) implements Value {}
    // A value read plus a constant: `buf.readVarInt() - 1`, which a branch then compares with
    // a number, meaning a comparison of what was read with that number shifted back.
    record OffsetV(Map<String, Object> of, int delta) implements Value {}
    // An object constructed with no codec of its own, kept with what it was built from: a
    // getter asked of it later (StatType::streamCodec) answers from those arguments.
    record NewV(String cls, String desc, List<Value> args) implements Value {}
    record OtherV(String what) implements Value {}

    // ---- primitives ---------------------------------------------------------------

    /**
     * The named primitives, from gen/hand-crafted/prims.json (MC_PRIMS_JSON): which Java
     * members mean each name. A member that maps to a name stops the walker there, so the
     * packets keep their leaves' names; the definition of a name that is not a native is then
     * read from its first member with its own stop rule off (derivePrims), so that what the
     * jar says a block position or an item stack is travels with the schema, like any packet.
     */
    static final Map<String, String> MEMBER_PRIM = new HashMap<>();          // "owner.member" -> name
    static final Map<String, Map<String, Object>> PRIM_ENTRIES = new LinkedHashMap<>();   // name -> the prims.json entry
    static String derivingPrim = null;   // the name whose definition is being read: its members do not stop the walker

    static void loadPrims() throws Exception {
        String path = System.getenv("MC_PRIMS_JSON");
        if (path == null) throw new IllegalStateException("MC_PRIMS_JSON is not set: gen/hand-crafted/prims.json names the primitives");
        Object doc = fromJson(com.google.gson.JsonParser.parseString(Files.readString(Path.of(path))));
        Map<String, Object> prims = castNode((Map<?, ?>) ((Map<?, ?>) doc).get("prims"));
        for (var e : prims.entrySet()) {
            Map<String, Object> entry = castNode((Map<?, ?>) e.getValue());
            PRIM_ENTRIES.put(e.getKey(), entry);
            for (Object m : (List<?>) entry.get("members")) MEMBER_PRIM.put(String.valueOf(m), e.getKey());
        }
    }

    static Object fromJson(com.google.gson.JsonElement el) {
        if (el.isJsonNull()) return null;
        if (el.isJsonPrimitive()) {
            var p = el.getAsJsonPrimitive();
            if (p.isBoolean()) return p.getAsBoolean();
            if (p.isNumber()) { double d = p.getAsDouble(); return d == Math.rint(d) && Math.abs(d) < 1e15 ? (Object) (long) d : (Object) d; }
            return p.getAsString();
        }
        if (el.isJsonArray()) { List<Object> l = new ArrayList<>(); for (var x : el.getAsJsonArray()) l.add(fromJson(x)); return l; }
        Map<String, Object> m = new LinkedHashMap<>();
        for (var x : el.getAsJsonObject().entrySet()) m.put(x.getKey(), fromJson(x.getValue()));
        return m;
    }

    /** The primitive a Java member stands for, or null: also null for the one being derived. */
    static String primOfMember(String member) {
        String p = MEMBER_PRIM.get(member);
        return p == null || p.equals(derivingPrim) ? null : p;
    }

    /** The primitive a FriendlyByteBuf reader stands for, by method name (any buffer class). */
    static String primOfBufRead(String name) { return primOfMember("net/minecraft/network/FriendlyByteBuf." + name); }

    /**
     * The definition of every named primitive: the natives' and the hand-written ones' from
     * prims.json, the rest read from the first member's bytecode.
     */
    static Map<String, Map<String, Object>> derivePrims() {
        Map<String, Map<String, Object>> out = new LinkedHashMap<>();
        for (var e : PRIM_ENTRIES.entrySet()) {
            String name = e.getKey();
            Map<String, Object> entry = e.getValue();
            List<?> members = (List<?>) entry.get("members");
            Map<String, Object> o = new LinkedHashMap<>();
            if (entry.get("def") != null) {
                o.put("def", entry.get("def"));
                if (entry.get("hand") != null) o.put("hand", entry.get("hand"));
            } else {
                derivingPrim = name;
                try {
                    // the first member this version has: readInstant left FriendlyByteBuf, the codec constant stayed
                    Map<String, Object> def = null;
                    String from = null;
                    for (Object m : members) {
                        Map<String, Object> d = deriveMember(String.valueOf(m));
                        from = String.valueOf(m);
                        if (d != null && !(String.valueOf(d.get("java")).startsWith("no-") && "opaque".equals(d.get("k")))) { def = d; break; }
                        if (def == null) def = d;
                    }
                    if (def == null || "opaque".equals(def.get("k")) && String.valueOf(def.get("java")).startsWith("no-")) {
                        System.err.println("GenPacketSchema: primitive " + name + " has no member in this version, left out");
                        continue;
                    }
                    o.put("def", def);
                    o.put("derived", from);
                } finally {
                    derivingPrim = null;
                }
            }
            o.put("java", members);
            out.put(name, o);
        }
        return out;
    }

    /** Reads a member: a static codec field, a reader of a buffer, or a factory that returns a codec. */
    static Map<String, Object> deriveMember(String member) {
        int dot = member.lastIndexOf('.');
        String owner = member.substring(0, dot), name = member.substring(dot + 1);
        if (name.equals(name.toUpperCase(Locale.ROOT))) return codecFieldNode(owner, name, 0);
        ClassModel cm = classModel(owner);
        if (cm == null) return opaque("no-class:" + member);
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(name)) continue;
            String d = m.methodType().stringValue();
            if (owner.endsWith("FriendlyByteBuf") || d.contains("FriendlyByteBuf") || d.contains("ByteBuf;)")) return readerNode(owner, name, d, 0);
        }
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(name)) continue;
            String d = m.methodType().stringValue();
            List<Value> args = new ArrayList<>();
            for (int i = 0; i < arity(d); i++) args.add(new OtherV("param:" + i));
            Map<String, Object> n = inlineFactory(owner, name, d, args, 0);
            if (n != null) return n;
        }
        return opaque("no-reader:" + member);
    }

    // ---- entry point --------------------------------------------------------------

    record Entry(String key, String state, String className, Map<String, Object> type) {}

    public static void main(String[] args) throws Exception {
        // Enum/record reflection initialises MC classes that touch the registries.
        net.minecraft.SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        loadPrims();
        List<Entry> packets = new ArrayList<>();
        List<Entry> structs = new ArrayList<>();
        for (String[] tc : TYPES_CLASSES) {
            Class<?> types;
            try { types = Class.forName(tc[0]); }
            catch (ClassNotFoundException e) { System.err.println("GenPacketSchema: no " + tc[0] + " (skipped)"); continue; }
            for (Field f : types.getDeclaredFields()) {
                if (!Modifier.isStatic(f.getModifiers())) continue;
                if (!f.getType().getName().equals("net.minecraft.network.protocol.PacketType")) continue;
                Object pt = f.get(null);
                String flow = String.valueOf(pt.getClass().getMethod("flow").invoke(pt)).toLowerCase(Locale.ROOT);
                String name = String.valueOf(pt.getClass().getMethod("id").invoke(pt));
                Class<?> packetClass = packetClassOf(f);
                Map<String, Object> type = packetClass == null ? opaque("no-packet-class")
                    : codecFieldNode(packetClass.getName().replace('.', '/'), streamCodecField(packetClass.getName().replace('.', '/')), 0);
                packets.add(new Entry(flow + "/" + name, tc[1], packetClass == null ? null : packetClass.getName(), type));
            }
        }
        for (String[] st : STRUCTS) {
            String owner = st[0], member = st[1];
            if (classModel(owner) == null) { System.err.println("GenPacketSchema: no class " + owner + " (struct skipped)"); continue; }
            Map<String, Object> type = member.equals("<init>") || Character.isLowerCase(member.charAt(0))
                ? readerNodeByName(owner, member, 0) : codecFieldNode(owner, member, 0);
            structs.add(new Entry(shortName(owner) + "." + member, "struct", owner.replace('/', '.'), type));
        }
        packets.sort(Comparator.comparing((Entry e) -> e.state).thenComparing(e -> e.key));
        List<Entry> components = componentEntries();
        for (List<Entry> es : List.of(packets, structs, components)) for (Entry e : es) pinFields(e.type);

        StringBuilder sb = new StringBuilder("{\n  \"version\": 2,\n  \"packets\": {\n");
        writeEntries(sb, packets);
        sb.append("  },\n  \"structs\": {\n");
        writeEntries(sb, structs);
        sb.append("  },\n  \"components\": {\n");
        writeEntries(sb, components);
        Map<String, Map<String, Object>> prims = derivePrims();
        sb.append("  },\n  \"prims\": {\n");
        int pi = 0;
        for (var e : prims.entrySet()) {
            sb.append("    ").append(json(e.getKey())).append(": ").append(jsonNode(e.getValue()));
            sb.append(++pi < prims.size() ? ",\n" : "\n");
        }
        sb.append("  }\n}\n");
        Files.writeString(Path.of("packet_schema.json"), sb.toString(), StandardCharsets.UTF_8);
        long full = packets.stream().filter(e -> coverage(e.type).equals("full")).count();
        long fullC = components.stream().filter(e -> coverage(e.type).equals("full")).count();
        long derived = prims.values().stream().filter(v -> v.containsKey("derived")).count();
        System.err.printf("GenPacketSchema: %d packets (%d fully typed, %d partial) + %d structs + %d data components (%d fully typed) + %d primitives (%d read from the jar) written to packet_schema.json%n",
            packets.size(), full, packets.size() - full, structs.size(), components.size(), fullC, prims.size(), derived);
    }

    // ---- data components -----------------------------------------------------------

    static final String DATA_COMPONENTS = "net/minecraft/core/component/DataComponents";

    /**
     * One entry per data component registered in DataComponents.<clinit>:
     * register("name", builder -> builder.persistent(CODEC).networkSynchronized(STREAM_CODEC)).
     * The wire form is the stream codec given to networkSynchronized; a component without one
     * is sent as the NBT of its persistent codec (a chat component as text).
     */
    static List<Entry> componentEntries() {
        List<Entry> out = new ArrayList<>();
        ClassModel cm = classModel(DATA_COMPONENTS);
        if (cm == null) { System.err.println("GenPacketSchema: no DataComponents (components skipped)"); return out; }
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            String name = null;
            LambdaV lambda = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof ConstantInstruction ci && ci.constantValue() instanceof String s) name = s;
                else if (el instanceof InvokeDynamicInstruction idi) lambda = lambdaOf(idi);
                else if (el instanceof InvokeInstruction ii && ii.name().stringValue().equals("register")
                        && ii.owner().asInternalName().equals(DATA_COMPONENTS)) {
                    if (name != null && lambda != null) out.add(new Entry("minecraft:" + name, "component", null, componentNode(lambda)));
                    name = null;
                    lambda = null;
                }
            }
        }
        return out;
    }

    /** The wire node of one registration lambda (see componentEntries). */
    static Map<String, Object> componentNode(LambdaV l) {
        ClassModel cm = classModel(l.owner());
        if (cm == null) return opaque("component:no-class:" + l.owner());
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(l.name()) || !m.methodType().stringValue().equals(l.desc())) continue;
            Deque<Value> stack = new ArrayDeque<>();
            Value persistent = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeInstruction ii && ii.owner().asInternalName().endsWith("DataComponentType$Builder")) {
                    String method = ii.name().stringValue();
                    Value arg = arity(ii.typeSymbol().descriptorString()) == 0 ? null : (stack.isEmpty() ? null : stack.pop());
                    if (!stack.isEmpty()) stack.pop();      // the builder
                    stack.push(new OtherV("builder"));
                    if (method.equals("networkSynchronized")) return arg instanceof CodecV cv ? cv.n() : opaque("component:" + lambdaOrOther(arg));
                    if (method.equals("persistent")) persistent = arg;
                    continue;
                }
                step(el, stack, l.owner(), 0);
            }
            return persistent == null ? opaque("component:no-codec") : nbtOrText(persistent);
        }
        return opaque("component:no-lambda:" + l.name());
    }

    static void writeEntries(StringBuilder sb, List<Entry> entries) {
        for (int i = 0; i < entries.size(); i++) {
            Entry e = entries.get(i);
            sb.append("    ").append(json(e.key)).append(": {\n");
            sb.append("      \"state\": ").append(json(e.state)).append(",\n");
            sb.append("      \"class\": ").append(e.className == null ? "null" : json(e.className)).append(",\n");
            sb.append("      \"coverage\": ").append(json(coverage(e.type))).append(",\n");
            sb.append("      \"type\": ").append(jsonNode(e.type)).append(",\n");
            sb.append("      \"tokens\": [");
            List<String> toks = new ArrayList<>();
            tokens(e.type, toks, true);
            for (int j = 0; j < toks.size(); j++) { if (j > 0) sb.append(", "); sb.append(json(toks.get(j))); }
            sb.append("]\n    }").append(i + 1 < entries.size() ? ",\n" : "\n");
        }
    }

    static Class<?> packetClassOf(Field f) {
        Type t = f.getGenericType();
        if (t instanceof ParameterizedType p && p.getActualTypeArguments().length == 1) {
            Type a = p.getActualTypeArguments()[0];
            if (a instanceof Class<?> c) return c;
            if (a instanceof ParameterizedType ap && ap.getRawType() instanceof Class<?> c) return c;
            if (a instanceof WildcardType w && w.getUpperBounds().length == 1 && w.getUpperBounds()[0] instanceof Class<?> c) return c;
        }
        return null;
    }

    // ---- class access -------------------------------------------------------------

    static final Map<String, ClassModel> classCache = new HashMap<>();
    static ClassModel classModel(String internalName) {
        return classCache.computeIfAbsent(internalName, n -> {
            try (var in = GenPacketSchema.class.getClassLoader().getResourceAsStream(n + ".class")) {
                if (in == null) return null;
                return ClassFile.of().parse(in.readAllBytes());
            } catch (Exception e) { return null; }
        });
    }
    static boolean hasStaticField(String internalName, String field) {
        ClassModel cm = classModel(internalName);
        if (cm == null) return false;
        for (FieldModel f : cm.fields()) if (f.fieldName().stringValue().equals(field)) return true;
        return false;
    }
    /** STREAM_CODEC, or the first other static StreamCodec field (ClientboundCustomPayloadPacket has GAMEPLAY_ and CONFIG_STREAM_CODEC). */
    static String streamCodecField(String internalName) {
        if (hasStaticField(internalName, "STREAM_CODEC")) return "STREAM_CODEC";
        ClassModel cm = classModel(internalName);
        if (cm != null) for (FieldModel f : cm.fields()) {
            if ((f.flags().flagsMask() & 0x0008) != 0 && f.fieldType().stringValue().contains("StreamCodec")) return f.fieldName().stringValue();
        }
        return "STREAM_CODEC";
    }
    static String shortName(String internalName) { return internalName.substring(internalName.lastIndexOf('/') + 1); }

    /** The local variable live in `slot` from `bci` on: the one a store at bci - 1 assigned. */
    static String localName(Map<Integer, List<LocalVariableInfo>> table, int slot, int bci) {
        List<LocalVariableInfo> lvs = table.get(slot);
        if (lvs == null) return null;
        for (LocalVariableInfo lv : lvs) if (lv.startPc() == bci) return lv.name().stringValue();
        for (LocalVariableInfo lv : lvs) if (lv.startPc() <= bci && bci < lv.startPc() + lv.length()) return lv.name().stringValue();
        return null;
    }

    /**
     * What a static codec field carries, from its generic signature: ParticleTypes.STREAM_CODEC
     * is a StreamCodec<RegistryFriendlyByteBuf, ParticleOptions>, so the last type argument.
     * Null when the field has no signature or the argument is not a class.
     */
    static String codecPayloadClass(ClassModel cm, String field) {
        for (FieldModel f : cm.fields()) {
            if (!f.fieldName().stringValue().equals(field)) continue;
            String sig = f.findAttribute(Attributes.signature()).map(a -> a.signature().stringValue()).orElse(null);
            if (sig == null || !sig.endsWith(">;")) return null;
            String args = sig.substring(sig.indexOf('<') + 1, sig.length() - 2);
            // the last top-level argument, generics of its own included
            int depth = 0, start = 0;
            String last = null;
            for (int i = 0; i < args.length(); i++) {
                char c = args.charAt(i);
                if (c == '<') depth++;
                else if (c == '>') depth--;
                else if (c == ';' && depth == 0) { last = args.substring(start, i); start = i + 1; }
            }
            if (last == null || !last.startsWith("L")) return null;
            int lt = last.indexOf('<');
            return lt < 0 ? last.substring(1) : last.substring(1, lt);
        }
        return null;
    }
    static Class<?> loadClass(String internalName) {
        try { return Class.forName(internalName.replace('/', '.'), false, GenPacketSchema.class.getClassLoader()); }
        catch (Throwable t) { return null; }
    }
    /** The Java class an enum node came from, for the rules that need more than its values. */
    static final Map<Map<String, Object>, String> enumClassOf = new IdentityHashMap<>();

    /** An enum node that remembers which class it is, so a dispatch on it can be read. */
    static Map<String, Object> enumNode(String internalName) {
        Map<String, Object> n = node("enum", "name", shortName(internalName), "values", enumValues(internalName));
        enumClassOf.put(n, internalName);
        return n;
    }

    static List<String> enumValues(String internalName) {
        try {
            Class<?> c = loadClass(internalName);
            if (c == null || !c.isEnum()) return null;
            List<String> out = new ArrayList<>();
            for (Object o : c.getEnumConstants()) out.add(((Enum<?>) o).name());
            return out;
        } catch (Throwable t) {
            // the enum's static initialiser needs more of the game than we bootstrap;
            // fall back to the constant names from the class file (declaration order)
            ClassModel cm = classModel(internalName);
            if (cm == null) return null;
            List<String> out = new ArrayList<>();
            String self = "L" + internalName + ";";
            for (FieldModel f : cm.fields()) {
                if ((f.flags().flagsMask() & 0x4000) != 0 && f.fieldType().stringValue().equals(self)) out.add(f.fieldName().stringValue());
            }
            return out.isEmpty() ? null : out;
        }
    }
    /**
     * The node for an enum that travels as its serialized name: the constants and the names
     * they are written as. Null when the names cannot be read, so the caller leaves a hole
     * rather than inventing them.
     */
    static Map<String, Object> stringEnumNode(String internalName) {
        List<String> values = enumValues(internalName);
        List<String> names = serializedNames(internalName);
        if (values == null || names == null || values.size() != names.size()) return null;
        return node("stringenum", "name", shortName(internalName), "values", values, "names", names);
    }

    /** getSerializedName() of every constant of a StringRepresentable enum. */
    static List<String> serializedNames(String internalName) {
        try {
            Class<?> c = loadClass(internalName);
            if (c == null || !c.isEnum()) return null;
            Method m = c.getMethod("getSerializedName");
            List<String> out = new ArrayList<>();
            for (Object o : c.getEnumConstants()) out.add((String) m.invoke(o));
            return out;
        } catch (Throwable t) {
            return null;
        }
    }

    static List<String> recordComponents(String internalName) {
        try {
            Class<?> c = loadClass(internalName);
            if (c == null || !c.isRecord()) return null;
            List<String> out = new ArrayList<>();
            for (RecordComponent rc : c.getRecordComponents()) out.add(rc.getName());
            return out;
        } catch (Throwable t) {
            return null;
        }
    }
    static List<String> ctorParamNames(String internalName, String desc) { return ctorParamNames(internalName, desc, "<init>"); }
    static List<String> ctorParamNames(String internalName, String desc, String method) {
        // Unobfuscated 26.x jars keep parameter names (MethodParameters attribute).
        ClassModel cm = classModel(internalName);
        if (internalName.startsWith("java/") && method.equals("<init>")) {
            // a JDK class keeps no parameter names; its constructor stores them in its fields, in order (UUID: mostSigBits, leastSigBits)
            try {
                List<String> names = new ArrayList<>();
                for (Field f : Class.forName(internalName.replace('/', '.')).getDeclaredFields()) if (!Modifier.isStatic(f.getModifiers())) names.add(f.getName());
                return names.size() == arity(desc) ? names : null;
            } catch (Throwable t) {
                return null;
            }
        }
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(method) || !m.methodType().stringValue().equals(desc)) continue;
            for (var attr : m.attributes()) {
                if (attr instanceof java.lang.classfile.attribute.MethodParametersAttribute mp) {
                    List<String> names = new ArrayList<>();
                    for (var p : mp.parameters()) names.add(p.name().map(u -> u.stringValue()).orElse("arg" + names.size()));
                    return names;
                }
            }
        }
        return null;
    }

    // ---- <clinit> codec interpretation ---------------------------------------------

    static final Map<String, Map<String, Object>> codecFieldCache = new HashMap<>();

    /** The node for a static codec field of a class, interpreting its <clinit> segment. */
    static Map<String, Object> codecFieldNode(String owner, String field, int depth) {
        String known = primOfMember(owner + "." + field);
        if (known != null) return prim(known);
        if (field.equals("STREAM_CODEC") && !hasStaticField(owner, field)) {
            ClassModel cm0 = classModel(owner);
            if (cm0 != null && cm0.fields().isEmpty()) return node("unit");   // e.g. bundle delimiter: no payload
        }
        // ByteBufCodecs.RGB_COLOR reads three bytes and packs them with ARGB.color, so it is
        // three unsigned bytes on the wire and not the int the packed value looks like.
        if (owner.endsWith("ByteBufCodecs") && field.equals("RGB_COLOR")) {
            List<Map<String, Object>> fs = new ArrayList<>();
            for (String c : List.of("red", "green", "blue")) fs.add(field(c, prim("UNSIGNED_BYTE")));
            return node("struct", "name", "RGBColor", "fields", fs);
        }
        String key = owner + "." + field;
        if (codecFieldCache.containsKey(key)) return codecFieldCache.get(key);
        if (depth > MAX_DEPTH) return opaque("depth:" + key);
        // A codec that refers to itself (a composite slot display holds slot displays) gets a
        // ref node while it is being built; once built, the ref names what it refers to.
        Map<String, Object> ref = node("ref", "field", key);
        codecFieldCache.put(key, ref);
        Map<String, Object> result = opaque("no-clinit:" + key);
        ClassModel cm = classModel(owner);
        if (cm != null) {
            for (MethodModel m : cm.methods()) {
                if (!m.methodName().stringValue().equals("<clinit>")) continue;
                Deque<Value> stack = new ArrayDeque<>();
                for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                    if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC && fi.owner().asInternalName().equals(owner)) {
                        Value top = stack.isEmpty() ? null : stack.pop();
                        if (fi.name().stringValue().equals(field)) {
                            result = top instanceof CodecV cv ? cv.n() : opaque("not-a-codec:" + key);
                            break;
                        }
                        stack.clear();
                        continue;
                    }
                    step(el, stack, owner, depth);
                }
                break;
            }
        }
        // A dispatch built in a bootstrap class was named after that class, which names the
        // registry rather than what travels: the field's signature names the payload.
        if (cm != null && "dispatch".equals(result.get("k")) && shortName(owner).equals(result.get("name"))) {
            String payload = codecPayloadClass(cm, field);
            if (payload != null) result.put("name", shortName(payload));
        }
        if (result.get("name") != null && ("dispatch".equals(result.get("k")) || "struct".equals(result.get("k")))) {
            ref.put("name", result.get("name"));
            ref.put("of", result.get("k"));
        } else {
            ref.put("k", "opaque");
            ref.put("java", "recursive:" + key);
        }
        codecFieldCache.put(key, result);
        return result;
    }

    /** One instruction of the codec-building interpreter. */
    static void step(CodeElement el, Deque<Value> stack, String self, int depth) {
        switch (el) {
            case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> {
                String o = fi.owner().asInternalName(), n = fi.name().stringValue(), d = fi.typeSymbol().descriptorString();
                if (o.endsWith("core/registries/Registries") || d.contains("ResourceKey")) stack.push(new KeyV(registryName(n)));
                else if (d.contains("Codec") || o.endsWith("ByteBufCodecs")) {
                    Map<String, Object> cn = codecFieldNode(o, n, depth + 1);
                    String known = primOfMember(o + "." + n);
                    hintOf.putIfAbsent(cn, known != null ? hintName("read" + camel(known))
                        : o.endsWith("ByteBufCodecs") ? hintName("read" + camel(n)) : lowerFirst(shortName(o).replace("$", "")));
                    stack.push(new CodecV(cn));
                }
                else if (d.contains("IdMap") || d.contains("Registry;")) stack.push(new OtherV("idmap:" + shortName(o) + "." + n));
                else stack.push(new OtherV("static:" + o + "." + n));
            }
            case InvokeDynamicInstruction idi -> { popArgs(stack, arity(idi.typeSymbol().descriptorString())); stack.push(lambdaOf(idi)); }
            case ConstantInstruction ci -> {
                Object v = ci.constantValue();
                if (v instanceof ClassDesc cd) stack.push(new ClassV(cd.descriptorString().replaceAll("^L|;$", "")));
                else stack.push(new ConstV(v));
            }
            case InvokeInstruction ii -> invoke(ii, stack, self, depth);
            case NewObjectInstruction no -> stack.push(new OtherV("new:" + no.className().asInternalName()));
            case LoadInstruction li -> stack.push(new OtherV("local:" + li.slot()));
            case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!stack.isEmpty()) stack.push(stack.peek()); }
            case StackInstruction si when si.opcode() == Opcode.POP -> { if (!stack.isEmpty()) stack.pop(); }
            default -> { }
        }
    }

    static LambdaV lambdaOf(InvokeDynamicInstruction idi) {
        DirectMethodHandleDesc impl = null;
        MethodTypeDesc instantiated = null;
        for (ConstantDesc cd : idi.bootstrapArgs()) {
            if (cd instanceof DirectMethodHandleDesc dmh) { if (impl == null) impl = dmh; }
            else if (cd instanceof MethodTypeDesc mt) instantiated = mt;   // the last one is the instantiated type
        }
        if (impl == null) return new LambdaV("?", "?", "");
        String owner = impl.owner().descriptorString().replaceAll("^L|;$", "");
        // Operation::ordinal is a handle on java/lang/Enum, which names no enum. The type the
        // lambda was instantiated with does: its first parameter is the receiver.
        if (owner.equals("java/lang/Enum") && instantiated != null && instantiated.parameterCount() > 0) {
            String p = instantiated.parameterType(0).descriptorString();
            if (p.startsWith("L")) owner = p.substring(1, p.length() - 1);
        }
        return new LambdaV(owner, impl.methodName(), impl.lookupDescriptor());
    }

    static String registryName(String field) { return field.toLowerCase(Locale.ROOT); }

    /**
     * The local slot of every parameter of a method: slot 0 is the receiver of an instance
     * method, and a long or a double takes two slots.
     */
    static List<Integer> paramSlots(String desc, boolean isStatic) {
        List<Integer> out = new ArrayList<>();
        int slot = isStatic ? 0 : 1, i = 1;
        while (desc.charAt(i) != ')') {
            char c = desc.charAt(i);
            out.add(slot);
            if (c == 'L') { i = desc.indexOf(';', i) + 1; slot++; }
            else if (c == '[') { i++; continue; }
            else { slot += (c == 'J' || c == 'D') ? 2 : 1; i++; }
        }
        return out;
    }

    static int arity(String desc) {
        // count parameters in a method descriptor
        int n = 0, i = 1;
        while (desc.charAt(i) != ')') {
            char c = desc.charAt(i);
            if (c == 'L') { i = desc.indexOf(';', i) + 1; n++; }
            else if (c == '[') { i++; }
            else { i++; n++; }
        }
        return n;
    }

    static List<Value> popArgs(Deque<Value> stack, int n) {
        List<Value> args = new ArrayList<>();
        for (int i = 0; i < n; i++) args.add(0, stack.isEmpty() ? new OtherV("underflow") : stack.pop());
        return args;
    }

    static void invoke(InvokeInstruction ii, Deque<Value> stack, String self, int depth) {
        String owner = ii.owner().asInternalName(), name = ii.name().stringValue(), desc = ii.typeSymbol().descriptorString();
        boolean isStatic = ii.opcode() == Opcode.INVOKESTATIC;
        int n = arity(desc);
        List<Value> args = popArgs(stack, n);
        Value recv = isStatic ? null : (stack.isEmpty() ? new OtherV("underflow") : stack.pop());
        String ret = desc.substring(desc.indexOf(')') + 1);
        String o = shortName(owner);

        // Objects.requireNonNull(codec) sits between a field read and its use.
        if (owner.equals("java/util/Objects") && name.equals("requireNonNull") && !args.isEmpty()) { stack.push(args.get(0)); return; }

        // BuiltInRegistries.BLOCK.key(): the resource key of the registry a static field holds,
        // which names the registry the same way Registries.BLOCK does.
        if (name.equals("key") && args.isEmpty() && ret.contains("ResourceKey")
                && recv instanceof OtherV rv && rv.what().startsWith("idmap:BuiltInRegistries.")) {
            stack.push(new KeyV(registryName(rv.what().substring(rv.what().lastIndexOf('.') + 1))));
            return;
        }

        // StringRepresentable.fromEnum(JointType::values) is a codec that names an enum
        // constant by its serialized name, so what travels is that name as a string.
        if (owner.endsWith("util/StringRepresentable") && (name.equals("fromEnum") || name.equals("fromEnumWithMapping"))) {
            Map<String, Object> se = arg(args, 0) instanceof LambdaV l ? stringEnumNode(l.owner()) : null;
            stack.push(new CodecV(se != null ? se : opaque("string-enum:" + o + "." + name)));
            return;
        }

        // --- StreamCodec combinators -------------------------------------------
        if (owner.endsWith("codec/StreamCodec") || owner.endsWith("codec/StreamCodec$CodecOperation")) {
            switch (name) {
                case "composite" -> { stack.push(new CodecV(composite(args, self))); return; }
                case "unit" -> { stack.push(new CodecV(node("unit"))); return; }
                case "apply" -> { stack.push(new CodecV(applyFn(recv, arg(args, 0)))); return; }
                case "map", "cast", "mapStream", "dispatchToStream" -> { stack.push(recv instanceof CodecV cv ? cv : new CodecV(opaque("StreamCodec." + name))); return; }
                case "dispatch" -> {
                    Map<String, Object> key = nodeOf(recv);
                    Map<String, Object> d = node("dispatch", "name", shortName(self), "key", key);
                    if ("registry".equals(key.get("k"))) {
                        List<Map<String, Object>> cases = registryCases(String.valueOf(key.get("registry")), depth);
                        // dispatch(toKey, DebugSubscription$Update::streamCodec): a static
                        // one-argument factory wraps every element's own codec (in an optional,
                        // in a list), so a case is that factory applied to it.
                        Value f = arg(args, args.size() - 1);
                        if (cases != null && f instanceof LambdaV l && arity(l.desc()) == 1 && l.desc().endsWith("StreamCodec;")) {
                            List<Map<String, Object>> wrapped = new ArrayList<>();
                            for (Map<String, Object> c : cases) {
                                Map<String, Object> t = inlineMethod(l.owner(), l.name(), l.desc(), List.of(new CodecV(castNode((Map<?, ?>) c.get("type")))), depth + 1, true);
                                Map<String, Object> w = new LinkedHashMap<>(c);
                                if (t != null) w.put("type", t);
                                wrapped.add(w);
                            }
                            cases = wrapped;
                        }
                        // dispatch(Stat::getType, StatType::streamCodec) over elements with no
                        // codec of their own: the getter is asked of each element as its
                        // bootstrap constructed it. StatType builds its codec from the registry
                        // it was given, so `mined` reads a block id and `killed` an entity type id.
                        if (cases != null && f instanceof LambdaV l && arity(l.desc()) == 0 && l.desc().endsWith("StreamCodec;")) {
                            List<Map<String, Object>> bound = new ArrayList<>();
                            for (Map<String, Object> c : cases) {
                                NewV made = caseElemOf.get(c);
                                Map<String, Object> t = made != null ? getterFieldNode(made.cls(), l.name(), made, depth + 1) : null;
                                Map<String, Object> w = new LinkedHashMap<>(c);
                                if (t != null) w.put("type", t);
                                bound.add(w);
                            }
                            cases = bound;
                        }
                        if (cases != null) d.put("cases", cases);
                    }
                    // dispatch(Stat::getType, StatType::streamCodec): the codec of an element
                    // is a field its class builds the same way for every element — here an id
                    // in whichever registry the element wraps — so every case reads that one
                    // shape and the dispatch is a struct of the key and it.
                    // dispatch(PositionPath::type, Type::streamCodec): the constants of the
                    // enum the key names carry the codec of their own case.
                    if (d.get("cases") == null && "enum".equals(key.get("k")) && enumClassOf.containsKey(key)) {
                        // Which of the constant's fields, though: FilterMask$Type carries both a
                        // data codec and a wire one, and only the getter the dispatch was given
                        // says which of the two this is.
                        String held = arg(args, args.size() - 1) instanceof LambdaV g0 && g0.owner().equals(enumClassOf.get(key))
                                ? getterField(g0.owner(), g0.name()) : null;
                        List<Map<String, Object>> cases = enumDispatchCases(enumClassOf.get(key), held, depth + 1);
                        if (cases != null) d.put("cases", cases);
                    }
                    if (d.get("cases") == null && arg(args, args.size() - 1) instanceof LambdaV g
                            && arity(g.desc()) == 0 && g.desc().endsWith("StreamCodec;")) {
                        Map<String, Object> uniform = getterFieldNodeResolved(g.owner(), g.name(), depth + 1);
                        if (uniform != null && !hasHole(uniform)) {
                            List<Map<String, Object>> fs = new ArrayList<>();
                            fs.add(field("type", key));
                            fs.add(field(hintOf.getOrDefault(uniform, "value"), uniform));
                            stack.push(new CodecV(node("struct", "name", shortName(self), "fields", fs)));
                            return;
                        }
                    }
                    stack.push(new CodecV(d)); return;
                }
                case "of", "ofMember" -> { stack.push(new CodecV(readerNode(arg(args, args.size() - 1), depth))); return; }
                case "recursive" -> {
                    // recursive(op) is the fixpoint of op: the codec op builds when handed
                    // itself. Most are not recursive at all and exist only so a class can
                    // initialise lazily — DataComponentType's ignores what it is handed and
                    // returns a registry id — so interpret op with a placeholder for itself
                    // and keep the answer when the placeholder is not in it.
                    Value f = arg(args, args.size() - 1);
                    Map<String, Object> itself = opaque("recursive-self");
                    Map<String, Object> made = f instanceof LambdaV l && arity(l.desc()) == 1
                            ? inlineMethod(l.owner(), l.name(), l.desc(), List.of(new CodecV(itself)), depth + 1, true) : null;
                    if (made == null) { stack.push(new CodecV(opaque("StreamCodec.recursive"))); return; }
                    // One that really is recursive — a mob effect's hidden effect is another
                    // one — says so with a ref back to the struct being defined.
                    if (holds(made, itself)) {
                        String self0 = "struct".equals(made.get("k")) ? String.valueOf(made.get("name")) : null;
                        if (self0 == null || !replaceNode(made, itself, node("ref", "of", "struct", "name", self0))) {
                            stack.push(new CodecV(opaque("StreamCodec.recursive")));
                            return;
                        }
                    }
                    stack.push(new CodecV(made));
                    return;
                }
                case "codec" -> { stack.push(new CodecV(readerNode(arg(args, args.size() - 1), depth))); return; }
                default -> { }
            }
        }
        if (owner.endsWith("protocol/Packet") && name.equals("codec")) { stack.push(new CodecV(readerNode(arg(args, 1), depth))); return; }
        // --- ByteBufCodecs factories -------------------------------------------
        if (owner.endsWith("ByteBufCodecs")) {
            switch (name) {
                case "list" -> { stack.push(new FnV("list", args.isEmpty() ? null : constOf(arg(args, 0)))); return; }
                case "collection" -> {
                    if (args.size() == 1) { stack.push(new FnV("list", null)); return; }   // collection(IntFunction) is a list wrapper
                    stack.push(new CodecV(node("list", "elem", nodeOf(arg(args, 1)), "max", args.size() > 2 ? constOf(arg(args, 2)) : null))); return;
                }
                case "optional" -> { stack.push(new CodecV(node("optional", "elem", nodeOf(arg(args, 0))))); return; }
                case "map" -> {
                    if (args.size() == 1) { stack.push(new FnV("map", null)); return; }
                    stack.push(new CodecV(node("map", "key", nodeOf(arg(args, 1)), "val", nodeOf(arg(args, 2)), "max", args.size() > 3 ? constOf(arg(args, 3)) : null))); return;
                }
                case "registry" -> { stack.push(new CodecV(node("registry", "registry", keyOf(arg(args, 0))))); return; }
                case "holderRegistry" -> { stack.push(new CodecV(node("holder", "registry", keyOf(arg(args, 0))))); return; }
                case "holder" -> { stack.push(new CodecV(node("holder", "registry", keyOf(arg(args, 0)), "direct", nodeOf(arg(args, 1))))); return; }
                case "holderSet" -> { stack.push(new CodecV(node("holderset", "registry", keyOf(arg(args, 0))))); return; }
                case "idMapper" -> { stack.push(new CodecV(idMapper(args))); return; }
                case "stringUtf8" -> { stack.push(new CodecV(node("string", "max", constOf(arg(args, 0))))); return; }
                case "byteArray" -> { stack.push(new CodecV(node("prim", "t", "BYTE_ARRAY", "max", constOf(arg(args, 0))))); return; }
                case "fixedBitSet" -> { stack.push(new CodecV(node("prim", "t", "FIXED_BIT_SET", "bits", constOf(arg(args, 0))))); return; }
                case "fromCodec", "fromCodecTrusted", "fromCodecWithRegistries", "fromCodecWithRegistriesTrusted", "compoundTagCodec", "tagCodec" -> { stack.push(new CodecV(nbtOrText(args.isEmpty() ? null : arg(args, 0)))); return; }
                case "lengthPrefixed", "registryFriendlyLengthPrefixed" -> { stack.push(new FnV("lengthPrefixed", constOf(arg(args, 0)))); return; }
                case "either" -> { stack.push(new CodecV(node("either", "left", nodeOf(arg(args, 0)), "right", nodeOf(arg(args, 1))))); return; }
                // optionalTagCodec has no boolean of its own: it is readNbt, whose TAG_End is
                // the absent value, so the optionality is inside the tag.
                case "optionalTagCodec" -> { stack.push(new CodecV(node("nbt"))); return; }
                default -> {
                    String t = primOfMember(owner + "." + name);
                    if (t != null) { stack.push(new CodecV(node("prim", "t", t, "max", constOf(arg(args, 0))))); return; }
                    Map<String, Object> made = inlineFactory(owner, name, desc, args, depth + 1);
                    stack.push(new CodecV(made != null ? made : opaque("ByteBufCodecs." + name))); return;
                }
            }
        }
        if (owner.endsWith("resources/ResourceKey") && name.equals("streamCodec")) { stack.push(new CodecV(node("resourcekey", "registry", keyOf(arg(args, 0))))); return; }
        // validatedStreamCodec(x) only refuses an empty stack; it reads whatever x reads, and
        // x is not always the trusted codec: the creative mode slot packet passes the
        // untrusted one, whose components each carry their length.
        if (owner.endsWith("world/item/ItemStack") && name.equals("validatedStreamCodec")) {
            stack.push(arg(args, 0) instanceof CodecV c ? c : new CodecV(prim("ITEM_STACK")));
            return;
        }
        if (owner.endsWith("resources/ResourceKey") && name.equals("createRegistryKey")) { stack.push(new KeyV("dynamic")); return; }
        if (name.equals("streamCodec") && isComponentType(recv)) { stack.push(new CodecV(componentDispatch(nodeOfValue(recv)))); return; }
        if (name.equals("enumStreamCodec") || (name.equals("streamCodec") && ret.contains("StreamCodec"))) {
            // an enum's codec, or a static factory (Filterable.streamCodec(c), TypedEntityData.streamCodec(c), …)
            // whose body is interpreted with the arguments bound
            if (isStatic && enumValues(owner) == null) {
                Map<String, Object> inlined = inlineFactory(owner, name, desc, args, depth);
                if (inlined != null) { stack.push(new CodecV(inlined)); return; }
            }
            stack.push(new CodecV(codecOfOwner(owner, name, depth)));
            return;
        }
        // --- custom payloads: a channel id, then the rest of the packet (the payload codecs are keyed by channel)
        if (owner.endsWith("custom/CustomPacketPayload") && name.equals("codec")) {
            stack.push(new CodecV(node("struct", "name", "CustomPacketPayload", "fields", List.of(
                field("channel", prim("IDENTIFIER")), field("data", prim("REST_BYTES"))))));
            return;
        }
        // --- anything else returning a codec: a static factory is interpreted, else opaque
        if (ret.contains("StreamCodec")) {
            Map<String, Object> inlined = isStatic ? inlineFactory(owner, name, desc, args, depth) : null;
            stack.push(new CodecV(inlined != null ? inlined : opaque(o + "." + name)));
            return;
        }
        if (owner.endsWith("syncher/EntityDataSerializer") && name.equals("forValueType")) { stack.push(arg(args, 0)); return; }
        if (name.equals("<init>") && recv instanceof OtherV ov && ov.what().startsWith("new:")) {
            // A constructed object stands for its stream codec when it carries one:
            //  - new RecipeDisplay$Type(MAP_CODEC, STREAM_CODEC): the constructor argument typed StreamCodec;
            //  - new Vec3$1() / new ByteBufCodecs$20(): an anonymous StreamCodec whose decode(buf) is the reader;
            //  - new EntityDataSerializers$1() / new FixedFormat$1(): a codec() or streamCodec() method returns it.
            String cls = ov.what().substring(4);
            Value fromCtor = null;
            int pi = 0;
            for (int i = 1; desc.charAt(i) != ')'; pi++) {
                char c = desc.charAt(i);
                int end = c == 'L' ? desc.indexOf(';', i) + 1 : c == '[' ? i + 1 : i + 1;
                if (c == '[') { i = end; pi--; continue; }
                if (desc.substring(i, end).contains("network/codec/StreamCodec") && pi < args.size() && args.get(pi) instanceof CodecV cv) fromCtor = cv;
                i = end;
            }
            String decodeDesc = methodDesc(cls, "decode", "ByteBuf");
            if (decodeDesc != null) fromCtor = null;   // an anonymous codec built with a codec: its decode reads, with that codec as a field
            String[] codecMethod = fromCtor == null && decodeDesc == null ? codecMethodOf(cls) : null;
            if (fromCtor != null || decodeDesc != null || codecMethod != null) {
                if (!stack.isEmpty() && stack.peek() instanceof OtherV top && top.what().equals(ov.what())) stack.pop();
                if (fromCtor != null) stack.push(fromCtor);
                else if (decodeDesc != null) {
                    Map<String, Value> bound = capturedFields(cls, desc, args);
                    if (bound.isEmpty()) stack.push(new CodecV(readerNode(cls, "decode", decodeDesc, depth + 1)));
                    else {
                        // the answer depends on what was captured, so it is not cached
                        Map<String, Value> outer = new HashMap<>(captured);
                        captured.putAll(bound);
                        try { stack.push(new CodecV(interpretReader(cls, "decode", decodeDesc, depth + 1))); }
                        finally { captured.clear(); captured.putAll(outer); }
                    }
                }
                else {
                    Map<String, Object> n0 = inlineMethod(cls, codecMethod[0], codecMethod[1], List.of(), depth, false);
                    // a streamCodec() that reads a field the constructor built: kept whole,
                    // so a dispatch can ask the getter with the constructor's arguments bound
                    stack.push(n0 != null ? new CodecV(n0) : new NewV(cls, desc, args));
                }
            } else if (!stack.isEmpty() && stack.peek() instanceof OtherV top && top.what().equals(ov.what())) {
                stack.pop();
                stack.push(new NewV(cls, desc, args));
            }
            return;
        }
        if (!ret.equals("V")) stack.push(new OtherV(o + "." + name));
    }

    /** The values an anonymous class's constructor stores in its fields (val$patchCodec), by "class.field". */
    static final Map<String, Value> captured = new HashMap<>();

    static Map<String, Value> capturedFields(String cls, String ctorDesc, List<Value> args) {
        Map<String, Value> out = new HashMap<>();
        MethodModel init = findMethod(cls, "<init>", ctorDesc);
        if (init == null) return out;
        List<Integer> slots = paramSlots(ctorDesc, false);
        Integer loaded = null;
        for (CodeElement el : init.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            if (el instanceof LoadInstruction li) loaded = li.slot();
            else if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTFIELD && loaded != null) {
                int at = slots.indexOf(loaded);
                if (at >= 0 && at < args.size() && !(args.get(at) instanceof OtherV)) out.put(cls + "." + fi.name().stringValue(), args.get(at));
                loaded = null;
            } else if (el instanceof Instruction) loaded = null;
        }
        return out;
    }

    /** A zero-argument method of a class returning a StreamCodec (streamCodec(), codec()): {name, desc}, or null. */
    static String[] codecMethodOf(String internalName) {
        ClassModel cm = classModel(internalName);
        if (cm == null) return null;
        for (String candidate : new String[]{"streamCodec", "codec"}) {
            for (MethodModel m : cm.methods()) {
                String d = m.methodType().stringValue();
                if (m.methodName().stringValue().equals(candidate) && d.startsWith("()") && d.contains("network/codec/StreamCodec")) return new String[]{candidate, d};
            }
        }
        return null;
    }

    /** The descriptor of the first method of a class with this name whose parameters mention the type, or null. */
    static String methodDesc(String internalName, String method, String paramType) {
        ClassModel cm = classModel(internalName);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            String d = m.methodType().stringValue();
            if (m.methodName().stringValue().equals(method) && d.substring(0, d.indexOf(')')).contains(paramType)) return d;
        }
        return null;
    }

    /**
     * The class that registers a registry's elements — where register("block", …,
     * BlockParticleOption::streamCodec) is — read from BuiltInRegistries.<clinit>: every
     * registry is created there as registerSimple(Registries.X, bootstrap) (or a defaulted
     * variant), and the bootstrap is either Class::bootstrap, whose owner is the class, or a
     * lambda of BuiltInRegistries whose body touches the class's first constant
     * (registry -> ParticleTypes.BLOCK). A dispatch on such a registry has one case per
     * element, its codec interpreted with the element type bound.
     */
    static final Map<String, String> registryBootstrapCache = new HashMap<>();

    static String registryBootstrap(String registry) {
        if (registryBootstrapCache.isEmpty()) {
            String owner = "net/minecraft/core/registries/BuiltInRegistries";
            ClassModel cm = classModel(owner);
            if (cm != null) for (MethodModel m : cm.methods()) {
                if (!m.methodName().stringValue().equals("<clinit>")) continue;
                String key = null;
                for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                    if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETSTATIC && fi.owner().asInternalName().endsWith("core/registries/Registries")) {
                        key = registryName(fi.name().stringValue());
                    } else if (el instanceof InvokeDynamicInstruction idi && key != null) {
                        LambdaV l = lambdaOf(idi);
                        String cls = l.owner().equals(owner) ? firstStaticOwner(owner, l.name()) : l.owner();
                        if (cls != null) {
                            registryBootstrapCache.putIfAbsent(key, cls);
                            // the method reference itself (MaterialRules::bootstrapRules, next
                            // to bootstrapConditions in the same class); a lambda of
                            // BuiltInRegistries' own leaves the class's convention, "bootstrap"
                            if (!l.owner().equals(owner)) registryBootstrapMethodCache.putIfAbsent(key, l.name());
                        }
                        key = null;
                    }
                }
                break;
            }
        }
        return registryBootstrapCache.get(registry);
    }

    static final Map<String, String> registryBootstrapMethodCache = new HashMap<>();

    /** The static method of registryBootstrap's class that registers the elements ("bootstrap" unless a method reference names another). */
    static String registryBootstrapMethod(String registry) {
        registryBootstrap(registry);
        return registryBootstrapMethodCache.getOrDefault(registry, "bootstrap");
    }

    static String registryBootstrapMethodByLocation(String location) {
        registryBootstrapByLocation(location);
        String field = registryLocationField.get(location);
        return field == null ? "bootstrap" : registryBootstrapMethod(registryName(field));
    }

    static final Map<String, String> registryLocationField = new HashMap<>();

    /**
     * The same, by the registry's location (`worldgen/block_state_provider_type`, what an NBT
     * dispatch names): Registries.<clinit> creates each key as createRegistryKey("<location>")
     * and stores it in the field the bootstrap map is keyed by.
     */
    static String registryBootstrapByLocation(String location) {
        if (registryLocationField.isEmpty()) {
            ClassModel cm = classModel("net/minecraft/core/registries/Registries");
            if (cm != null) for (MethodModel m : cm.methods()) {
                if (!m.methodName().stringValue().equals("<clinit>")) continue;
                String last = null;
                for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                    if (el instanceof ConstantInstruction ci && ci.constantValue() instanceof String str) last = str;
                    else if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC && fi.typeSymbol().descriptorString().contains("ResourceKey") && last != null) {
                        registryLocationField.putIfAbsent(last, fi.name().stringValue());
                        last = null;
                    }
                }
                break;
            }
        }
        String field = registryLocationField.get(location);
        return field == null ? null : registryBootstrap(registryName(field));
    }

    /** The class whose static field a method reads first, or null. */
    static String firstStaticOwner(String owner, String method) {
        ClassModel cm = classModel(owner);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(method)) continue;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETSTATIC && fi.owner().asInternalName().startsWith("net/minecraft/")) return fi.owner().asInternalName();
            }
            return null;
        }
        return null;
    }

    /** The index of the first parameter of a descriptor whose type contains needle, or -1. */
    static int paramOfType(String desc, String needle) {
        int idx = 0;
        for (int i = 1; desc.charAt(i) != ')'; idx++) {
            char c = desc.charAt(i);
            while (c == '[') { i++; c = desc.charAt(i); }
            int end = c == 'L' ? desc.indexOf(';', i) + 1 : i + 1;
            if (desc.substring(i, end).contains(needle)) return idx;
            i = end;
        }
        return -1;
    }

    static final Map<String, List<Map<String, Object>>> registryCasesCache = new HashMap<>();

    static List<Map<String, Object>> registryCases(String registry, int depth) {
        String cls = registryBootstrap(registry);
        if (cls == null) return null;
        if (registryCasesCache.containsKey(registry)) return registryCasesCache.get(registry);
        registryCasesCache.put(registry, null);   // recursion guard
        ClassModel cm = classModel(cls);
        if (cm == null) return null;
        List<Map<String, Object>> cases = new ArrayList<>();
        // registrations sit in a static bootstrap(Registry) method or in <clinit>; a class may
        // have both, so whichever registers anything wins
        List<String> where = new ArrayList<>();
        for (MethodModel m : cm.methods()) if (m.methodName().stringValue().equals("bootstrap") && (m.flags().flagsMask() & 0x0008) != 0) where.add("bootstrap");
        where.add("<clinit>");
        for (String bootstrapMethod : where) {
            if (!cases.isEmpty()) break;
            for (MethodModel m : cm.methods()) {
                if (!m.methodName().stringValue().equals(bootstrapMethod)) continue;
                walkRegistrations(registry, cls, m, new HashMap<>(), cases, depth);
                break;
            }
        }
        List<Map<String, Object>> out = cases.isEmpty() ? null : cases;
        registryCasesCache.put(registry, out);
        return out;
    }

    /** The element a case was registered as, when it carries no codec of its own and was kept whole. */
    static final Map<Map<String, Object>, NewV> caseElemOf = new IdentityHashMap<>();

    /**
     * One bootstrap method, registration by registration. A static helper of the bootstrap
     * class that is handed a name is walked too, with its arguments bound — Stats registers
     * every stat type through makeRegistryStatType(name, registry) — and a registration into
     * some other registry on the way (the custom stats, into custom_stat) is not a case here.
     */
    static void walkRegistrations(String registry, String cls, MethodModel m, Map<Integer, Value> locals, List<Map<String, Object>> cases, int depth) {
        Deque<Value> stack = new ArrayDeque<>();
        for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESTATIC && ii.name().stringValue().startsWith("register")) {
                List<Value> a = popArgs(stack, arity(ii.typeSymbol().descriptorString()));
                String ret = ii.typeSymbol().descriptorString();
                ret = ret.substring(ret.indexOf(')') + 1);
                if (arg(a, 0) instanceof OtherV r0 && r0.what().startsWith("idmap:BuiltInRegistries.")
                        && !registryName(r0.what().substring(r0.what().lastIndexOf('.') + 1)).equals(registry)) {
                    if (!ret.equals("V")) stack.push(new OtherV("registered-elsewhere"));
                    continue;
                }
                String name = null;
                int nameAt = -1;
                for (int i = 0; i < a.size(); i++) if (a.get(i) instanceof ConstV c && c.v() instanceof String s0) { name = s0; nameAt = i; break; }
                if (name == null) { stack.push(new OtherV("register")); continue; }
                Map<String, Object> type = null;
                NewV elem = null;
                // register(name, CODEC, STREAM_CODEC) passes both the data codec and the
                // wire one, and a MapCodec looks like a codec to the walker, so take the
                // argument the method itself declares as a StreamCodec before guessing.
                int wireAt = paramOfType(ii.typeSymbol().descriptorString(), "network/codec/StreamCodec");
                if (wireAt > nameAt && arg(a, wireAt) instanceof CodecV wc) type = wc.n();
                for (int i = nameAt + 1; i < a.size() && type == null; i++) {
                    Value v = a.get(i);
                    if (v instanceof LambdaV l && l.desc().endsWith("StreamCodec;")) {
                        // register("block", …, BlockParticleOption::streamCodec): the codec with the type bound
                        Map<String, Object> t = inlineMethod(l.owner(), l.name(), l.desc(), List.of(new OtherV("type")), depth + 1, false);
                        type = t != null ? t : opaque("registry-case:" + shortName(l.owner()) + "." + l.name());
                    } else if (v instanceof CodecV c) {
                        type = c.n();   // new BlockPositionSource$Type(): its streamCodec()
                    } else if (v instanceof OtherV ov && ov.what().startsWith("static:")) {
                        // Registry.register(registry, "furnace", FurnaceRecipeDisplay.TYPE): the field's value carries the codec
                        String ref = ov.what().substring(7);
                        int dot = ref.lastIndexOf('.');
                        type = codecFieldNode(ref.substring(0, dot), ref.substring(dot + 1), depth + 1);
                    } else if (v instanceof NewV nv) {
                        // new StatType(registry, name): what it reads is whatever the dispatch
                        // asks of it, which only the dispatch knows
                        type = opaque("registry-element:" + shortName(nv.cls()));
                        elem = nv;
                    }
                }
                if (type == null) type = a.size() - nameAt <= 2 ? node("unit") : opaque("registry-case:" + name);
                Map<String, Object> c = node("case", "id", name.contains(":") ? name : "minecraft:" + name, "type", type);
                if (elem != null) caseElemOf.put(c, elem);
                cases.add(c);
                stack.push(new OtherV("register:" + name));
                continue;
            }
            if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESTATIC && ii.owner().asInternalName().equals(cls)) {
                String desc = ii.typeSymbol().descriptorString();
                List<Value> a = popArgs(stack, arity(desc));
                MethodModel helper = findMethod(cls, ii.name().stringValue(), desc);
                boolean named = a.stream().anyMatch(v -> v instanceof ConstV c && c.v() instanceof String);
                if (helper != null && named && depth < MAX_DEPTH) {
                    Map<Integer, Value> inner = new HashMap<>();
                    List<Integer> slots = paramSlots(desc, true);
                    for (int i = 0; i < slots.size() && i < a.size(); i++) inner.put(slots.get(i), a.get(i));
                    walkRegistrations(registry, cls, helper, inner, cases, depth + 1);
                }
                if (!desc.endsWith(")V")) stack.push(new OtherV("helper:" + ii.name().stringValue()));
                continue;
            }
            if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) { stack.clear(); continue; }
            if (el instanceof LoadInstruction li && locals.containsKey(li.slot())) { stack.push(locals.get(li.slot())); continue; }
            if (el instanceof StoreInstruction st) { if (!stack.isEmpty()) locals.put(st.slot(), stack.pop()); continue; }
            step(el, stack, cls, depth);
        }
    }

    static final Map<String, List<Map<String, Object>>> registryMethodCache = new HashMap<>();

    /**
     * The cases of a dispatch made by calling a method on an element taken out of a registry:
     * every registration in the registry's bootstrap class, with what that method reads on the
     * class the element was registered as. This is how the command argument types travel — an
     * id in `command_argument_type`, then whatever that type's `deserializeFromNetwork` reads.
     *
     * The bootstrap is walked with just enough of an interpreter to see two things: the name a
     * registration was given, and the class of the object registered under it — a `new X` or
     * the class a static factory returns. Null when the registry has no bootstrap class listed
     * or nothing was registered, so the caller leaves the dispatch without cases.
     */
    static List<Map<String, Object>> registryMethodCases(String registry, String method, int depth) {
        String cls = registryBootstrap(registry);
        if (cls == null) return null;
        String key = registry + "." + method;
        if (registryMethodCache.containsKey(key)) return registryMethodCache.get(key);
        registryMethodCache.put(key, null);   // recursion guard
        ClassModel cm = classModel(cls);
        if (cm == null) return null;
        List<Map<String, Object>> cases = new ArrayList<>();
        for (MethodModel m : cm.methods()) {
            String mn = m.methodName().stringValue();
            if (!mn.equals("bootstrap") && !mn.equals("<clinit>")) continue;
            Deque<Value> stack = new ArrayDeque<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                switch (el) {
                    case ConstantInstruction ci -> stack.push(new ConstV(ci.constantValue()));
                    case NewObjectInstruction no -> stack.push(new OtherV("new:" + no.className().asInternalName()));
                    case InvokeDynamicInstruction idi -> { popArgs(stack, arity(idi.typeSymbol().descriptorString())); stack.push(new OtherV("lambda")); }
                    case InvokeInstruction ii -> {
                        String iname = ii.name().stringValue(), idesc = ii.typeSymbol().descriptorString();
                        boolean isStatic = ii.opcode() == Opcode.INVOKESTATIC;
                        List<Value> a = popArgs(stack, arity(idesc));
                        if (!isStatic && !stack.isEmpty()) stack.pop();   // the receiver, or the object a constructor ran on
                        String iret = idesc.substring(idesc.indexOf(')') + 1);
                        if (isStatic && iname.startsWith("register")) {
                            String name = null;
                            int nameAt = -1;
                            for (int i = 0; i < a.size(); i++) if (a.get(i) instanceof ConstV c && c.v() instanceof String s0) { name = s0; nameAt = i; break; }
                            // the element is the last object argument: register(registry, name,
                            // class, info), and the class may itself have gone through a helper
                            // that returns a Class, which would otherwise look like the element
                            String elem = null;
                            for (int i = a.size() - 1; i > nameAt && elem == null; i--) if (a.get(i) instanceof OtherV ov && ov.what().startsWith("new:")) elem = ov.what().substring(4);
                            if (name != null && elem != null) {
                                Map<String, Object> t = readerNodeByName(elem, method, depth + 1);
                                // An element whose reader reads nothing carries no payload: the
                                // singleton argument types hand back a template they already
                                // hold. That is a unit, not a hole.
                                if ("opaque".equals(t.get("k")) && String.valueOf(t.get("java")).startsWith("empty-reader:")) t = node("unit");
                                cases.add(node("case", "id", name.contains(":") ? name : "minecraft:" + name, "type", t));
                            }
                            stack.push(new OtherV("registered"));
                            break;
                        }
                        // a static factory stands for the class it returns: SingletonArgumentInfo.contextFree(…)
                        if (!iret.equals("V")) stack.push(iret.startsWith("L") ? new OtherV("new:" + iret.substring(1, iret.length() - 1)) : new OtherV("v"));
                    }
                    case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!stack.isEmpty()) stack.push(stack.peek()); }
                    case StackInstruction si when si.opcode() == Opcode.POP -> { if (!stack.isEmpty()) stack.pop(); }
                    case FieldInstruction fi when fi.opcode() == Opcode.PUTSTATIC -> stack.clear();
                    case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> stack.push(new OtherV("static:" + fi.owner().asInternalName() + "." + fi.name().stringValue()));
                    case LoadInstruction li -> stack.push(new OtherV("local:" + li.slot()));
                    default -> { }
                }
            }
            if (!cases.isEmpty()) break;
        }
        List<Map<String, Object>> out = cases.isEmpty() ? null : cases;
        registryMethodCache.put(key, out);
        return out;
    }

    /** The codec a static factory method returns, interpreting its body with the parameters bound to the call's arguments. */
    static Map<String, Object> inlineFactory(String owner, String name, String desc, List<Value> args, int depth) {
        return inlineMethod(owner, name, desc, args, depth, true);
    }
    /**
     * A method by name and descriptor, on a class or on one of its superclasses. A call names
     * the class it was written in, which for an inherited static is a subclass of the one that
     * declares it: Target.createDebugStreamCodec is Node's, and ClientboundMoveEntityPacket$Pos
     * .unpackStepCount is its packet's.
     */
    static MethodModel findMethod(String owner, String name, String desc) {
        for (String cls = owner; cls != null; ) {
            ClassModel cm = classModel(cls);
            if (cm == null) return null;
            for (MethodModel m : cm.methods()) {
                if (m.methodName().stringValue().equals(name) && m.methodType().stringValue().equals(desc)) return m;
            }
            cls = cm.superclass().map(c -> c.asInternalName()).orElse(null);
        }
        return null;
    }

    static Map<String, Object> inlineMethod(String owner, String name, String desc, List<Value> args, int depth, boolean requireStatic) {
        if (depth > MAX_DEPTH) return null;
        MethodModel found = findMethod(owner, name, desc);
        if (found == null) return null;
        for (MethodModel m : List.of(found)) {
            boolean isStatic = (m.flags().flagsMask() & 0x0008) != 0;
            if (requireStatic && !isStatic) return null;
            Map<Integer, Value> locals = new HashMap<>();
            int slot = isStatic ? 0 : 1, ai = 0;
            if (!isStatic) locals.put(0, new OtherV("this"));
            for (int i = 1; desc.charAt(i) != ')'; ) {
                char c = desc.charAt(i);
                if (c == '[') { i++; continue; }
                int width = (c == 'J' || c == 'D') ? 2 : 1;
                i = c == 'L' ? desc.indexOf(';', i) + 1 : i + 1;
                if (ai < args.size()) locals.put(slot, args.get(ai));
                slot += width;
                ai++;
            }
            Deque<Value> stack = new ArrayDeque<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                switch (el) {
                    case LoadInstruction li -> stack.push(locals.getOrDefault(li.slot(), new OtherV("local:" + li.slot())));
                    case StoreInstruction st -> { if (!stack.isEmpty()) locals.put(st.slot(), stack.pop()); }
                    case ReturnInstruction ri when ri.opcode() == Opcode.ARETURN -> {
                        Value top = stack.isEmpty() ? null : stack.pop();
                        return top instanceof CodecV cv ? cv.n() : null;
                    }
                    default -> step(el, stack, owner, depth + 1);
                }
            }
            return null;
        }
        return null;
    }

    /** The instance field a plain getter reads, or null when it reads none or more than one. */
    static String getterField(String owner, String method) {
        ClassModel cm = classModel(owner);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(method)) continue;
            String field = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETFIELD && fi.owner().asInternalName().equals(owner)) {
                    if (field != null) return null;   // more than one field read: not a plain getter
                    field = fi.name().stringValue();
                }
            }
            return field;
        }
        return null;
    }

    /**
     * Which constructor argument a field is assigned from, as an index into the arguments a
     * call to that constructor passes. A constant that carries two codecs is only told apart
     * by which of them the dispatch asked for.
     */
    static int ctorArgOfField(String cls, String field) {
        ClassModel cm = classModel(cls);
        if (cm == null) return -1;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<init>")) continue;
            List<Integer> slots = paramSlots(m.methodType().stringValue(), false);
            int loaded = -1;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof LoadInstruction li) { loaded = li.slot(); continue; }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTFIELD
                        && fi.name().stringValue().equals(field) && fi.owner().asInternalName().equals(cls)) {
                    // The last argument loaded before the assignment, which is the one the field
                    // comes from even when it is wrapped on the way in: FilterMask$Type memoizes
                    // the supplier it is given before storing it.
                    int i = slots.indexOf(loaded);
                    if (i >= 0) return i;
                }
            }
        }
        return -1;
    }

    /**
     * The codec an instance getter returns, when the field it returns is assigned in exactly
     * one constructor: StatType.streamCodec() hands back a codec its constructor built from
     * the registry the stat type wraps, which is the same shape whichever stat type it is.
     * Null when the field is set in more than one place, or by something that is not a codec —
     * then the shape may differ per element and saying otherwise would be a guess.
     */
    /**
     * The same, following an abstract getter to the classes that implement it. A dispatch may
     * name a getter on an interface — DataComponentPredicate$Type.singleStreamCodec() — and then
     * the codec lives on whatever implements it. Every implementation has to build the same
     * shape: a family whose members read different bytes is a dispatch with real cases, not one
     * value, and stays a hole rather than being described as the first member found.
     */
    static Map<String, Object> getterFieldNodeResolved(String owner, String method, int depth) {
        Map<String, Object> n = getterFieldNode(owner, method, depth);
        if (n != null || !abstractMethod(owner, method)) return n;
        Map<String, Object> found = null;
        for (String impl : nestMembers(owner)) {
            if (impl.equals(owner)) continue;
            Map<String, Object> m = getterFieldNode(impl, method, depth);
            if (m == null) continue;
            if (found != null && !sameShape(found, m)) return null;
            found = m;
        }
        return found;
    }

    /** Whether a class declares that method with no body, which is what an interface getter is. */
    static boolean abstractMethod(String owner, String method) {
        ClassModel cm = classModel(owner);
        if (cm == null) return false;
        for (MethodModel m : cm.methods()) if (m.methodName().stringValue().equals(method)) return m.code().isEmpty();
        return false;
    }

    /**
     * The classes nested alongside one, which is where an inner interface's implementations are.
     * The list lives on the nest HOST, not on the member, so it is read from there.
     */
    static List<String> nestMembers(String owner) {
        ClassModel cm = classModel(owner);
        if (cm == null) return List.of();
        String host = cm.findAttribute(Attributes.nestHost()).map(a -> a.nestHost().asInternalName()).orElse(owner);
        ClassModel hm = host.equals(owner) ? cm : classModel(host);
        if (hm == null) return List.of();
        List<String> out = new ArrayList<>();
        hm.findAttribute(Attributes.nestMembers()).ifPresent(a -> {
            for (var e : a.nestMembers()) out.add(e.asInternalName());
        });
        return out;
    }

    /** Two nodes describing the same bytes, ignoring the note saying where they were read. */
    @SuppressWarnings("unchecked")
    static boolean sameShape(Object a, Object b) {
        if (a instanceof Map<?, ?> ma && b instanceof Map<?, ?> mb) {
            Set<String> keys = new LinkedHashSet<>();
            for (Object k : ma.keySet()) if (!"java".equals(k)) keys.add((String) k);
            for (Object k : mb.keySet()) if (!"java".equals(k)) keys.add((String) k);
            for (String k : keys) {
                if (!sameShape(((Map<String, Object>) ma).get(k), ((Map<String, Object>) mb).get(k))) return false;
            }
            return true;
        }
        if (a instanceof List<?> la && b instanceof List<?> lb) {
            if (la.size() != lb.size()) return false;
            for (int i = 0; i < la.size(); i++) if (!sameShape(la.get(i), lb.get(i))) return false;
            return true;
        }
        return Objects.equals(a, b);
    }

    static Map<String, Object> getterFieldNode(String owner, String method, int depth) {
        return getterFieldNode(owner, method, null, depth);
    }

    /**
     * The same, for one particular object: the constructor that built `made` runs with the
     * arguments it was given, so a field the constructor derives from an argument — the
     * codec StatType builds from the registry it wraps — comes out as that element's.
     */
    static Map<String, Object> getterFieldNode(String owner, String method, NewV made, int depth) {
        ClassModel cm = classModel(owner);
        if (cm == null || depth > MAX_DEPTH) return null;
        String field = null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(method)) continue;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETFIELD && fi.owner().asInternalName().equals(owner)) {
                    if (field != null) return null;   // more than one field read: not a plain getter
                    field = fi.name().stringValue();
                }
            }
            break;
        }
        if (field == null) return null;
        Map<String, Object> found = null;
        int assigned = 0;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<init>")) continue;
            if (made != null && !m.methodType().stringValue().equals(made.desc())) continue;
            Map<Integer, Value> locals = new HashMap<>();
            if (made != null) {
                List<Integer> slots = paramSlots(made.desc(), false);
                for (int i = 0; i < slots.size() && i < made.args().size(); i++) locals.put(slots.get(i), made.args().get(i));
            }
            Deque<Value> stack = new ArrayDeque<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTFIELD) {
                    Value v = stack.isEmpty() ? null : stack.pop();
                    if (fi.name().stringValue().equals(field) && fi.owner().asInternalName().equals(owner)) {
                        assigned++;
                        found = v instanceof CodecV c ? c.n() : null;
                    }
                    continue;
                }
                if (el instanceof LoadInstruction li && locals.containsKey(li.slot())) { stack.push(locals.get(li.slot())); continue; }
                if (el instanceof StoreInstruction st) { if (!stack.isEmpty()) locals.put(st.slot(), stack.pop()); continue; }
                step(el, stack, owner, depth);
            }
        }
        return assigned == 1 ? found : null;
    }

    static Map<String, Object> codecOfOwner(String owner, String method, int depth) {
        if (enumValues(owner) != null) return enumWireNode(owner);
        return opaque(shortName(owner) + "." + method);
    }

    static Map<String, Object> idMapper(List<Value> args) {
        // idMapper(IntFunction byId, ToIntFunction toId) → enum of the lambda owner; idMapper(IdMap) → registry-like
        for (Value a : args) if (a instanceof LambdaV l) {
            if (enumValues(l.owner()) != null) return enumWireNode(l.owner());
            // Not a Java enum, so there are no constants to name — but the bytes are the same
            // one var int (ByteBufCodecs$31.decode is VarInt.read then IntFunction.apply), which
            // is what a `registry` node says: an id in a named id space the schema does not list.
            return node("registry", "registry", shortName(l.owner()));
        }
        for (Value a : args) if (a instanceof OtherV ov && ov.what().startsWith("idmap:")) return node("registry", "registry", ov.what().substring(6));
        return opaque("ByteBufCodecs.idMapper");
    }

    /**
     * An enum as its own stream codec sends it. Most send the ordinal, and then the declaration
     * order is the whole answer; one whose codec is ByteBufCodecs.idMapper sends an id the
     * constant carries instead, and that id is not always the ordinal — Rabbit$Variant.EVIL is
     * declared seventh and travels as 99. Such a node records the numbers in `ids`, or says with
     * `idsUnknown` that it could not read them, which is a hole rather than a silent ordinal.
     */
    static final Set<String> readingIds = new HashSet<>();   // enums whose numbering is being read

    static Map<String, Object> enumWireNode(String enumClass) {
        Map<String, Object> n = enumNode(enumClass);
        // Reading the numbering walks the enum's own <clinit>, which mentions the codec that
        // asked for it: without this the walk would ask again, and again.
        if (!readingIds.add(enumClass)) return n;
        try {
            attachEnumIds(n, enumClass);
        } finally {
            readingIds.remove(enumClass);
        }
        return n;
    }

    static void attachEnumIds(Map<String, Object> n, String enumClass) {
        attachEnumIds(n, enumClass, idMapperOf(enumClass));
    }

    static void attachEnumIds(Map<String, Object> n, String enumClass, LambdaV toId) {
        // ByIdMap.continuous(Enum::ordinal, …): an id mapper that maps the ordinal, which is
        // what a plain enum node already says
        if (toId == null || toId.name().equals("ordinal")) return;
        List<Integer> ids = enumWireIds(enumClass, toId);
        if (ids == null) { n.put("idsUnknown", true); return; }
        for (int i = 0; i < ids.size(); i++) if (ids.get(i) != i) { n.put("ids", ids); return; }
    }

    /**
     * What readById(IntFunction) reads: an id in a registry, or — DisplaySlot.BY_ID — a
     * constant of an enum by the number its class gave it.
     */
    static Map<String, Object> byIdNode(Value v) {
        if (v instanceof OtherV ov && ov.what().startsWith("idmap:")) return node("registry", "registry", ov.what().substring(6));
        if (v instanceof OtherV ov && ov.what().startsWith("static:")) {
            String ref = ov.what().substring(7);
            int dot = ref.lastIndexOf('.');
            String cls = ref.substring(0, dot);
            Class<?> c = loadClass(cls);
            if (c != null && c.isEnum()) {
                Map<String, Object> n = enumNode(cls);
                if (readingIds.add(cls)) {
                    try { attachEnumIds(n, cls, byIdMapperOf(cls, ref.substring(dot + 1))); }
                    finally { readingIds.remove(cls); }
                }
                return n;
            }
        }
        return node("registry", "registry", "?");
    }

    /**
     * The ToIntFunction an enum's class hands ByIdMap.continuous or ByIdMap.sparse to build the
     * IntFunction it stores in `field`: BY_ID = ByIdMap.continuous(DisplaySlot::id, values(), ZERO).
     * Null when the field is built some other way.
     */
    static LambdaV byIdMapperOf(String enumClass, String field) {
        ClassModel cm = classModel(enumClass);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            LambdaV toInt = null, candidate = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeDynamicInstruction idi) {
                    if (idi.name().stringValue().equals("applyAsInt") && lambdaOf(idi) instanceof LambdaV l) toInt = l;
                    continue;
                }
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESTATIC && ii.owner().asInternalName().endsWith("util/ByIdMap")) { candidate = toInt; continue; }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) {
                    if (fi.name().stringValue().equals(field)) return candidate;
                    candidate = null; toInt = null;
                }
            }
            break;
        }
        return null;
    }

    /** The ToIntFunction an enum's own class hands ByteBufCodecs.idMapper, if it uses one. */
    static LambdaV idMapperOf(String enumClass) {
        ClassModel cm = classModel(enumClass);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            LambdaV last = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeDynamicInstruction idi) { last = lambdaOf(idi) instanceof LambdaV l ? l : null; continue; }
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESTATIC
                        && ii.owner().asInternalName().endsWith("ByteBufCodecs") && ii.name().stringValue().equals("idMapper")
                        && ii.typeSymbol().descriptorString().startsWith("(Ljava/util/function/IntFunction;")) {
                    return last;
                }
                if (el instanceof Instruction) last = null;
            }
            break;
        }
        return null;
    }

    /**
     * The number each constant of an enum travels as, read from the field the id mapper's
     * function returns. The enum is loaded and its constants asked, rather than its constructor
     * calls read, because the number is not always an argument: TropicalFish$Pattern packs its
     * base and its index into one. Null when the function is not a plain field, when the class
     * will not load, or when two constants would share a number.
     */
    /**
     * The numbering of an enum whose static byId(int) is a switch handing back a constant per
     * number (ClientIntent: 1 STATUS, 2 LOGIN, 3 TRANSFER): the number of each constant in
     * declaration order, or null when a constant has no case or the method is not such a switch.
     */
    static List<Integer> enumSwitchIds(String enumClass) {
        ClassModel cm = classModel(enumClass);
        List<String> constants = enumValues(enumClass);
        if (cm == null || constants == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("byId") || !m.methodType().stringValue().startsWith("(I)")) continue;
            Map<Label, Integer> caseOf = new HashMap<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof TableSwitchInstruction ts) for (SwitchCase sc : ts.cases()) caseOf.put(sc.target(), sc.caseValue());
                if (el instanceof LookupSwitchInstruction ls) for (SwitchCase sc : ls.cases()) caseOf.put(sc.target(), sc.caseValue());
            }
            if (caseOf.isEmpty()) return null;
            Map<String, Integer> idOf = new HashMap<>();
            Integer current = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof LabelTarget lt && caseOf.containsKey(lt.label())) current = caseOf.get(lt.label());
                else if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETSTATIC && fi.owner().asInternalName().equals(enumClass) && current != null) {
                    idOf.putIfAbsent(fi.name().stringValue(), current);
                    current = null;
                }
            }
            List<Integer> out = new ArrayList<>();
            for (String c : constants) { Integer id = idOf.get(c); if (id == null) return null; out.add(id); }
            return out;
        }
        return null;
    }

    /**
     * The numbering of an enum whose static byId(int) applies an IntFunction field of its own
     * (GameType.byId is BY_ID.apply, BY_ID = ByIdMap.continuous(GameType::getId, …)): the ids
     * that function's ToIntFunction gives the constants, the ordinals when it is Enum::ordinal.
     */
    static List<Integer> enumByIdFieldIds(String enumClass) {
        ClassModel cm = classModel(enumClass);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("byId") || !m.methodType().stringValue().startsWith("(I)")) continue;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETSTATIC && fi.owner().asInternalName().equals(enumClass)
                        && fi.typeSymbol().descriptorString().contains("IntFunction")) {
                    LambdaV toId = byIdMapperOf(enumClass, fi.name().stringValue());
                    if (toId == null) return null;
                    if (toId.name().equals("ordinal")) {
                        List<String> constants = enumValues(enumClass);
                        if (constants == null) return null;
                        List<Integer> out = new ArrayList<>();
                        for (int i = 0; i < constants.size(); i++) out.add(i);
                        return out;
                    }
                    return enumWireIds(enumClass, toId);
                }
            }
        }
        return null;
    }

    static List<Integer> enumWireIds(String enumClass, LambdaV toId) {
        if (toId == null || !toId.owner().equals(enumClass)) return null;
        String f = getterField(enumClass, toId.name());
        if (f == null) return null;
        try {
            Class<?> c = loadClass(enumClass);
            if (c == null || !c.isEnum()) return null;
            java.lang.reflect.Field fd = c.getDeclaredField(f);
            fd.setAccessible(true);
            List<Integer> out = new ArrayList<>();
            Set<Integer> distinct = new HashSet<>();
            for (Object o : c.getEnumConstants()) {
                int v = fd.getInt(o);
                if (!distinct.add(v)) return null;   // two constants cannot share a number
                out.add(v);
            }
            return out.isEmpty() ? null : out;
        } catch (Throwable t) {
            return null;
        }
    }

    /**
     * What to call the union a dispatch on an enum builds. It is the part of the value the
     * constant selects, so it is named for that: TrackedWaypoint$Type dispatches to
     * TrackedWaypointPayload, which leaves the name of the class itself to the reader that
     * holds the payload along with everything read before it.
     */
    static String dispatchName(String enumClass) {
        String s = shortName(enumClass);
        int at = s.lastIndexOf('$');
        return (at > 0 ? s.substring(0, at) : s) + "Payload";
    }

    /**
     * The cases of a dispatch on an enum whose constants each carry their own case: a reader
     * held in a field (the tracked waypoints) or the codec of that case (a position path).
     * Every constant, in ordinal order, with what it reads. Null when a constant carries
     * neither, so the caller leaves the dispatch without cases rather than describing only
     * some of them.
     */
    static List<Map<String, Object>> enumDispatchCases(String enumClass, String field, int depth) {
        List<String> constants = enumValues(enumClass);
        ClassModel cm = classModel(enumClass);
        if (constants == null || cm == null) return null;
        int argIdx = field == null || field.isEmpty() ? -1 : ctorArgOfField(enumClass, field);
        Map<String, Value> readers = new LinkedHashMap<>();
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            Deque<Value> stack = new ArrayDeque<>();
            String name = null;
            Value fn = null;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESPECIAL
                        && ii.name().stringValue().equals("<init>") && ii.owner().asInternalName().equals(enumClass)) {
                    // new Type("VEC3I", 1, Vec3iWaypoint::new)
                    List<Value> a = popArgs(stack, arity(ii.typeSymbol().descriptorString()));
                    if (!stack.isEmpty()) stack.pop();   // the object the constructor ran on
                    name = constOf(arg(a, 0)) instanceof String c ? c : null;
                    fn = null;
                    if (argIdx >= 0) {
                        Value v = arg(a, argIdx);
                        fn = v instanceof LambdaV || v instanceof CodecV ? v : null;
                    } else {
                        for (Value v : a) if (v instanceof LambdaV || v instanceof CodecV) { fn = v; break; }
                    }
                    continue;
                }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) {
                    if (name != null && fn != null && constants.contains(name)) readers.put(name, fn);
                    name = null; fn = null;
                    if (!stack.isEmpty()) stack.pop();
                    continue;
                }
                step(el, stack, enumClass, depth);
            }
            break;
        }
        List<Map<String, Object>> cases = new ArrayList<>();
        for (int i = 0; i < constants.size(); i++) {
            Value held = readers.get(constants.get(i));
            if (held == null) return null;
            // The constant holds a reader to run, the codec of its own case, or a supplier
            // of that codec — which is a codec to interpret, not a reader to walk.
            Map<String, Object> t;
            if (held instanceof CodecV c) {
                t = c.n();
            } else {
                LambdaV l = (LambdaV) held;
                Map<String, Object> made = arity(l.desc()) == 0 && l.desc().endsWith("StreamCodec;")
                        ? inlineMethod(l.owner(), l.name(), l.desc(), List.of(), depth + 1, true) : null;
                t = made != null ? made : readerNode(l.owner(), l.name(), l.desc(), depth + 1);
            }
            // A case whose reader read nothing carries no payload (TrackedWaypoint.EMPTY only
            // passes the id and icon on to its superclass): that is a unit, not a hole. It is
            // the one place "read nothing" is meaningful — everything a reader does read
            // becomes a node, so an empty one really did leave the buffer alone.
            if ("opaque".equals(t.get("k")) && String.valueOf(t.get("java")).startsWith("empty-reader:")) t = node("unit");
            Map<String, Object> c = new LinkedHashMap<>();
            c.put("k", "case");
            c.put("id", constants.get(i));
            c.put("num", i);
            c.put("type", t);
            cases.add(c);
        }
        return cases.isEmpty() ? null : cases;
    }

    static Map<String, Object> applyFn(Value recv, Value fn) {
        Map<String, Object> base = nodeOf(recv);
        if (fn instanceof FnV f) {
            return switch (f.kind()) {
                case "list" -> node("list", "elem", base, "max", f.arg());
                // lengthPrefixed(max) puts the value's byte count in front of it, which is
                // what lets a reader step over a value it does not understand.
                case "lengthPrefixed" -> node("lenprefixed", "elem", base);
                default -> opaque("apply:" + f.kind());
            };
        }
        if (fn instanceof LambdaV l && l.owner().endsWith("ByteBufCodecs")) {
            return switch (l.name()) {
                case "optional" -> node("optional", "elem", base);
                case "list", "collection" -> node("list", "elem", base);
                case "lengthPrefixed" -> node("lenprefixed", "elem", base);
                case "lenientJson" -> base;
                default -> opaque("apply:ByteBufCodecs." + l.name());
            };
        }
        if (fn instanceof LambdaV l) return opaque("apply:" + shortName(l.owner()) + "." + l.name());
        return base;
    }

    static Map<String, Object> composite(List<Value> args, String self) {
        // composite(c1, g1, c2, g2, ..., ctor)
        List<Map<String, Object>> fields = new ArrayList<>();
        String structName = shortName(self);
        Value ctor = arg(args, args.size() - 1);
        if (ctor instanceof LambdaV l && !l.owner().equals("?")) structName = shortName(l.owner());
        for (int i = 0; i + 1 < args.size() - 1; i += 2) {
            Map<String, Object> f = new LinkedHashMap<>();
            String fname = args.get(i + 1) instanceof LambdaV l ? getterName(l) : "f" + (i / 2);
            f.put("name", fname);
            f.put("type", nodeOf(args.get(i)));
            fields.add(f);
        }
        return node("struct", "name", structName, "fields", fields);
    }

    /** A getter reference names the field; a synthetic lambda (d -> d.heightmaps, d -> d.heightmaps()) is read for the field or accessor it returns. */
    static String getterName(LambdaV l) {
        if (!l.name().startsWith("lambda$")) return l.name();
        ClassModel cm = classModel(l.owner());
        if (cm != null) for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(l.name()) || !m.methodType().stringValue().equals(l.desc())) continue;
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.GETFIELD) return fi.name().stringValue();
                if (el instanceof InvokeInstruction ii && arity(ii.typeSymbol().descriptorString()) == 0 && !ii.name().stringValue().equals("<init>")) return ii.name().stringValue();
            }
        }
        return l.name();
    }

    static Map<String, Object> nodeOf(Value v) {
        if (v instanceof CodecV c) return c.n();
        if (v instanceof LambdaV l) return opaque("lambda:" + shortName(l.owner()) + "." + l.name());
        if (v == null) return opaque("null");
        return opaque(v.toString());
    }
    static Value arg(List<Value> a, int i) { return i < a.size() ? a.get(i) : null; }
    /** The wire node behind a value, whether this reader read it or its caller did. */
    static Map<String, Object> nodeOfValue(Value v) {
        return v instanceof CodecV c ? c.n() : v instanceof PassedV p ? p.of() : v instanceof BitsV b ? b.of()
            : v instanceof NewV nv ? opaque(shortName(nv.cls()) + ".streamCodec") : null;
    }
    /**
     * A value as a range of bits of a long read or passed in, when it is one: an unnamed
     * `BitsV` from a shift, mask or (int) cast already, or the whole long. Null for anything
     * else, so the arithmetic of a reader never cuts fields out of a value that is not packed.
     */
    static BitsV inlineBits(Value v) {
        if (v instanceof BitsV b && b.name() == null) return b;
        Map<String, Object> of = nodeOfValue(v);
        if (of == null || subOfValue(v) != null || !("prim".equals(of.get("k")) && "LONG".equals(of.get("t")) || "bits".equals(of.get("k")) && "LONG".equals(of.get("of")))) return null;
        return new BitsV(of, null, 0, 64, true);
    }
    /** The bit field a value is one of, if it is one: the name goes on the guard or the count. */
    static String subOfValue(Value v) {
        return v instanceof BitsV b ? b.name() : v instanceof PassedV p ? p.sub() : null;
    }
    static Object constOf(Value v) { return unwrap(v) instanceof ConstV c ? c.v() : null; }
    /** An array stands for the size it was created with (new byte[256], new String[4]). */
    static Value unwrap(Value v) { return v instanceof ArrayV a ? a.size() : v; }
    static String keyOf(Value v) { return v instanceof KeyV k ? k.registry() : "?"; }
    static String lambdaOrOther(Value v) {
        if (v instanceof LambdaV l) return shortName(l.owner()) + "." + l.name();
        if (v instanceof CodecV c && "opaque".equals(c.n().get("k")) && String.valueOf(c.n().get("java")).startsWith("not-a-codec:")) return String.valueOf(c.n().get("java")).substring(12);
        return v == null ? null : v.toString();
    }

    // nbtOrText is the node for ByteBufCodecs.fromCodec*(codec) and buf.readWithCodec(codec):
    // an NBT payload, except when the codec is ComponentSerialization.CODEC (a chat
    // component), which has the same wire form as ComponentSerialization.STREAM_CODEC → TEXT.
    static Map<String, Object> nbtOrText(Value codec) {
        if (codec instanceof CodecV cv && "opaque".equals(cv.n().get("k"))
                && String.valueOf(cv.n().get("java")).endsWith("network/chat/ComponentSerialization.CODEC") && !"TEXT".equals(derivingPrim)) return prim("TEXT");
        return node("nbt", "java", lambdaOrOther(codec));
    }

    // ---- reader walking (Packet.codec / StreamCodec.of / buffer constructors) -----------

    static final Map<String, Map<String, Object>> readerCache = new HashMap<>();

    /**
     * Guards a reader could not attach to its own value, because they test something its
     * caller read and passed in. The caller takes them together with the fields they guard,
     * which is what inlining such a reader means. It is consumed immediately by the call that
     * produced it, so the walk being depth first is what keeps it to one reader at a time.
     */
    static final Map<Map<String, Object>, List<List<Map<String, Object>>>> carriedGuards = new IdentityHashMap<>();

    /**
     * Counted repetitions waiting for the name of the field that counts them. A loop bounded by
     * a value its caller read repeats without a count of its own in front of it, so the schema
     * has to point at the field holding the count; which field that is only becomes known in
     * the struct both end up in, so the node is recorded here and named there.
     */
    static final Map<Map<String, Object>, PassedV> pendingCounts = new IdentityHashMap<>();
    /** Dispatches whose key is a field read earlier (type.streamCodec() on a component type): the key's name is resolved like a count's. */
    static final Map<Map<String, Object>, Map<String, Object>> pendingKeys = new IdentityHashMap<>();

    /** The codec of a data component whose type was just read: every component's own, by that type. */
    static Map<String, Object> componentDispatch(Map<String, Object> typeNode) {
        Map<String, Object> d = node("dispatch", "name", "DataComponent", "key", new LinkedHashMap<String, Object>(), "casesFrom", "packet_schema.json#components");
        d.put("recursive", true);
        pendingKeys.put(d, typeNode);
        return d;
    }
    static boolean isComponentType(Value v) {
        Map<String, Object> n = nodeOfValue(v);
        return n != null && "registry".equals(n.get("k")) && "data_component_type".equals(n.get("registry"));
    }

    static Map<String, Object> readerNode(Value decoder, int depth) {
        if (!(decoder instanceof LambdaV l)) return opaque("decoder:" + decoder);
        return readerNode(l.owner(), l.name(), l.desc(), depth + 1);
    }

    static Map<String, Object> readerNodeByName(String owner, String name, int depth) {
        ClassModel cm = classModel(owner);
        if (cm == null) return opaque("no-class:" + owner);
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(name)) continue;
            String d = m.methodType().stringValue();
            if (d.contains("FriendlyByteBuf")) return readerNode(owner, name, d, depth);
        }
        return opaque(shortName(owner) + "." + name + "(no buffer overload)");
    }

    /** Interprets a method that reads one value from a FriendlyByteBuf and returns its node. */
    static Map<String, Object> readerNode(String owner, String name, String desc, int depth) {
        Map<String, Object> rule = READER_RULES.get(owner + "." + name);
        if (rule != null) return rule;
        String key = owner + "." + name + desc;
        if (readerCache.containsKey(key)) return readerCache.get(key);
        if (depth > MAX_DEPTH) return opaque("depth:" + shortName(owner) + "." + name);
        readerCache.put(key, opaque("recursive:" + shortName(owner) + "." + name));
        Map<String, Object> result = interpretReader(owner, name, desc, depth);
        readerCache.put(key, result);
        return result;
    }

    static Map<String, Object> interpretReader(String owner, String name, String desc, int depth) {
        return interpretReader(owner, name, desc, depth, Map.of());
    }

    /**
     * passed holds the values the caller gave, by local slot, so a reader called with
     * something read earlier — read(buf, flags) in the command tree — reads its branches as
     * conditions on that value instead of as an unreadable test.
     */
    static Map<String, Object> interpretReader(String owner, String name, String desc, int depth, Map<Integer, Value> passed) {
        ClassModel cm = classModel(owner);
        if (cm == null) return opaque("no-class:" + shortName(owner) + "." + name);
        // Up the superclass chain, for the same reason findMethod does it: a call names the
        // class it was written in, and Target.readContents is Node's.
        MethodModel target = null;
        for (String cls = owner; cls != null && target == null; ) {
            ClassModel c0 = classModel(cls);
            if (c0 == null) break;
            for (MethodModel m : c0.methods()) {
                if (!m.methodName().stringValue().equals(name)) continue;
                if (desc.isEmpty() || m.methodType().stringValue().equals(desc)) { target = m; break; }
            }
            cls = c0.superclass().map(x -> x.asInternalName()).orElse(null);
        }
        if (target == null) return opaque("no-method:" + shortName(owner) + "." + name);
        // An abstract or interface method has no code, which is not the same as code that
        // reads nothing: Palette.read reads a whole palette, and treating it as empty made
        // the chunk section's description claim to be complete while the palette was missing
        // from it.
        if (target.code().isEmpty()) return opaque("no-body:" + shortName(owner) + "." + name);
        boolean isCtor = name.equals("<init>");
        List<Map<String, Object>> fields = new ArrayList<>();   // for constructors: PUTFIELD order
        List<Map<String, Object>> values = new ArrayList<>();   // for static readers: values produced in order
        Deque<Value> stack = new ArrayDeque<>();
        Map<Integer, Value> locals = new HashMap<>(passed);   // slot → last stored value (array sizes, read values)
        Map<String, Value> fieldValues = new HashMap<>(); // this.x set earlier in this reader (new byte[n] then readBytes(this.buffer))
        // A read between a forward branch and its target is guarded: when the branch tests a
        // value read earlier (a flag bit, a boolean, an enum or int constant, a predicate of it)
        // the read gets a "when" condition; a branch the walker cannot read, or a loop (a
        // backward jump), makes the reader conditional. A branch that only guards a throw
        // (size checks) has no read in its region and does not count.
        List<Region> pending = new ArrayList<>();
        Set<Label> bound = new HashSet<>();
        Map<Map<String, Object>, List<List<Map<String, Object>>>> guards = new IdentityHashMap<>();
        Set<Map<String, Object>> seen = Collections.newSetFromMap(new IdentityHashMap<>());   // values already classified
        // a region whose condition the walker cannot read makes the reads inside it
        // conditional — unless the region turns out to be a loop, which is collapsed instead
        Set<Region> forcedBy = Collections.newSetFromMap(new IdentityHashMap<>());
        // A switch on the ordinal of an enum just read is a dispatch on that enum: each arm
        // reads that constant's payload. Walked as the arms come, one after the other.
        SwitchWalk sw = null;
        Set<Region> collapsed = Collections.newSetFromMap(new IdentityHashMap<>());
        boolean conditional = false;
        Label lastLabel = null;
        Map<Label, Integer> labelAt = new HashMap<>();   // label → how much had been read when it was bound
        Region lastClosed = null;                        // the region that just ended, for a loop that jumps back from it
        int iincs = 0;
        int expansionsBefore = guardExpansions;
        int idx = 0;
        boolean exited = false;   // the last instruction was a return or a throw
        boolean returned0 = false; // … a return: `if (count <= 0) return EMPTY;` leaves the rest as the other branch
        Map<String, Object> returned = null;
        // The names of the method's local variables (Mojang's jars keep the table): a read
        // stored in `slot` or `itemStack` is named by that, which says more than the read
        // method's name (`byte`, `optionalItemStack`) that would otherwise name the field.
        Map<Integer, List<LocalVariableInfo>> localNames = new HashMap<>();
        target.code().flatMap(c -> c.findAttribute(Attributes.localVariableTable())).ifPresent(a -> {
            for (LocalVariableInfo lv : a.localVariables()) localNames.computeIfAbsent(lv.slot(), k -> new ArrayList<>()).add(lv);
        });
        int bci = 0;
        // reads pushed and neither stored nor assigned to a field yet, by identity
        Set<Map<String, Object>> unstored = Collections.newSetFromMap(new IdentityHashMap<>());
        for (CodeElement el : target.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            int bciAfter = bci + (el instanceof Instruction ins0 ? ins0.sizeInBytes() : 0);
            bci = bciAfter;
            if (!stack.isEmpty() && stack.peek() instanceof CodecV top0) unstored.add(top0.n());
            idx++;
            if (el instanceof Instruction) { exited = el instanceof ReturnInstruction || el instanceof ThrowInstruction; returned0 = el instanceof ReturnInstruction; }
            switch (el) {
                case BranchInstruction bi -> {
                    if (bound.contains(bi.target())) {
                        // a jump backwards: a loop. When it returns to a head that compared a
                        // counter with a bound, and values were read in between, the body is
                        // that bound's worth of repeats rather than an unreadable condition.
                        Region loop = null;
                        for (Region r : pending) if (r.head != null && r.head.equals(bi.target()) && values.size() > r.valuesAt && iincs > r.iincAt) loop = r;
                        if (loop != null && collapseLoop(loop, values, fields, fieldValues, isCtor, owner)) {
                            pending.remove(loop);
                            collapsed.add(loop);
                            // the repetition stands where its body was read: guarded by what was
                            // open around the loop, not by whatever the next read is inside
                            guardNew(values, seen, pending, forcedBy, guards);
                        } else if (!collapseTerminated(bi.target(), labelAt, lastClosed, values, fields, fieldValues, isCtor, owner)) {
                            conditional = true;
                        }
                        break;
                    }
                    boolean jump = bi.opcode() == Opcode.GOTO || bi.opcode() == Opcode.GOTO_W;
                    if (jump && sw != null && sw.current != null) {
                        // the arm is done; every arm leaves for the same label, which ends the switch
                        sw.arm(values);
                        sw.end = bi.target();
                        break;
                    }
                    Value[] ops = jump ? null : peek2(stack);
                    Region r = new Region(bi.target(), idx, jump ? null : branchTest(bi.opcode(), stack));
                    // `for (i = 0; i < bound; i++)`: the counter starts at zero, so the other
                    // operand is the bound and the label just passed is the loop head
                    if (ops != null && ops.length == 2 && bi.opcode().name().startsWith("IF_ICMP")
                            && ops[1] instanceof ConstV z && Integer.valueOf(0).equals(z.v())) {
                        r.head = lastLabel; r.bound = ops[0]; r.valuesAt = values.size(); r.iincAt = iincs;
                    }
                    pending.add(r);
                }
                case IncrementInstruction inc -> iincs++;
                case LookupSwitchInstruction ls -> {
                    Value on = stack.isEmpty() ? null : stack.pop();
                    if (on instanceof PassedV pv && "ordinal".equals(pv.sub()) && values.contains(pv.of())) {
                        sw = new SwitchWalk(pv.of(), values.indexOf(pv.of()));
                        for (var c : ls.cases()) sw.caseOf.put(c.target(), c.caseValue());
                        sw.dflt = ls.defaultTarget();
                    } else {
                        pending.add(new Region(ls.defaultTarget(), idx, null));
                        for (var c : ls.cases()) pending.add(new Region(c.target(), idx, null));
                    }
                }
                case TableSwitchInstruction ts -> {
                    Value on = stack.isEmpty() ? null : stack.pop();
                    if (on instanceof PassedV pv && "ordinal".equals(pv.sub()) && values.contains(pv.of())) {
                        sw = new SwitchWalk(pv.of(), values.indexOf(pv.of()));
                        for (var c : ts.cases()) sw.caseOf.put(c.target(), c.caseValue());
                        sw.dflt = ts.defaultTarget();
                    } else {
                        pending.add(new Region(ts.defaultTarget(), idx, null));
                        for (var c : ts.cases()) pending.add(new Region(c.target(), idx, null));
                    }
                }
                case LabelTarget lt -> {
                    lastLabel = lt.label();
                    bound.add(lt.label());
                    labelAt.putIfAbsent(lt.label(), values.size());
                    if (sw != null) {
                        if (lt.label().equals(sw.end)) { sw.arm(values); sw.finish(values, owner); sw = null; }   // the last arm falls through
                        else if (sw.caseOf.containsKey(lt.label())) { sw.current = sw.caseOf.get(lt.label()); sw.segStart = values.size(); }
                        else if (lt.label().equals(sw.dflt)) { sw.current = -1; sw.segStart = values.size(); }
                    }
                    Region negated = null;
                    for (Region r : new ArrayList<>(pending)) {
                        if (!lt.label().equals(r.target)) continue;
                        pending.remove(r);
                        if (r.known && r.alts.size() == 1) lastClosed = r;
                        // `if (n > 0) { … return a; } return b;`: the region left the method, so
                        // everything after its target is the other branch. A region with no
                        // target never closes, which is what "to the end of the reader" is.
                        // `if (a == 0 && b == 0) return EMPTY;` is two regions ending here, the
                        // inner one returning: the rest runs when either condition fails.
                        if (r.known && r.alts.size() == 1 && (r.reads ? exited : returned0)) {
                            if (negated == null) { negated = new Region(null, idx, negate(r.alts.get(0))); pending.add(negated); }
                            else negated.alts.add(negate(r.alts.get(0)));
                        }
                        // if (a) goto body; if (b) …: a jump into a region still open is an
                        // alternative to its condition; a region ending at the same label is not
                        // jumped into
                        if (r.known && !r.reads && r.alts.size() == 1) {
                            for (Region s : pending) if (s.openedAt > r.openedAt && s.known && s != negated && !lt.label().equals(s.target)) s.alts.add(negate(r.alts.get(0)));
                        }
                    }
                }
                case FieldInstruction fi when fi.opcode() == Opcode.PUTFIELD -> {
                    Value v = stack.isEmpty() ? new OtherV("underflow") : stack.pop();
                    fieldValues.put(fi.name().stringValue(), v);
                    // `this.buffer = new byte[buf.readVarInt()]` is a field of the size read so
                    // far; readBytes(this.buffer) later retypes it to the array it fills, and a
                    // loop over the array replaces it with the list it counts.
                    Value inner = unwrap(v);
                    if (inner instanceof CodecV c) {
                        Map<String, Object> f = new LinkedHashMap<>();
                        f.put("name", fi.name().stringValue()); f.put("type", c.n()); fields.add(f);
                    }
                }
                case FieldInstruction fi when fi.opcode() == Opcode.GETFIELD -> {
                    Value obj = stack.isEmpty() ? null : stack.pop();
                    // `type.constructor` on an enum just read: the field holds that constant's
                    // own reader, so the value becomes a dispatch waiting for its arguments.
                    if (obj instanceof CodecV c && enumClassOf.containsKey(c.n())) {
                        stack.push(new EnumFnV(c.n(), enumClassOf.get(c.n()), fi.name().stringValue()));
                    } else if (captured.containsKey(fi.owner().asInternalName() + "." + fi.name().stringValue())) {
                        stack.push(captured.get(fi.owner().asInternalName() + "." + fi.name().stringValue()));
                    } else {
                        stack.push(fieldValues.getOrDefault(fi.name().stringValue(), new OtherV("field:" + fi.name().stringValue())));
                    }
                }
                case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> step(el, stack, owner, depth);
                case InvokeInstruction ii -> {
                    boolean outer = inKnownRegion;
                    inKnownRegion = !pending.isEmpty() && pending.stream().allMatch(r -> r.known);
                    readerInvoke(ii, stack, owner, depth, values, guards);
                    inKnownRegion = outer;
                    // every value this call produced (a read, or a record built from reads that
                    // replaced them) is guarded by the regions open now
                    guardNew(values, seen, pending, forcedBy, guards);
                }
                case InvokeDynamicInstruction idi -> { popArgs(stack, arity(idi.typeSymbol().descriptorString())); stack.push(lambdaOf(idi)); }
                case ConstantInstruction ci -> step(el, stack, owner, depth);
                case NewObjectInstruction no -> stack.push(new OtherV("new:" + no.className().asInternalName()));
                case LoadInstruction li -> stack.push(locals.getOrDefault(li.slot(), new OtherV("local:" + li.slot())));
                case StoreInstruction st -> {
                    if (stack.isEmpty()) break;
                    Value v = stack.pop();
                    // `min = hasMin ? buf.readFloat() : -MAX`: the walker runs both arms, so
                    // the read sits under the default it did not take; the variable holds the
                    // read, and the default is what the wire does not carry.
                    if (!(v instanceof CodecV) && !stack.isEmpty() && stack.peek() instanceof CodecV under && unstored.contains(under.n())) v = stack.pop();
                    locals.put(st.slot(), v);
                    String local = localName(localNames, st.slot(), bciAfter);
                    if (v instanceof CodecV cv) {
                        unstored.remove(cv.n());
                        if (local != null) hintOf.put(cv.n(), local);
                    }
                }
                case NewPrimitiveArrayInstruction na -> { if (!stack.isEmpty()) stack.push(new ArrayV(stack.pop())); }
                case NewReferenceArrayInstruction na -> { if (!stack.isEmpty()) stack.push(new ArrayV(stack.pop())); }
                case OperatorInstruction oi when oi.opcode() == Opcode.IADD || oi.opcode() == Opcode.ISUB -> {
                    // `buf.readVarInt() - 1`: the value read, shifted
                    Value b = stack.isEmpty() ? null : stack.pop();
                    Value a = stack.isEmpty() ? null : stack.pop();
                    int sign = oi.opcode() == Opcode.ISUB ? -1 : 1;
                    Map<String, Object> of = nodeOfValue(a);
                    Object d = constOf(b);
                    if (of == null && sign > 0) { of = nodeOfValue(b); d = constOf(a); }
                    stack.push(of != null && d instanceof Integer k ? new OffsetV(of, sign * k) : new OtherV("arith"));
                }
                case ConvertInstruction cv when cv.opcode() == Opcode.L2I -> {
                    // `(int) packed`: the low 32 bits of the long, as a signed int
                    Value a = stack.isEmpty() ? null : stack.pop();
                    BitsV b = inlineBits(a);
                    if (b == null) { if (a != null) stack.push(a); }
                    else stack.push(new BitsV(b.of(), null, b.offset(), Math.min(b.width(), 32), b.width() >= 32 || b.signed()));
                }
                case OperatorInstruction oi when oi.opcode() == Opcode.LSHR || oi.opcode() == Opcode.LUSHR -> {
                    // `packed >> 32`: the bits above the shift; `>>` keeps the sign, `>>>` does not
                    Value b = stack.isEmpty() ? null : stack.pop();
                    Value a = stack.isEmpty() ? null : stack.pop();
                    BitsV bv = inlineBits(a);
                    if (bv != null && constOf(b) instanceof Integer sh && sh > 0 && sh < bv.width())
                        stack.push(new BitsV(bv.of(), null, bv.offset() + sh, bv.width() - sh, oi.opcode() == Opcode.LSHR && bv.signed()));
                    else stack.push(new OtherV("shift"));
                }
                case OperatorInstruction oi when oi.opcode() == Opcode.LAND -> {
                    // `packed & 0xFFFFFFFFL`: the low bits the mask keeps, unsigned
                    Value b = stack.isEmpty() ? null : stack.pop();
                    Value a = stack.isEmpty() ? null : stack.pop();
                    BitsV bv = inlineBits(a) != null ? inlineBits(a) : inlineBits(b);
                    Object m = inlineBits(a) != null ? constOf(b) : constOf(a);
                    if (bv != null && m instanceof Long mask && mask > 0 && (mask & (mask + 1)) == 0)
                        stack.push(new BitsV(bv.of(), null, bv.offset(), Math.min(bv.width(), Long.bitCount(mask)), false));
                    else stack.push(new OtherV("and"));
                }
                case OperatorInstruction oi when oi.opcode() == Opcode.IAND -> {
                    // `flags & 3`: the bits of a value read earlier, which a branch then tests
                    Value b = stack.isEmpty() ? null : stack.pop();
                    Value a = stack.isEmpty() ? null : stack.pop();
                    Map<String, Object> of = nodeOfValue(a) != null ? nodeOfValue(a) : nodeOfValue(b);
                    Object m = nodeOfValue(a) != null ? constOf(b) : constOf(a);
                    stack.push(of != null && m instanceof Integer mask ? new MaskedV(of, mask) : new OtherV("and"));
                }
                case OperatorInstruction oi when oi.opcode() == Opcode.ARRAYLENGTH -> {
                    Value a = stack.isEmpty() ? null : stack.pop();
                    stack.push(a instanceof ArrayV av ? av.size() : new OtherV("length"));
                }
                case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!stack.isEmpty()) stack.push(stack.peek()); }
                case StackInstruction si when si.opcode() == Opcode.POP -> { if (!stack.isEmpty()) stack.pop(); }
                case ReturnInstruction ri -> {
                    if (sw != null && sw.current != null) { sw.arm(values); sw.finish(values, owner); sw = null; }
                    if (!stack.isEmpty() && stack.peek() instanceof CodecV c) returned = c.n();
                }
                default -> { }
            }
        }
        for (Region r : forcedBy) if (!collapsed.contains(r)) conditional = true;
        if (guardExpansions > expansionsBefore) conditional = false;   // the loop was the EnumSet-guarded reader loop
        Map<String, Object> result;
        if (isCtor && !fields.isEmpty()) {
            // A constructor names its fields by what it stores, but it may read something it
            // never stores: the player abilities packet reads a flags byte and keeps only the
            // four booleans its bits stand for, so the byte itself belonged to no field and
            // disappeared from the wire description. Put back, in the order it was read, every
            // value no field accounts for.
            for (int i = values.size() - 1; i >= 0; i--) {
                Map<String, Object> v = values.get(i);
                boolean kept = false;
                for (Map<String, Object> f : fields) if (holds(f.get("type"), v)) kept = true;
                if (kept) continue;
                int at = 0;
                for (int j = 0; j < fields.size(); j++) {
                    int vi = -1;
                    for (int k = 0; k < values.size(); k++) if (values.get(k) == fields.get(j).get("type")) vi = k;
                    if (vi >= 0 && vi < i) at = j + 1;
                }
                fields.add(at, field(hintOf.getOrDefault(v, "value"), v));
            }
            result = node("struct", "name", shortName(owner), "fields", fields);
        } else if (!isCtor && !fields.isEmpty() && fields.size() == values.size()) {
            // a builder-style reader: every value read was stored into a named field
            result = node("struct", "name", shortName(owner), "fields", fields);
        } else if (returned != null && !isCtor && !(values.size() > 1 && values.get(values.size() - 1) == returned)) {
            result = returned;
        } else if (!values.isEmpty() && !isCtor) {
            // static X.read(buf) or buf.readX() building the value from what was read: the struct is
            // named after the returned type, its fields by record components or the reads
            String ret = desc.substring(desc.indexOf(')') + 1);
            String structName = ret.startsWith("L") && ret.startsWith("Lnet/minecraft/") ? shortName(ret.substring(1, ret.length() - 1)) : shortName(owner);
            String structClass = ret.startsWith("L") ? ret.substring(1, ret.length() - 1) : owner;
            List<String> names = recordComponents(structClass);
            List<Map<String, Object>> fs = new ArrayList<>();
            for (int i = 0; i < values.size(); i++) {
                Map<String, Object> f = new LinkedHashMap<>();
                f.put("name", names != null && i < names.size() && names.size() == values.size() ? names.get(i) : hintOf.getOrDefault(values.get(i), "v" + i));
                f.put("type", values.get(i)); fs.add(f);
            }
            result = fs.size() == 1 ? fs.get(0).get("type") instanceof Map<?, ?> m ? castNode(m) : opaque("?") : node("struct", "name", structName, "fields", fs);
        } else if (isCtor && !values.isEmpty()) {
            result = node("struct", "name", shortName(owner), "fields", namedByCtor(owner, desc, values));
        } else {
            result = opaque("empty-reader:" + shortName(owner) + "." + name);
        }
        // A guard on something this reader read is attached here. One on a value its caller
        // passed in belongs to the caller's struct, where that value is a field: hand those
        // over, so the caller can splice this reader's fields in beside its own.
        if (!guards.isEmpty() && !passed.isEmpty()) {
            Map<Map<String, Object>, List<List<Map<String, Object>>>> carried = new IdentityHashMap<>();
            for (Map.Entry<Map<String, Object>, List<List<Map<String, Object>>>> e : guards.entrySet()) {
                boolean own = true;
                for (List<Map<String, Object>> alts : e.getValue()) for (Map<String, Object> t : alts) {
                    boolean found = false;
                    for (Map<String, Object> v : values) if (v == t.get("node")) found = true;
                    if (!found) own = false;
                }
                if (!own) carried.put(e.getKey(), e.getValue());
            }
            for (Map<String, Object> k : carried.keySet()) guards.remove(k);
            carriedGuards.putAll(carried);
        }
        if (!guards.isEmpty() && result.get("k").equals("struct") && !attachGuards(result, values, guards)) conditional = true;
        if (result.get("k").equals("struct")) attachCounts(result);
        if (conditional && result.get("k").equals("struct")) result.put("conditional", true);
        return result;
    }

    /** Guards every value not classified yet by the regions open now. */
    static void guardNew(List<Map<String, Object>> values, Set<Map<String, Object>> seen, List<Region> pending,
                         Set<Region> forcedBy, Map<Map<String, Object>, List<List<Map<String, Object>>>> guards) {
        for (Map<String, Object> v : values) {
            if (!seen.add(v) || pending.isEmpty()) continue;
            boolean known = pending.stream().allMatch(r -> r.known);
            for (Region r : pending) r.reads = true;
            if (!known) {
                for (Region r : pending) if (!r.known) forcedBy.add(r);
                continue;
            }
            List<List<Map<String, Object>>> when = new ArrayList<>();
            for (Region r : pending) when.add(new ArrayList<>(r.alts));
            guards.put(v, when);
        }
    }

    /** A forward-branch region of a reader: [branch, target). */
    static final class Region {
        final Label target;
        final int openedAt;
        final List<Map<String, Object>> alts = new ArrayList<>();   // OR-ed conditions under which the region executes
        final boolean known;
        boolean reads;
        // a loop head: the label the body jumps back to, the bound it is compared with, and
        // where the body's reads start
        Label head;
        Value bound;
        int valuesAt, iincAt;
        int contributed;   // how many reads it forced to "conditional"
        Region(Label target, int openedAt, Map<String, Object> test) {
            this.target = target; this.openedAt = openedAt; this.known = test != null;
            if (test != null) alts.add(test);
        }
    }

    /** The top two values of the stack without popping them: [top, second]. */
    static Value[] peek2(Deque<Value> stack) {
        Value[] out = new Value[2];
        int i = 0;
        for (Value v : stack) { out[i++] = v; if (i == 2) break; }
        return i == 2 ? out : new Value[0];
    }

    /**
     * Collapses a loop body into the repetition it is: a constant bound repeats the body that
     * many times, a bound read from the buffer makes it a length-prefixed list (the bound is
     * the prefix and stops being a field of its own). Returns false when the body cannot be
     * collapsed, leaving the reader conditional as before.
     */
    static boolean collapseLoop(Region loop, List<Map<String, Object>> values, List<Map<String, Object>> fields,
                                Map<String, Value> fieldValues, boolean isCtor, String owner) {
        List<Map<String, Object>> body = new ArrayList<>(values.subList(loop.valuesAt, values.size()));
        for (Map<String, Object> f : fields) if (f.get("type") instanceof Map<?, ?> t) for (Map<String, Object> b : body) if (t == b) return false;
        // A constructor reader keeps its fields, and a loop fills an array it stored earlier:
        // the repetition belongs to that field. Without a field to put it on the reads would
        // vanish from the struct while the packet still counted as fully typed.
        String arrayField = null;
        for (Map.Entry<String, Value> e : fieldValues.entrySet()) {
            if (!(e.getValue() instanceof ArrayV av)) continue;
            if (arrayField == null || av.size() == loop.bound) arrayField = e.getKey();
        }
        if (isCtor && !fields.isEmpty() && arrayField == null) return false;
        Map<String, Object> elem = elemStruct(body, owner);
        Object bound = constOf(loop.bound);
        if (bound instanceof Integer count && count > 0) {
            // a constant number of repeats: the body again, count times
            String hint = arrayField != null ? arrayField : hintOf.get(body.get(0));
            if (hint != null) for (Map<String, Object> b : body) hintOf.put(b, hint);
            for (int i = 1; i < count; i++) {
                for (Map<String, Object> b : body) {
                    Map<String, Object> copy = castNode((Map<?, ?>) copyNode(b));
                    if (hint != null) hintOf.put(copy, hint);
                    values.add(copy);
                    if (isCtor && !fields.isEmpty()) fields.add(field(hint != null ? hint : "v", copy));
                }
            }
            if (isCtor && !fields.isEmpty()) for (Map<String, Object> b : body) fields.add(fields.size() - (count - 1) * body.size(), field(hint != null ? hint : "v", b));
            return true;
        }
        Value b = unwrap(loop.bound);
        if (b instanceof CodecV c && "prim".equals(c.n().get("k")) && String.valueOf(c.n().get("t")).startsWith("VAR_INT")
                && loop.valuesAt > 0 && values.get(loop.valuesAt - 1) != c.n() && values.contains(c.n())) {
            // The count was read earlier, with other reads between it and the loop (a component
            // patch reads both of its counts first): the count stays where it is and the
            // repetition points back at it by name, resolved in the struct they share.
            while (values.size() > loop.valuesAt) values.remove(values.size() - 1);
            Map<String, Object> counted = node("counted", "elem", elem);
            pendingCounts.put(counted, new PassedV(c.n()));
            String countName = hintOf.get(c.n());
            String hint = arrayField != null ? arrayField
                : countName != null && countName.endsWith("Count") && countName.length() > 5 ? countName.substring(0, countName.length() - 5)
                : hintOf.getOrDefault(body.get(0), "entries");
            hintOf.putIfAbsent(counted, hint);
            values.add(counted);
            if (isCtor && !fields.isEmpty()) fields.add(field(hint, counted));
            return true;
        }
        if (b instanceof CodecV c && "prim".equals(c.n().get("k")) && String.valueOf(c.n().get("t")).startsWith("VAR_INT")) {
            // Truncate the body first: removing the count shifts everything after it down,
            // and a truncation to the index the body started at would then stop one short and
            // leave the first turn of the loop standing beside the list.
            while (values.size() > loop.valuesAt) values.remove(values.size() - 1);
            removeIdentity(values, c.n());          // the bound was the list's length prefix
            Map<String, Object> list = node("list", "elem", elem, "max", c.n().get("max"));
            String hint = arrayField != null ? arrayField : hintOf.getOrDefault(body.get(0), "entries");
            hintOf.putIfAbsent(list, hint);
            values.add(list);
            if (isCtor && !fields.isEmpty()) {
                for (int i = fields.size() - 1; i >= 0; i--) if (fields.get(i).get("type") == c.n()) fields.remove(i);
                fields.add(field(hint, list));
            }
            return true;
        }
        if (loop.bound instanceof PassedV pv) {
            // `read(buf, n)` looping n times: the count is not in front of the elements, it is
            // a field the caller read, so the repetition has to point back at it by name. Which
            // name that is belongs to the struct they share, so it is resolved there.
            while (values.size() > loop.valuesAt) values.remove(values.size() - 1);
            Map<String, Object> counted = node("counted", "elem", elem);
            pendingCounts.put(counted, pv);
            String hint = arrayField != null ? arrayField : hintOf.getOrDefault(body.get(0), "entries");
            hintOf.putIfAbsent(counted, hint);
            values.add(counted);
            if (isCtor && !fields.isEmpty()) fields.add(field(hint, counted));
            return true;
        }
        return false;
    }

    /**
     * Names the field that counts each counted repetition in a struct. A repetition whose count
     * the caller read is only nameable here, where both are fields; one whose count belongs to a
     * struct further out is left for that struct to name.
     */
    @SuppressWarnings("unchecked")
    static void attachCounts(Map<String, Object> result) {
        if (pendingCounts.isEmpty() && pendingKeys.isEmpty()) return;
        for (Map<String, Object> st : structsIn(result)) attachCountsIn(st);
    }

    /** Every struct node in a tree, outer first. */
    @SuppressWarnings("unchecked")
    static List<Map<String, Object>> structsIn(Map<String, Object> n) {
        List<Map<String, Object>> out = new ArrayList<>();
        if ("struct".equals(n.get("k"))) out.add(n);
        for (Object v : n.values()) {
            if (v instanceof Map<?, ?> m) out.addAll(structsIn(castNode(m)));
            if (v instanceof List<?> l) for (Object e : l) if (e instanceof Map<?, ?> m) {
                Object t = ((Map<String, Object>) m).get("type");
                out.addAll(structsIn(t instanceof Map<?, ?> tm ? castNode(tm) : castNode(m)));
            }
        }
        return out;
    }

    static void attachCountsIn(Map<String, Object> result) {
        if (!(result.get("fields") instanceof List<?> fs)) return;
        Map<Map<String, Object>, String> nameOf = new IdentityHashMap<>();
        for (Object o : fs) {
            Map<String, Object> f = castNode((Map<?, ?>) o);
            nameOf.put(castNode((Map<?, ?>) f.get("type")), (String) f.get("name"));
        }
        for (Object o : fs) {
            Map<String, Object> f = castNode((Map<?, ?>) o);
            for (Map<String, Object> c : countedIn(castNode((Map<?, ?>) f.get("type")))) {
                if (pendingKeys.containsKey(c)) {
                    String name = nameOf.get(pendingKeys.get(c));
                    if (name == null) continue;
                    castNode((Map<?, ?>) c.get("key")).put("field", name);
                    pendingKeys.remove(c);
                    continue;
                }
                PassedV pv = pendingCounts.get(c);
                String name = nameOf.get(pv.of());
                if (name == null) continue;
                c.put("count", pv.sub() == null ? name : name + "." + pv.sub());
                pendingCounts.remove(c);
            }
        }
    }

    /** Every counted node still waiting for a name, at any depth of a field's type. */
    @SuppressWarnings("unchecked")
    static List<Map<String, Object>> countedIn(Map<String, Object> n) {
        List<Map<String, Object>> out = new ArrayList<>();
        if (pendingCounts.containsKey(n) || pendingKeys.containsKey(n)) out.add(n);
        for (Object v : n.values()) {
            if (v instanceof Map<?, ?> m) out.addAll(countedIn(castNode(m)));
            if (v instanceof List<?> l) for (Object e : l) if (e instanceof Map<?, ?> m) {
                Object t = ((Map<String, Object>) m).get("type");
                out.addAll(countedIn(t instanceof Map<?, ?> tm ? castNode(tm) : castNode(m)));
            }
        }
        return out;
    }

    /**
     * Drops an ANDed alternative that another one already implies, so a guard says no more than
     * it has to. A branch whose body leaves the reader makes everything after it the other case,
     * which is right but often already said: `flags & 3 == 1` needs no `flags & 3 != 2` beside it.
     */
    static List<Object> reduceGuards(List<Object> when) {
        List<Object> keep = new ArrayList<>();
        for (int i = 0; i < when.size(); i++) {
            boolean implied = false;
            for (int j = 0; j < when.size(); j++) {
                if (i == j || !implies(when.get(j), when.get(i))) continue;
                if (j < i || !implies(when.get(i), when.get(j))) implied = true;   // keep the first of two equals
            }
            if (!implied) keep.add(when.get(i));
        }
        return keep;
    }

    /** Whether one alternative of a guard makes another one true, and so redundant beside it. */
    @SuppressWarnings("unchecked")
    static boolean implies(Object a, Object b) {
        if (!(a instanceof List<?> la) || !(b instanceof List<?> lb)) return false;
        if (la.equals(lb)) return true;
        if (la.size() != 1 || lb.size() != 1) return false;
        Map<String, Object> x = (Map<String, Object>) la.get(0), y = (Map<String, Object>) lb.get(0);
        // `f == a` says `f != b` for every other b, whether the equality is of the whole
        // value or of the bits under a mask
        if (!Objects.equals(x.get("field"), y.get("field"))) return false;
        if (!Objects.equals(x.get("test"), y.get("test")) || !Objects.equals(x.get("mask"), y.get("mask"))) return false;
        if (!"eq".equals(x.get("test")) && !"maskeq".equals(x.get("test"))) return false;
        return !Boolean.TRUE.equals(x.get("not")) && Boolean.TRUE.equals(y.get("not"))
                && !Objects.equals(x.get("value"), y.get("value"));
    }

    /**
     * Replaces every occurrence of one node inside a tree, by identity. Used to tie a recursive
     * codec's knot: the placeholder its operator was handed becomes a ref to what it built.
     */
    @SuppressWarnings("unchecked")
    static boolean replaceNode(Object tree, Map<String, Object> find, Map<String, Object> with) {
        boolean any = false;
        if (tree instanceof Map<?, ?> m) {
            for (Map.Entry<String, Object> e : ((Map<String, Object>) m).entrySet()) {
                if (e.getValue() == find) { e.setValue(with); any = true; }
                else any |= replaceNode(e.getValue(), find, with);
            }
        } else if (tree instanceof List<?> l) {
            List<Object> list = (List<Object>) l;
            for (int i = 0; i < list.size(); i++) {
                if (list.get(i) == find) { list.set(i, with); any = true; }
                else any |= replaceNode(list.get(i), find, with);
            }
        }
        return any;
    }

    /**
     * A switch on an enum read, walked arm by arm: `switch (type) { case A -> …; case B ->
     * buf.readX() }` is a dispatch on the enum whose case for each constant is what its arm
     * read — nothing (unit), one value, or a struct of several. The enum node in the reader's
     * values is replaced by the dispatch, and the arms' reads leave the values, since the
     * dispatch carries them.
     */
    static final class SwitchWalk {
        final Map<String, Object> key;
        final int keyAt;
        final Map<Label, Integer> caseOf = new HashMap<>();
        Label dflt, end;
        Integer current;
        int segStart;
        final Map<Integer, List<Map<String, Object>>> arms = new TreeMap<>();

        SwitchWalk(Map<String, Object> key, int keyAt) { this.key = key; this.keyAt = keyAt; }

        void arm(List<Map<String, Object>> values) {
            if (current != null && current >= 0) arms.put(current, new ArrayList<>(values.subList(Math.min(segStart, values.size()), values.size())));
            current = null;
        }

        void finish(List<Map<String, Object>> values, String owner) {
            @SuppressWarnings("unchecked")
            List<String> constants = (List<String>) key.get("values");
            if (constants == null) return;
            List<Map<String, Object>> cases = new ArrayList<>();
            for (int i = 0; i < constants.size(); i++) {
                List<Map<String, Object>> body = arms.getOrDefault(i, List.of());
                Map<String, Object> t = body.isEmpty() ? node("unit") : body.size() == 1 ? body.get(0) : elemStruct(body, owner);
                Map<String, Object> c = new LinkedHashMap<>();
                c.put("k", "case"); c.put("id", constants.get(i)); c.put("num", i); c.put("type", t);
                cases.add(c);
            }
            Map<String, Object> d = node("dispatch", "name", shortName(owner), "key", key, "cases", cases);
            for (List<Map<String, Object>> body : arms.values()) for (Map<String, Object> b : body) removeIdentity(values, b);
            int at = values.indexOf(key);
            if (at >= 0) values.set(at, d); else values.add(keyAt, d);
            hintOf.putIfAbsent(d, hintOf.getOrDefault(key, lowerFirst(shortName(owner))));
        }
    }

    /** What one turn of a loop reads: the value itself, or a struct of the values in order. */
    static Map<String, Object> elemStruct(List<Map<String, Object>> body, String owner) {
        if (body.size() == 1) return body.get(0);
        List<Map<String, Object>> fs = new ArrayList<>();
        for (Map<String, Object> b : body) {
            Map<String, Object> f = new LinkedHashMap<>();
            f.put("name", hintOf.getOrDefault(b, "v" + fs.size()));
            f.put("type", b);
            fs.add(f);
        }
        return node("struct", "name", shortName(owner).replaceAll("Packet$", "") + "Entry", "fields", fs);
    }

    /**
     * Collapses a loop that has no counter but ends on a test of something it read — the
     * equipment packet reads a slot byte whose top bit says another entry follows — into a
     * list that is read until that test fails. Returns false when the body cannot be
     * collapsed, leaving the reader conditional as before.
     */
    static boolean collapseTerminated(Label head, Map<Label, Integer> labelAt, Region cond,
                                      List<Map<String, Object>> values, List<Map<String, Object>> fields,
                                      Map<String, Value> fieldValues, boolean isCtor, String owner) {
        Integer at = labelAt.get(head);
        if (at == null || cond == null || !cond.known || cond.alts.size() != 1) return false;
        if (at >= values.size()) return false;
        List<Map<String, Object>> body = new ArrayList<>(values.subList(at, values.size()));
        for (Map<String, Object> f : fields) if (f.get("type") instanceof Map<?, ?> t) for (Map<String, Object> b : body) if (t == b) return false;
        // The test has to be on a value of the body: an entry that says whether another one
        // follows. A condition on anything else says nothing about how long the list is.
        Map<String, Object> test = cond.alts.get(0);
        // Only a continuation bit ends a list this way. A loop that ends on anything else is
        // some other repetition — a var int's own byte loop, say — and calling it a list would
        // claim a shape the packet does not have.
        if (!"bit".equals(test.get("test"))) return false;
        boolean onBody = false;
        for (Map<String, Object> b : body) if (b == test.get("node")) onBody = true;
        if (!onBody) return false;
        // A constructor reader keeps its fields, and this loop fills a collection it made
        // before entering: the list belongs to that field. One candidate only — guessing
        // between several would put the reads on the wrong field.
        String listField = null;
        if (isCtor) {
            for (Map.Entry<String, Value> e : fieldValues.entrySet()) {
                if (unwrap(e.getValue()) instanceof CodecV) continue;
                if (listField != null) return false;
                listField = e.getKey();
            }
            if (listField == null) return false;
        }
        Map<String, Object> elem = elemStruct(body, owner);
        // Name the field the test is on: a reader of the schema has no identity to follow it by.
        String on = null;
        if ("struct".equals(elem.get("k"))) {
            for (Map<String, Object> f : castList(elem.get("fields"))) if (f.get("type") == test.get("node")) on = String.valueOf(f.get("name"));
        } else if (elem == test.get("node")) {
            on = "";   // the entry is the value the test is on
        }
        if (on == null) return false;
        Map<String, Object> when = new LinkedHashMap<>(test);
        when.remove("node");
        when.put("field", on);
        while (values.size() > at) values.remove(values.size() - 1);
        Map<String, Object> list = node("whilelist", "elem", elem, "while", when);
        hintOf.putIfAbsent(list, listField != null ? listField : "entries");
        values.add(list);
        if (isCtor) fields.add(field(listField, list));
        return true;
    }

    /** Whether a node tree contains one particular node, by identity. */
    static boolean holds(Object tree, Map<String, Object> v) {
        if (tree == v) return true;
        if (tree instanceof Map<?, ?> m) {
            for (Object x : m.values()) if (holds(x, v)) return true;
        } else if (tree instanceof List<?> l) {
            for (Object x : l) if (holds(x, v)) return true;
        }
        return false;
    }

    /** A copy of a node with fresh identity, so guards and hints do not follow it. */
    @SuppressWarnings("unchecked")
    static Object copyNode(Object o) {
        if (o instanceof Map<?, ?> m) {
            Map<String, Object> out = new LinkedHashMap<>();
            for (Map.Entry<?, ?> e : m.entrySet()) out.put(String.valueOf(e.getKey()), copyNode(e.getValue()));
            return out;
        }
        if (o instanceof List<?> l) {
            List<Object> out = new ArrayList<>();
            for (Object v : l) out.add(copyNode(v));
            return out;
        }
        return o;
    }

    static Map<String, Object> test(Map<String, Object> node, String kind, Object value, boolean not) {
        Map<String, Object> t = new LinkedHashMap<>();
        t.put("node", node); t.put("test", kind);
        if (value != null) t.put("value", value);
        if (not) t.put("not", true);
        return t;
    }
    static Map<String, Object> negate(Map<String, Object> t) {
        Map<String, Object> m = new LinkedHashMap<>(t);
        if ("cmp".equals(m.get("test"))) {
            m.put("op", switch (String.valueOf(m.get("op"))) { case ">" -> "<="; case "<=" -> ">"; case "<" -> ">="; case ">=" -> "<"; default -> "?"; });
            return m;
        }
        if (Boolean.TRUE.equals(m.get("not"))) m.remove("not"); else m.put("not", true);
        return m;
    }

    /**
     * The condition under which a conditional branch falls through, from its operands: a bit of a
     * value read (iand with a constant), a boolean, an int or enum compared with a constant, or a
     * static predicate of a value (evaluated over its domain). Null when the walker cannot tell.
     */
    static Map<String, Object> branchTest(Opcode op, Deque<Value> stack) {
        // which of the operands are bit fields of a packed integer, before they are popped
        Map<Map<String, Object>, String> subs = new IdentityHashMap<>();
        int seen = 0;
        for (Value v : stack) {
            if (subOfValue(v) != null) subs.put(nodeOfValue(v), subOfValue(v));
            if (++seen == 2) break;
        }
        Map<String, Object> t = branchTestOf(op, stack);
        if (t != null && t.get("node") instanceof Map<?, ?> node) {
            String sub = subs.get(castNode(node));
            if (sub != null) t.put("sub", sub);
        }
        return t;
    }

    static Map<String, Object> branchTestOf(Opcode op, Deque<Value> stack) {
        String n = op.name();
        boolean two = n.startsWith("IF_I") || n.startsWith("IF_A");
        Value top = stack.isEmpty() ? null : stack.pop();
        Value second = two && !stack.isEmpty() ? stack.pop() : null;
        // a value the caller passed in is tested like one read here
        if (top instanceof PassedV pt) top = new CodecV(pt.of());
        if (second instanceof PassedV ps) second = new CodecV(ps.of());
        if (top instanceof BitsV bt) top = new CodecV(bt.of());
        if (second instanceof BitsV bs) second = new CodecV(bs.of());
        if (!two) {
            if (top instanceof MaskedV mv) {
                return switch (n) {
                    case "IFEQ", "IFLE" -> test(mv.of(), "bit", mv.mask(), false);
                    case "IFNE", "IFGT" -> test(mv.of(), "bit", mv.mask(), true);
                    default -> null;
                };
            }
            if (top instanceof ConstV c && c.v() instanceof Integer mask && !stack.isEmpty() && stack.peek() instanceof CodecV x) {
                stack.pop();   // value & mask, where the and was not interpreted
                return switch (n) {
                    case "IFEQ", "IFLE" -> test(x.n(), "bit", mask, false);
                    case "IFNE", "IFGT" -> test(x.n(), "bit", mask, true);
                    default -> null;
                };
            }
            if (top instanceof CodecV x) {
                Map<String, Object> node = x.n();
                if ("pred".equals(node.get("k"))) {
                    Map<String, Object> of = castNode((Map<?, ?>) node.get("of"));
                    return switch (n) {
                        case "IFEQ" -> test(of, "in", node.get("values"), false);
                        case "IFNE" -> test(of, "in", node.get("values"), true);
                        default -> null;
                    };
                }
                boolean isBool = "prim".equals(node.get("k")) && "BOOL".equals(node.get("t"));
                return switch (n) {
                    case "IFEQ" -> isBool ? test(node, "true", null, false) : test(node, "eq", 0, true);
                    case "IFNE" -> isBool ? test(node, "true", null, true) : test(node, "eq", 0, false);
                    case "IFLE" -> cmp(node, ">", 0);    // jumps when X <= 0
                    case "IFGT" -> cmp(node, "<=", 0);
                    case "IFLT" -> cmp(node, ">=", 0);
                    case "IFGE" -> cmp(node, "<", 0);
                    default -> null;
                };
            }
            return null;
        }
        // `(x - 1) == -1`: a value read, shifted, compared with a number — which is the value
        // itself compared with that number shifted back
        OffsetV off = second instanceof OffsetV o2 ? o2 : top instanceof OffsetV o1 ? o1 : null;
        if (off != null) {
            Object v = constOf(second instanceof OffsetV ? top : second);
            if (!(v instanceof Integer k)) return null;
            return switch (n) {
                case "IF_ICMPNE" -> test(off.of(), "eq", k - off.delta(), false);
                case "IF_ICMPEQ" -> test(off.of(), "eq", k - off.delta(), true);
                default -> null;
            };
        }
        // `(flags & 3) == 2`: some bits of a value read earlier compared with a constant
        MaskedV masked = second instanceof MaskedV m2 ? m2 : top instanceof MaskedV m1 ? m1 : null;
        if (masked != null) {
            Object v = constOf(second instanceof MaskedV ? top : second);
            if (v == null) return null;
            Map<String, Object> t = switch (n) {
                case "IF_ICMPNE" -> test(masked.of(), "maskeq", v, false);
                case "IF_ICMPEQ" -> test(masked.of(), "maskeq", v, true);
                default -> null;
            };
            if (t != null) t.put("mask", masked.mask());
            return t;
        }
        CodecV x = second instanceof CodecV c2 ? c2 : top instanceof CodecV c1 ? c1 : null;
        Value other = second instanceof CodecV ? top : second;
        if (x == null || "pred".equals(x.n().get("k"))) return null;
        Object val = other instanceof ConstV c ? c.v()
            : other instanceof OtherV ov && ov.what().startsWith("static:") ? ov.what().substring(ov.what().lastIndexOf('.') + 1) : null;
        if (val == null) return null;
        boolean xLeft = second instanceof CodecV;   // X op N, or N op X
        return switch (n) {
            case "IF_ICMPNE", "IF_ACMPNE" -> test(x.n(), "eq", val, false);
            case "IF_ICMPEQ", "IF_ACMPEQ" -> test(x.n(), "eq", val, true);
            case "IF_ICMPGE" -> val instanceof Integer ? cmp(x.n(), xLeft ? "<" : ">", val) : null;    // jumps when X >= N
            case "IF_ICMPLT" -> val instanceof Integer ? cmp(x.n(), xLeft ? ">=" : "<=", val) : null;
            case "IF_ICMPGT" -> val instanceof Integer ? cmp(x.n(), xLeft ? "<=" : ">=", val) : null;
            case "IF_ICMPLE" -> val instanceof Integer ? cmp(x.n(), xLeft ? ">" : "<", val) : null;
            default -> null;
        };
    }

    static Map<String, Object> cmp(Map<String, Object> node, String op, Object value) {
        Map<String, Object> t = test(node, "cmp", value, false);
        t.put("op", op);
        return t;
    }

    /**
     * Names the guard values of a struct's guarded fields and writes them as "when" (a list of
     * alternatives, each a list of tests that must all hold). A guard value that is not a field
     * (a flags byte only kept in a local) becomes one, at its place in the read order.
     */
    @SuppressWarnings("unchecked")
    static boolean attachGuards(Map<String, Object> result, List<Map<String, Object>> values, Map<Map<String, Object>, List<List<Map<String, Object>>>> guards) {
        List<Map<String, Object>> fields = (List<Map<String, Object>>) result.get("fields");
        Map<Map<String, Object>, String> nameOf = new IdentityHashMap<>();
        for (Map<String, Object> f : fields) nameOf.put(castNode((Map<?, ?>) f.get("type")), (String) f.get("name"));
        for (Map<String, Object> f : new ArrayList<>(fields)) {
            List<List<Map<String, Object>>> when = guards.get(castNode((Map<?, ?>) f.get("type")));
            if (when == null) continue;
            List<Object> out = new ArrayList<>();
            for (List<Map<String, Object>> alts : when) {
                List<Object> altsOut = new ArrayList<>();
                for (Map<String, Object> t : alts) {
                    Map<String, Object> node = castNode((Map<?, ?>) t.get("node"));
                    String name = nameOf.get(node);
                    if (name == null) {
                        // synthesise the guard field at its read position
                        int at = -1;
                        for (int i = 0; i < values.size(); i++) if (values.get(i) == node) at = i;
                        if (at < 0) return false;
                        int insert = fields.size();
                        for (int i = 0; i < fields.size(); i++) {
                            int vi = -1;
                            for (int j = 0; j < values.size(); j++) if (values.get(j) == fields.get(i).get("type")) vi = j;
                            if (vi > at) { insert = i; break; }
                        }
                        name = "bit".equals(t.get("test")) ? "flags" : hintOf.getOrDefault(node, "guard");
                        Map<String, Object> g = new LinkedHashMap<>();
                        g.put("name", name); g.put("type", node);
                        fields.add(insert, g);
                        nameOf.put(node, name);
                    }
                    Map<String, Object> o = new LinkedHashMap<>();
                    o.put("field", t.get("sub") == null ? name : name + "." + t.get("sub"));
                    o.put("test", t.get("test"));
                    if (t.get("value") != null) o.put("value", t.get("value"));
                    if (t.get("mask") != null) o.put("mask", t.get("mask"));
                    if (t.get("op") != null) o.put("op", t.get("op"));
                    if (Boolean.TRUE.equals(t.get("not"))) o.put("not", true);
                    altsOut.add(o);
                }
                out.add(altsOut);
            }
            f.put("when", reduceGuards(out));
        }
        return true;
    }

    /**
     * Evaluates a static boolean method of one int-like argument (shouldHaveParameters(method))
     * for every input in -128..127 by running its straight-line integer code; the inputs for
     * which it returns true, or null when the code does more than compare and jump.
     */
    /**
     * The bit field a static helper of one integer cuts out of it, as {offset, width, signed}:
     * ClientboundMoveEntityPacket.unpackStepCount is `packed >>> 1` and unpackOnGround is
     * `(packed & 1) != 0`, so the var int they are both given is not two values converted but
     * one packed integer. Null when the body is anything but shifts, masks and a test of them.
     */
    /**
     * The bit field a static helper of one integer returns, as {offset, width, signed}: the
     * helper's shifts and masks are followed over a model of the value — which bits of the
     * original sit where — so `packed << (64 - X_OFFSET - PACKED_HORIZONTAL_LENGTH) >> (64 -
     * PACKED_HORIZONTAL_LENGTH)` (BlockPos.getX) is bits 38..63 signed, and `(int) (packed &
     * 0xFFFFFFFFL)` (ChunkPos.getX) bits 0..31 signed. Constants come from the bytecode and
     * from the static final fields it reads, folded through the arithmetic between them.
     * Null when the method is not such a helper.
     */
    static int[] bitField(String owner, String name, String desc) {
        int[] r = bitField0(owner, name, desc);
        if (System.getenv("MC26_TRACE") != null && name.startsWith("get")) System.err.println("trace: bitField " + shortName(owner) + "." + name + desc + " -> " + (r == null ? "null" : r[0] + ":" + r[1]) + " last=" + lastBitFieldStop);
        return r;
    }
    static String lastBitFieldStop = "";
    static int[] bitField0(String owner, String name, String desc) {
        MethodModel target = findMethod(owner, name, desc);
        if (target == null) { lastBitFieldStop = "no method"; return null; }
        int width = desc.startsWith("(J") ? 64 : 32;
        // a run: {source offset, width, signed, position in the value}; a constant: Long
        Deque<Object> st = new ArrayDeque<>();
        for (CodeElement el : target.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            switch (el) {
                case LoadInstruction li -> {
                    if (li.slot() != 0) { lastBitFieldStop = "load " + li.slot(); return null; }
                    st.push(new int[]{0, width, 0, 0});
                }
                case ConstantInstruction ci -> {
                    Object v = ci.constantValue();
                    if (v instanceof Integer k) st.push((long) k);
                    else if (v instanceof Long k) st.push(k);
                    else return null;
                }
                case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> {
                    Object v = staticConstant(fi.owner().asInternalName(), fi.name().stringValue());
                    if (v == null) { lastBitFieldStop = "getstatic " + fi.name().stringValue(); return null; }
                    st.push(v);
                }
                case ConvertInstruction cv -> {
                    // (int) of a long keeps its low 32 bits; the int's top bit is then the sign
                    Opcode op = cv.opcode();
                    if (op != Opcode.L2I && op != Opcode.I2L) { lastBitFieldStop = "convert " + op; return null; }
                    if (!(st.peek() instanceof int[] f)) { if (st.peek() instanceof Long) break; lastBitFieldStop = "convert of nothing"; return null; }
                    if (op == Opcode.L2I) {
                        if (f[3] >= 32) { lastBitFieldStop = "l2i above 32"; return null; }
                        f[1] = Math.min(f[1], 32 - f[3]);
                        if (f[1] + f[3] == 32) f[2] = 1;
                    }
                }
                case OperatorInstruction oi -> {
                    Opcode op = oi.opcode();
                    Object b = st.isEmpty() ? null : st.pop(), a = st.isEmpty() ? null : st.pop();
                    if (a instanceof Long x && b instanceof Long y) {
                        switch (op) {
                            case IADD, LADD -> st.push(x + y);
                            case ISUB, LSUB -> st.push(x - y);
                            case IMUL, LMUL -> st.push(x * y);
                            case ISHL, LSHL -> st.push(x << y);
                            case ISHR, LSHR -> st.push(x >> y);
                            case IUSHR, LUSHR -> st.push(x >>> y);
                            case IAND, LAND -> st.push(x & y);
                            case IOR, LOR -> st.push(x | y);
                            default -> { return null; }
                        }
                        break;
                    }
                    if (!(a instanceof int[] f) || !(b instanceof Long kl)) { lastBitFieldStop = "operands " + op + " " + a + " " + b; return null; }
                    int k = (int) (long) kl;
                    switch (op) {
                        case ISHL, LSHL -> {
                            // the run moves up; what leaves the top is gone
                            f[3] += k;
                            f[1] = Math.min(f[1], width - f[3]);
                            if (f[1] <= 0) return null;
                            st.push(f);
                        }
                        case IUSHR, LUSHR, ISHR, LSHR -> {
                            f[3] -= k;
                            if (f[3] < 0) { f[0] += -f[3]; f[1] += f[3]; f[3] = 0; }
                            if (f[1] <= 0) return null;
                            f[2] = (op == Opcode.ISHR || op == Opcode.LSHR) ? 1 : 0;
                            st.push(f);
                        }
                        case IAND, LAND -> {
                            // one run of bits: `& 1` is bit 0, `& 2` is bit 1, `& 12` is bits 2-3
                            long mask = op == Opcode.IAND ? (kl & 0xFFFFFFFFL) : kl;
                            if (mask <= 0) return null;
                            int off = Long.numberOfTrailingZeros(mask), w = Long.numberOfTrailingZeros((mask >>> off) + 1);
                            if (((1L << w) - 1) << off != mask) return null;
                            int from = Math.max(f[3], off), to = Math.min(f[3] + f[1], off + w);
                            if (to <= from) return null;
                            st.push(new int[]{f[0] + (from - f[3]), to - from, 0, from});
                        }
                        default -> { return null; }
                    }
                }
                // `(x & 1) != 0`: the branch is the boolean, the bits it tests are the field.
                // A test of the whole value is a predicate of it, not a field of it.
                case BranchInstruction bi -> {
                    if (!bi.opcode().name().equals("IFEQ") && !bi.opcode().name().equals("IFNE")) return null;
                    return st.peek() instanceof int[] f && (f[0] > 0 || f[1] < width) ? new int[]{f[0], f[1], f[2]} : null;
                }
                case ReturnInstruction ri -> {
                    return st.peek() instanceof int[] f && (f[0] > 0 || f[1] < width) ? new int[]{f[0], f[1], f[2]} : null;
                }
                case Instruction any -> { lastBitFieldStop = "instruction " + any.opcode(); return null; }
                default -> { }
            }
        }
        lastBitFieldStop = "fell off";
        return null;
    }

    /** A static final int or long of a loaded class, read by reflection (BlockPos.X_OFFSET is computed in <clinit>). */
    static Long staticConstant(String owner, String field) {
        try {
            Class<?> c = loadClass(owner);
            if (c == null) return null;
            Field f = c.getDeclaredField(field);
            f.setAccessible(true);
            Object v = f.get(null);
            if (v instanceof Integer i) return (long) i;
            if (v instanceof Long l) return l;
        } catch (Throwable t) {
            return null;
        }
        return null;
    }

    /** `unpackStepCount` names the field `stepCount`; so do `getX`, `isOnGround`, `readFlags`. */
    static String bitFieldName(String method) {
        for (String p : new String[]{"unpack", "get", "is", "read", "has"}) {
            if (method.startsWith(p) && method.length() > p.length() && Character.isUpperCase(method.charAt(p.length()))) {
                method = method.substring(p.length());
                break;
            }
        }
        return Character.toLowerCase(method.charAt(0)) + method.substring(1);
    }

    /**
     * Records one named bit field on the integer it is cut from, turning that node into a `bits`
     * node the first time. The node is changed in place, so every guard and count already
     * pointing at it goes on pointing at the same field.
     */
    @SuppressWarnings("unchecked")
    static Map<String, Object> putBitField(Map<String, Object> of, String owner, String name, int[] bf) {
        if (!"bits".equals(of.get("k"))) {
            String t = String.valueOf(of.get("t"));
            if (!"prim".equals(of.get("k"))) return null;
            of.put("k", "bits");
            of.put("bits", switch (t) {
                case "BYTE", "UNSIGNED_BYTE" -> 8;
                case "SHORT", "UNSIGNED_SHORT", "CHAR" -> 16;
                case "LONG", "VAR_LONG" -> 64;
                default -> 32;
            });
            of.put("of", t);
            of.remove("t");
            of.put("name", shortName(owner) + "$Packed");
            of.put("fields", new ArrayList<Map<String, Object>>());
            hintOf.put(of, "flags");
        }
        List<Map<String, Object>> fs = (List<Map<String, Object>>) of.get("fields");
        for (Map<String, Object> f : fs) if (name.equals(f.get("name"))) return of;
        Map<String, Object> f = new LinkedHashMap<>();
        f.put("name", name); f.put("offset", bf[0]); f.put("width", bf[1]);
        if (bf[2] != 0) f.put("signed", true);
        fs.add(f);
        fs.sort(Comparator.comparingInt(x -> (Integer) x.get("offset")));
        return of;
    }

    static List<Integer> evalPredicate(String owner, String name, String desc) {
        ClassModel cm = classModel(owner);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(name) || !m.methodType().stringValue().equals(desc)) continue;
            List<CodeElement> code = new ArrayList<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) code.add(el);
            Map<Label, Integer> at = new HashMap<>();
            for (int i = 0; i < code.size(); i++) if (code.get(i) instanceof LabelTarget lt) at.put(lt.label(), i);
            List<Integer> yes = new ArrayList<>();
            for (int input = -128; input <= 127; input++) {
                Integer r = runPredicate(code, at, input);
                if (r == null) return null;
                if (r != 0) yes.add(input);
            }
            return yes;
        }
        return null;
    }

    static Integer runPredicate(List<CodeElement> code, Map<Label, Integer> at, int input) {
        Deque<Integer> st = new ArrayDeque<>();
        int pc = 0, steps = 0;
        while (pc < code.size() && steps++ < 500) {
            CodeElement el = code.get(pc++);
            switch (el) {
                case LoadInstruction li -> { if (li.slot() != 0) return null; st.push(input); }
                case ConstantInstruction ci -> { if (!(ci.constantValue() instanceof Integer v)) return null; st.push(v); }
                case OperatorInstruction oi -> {
                    if (st.size() < 2) return null;
                    int b = st.pop(), a = st.pop();
                    switch (oi.opcode()) {
                        case IAND -> st.push(a & b);
                        case IOR -> st.push(a | b);
                        case IXOR -> st.push(a ^ b);
                        case IADD -> st.push(a + b);
                        case ISUB -> st.push(a - b);
                        default -> { return null; }
                    }
                }
                case BranchInstruction bi -> {
                    String n = bi.opcode().name();
                    boolean taken;
                    if (n.equals("GOTO") || n.equals("GOTO_W")) taken = true;
                    else if (n.startsWith("IF_ICMP")) {
                        if (st.size() < 2) return null;
                        int b = st.pop(), a = st.pop();
                        taken = switch (n) { case "IF_ICMPEQ" -> a == b; case "IF_ICMPNE" -> a != b; case "IF_ICMPLT" -> a < b; case "IF_ICMPGE" -> a >= b; case "IF_ICMPGT" -> a > b; case "IF_ICMPLE" -> a <= b; default -> false; };
                    } else if (n.startsWith("IF")) {
                        if (st.isEmpty()) return null;
                        int a = st.pop();
                        taken = switch (n) { case "IFEQ" -> a == 0; case "IFNE" -> a != 0; case "IFLT" -> a < 0; case "IFGE" -> a >= 0; case "IFGT" -> a > 0; case "IFLE" -> a <= 0; default -> false; };
                    } else return null;
                    if (taken) { Integer t = at.get(bi.target()); if (t == null) return null; pc = t; }
                }
                case ReturnInstruction ri -> { return ri.opcode() == Opcode.IRETURN && !st.isEmpty() ? st.pop() : null; }
                case PseudoInstruction pi -> { }   // labels, line numbers, local variable tables
                default -> { return null; }
            }
        }
        return null;
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> castNode(Map<?, ?> m) { return (Map<String, Object>) m; }

    /**
     * When every argument of a constructor is an unnamed range of bits of one long, the
     * constructor's parameter names name those ranges as bit fields of that long, which is
     * returned as the packed node it now is. Null otherwise.
     */
    static Map<String, Object> namedInlineBits(String cls, String desc, List<Value> args) {
        if (args.isEmpty()) return null;
        Map<String, Object> of = null;
        for (Value a : args) {
            if (!(a instanceof BitsV b) || b.name() != null) return null;
            if (of == null) of = b.of(); else if (of != b.of()) return null;
        }
        List<String> params = ctorParamNames(cls, desc);
        if (params == null || params.size() != args.size()) return null;
        for (int i = 0; i < args.size(); i++) {
            BitsV b = (BitsV) args.get(i);
            if (putBitField(of, cls, params.get(i), new int[] {b.offset(), b.width(), b.signed() ? 1 : 0}) == null) return null;
        }
        return of;
    }

    static List<Map<String, Object>> namedByCtor(String owner, String desc, List<Map<String, Object>> values) {
        List<String> names = recordComponents(owner);
        if (names == null || names.size() != values.size()) {
            List<String> params = ctorParamNames(owner, desc);
            if (params != null && params.size() == values.size()) names = params;
        }
        List<Map<String, Object>> fs = new ArrayList<>();
        for (int i = 0; i < values.size(); i++) {
            Map<String, Object> f = new LinkedHashMap<>();
            f.put("name", names != null && names.size() == values.size() ? names.get(i) : hintOf.getOrDefault(values.get(i), "v" + i));
            f.put("type", values.get(i)); fs.add(f);
        }
        return fs;
    }

    /**
     * A constructor call inside a reader: new X(v1, v2, …). All arguments wire values → a struct
     * X of them; some of them (new GameProfile(uuid, name, properties) with the uuid read
     * earlier) → the constructor names the wire values, which stay in the enclosing reader in
     * read order; none → an ordinary object, not a wire value.
     */
    static Map<String, Object> constructed(String cls, String desc, List<Value> args, List<Map<String, Object>> values,
                                           Map<Map<String, Object>, List<List<Map<String, Object>>>> guards) {
        List<Map<String, Object>> wire = new ArrayList<>();
        for (Value a : args) if (a instanceof CodecV c) wire.add(c.n());
        if (wire.isEmpty()) return null;
        List<String> params = ctorParamNames(cls, desc);
        List<String> comps = recordComponents(cls);
        List<String> names = params != null && params.size() == args.size() ? params : comps != null && comps.size() == args.size() ? comps : null;
        // A value read under a condition on an earlier read stays a field of the reader
        // whose value it is conditional on: a `when` names a sibling, and inside a struct of
        // its own the guard would be out of reach. When every value is read under the same
        // condition the struct is what is conditional (a literal node stub, when the flags
        // say literal), and the guard moves onto it; when the conditions differ (a command
        // argument node's suggestion id, only when a further bit says so) the values stay
        // flat, named by the constructor.
        List<List<Map<String, Object>>> shared = null;
        boolean guarded = false;
        for (Map<String, Object> w : wire) {
            List<List<Map<String, Object>>> g = guards.get(w);
            if (g == null) { if (shared != null) guarded = true; continue; }
            if (shared == null) { if (w != wire.get(0)) guarded = true; shared = g; }
            else if (!shared.equals(g)) guarded = true;
        }
        if (wire.size() < args.size() || guarded) {
            // some arguments were computed (new Vec3(pos.x + dx, …)): the wire values keep their
            // read order in the enclosing reader; the constructor only names them
            if (names != null) for (int i = 0; i < args.size(); i++) if (args.get(i) instanceof CodecV c) hintOf.put(c.n(), names.get(i));
            return null;
        }
        // the wire order is the read order (values), not the argument order: new MapPatch(startX, startY, width, height, …)
        List<Integer> order = new ArrayList<>();
        for (int i = 0; i < wire.size(); i++) order.add(i);
        List<Integer> readAt = new ArrayList<>();
        for (Map<String, Object> w : wire) { int at = -1; for (int j = 0; j < values.size(); j++) if (values.get(j) == w) at = j; readAt.add(at); }
        order.sort(Comparator.comparingInt(readAt::get));
        for (Map<String, Object> w : wire) removeIdentity(values, w);
        List<Map<String, Object>> fs = new ArrayList<>();
        for (int i : order) {
            Map<String, Object> f = new LinkedHashMap<>();
            f.put("name", names != null ? names.get(i) : hintOf.getOrDefault(wire.get(i), "v" + i));
            f.put("type", wire.get(i)); fs.add(f);
        }
        Map<String, Object> st = node("struct", "name", shortName(cls), "fields", fs);
        if (shared != null) {
            for (Map<String, Object> w : wire) guards.remove(w);
            guards.put(st, shared);
        }
        return st;
    }

    static void removeIdentity(List<Map<String, Object>> values, Map<String, Object> v) {
        for (int i = values.size() - 1; i >= 0; i--) if (values.get(i) == v) { values.remove(i); return; }
    }

    /**
     * enum X { A(reader, writer), … } with the reader lambdas applied in a loop over an EnumSet
     * (ClientboundPlayerInfoUpdatePacket.Action): every constant's reader is interpreted and its
     * fields join the builder's struct, guarded by the constant ("when"); the struct records the
     * enum ("guard") so the generator finds the packet's EnumSet field.
     */
    static boolean expandEnumReaders(String enumClass, Map<String, Object> builder, int depth) {
        ClassModel cm = classModel(enumClass);
        if (cm == null || enumValues(enumClass) == null) return false;
        List<Map<String, Object>> fields = castList(builder.get("fields"));
        if (fields == null) return false;
        int added = 0;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<clinit>")) continue;
            Deque<Value> stack = new ArrayDeque<>();
            for (CodeElement el : m.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
                if (el instanceof InvokeInstruction ii && ii.opcode() == Opcode.INVOKESPECIAL && ii.name().stringValue().equals("<init>")
                        && ii.owner().asInternalName().equals(enumClass)) {
                    List<Value> a = popArgs(stack, arity(ii.typeSymbol().descriptorString()));
                    if (!stack.isEmpty()) stack.pop();
                    String constant = a.size() > 0 && a.get(0) instanceof ConstV c && c.v() instanceof String s ? s : null;
                    LambdaV reader = null;
                    for (Value v : a) {
                        // the reader takes the buffer last: (Builder, FriendlyByteBuf)V; the writer takes it first
                        if (v instanceof LambdaV l && l.desc().matches("\\(.*FriendlyByteBuf;\\)V")) reader = l;
                    }
                    if (constant == null || reader == null) continue;
                    Map<String, Object> part = readerNode(reader.owner(), reader.name(), reader.desc(), depth + 1);
                    if (!"struct".equals(part.get("k"))) continue;
                    for (Map<String, Object> f : castList(part.get("fields"))) {
                        Map<String, Object> g = new LinkedHashMap<>(f);
                        g.put("when", constant);
                        fields.add(g);
                        added++;
                    }
                    continue;
                }
                if (el instanceof FieldInstruction fi && fi.opcode() == Opcode.PUTSTATIC) { stack.clear(); continue; }
                step(el, stack, enumClass, depth);
            }
            break;
        }
        if (added == 0) return false;
        builder.put("guard", enumClass);
        guardExpansions++;
        return true;
    }

    @SuppressWarnings("unchecked")
    static List<Map<String, Object>> castList(Object o) { return o instanceof List<?> l ? (List<Map<String, Object>>) l : null; }

    /** Invocations inside a reader: buffer reads, codec decodes, nested readers, constructors. */
    static void readerInvoke(InvokeInstruction ii, Deque<Value> stack, String self, int depth, List<Map<String, Object>> values,
                             Map<Map<String, Object>, List<List<Map<String, Object>>>> guards) {
        String owner = ii.owner().asInternalName(), name = ii.name().stringValue(), desc = ii.typeSymbol().descriptorString();
        boolean isStatic = ii.opcode() == Opcode.INVOKESTATIC;
        int n = arity(desc);
        List<Value> args = popArgs(stack, n);
        Value recv = isStatic ? null : (stack.isEmpty() ? new OtherV("underflow") : stack.pop());
        String ret = desc.substring(desc.indexOf(')') + 1);
        // type.ordinal() on an enum just read: the number a switch is about to select on
        if (name.equals("ordinal") && desc.equals("()I") && recv instanceof CodecV oc && "enum".equals(oc.n().get("k"))) {
            stack.push(new PassedV(oc.n(), "ordinal"));
            return;
        }
        // type.streamCodec() on a data component type just read: the component's own codec,
        // every one of which the components section describes, selected by that type
        if (name.equals("streamCodec") && isComponentType(recv)) { stack.push(new CodecV(componentDispatch(nodeOfValue(recv)))); return; }
        // codecGetter.apply(type): a captured function of the type — a method reference to
        // streamCodec, or a lambda wrapping it (in a length prefix)
        if (recv instanceof LambdaV l && (name.equals("apply") || name.equals("get"))) {
            if (l.name().equals("streamCodec") && isComponentType(arg(args, 0))) { stack.push(new CodecV(componentDispatch(nodeOfValue(arg(args, 0))))); return; }
            Map<String, Object> made = inlineFactory(l.owner(), l.name(), l.desc(), args, depth + 1);
            stack.push(made != null ? new CodecV(made) : new OtherV("apply:" + shortName(l.owner()) + "." + l.name()));
            return;
        }
        if ((recv instanceof NewV || recv instanceof OtherV ov1 && ov1.what().startsWith("new:")) && (name.equals("apply") || name.equals("get"))) {
            String cls = recv instanceof NewV nv1 ? nv1.cls() : ((OtherV) recv).what().substring(4);
            Map<String, Object> made = inlineMethod(cls, name, desc, args, depth + 1, false);
            stack.push(made != null ? new CodecV(made) : new OtherV("apply:" + shortName(cls)));
            return;
        }
        // BlockPos.of(readLong()), new ChunkPos(readLong()): a factory or constructor over one
        // integer read here. Its body is read with the integer bound, so the static helpers it
        // applies (getX(packed)) cut their bit fields out of that integer's node in place; when
        // they did, the value is the integer, as a packed one.
        if (args.size() == 1 && owner.startsWith("net/minecraft/") && arg(args, 0) instanceof CodecV pv && "prim".equals(pv.n().get("k"))
                && (isStatic && ret.startsWith("L") || name.equals("<init>") && recv instanceof OtherV nv && nv.what().startsWith("new:"))) {
            String t = String.valueOf(pv.n().get("t"));
            if (t.equals("LONG") || t.equals("INT") || t.equals("VAR_INT") || t.equals("SHORT") || t.equals("BYTE")) {
                List<Integer> slots = paramSlots(desc, isStatic);
                if (!slots.isEmpty() && depth < MAX_DEPTH) {
                    interpretReader(owner, name, desc, depth + 1, Map.of(slots.get(0), new PassedV(pv.n())));
                    if (System.getenv("MC26_TRACE") != null) System.err.println("trace: " + owner + "." + name + " over " + t + " -> " + pv.n().get("k"));
                    if ("bits".equals(pv.n().get("k"))) {
                        if (!isStatic && !stack.isEmpty() && stack.peek() instanceof OtherV top && top.what().equals(((OtherV) recv).what())) stack.pop();
                        stack.push(pv);
                        return;
                    }
                }
            }
        }
        boolean isBuf = owner.endsWith("FriendlyByteBuf") || owner.endsWith("RegistryFriendlyByteBuf") || owner.endsWith("io/netty/buffer/ByteBuf")
            // VarInt moved out of the codec package in 26.3; its own byte loop is not a
            // packet's, so it has to be recognised wherever it lives
            || owner.endsWith("/VarInt") || owner.endsWith("/VarLong") || owner.endsWith("/Utf8String");
        Map<String, Object> produced = null;
        // ByteBufCodecs.readCount(buf, 16): a var int that is a count, at most that many
        if (isStatic && owner.endsWith("codec/ByteBufCodecs") && name.equals("readCount") && constOf(arg(args, 1)) instanceof Integer max) {
            Map<String, Object> cnt = node("prim", "t", "VAR_INT", "max", max);
            values.add(cnt); stack.push(new CodecV(cnt)); return;
        }
        // Wrappers that keep the value: Optional.of(x) (a branch-guarded read), List.of(x), requireNonNull(x)…
        if (owner.equals("java/util/Optional") && (name.equals("of") || name.equals("ofNullable")) && arg(args, 0) instanceof CodecV c) {
            if (inKnownRegion) { stack.push(c); return; }   // the region's condition says when the value is present
            if ("nbt".equals(c.n().get("k")) || "prim".equals(c.n().get("k")) && "NBT".equals(c.n().get("t"))) { stack.push(c); return; }   // TAG_End is the absent tag
            Map<String, Object> opt = node("optional", "elem", c.n()); stack.push(new CodecV(opt)); return;
        }
        if ((owner.equals("java/util/Objects") && name.equals("requireNonNull")) || (owner.endsWith("ImmutableList") && name.equals("copyOf"))
                || (owner.equals("java/util/List") && name.equals("copyOf"))) {
            if (arg(args, 0) instanceof CodecV c) { stack.push(c); return; }
        }
        // JointType.CODEC.byName(buf.readUtf(), ALIGNED): the string just read is the enum's
        // serialized name. The default only stands in for a name this version does not know,
        // so the wire form is the name and nothing else.
        if (owner.endsWith("StringRepresentable$EnumCodec") && name.equals("byName")
                && recv instanceof CodecV rc && "stringenum".equals(rc.n().get("k")) && arg(args, 0) instanceof CodecV sc) {
            removeIdentity(values, sc.n());
            Map<String, Object> se = castNode((Map<?, ?>) copyNode(rc.n()));
            hintOf.putIfAbsent(se, hintOf.getOrDefault(sc.n(), lowerFirst(String.valueOf(rc.n().get("name")))));
            stack.push(new CodecV(se));
            values.add(se);
            return;
        }
        // `type.constructor.apply(id, icon, buf)`: an enum whose constants each hold a reader
        // is a dispatch on that enum — the constant just read says which reader takes the rest
        // of the buffer, so the value is one of as many shapes as the enum has constants.
        // The call is `apply` for a TriFunction and `decode` for a StreamDecoder; what makes it
        // a dispatch is the receiver being a field of the constant just read, not the name.
        if (recv instanceof EnumFnV ef0 && !ret.equals("V")
                && (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf") || desc.contains("java/lang/Object"))) {
            EnumFnV ef = ef0;
            Map<String, Object> d = node("dispatch", "name", dispatchName(ef.enumClass()), "key", ef.key());
            List<Map<String, Object>> cases = enumDispatchCases(ef.enumClass(), ef.field(), depth);
            if (cases != null) d.put("cases", cases);
            removeIdentity(values, ef.key());
            stack.push(new CodecV(d));
            values.add(d);
            hintOf.putIfAbsent(d, hintOf.getOrDefault(ef.key(), lowerFirst(dispatchName(ef.enumClass()))));
            return;
        }
        // BuiltInRegistries.COMMAND_ARGUMENT_TYPE.byId(buf.readVarInt()): the element the id
        // just read names. Nothing about it is known yet — what matters is the method called
        // on it next.
        if (name.equals("byId") && recv instanceof OtherV ov && ov.what().startsWith("idmap:") && arg(args, 0) instanceof CodecV idc) {
            String reg = ov.what().substring(ov.what().lastIndexOf('.') + 1).toLowerCase(Locale.ROOT);
            removeIdentity(values, idc.n());
            stack.push(new RegistryElemV(reg, idc.n()));
            return;
        }
        // info.deserializeFromNetwork(buf): each element of the registry reads its own payload,
        // so the value is a dispatch on that registry — the id, then that element's own reader.
        if (recv instanceof RegistryElemV re && !ret.equals("V")
                && (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf"))) {
            Map<String, Object> d = node("dispatch", "name", shortName(owner), "key", node("registry", "registry", re.registry()));
            List<Map<String, Object>> cases = registryMethodCases(re.registry(), name, depth + 1);
            if (cases != null) d.put("cases", cases);
            produced = d;
        } else if (isStatic && name.equals("byId") && desc.startsWith("(I)L") && arg(args, 0) instanceof CodecV idv
                && "prim".equals(idv.n().get("k")) && "VAR_INT".equals(idv.n().get("t")) && loadClass(owner) instanceof Class<?> ec && ec.isEnum()) {
            // ClientIntent.byId(buf.readVarInt()): the var int is a constant of the enum, by the
            // number its byId gives it. The node already read is retyped in place.
            Map<String, Object> en = enumNode(owner);
            List<Integer> ids = enumSwitchIds(owner);
            if (ids == null) ids = enumByIdFieldIds(owner);
            if (ids == null) en.put("idsUnknown", true);
            else { boolean ordinal = true; for (int i = 0; i < ids.size(); i++) if (ids.get(i) != i) ordinal = false; if (!ordinal) en.put("ids", ids); }
            idv.n().clear();
            idv.n().putAll(en);
            enumClassOf.put(idv.n(), owner);
            stack.push(idv);
            return;
        } else if (isBuf && name.startsWith("read")) {
            switch (name) {
                case "readList", "readCollection" -> produced = node("list", "elem", elemOf(args, depth));
                case "readNullable", "readOptional" -> produced = node("optional", "elem", elemOf(args, depth));
                case "readMap" -> produced = node("map", "key", readerOf(args.size() > 1 ? arg(args, args.size() - 2) : null, depth), "val", readerOf(arg(args, args.size() - 1), depth));
                case "readEnum" -> produced = arg(args, 0) instanceof ClassV cv ? enumNode(cv.internal()) : opaque("readEnum");
                case "readResourceKey" -> produced = node("resourcekey", "registry", keyOf(arg(args, 0)));
                case "readById" -> produced = byIdNode(arg(args, 0));
                case "readJsonWithCodec", "readWithCodec" -> produced = nbtOrText(args.isEmpty() ? null : arg(args, 0));
                case "readEnumSet" -> produced = arg(args, 0) instanceof ClassV cv ? node("enumset", "name", shortName(cv.internal()), "values", enumValues(cv.internal())) : opaque("readEnumSet");
                // readableBytes() asks how much is left; it consumes nothing. Reading the rest
                // is readBytes(readableBytes()), where the outer call is the read. Treating the
                // question as an answer put a REST_BYTES in the middle of the 26.3 movement
                // packets, where the count is only divided to check it fits.
                case "readableBytes" -> { stack.push(new OtherV("readableBytes")); return; }
                case "readFixedBitSet" -> produced = node("prim", "t", "FIXED_BIT_SET", "bits", constOf(arg(args, 0)));
                // readFixedSizeLongArray(array) fills an array whose length came from
                // somewhere else entirely — the bits per entry of a chunk section — so the
                // count is not on the wire and this is not a list of any length the schema
                // can state.
                case "readFixedSizeLongArray" -> {
                    Value arr = args.size() > 1 && arg(args, 1) instanceof ArrayV ? arg(args, 1) : arg(args, 0);
                    if (unwrap(arr) instanceof CodecV len && "prim".equals(len.n().get("k")) && "VAR_INT".equals(len.n().get("t"))) {
                        // new long[buf.readVarInt()] + readFixedSizeLongArray(array): the count node becomes the list, in place
                        len.n().clear(); len.n().putAll(node("list", "elem", prim("LONG")));
                        return;
                    }
                    produced = opaque("length-not-on-the-wire:readFixedSizeLongArray");
                }
                case "readBytes" -> {
                    if (constOf(arg(args, 0)) != null) produced = node("prim", "t", "FIXED_BYTES", "len", constOf(arg(args, 0)));   // readBytes(256): a fixed-size block
                    else if (unwrap(arg(args, 0)) instanceof CodecV len && "prim".equals(len.n().get("k")) && "VAR_INT".equals(len.n().get("t"))) {
                        // new byte[buf.readVarInt()] + readBytes(array): the length node becomes the array, in place
                        if ("BYTE_ARRAY".equals(derivingPrim)) { len.n().clear(); len.n().putAll(node("list", "elem", prim("BYTE"))); }
                        else len.n().put("t", "BYTE_ARRAY");
                        return;
                    } else if (arg(args, 0) instanceof OtherV rb && rb.what().equals("readableBytes")) {
                        produced = prim("REST_BYTES");   // readBytes(readableBytes()): the rest of the packet
                    } else produced = prim("RAW_BYTES");
                }
                case "readEither" -> produced = node("either", "left", readerOf(arg(args, 0), depth), "right", readerOf(arg(args, 1), depth));
                case "readUtf" -> produced = node("string", "max", args.isEmpty() ? null : constOf(arg(args, 0)));
                default -> {
                    if (owner.endsWith("network/Utf8String") && name.equals("read")) { produced = node("string", "max", constOf(arg(args, 1))); break; }
                    String t = primOfMember(owner + "." + name);
                    if (t == null) t = primOfBufRead(name);
                    produced = t != null ? prim(t) : owner.startsWith("net/minecraft/") ? readerNode(owner, name, desc, depth + 1) : opaque("buf." + name);
                }
            }
        } else if (isBuf) {
            // skipBytes(readableBytes()) is how a reader discards the rest of the packet, and
            // the bytes are on the wire whether or not this reader wanted them.
            if (name.equals("skipBytes") && arg(args, 0) instanceof OtherV rb && rb.what().equals("readableBytes")) {
                produced = prim("REST_BYTES");
            } else return; // writes / bookkeeping on the buffer
        } else if (name.equals("decode") && recv instanceof CodecV c) {
            produced = new LinkedHashMap<>(c.n());
            String h = hintOf.get(c.n());
            if (h != null) hintOf.put(produced, h);
            // the copy waits for the same name as the node it was made from
            if (pendingKeys.containsKey(c.n())) pendingKeys.put(produced, pendingKeys.get(c.n()));
            if (pendingCounts.containsKey(c.n())) pendingCounts.put(produced, pendingCounts.get(c.n()));
        } else if (name.equals("<init>") && !(recv instanceof OtherV ov0 && ov0.what().startsWith("new:"))) {
            // this(v1, v2, …): a delegating constructor names the values by its parameters
            List<String> params = ctorParamNames(owner, desc);
            if (params != null && params.size() == args.size()) {
                for (int i = 0; i < args.size(); i++) if (args.get(i) instanceof CodecV c) hintOf.put(c.n(), params.get(i));
            }
            return;
        } else if (name.equals("<init>") && recv instanceof OtherV ov && ov.what().startsWith("new:")) {
            String cls = ov.what().substring(4);
            // `new X; dup; <args>; invokespecial <init>` leaves the dup'd reference on the
            // stack: the constructed value replaces it rather than sitting on top of it.
            if (!stack.isEmpty() && stack.peek() instanceof OtherV top && top.what().equals(ov.what())) stack.pop();
            if (cls.endsWith("Exception") || cls.endsWith("Error")) {
                return; // a validation failure, not a wire value
            } else if (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf")) {
                produced = readerNode(cls, "<init>", desc, depth + 1);
            } else if (namedInlineBits(cls, desc, args) instanceof Map<String, Object> packed) {
                // new ChunkPos((int) l, (int) (l >> 32)): the constructor's parameters name the
                // bit fields cut out of one long, which becomes a packed integer; the object is
                // that integer
                stack.push(values.contains(packed) ? new CodecV(packed) : new PassedV(packed, null));
                return;
            } else if (cls.equals(self)) {
                // the packet's own constructor from the values read: it names them (handled by the caller)
                List<String> params = ctorParamNames(cls, desc);
                if (params == null || params.size() != args.size()) {
                    List<String> comps = recordComponents(cls);
                    if (comps != null && comps.size() == args.size()) params = comps;
                }
                if (params != null && params.size() == args.size()) {
                    for (int i = 0; i < args.size(); i++) if (args.get(i) instanceof CodecV c) hintOf.put(c.n(), params.get(i));
                }
                produced = null;
            } else {
                Map<String, Object> st = constructed(cls, desc, args, values, guards);
                if (st == null) { stack.push(new OtherV("obj:" + shortName(cls))); return; }
                produced = st;
            }
        } else if (name.equals("read") && ret.equals("V") && (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf"))
                && owner.contains("$") && arg(args, 0) instanceof CodecV builder && "struct".equals(builder.n().get("k"))) {
            // Action.Reader.read(builder, buf) in a loop over the packet's EnumSet<Action>
            String enumClass = owner.substring(0, owner.lastIndexOf('$'));
            if (expandEnumReaders(enumClass, builder.n(), depth)) return;
            stack.push(new CodecV(opaque("reader-loop:" + shortName(owner))));
            return;
        } else if (ret.equals("V") && owner.startsWith("net/minecraft/") && !isBuf
                && (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf"))) {
            // A reader that hands the buffer to a void helper (Node.readContents(buf, node))
            // reads through it: interpret the helper and take what it read, or the hole it
            // leaves. A helper that reads nothing is a writer or bookkeeping, and is ignored —
            // silently dropping one that does read would produce a struct short of fields
            // while the schema still called the packet fully typed.
            Map<String, Object> inner = readerNode(owner, name, desc, depth + 1);
            if ("opaque".equals(inner.get("k")) && String.valueOf(inner.get("java")).startsWith("empty-reader:")) return;
            produced = inner;
        } else if (owner.startsWith("net/minecraft/") && (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf")) && !ret.equals("V")) {
            // A reader handed values read earlier is interpreted with them bound to its
            // parameters: what it reads then depends on those values in a way the schema can
            // say (read(buf, flags) in the command tree), instead of being a branch on
            // something unknown. Only calls that really pass such a value take this path, and
            // it does not use the cache, since the answer depends on what was passed.
            Map<Integer, Value> bind = new HashMap<>();
            List<Integer> slots = paramSlots(desc, isStatic);
            for (int i = 0; i < args.size() && i < slots.size(); i++) {
                Map<String, Object> passedNode = nodeOfValue(args.get(i));
                if (passedNode != null) bind.put(slots.get(i), new PassedV(passedNode, subOfValue(args.get(i))));
            }
            if (bind.isEmpty() || depth >= MAX_DEPTH || READER_RULES.containsKey(owner + "." + name)) {
                produced = readerNode(owner, name, desc, depth + 1);
            } else {
                carriedGuards.clear();
                Map<String, Object> inner = interpretReader(owner, name, desc, depth + 1, bind);
                // The reader read under conditions on what it was passed, so its fields belong
                // beside that value in this struct rather than in one of their own: splice them
                // in, guards and all. Anything else stays the nested value it was.
                if (!carriedGuards.isEmpty() && "struct".equals(inner.get("k"))) {
                    for (Map<String, Object> f : castList(inner.get("fields"))) {
                        Map<String, Object> t = castNode((Map<?, ?>) f.get("type"));
                        values.add(t);
                        hintOf.putIfAbsent(t, String.valueOf(f.get("name")));
                        List<List<Map<String, Object>>> when = carriedGuards.get(t);
                        if (when != null) guards.put(t, when);
                    }
                    carriedGuards.clear();
                    stack.push(new OtherV("inlined:" + shortName(owner) + "." + name));
                    return;
                }
                carriedGuards.clear();
                produced = inner;
            }
        } else if (ret.contains("StreamCodec")) {
            // a codec factory used inline (ByteBufCodecs.registry(Registries.X).decode(buf)):
            // re-run the call through the codec interpreter and keep its node on the stack
            Deque<Value> tmp = new ArrayDeque<>();
            if (recv != null) tmp.push(recv);
            for (Value a : args) tmp.push(a);
            invoke(ii, tmp, self, depth);
            stack.push(tmp.isEmpty() ? new CodecV(opaque(shortName(owner) + "." + name)) : tmp.pop());
            return;
        } else if (isBuf) {
            return; // writes / bookkeeping on the buffer
        } else if (isStatic && args.size() == 1 && owner.startsWith("net/minecraft/")
                && (ret.equals("I") || ret.equals("Z") || ret.equals("J")) && nodeOfValue(arg(args, 0)) != null
                && subOfValue(arg(args, 0)) == null && bitField(owner, name, desc) != null) {
            // unpackStepCount(packed): not a conversion of the value but one field of it, so the
            // integer it was read from becomes a packed one with that field named on it
            int[] bf = bitField(owner, name, desc);
            Map<String, Object> of = putBitField(nodeOfValue(arg(args, 0)), owner, bitFieldName(name), bf);
            stack.push(of == null ? new OtherV(shortName(owner) + "." + name) : new BitsV(of, bitFieldName(name), bf[0], bf[1], bf[2] != 0));
            return;
        } else if (isStatic && ret.equals("Z") && args.size() == 1 && arg(args, 0) instanceof CodecV pv && owner.startsWith("net/minecraft/")) {
            // shouldHaveParameters(method): a predicate of a value read, evaluated over its domain
            List<Integer> vals = evalPredicate(owner, name, desc);
            stack.push(vals != null ? new CodecV(node("pred", "of", pv.n(), "values", vals)) : new OtherV(shortName(owner) + "." + name));
            return;
        } else if (!ret.equals("V")) {
            // Some other call. If exactly one argument (or the receiver) is a wire value it
            // is a conversion of that value (Type.byId(buf.readByte()), Optional.ofNullable(x),
            // Objects.requireNonNull(x)…): keep the wire node. Two wire arguments combined
            // into one value cannot be described, so that becomes a hole.
            List<Map<String, Object>> wire = new ArrayList<>();
            for (Value a : args) if (a instanceof CodecV c) wire.add(c.n());
            if (recv instanceof CodecV c) wire.add(c.n());
            if (wire.size() == 1) { stack.push(new CodecV(wire.get(0))); return; }
            if (wire.size() > 1 && isStatic && owner.startsWith("net/minecraft/") && ret.startsWith("L") && wire.size() == args.size()) {
                // GlobalPos.of(dimension, pos): a factory of the returned type from the values read
                String cls = ret.substring(1, ret.length() - 1);
                List<String> params = ctorParamNames(owner, desc, name);
                List<Map<String, Object>> fs = new ArrayList<>();
                for (int i = 0; i < wire.size(); i++) {
                    Map<String, Object> f = new LinkedHashMap<>();
                    f.put("name", params != null && params.size() == wire.size() ? params.get(i) : hintOf.getOrDefault(wire.get(i), "v" + i));
                    f.put("type", wire.get(i)); fs.add(f);
                }
                for (Map<String, Object> w : wire) removeIdentity(values, w);
                Map<String, Object> st = node("struct", "name", shortName(cls), "fields", fs);
                stack.push(new CodecV(st)); values.add(st);
                return;
            }
            if (wire.size() > 1) { stack.push(new CodecV(opaque("combined:" + shortName(owner) + "." + name))); return; }
            stack.push(new OtherV(shortName(owner) + "." + name));
            return;
        } else {
            return;
        }
        if (produced != null) {
            stack.push(new CodecV(produced));
            values.add(produced);
            if (!hintOf.containsKey(produced)) {
                String h = "struct".equals(produced.get("k")) && produced.get("name") != null
                    ? lowerFirst(shortName(String.valueOf(produced.get("name"))).replace("$", "")) : hintName(name);
                hintOf.put(produced, h);
            }
        }
    }

    static String camel(String constant) {   // VAR_INT → VarInt
        StringBuilder sb = new StringBuilder();
        for (String w : constant.toLowerCase(Locale.ROOT).split("_")) if (!w.isEmpty()) sb.append(Character.toUpperCase(w.charAt(0))).append(w.substring(1));
        return sb.toString();
    }
    static String lowerFirst(String s) { return s.isEmpty() ? s : Character.toLowerCase(s.charAt(0)) + s.substring(1); }

    /** readVarInt → varInt, readIdentifier → identifier, readPayload → payload, readableBytes → data. */
    static String hintName(String method) {
        if (method.equals("readableBytes")) return "data";
        if (method.equals("<init>")) return "value";
        String s = method.startsWith("read") ? method.substring(4) : method;
        if (s.isEmpty()) return method;
        if (s.equals("UUID")) return "uuid";
        return Character.toLowerCase(s.charAt(0)) + s.substring(1);
    }

    static Map<String, Object> elemOf(List<Value> args, int depth) {
        // readCollection(HashSet::new, BlockPos.STREAM_CODEC): the element is the codec, not
        // the collection's constructor, so a codec argument wins; otherwise the last lambda
        // is the element reader.
        for (Value a : args) if (a instanceof CodecV c) return c.n();
        for (int i = args.size() - 1; i >= 0; i--) if (args.get(i) instanceof LambdaV) return readerOf(args.get(i), depth);
        return opaque("elem");
    }
    static Map<String, Object> readerOf(Value v, int depth) {
        if (v instanceof LambdaV l) {
            String t = primOfMember(l.owner() + "." + l.name());
            if (t == null && (l.owner().endsWith("FriendlyByteBuf") || l.owner().endsWith("RegistryFriendlyByteBuf"))) t = primOfBufRead(l.name());
            return t != null ? prim(t) : readerNode(l.owner(), l.name(), l.desc(), depth + 1);
        }
        if (v instanceof CodecV c) return c.n();
        return opaque("reader:" + v);
    }

    // ---- coverage, tokens, json ------------------------------------------------------

    static String coverage(Map<String, Object> n) { return hasHole(n) ? "partial" : "full"; }
    @SuppressWarnings("unchecked")
    static boolean hasHole(Map<String, Object> n) {
        String k = (String) n.get("k");
        if (k == null) return false;   // not a node: the condition a whilelist ends on, say
        if (k.equals("opaque")) return true;
        if (k.equals("dispatch") && n.get("cases") == null) return true;
        if (k.equals("counted") && n.get("count") == null) return true;
        if (Boolean.TRUE.equals(n.get("conditional"))) return true;
        if (k.equals("enum") && (n.get("values") == null || Boolean.TRUE.equals(n.get("idsUnknown")))) return true;
        if (k.equals("stringenum") && n.get("names") == null) return true;
        for (Object v : n.values()) {
            if (v instanceof Map<?, ?> m && hasHole((Map<String, Object>) m)) return true;
            if (v instanceof List<?> l) for (Object o : l) {
                if (o instanceof Map<?, ?> m) {
                    Map<String, Object> mm = (Map<String, Object>) m;
                    Object t = mm.get("type");
                    if (t instanceof Map<?, ?> tm ? hasHole((Map<String, Object>) tm) : mm.containsKey("k") && hasHole(mm)) return true;
                }
            }
        }
        return false;
    }

    @SuppressWarnings("unchecked")
    static void tokens(Map<String, Object> n, List<String> out, boolean top) {
        String k = (String) n.get("k");
        switch (k) {
            case "prim" -> out.add((String) n.get("t"));
            case "string" -> out.add("STRING");
            case "unit" -> { }
            case "enum" -> out.add("enum:" + n.get("name"));
            case "stringenum" -> out.add("stringenum:" + n.get("name"));
            case "whilelist" -> { List<String> in = new ArrayList<>(); tokens((Map<String, Object>) n.get("elem"), in, false); out.add("whilelist{" + String.join(", ", in) + "}"); }
            case "registry" -> out.add("registry:" + n.get("registry"));
            case "holder" -> out.add("holder:" + n.get("registry"));
            case "holderset" -> out.add("holderset:" + n.get("registry"));
            case "resourcekey" -> out.add("resourcekey:" + n.get("registry"));
            case "nbt" -> out.add("NBT");
            case "bits" -> out.add("bits{" + n.get("of") + "}");
            case "list" -> { List<String> in = new ArrayList<>(); tokens((Map<String, Object>) n.get("elem"), in, false); out.add("list{" + String.join(", ", in) + "}"); }
            case "counted" -> { List<String> in = new ArrayList<>(); tokens((Map<String, Object>) n.get("elem"), in, false); out.add("counted{" + String.join(", ", in) + "}"); }
            case "optional" -> { List<String> in = new ArrayList<>(); tokens((Map<String, Object>) n.get("elem"), in, false); out.add("optional{" + String.join(", ", in) + "}"); }
            case "map" -> { List<String> a = new ArrayList<>(), b = new ArrayList<>(); tokens((Map<String, Object>) n.get("key"), a, false); tokens((Map<String, Object>) n.get("val"), b, false); out.add("map{" + String.join(", ", a) + " → " + String.join(", ", b) + "}"); }
            case "either" -> out.add("either");
            case "dispatch" -> out.add("dispatch");
            case "opaque" -> out.add("opaque:" + n.get("java"));
            case "struct" -> {
                List<String> in = new ArrayList<>();
                for (Object f : (List<Object>) n.get("fields")) tokens((Map<String, Object>) ((Map<String, Object>) f).get("type"), in, false);
                if (top) out.addAll(in); else out.add(n.get("name") + "{" + String.join(", ", in) + "}");
            }
            default -> out.add(k);
        }
    }

    @SuppressWarnings("unchecked")
    static String jsonNode(Object o) {
        if (o == null) return "null";
        if (o instanceof String s) return json(s);
        if (o instanceof Boolean || o instanceof Number) return o.toString();
        if (o instanceof List<?> l) {
            StringBuilder sb = new StringBuilder("[");
            for (int i = 0; i < l.size(); i++) { if (i > 0) sb.append(", "); sb.append(jsonNode(l.get(i))); }
            return sb.append("]").toString();
        }
        if (o instanceof Map<?, ?> m) {
            StringBuilder sb = new StringBuilder("{");
            boolean first = true;
            for (Map.Entry<?, ?> e : m.entrySet()) {
                if (!first) sb.append(", "); first = false;
                sb.append(json(String.valueOf(e.getKey()))).append(": ").append(jsonNode(e.getValue()));
            }
            return sb.append("}").toString();
        }
        return json(o.toString());
    }

    static String json(String s) {
        StringBuilder sb = new StringBuilder("\"");
        for (char c : s.toCharArray()) {
            switch (c) {
                case '"' -> sb.append("\\\"");
                case '\\' -> sb.append("\\\\");
                case '\n' -> sb.append("\\n");
                default -> { if (c < 0x20) sb.append(String.format("\\u%04x", (int) c)); else sb.append(c); }
            }
        }
        return sb.append('"').toString();
    }
}
