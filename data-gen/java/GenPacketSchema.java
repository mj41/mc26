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
import java.lang.classfile.instruction.*;
import java.lang.constant.*;
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public class GenPacketSchema {
    static final int MAX_DEPTH = 6;
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
    static Map<String, Object> opaque(String java) { return node("opaque", "java", java); }

    /** Values on the simulated operand stack. */
    sealed interface Value permits CodecV, LambdaV, ConstV, KeyV, FnV, ClassV, OtherV {}
    record CodecV(Map<String, Object> n) implements Value {}
    record LambdaV(String owner, String name, String desc) implements Value {}
    record ConstV(Object v) implements Value {}
    record KeyV(String registry) implements Value {}          // Registries.X resource key
    record FnV(String kind, Object arg) implements Value {}     // ByteBufCodecs.list() etc.
    record ClassV(String internal) implements Value {}          // ldc Class
    record OtherV(String what) implements Value {}

    // ---- primitives ---------------------------------------------------------------

    static final Map<String, String> BYTEBUF_CODECS = Map.ofEntries(
        Map.entry("BOOL", "BOOL"), Map.entry("BYTE", "BYTE"), Map.entry("SHORT", "SHORT"),
        Map.entry("UNSIGNED_SHORT", "UNSIGNED_SHORT"), Map.entry("INT", "INT"), Map.entry("LONG", "LONG"),
        Map.entry("FLOAT", "FLOAT"), Map.entry("DOUBLE", "DOUBLE"), Map.entry("VAR_INT", "VAR_INT"),
        Map.entry("VAR_LONG", "VAR_LONG"), Map.entry("OPTIONAL_VAR_INT", "OPTIONAL_VAR_INT"),
        Map.entry("STRING_UTF8", "STRING"), Map.entry("BYTE_ARRAY", "BYTE_ARRAY"),
        Map.entry("LONG_ARRAY", "LONG_ARRAY"), Map.entry("VAR_INT_ARRAY", "VAR_INT_ARRAY"),
        Map.entry("BIT_SET", "BIT_SET"), Map.entry("INSTANT", "INSTANT"), Map.entry("GAME_PROFILE", "GAME_PROFILE"),
        Map.entry("PUBLIC_KEY", "PUBLIC_KEY"), Map.entry("TAG", "NBT"), Map.entry("TRUSTED_TAG", "NBT"),
        Map.entry("COMPOUND_TAG", "NBT"), Map.entry("TRUSTED_COMPOUND_TAG", "NBT"),
        Map.entry("OPTIONAL_COMPOUND_TAG", "OPTIONAL_NBT"), Map.entry("VECTOR3F", "VECTOR3F"),
        Map.entry("QUATERNIONF", "QUATERNIONF"), Map.entry("CONTAINER_ID", "CONTAINER_ID"),
        Map.entry("ROTATION_BYTE", "ROTATION_BYTE"), Map.entry("VAR_INT_UNSIGNED", "VAR_INT"),
        Map.entry("RGB_COLOR", "INT"), Map.entry("ARGB_COLOR", "INT")
    );
    /** Well-known codec constants on other classes that are wire primitives for our purposes. */
    static final Map<String, String> KNOWN_CODEC_FIELDS = Map.ofEntries(
        Map.entry("net/minecraft/core/UUIDUtil.STREAM_CODEC", "UUID"),
        Map.entry("net/minecraft/resources/Identifier.STREAM_CODEC", "IDENTIFIER"),
        Map.entry("net/minecraft/resources/ResourceLocation.STREAM_CODEC", "IDENTIFIER"),
        Map.entry("net/minecraft/core/BlockPos.STREAM_CODEC", "BLOCK_POS"),
        Map.entry("net/minecraft/world/level/ChunkPos.STREAM_CODEC", "CHUNK_POS"),
        Map.entry("net/minecraft/core/GlobalPos.STREAM_CODEC", "GLOBAL_POS"),
        Map.entry("net/minecraft/world/phys/Vec3.STREAM_CODEC", "VEC3"),
        Map.entry("net/minecraft/world/phys/Vec3.LP_STREAM_CODEC", "LP_VEC3"),
        Map.entry("net/minecraft/network/chat/ComponentSerialization.STREAM_CODEC", "TEXT"),
        Map.entry("net/minecraft/network/chat/ComponentSerialization.TRUSTED_STREAM_CODEC", "TEXT"),
        Map.entry("net/minecraft/network/chat/ComponentSerialization.OPTIONAL_STREAM_CODEC", "OPTIONAL_TEXT"),
        Map.entry("net/minecraft/network/chat/ComponentSerialization.TRUSTED_OPTIONAL_STREAM_CODEC", "OPTIONAL_TEXT"),
        Map.entry("net/minecraft/world/item/ItemStack.STREAM_CODEC", "ITEM_STACK"),
        Map.entry("net/minecraft/world/item/ItemStack.OPTIONAL_STREAM_CODEC", "OPTIONAL_ITEM_STACK"),
        Map.entry("net/minecraft/world/item/ItemStack.OPTIONAL_LIST_STREAM_CODEC", "OPTIONAL_ITEM_STACK_LIST"),
        Map.entry("net/minecraft/core/component/DataComponentPatch.STREAM_CODEC", "COMPONENT_PATCH"),
        Map.entry("net/minecraft/network/chat/MessageSignature.STREAM_CODEC", "MESSAGE_SIGNATURE"),
        Map.entry("net/minecraft/nbt/CompoundTag.STREAM_CODEC", "NBT")
    );
    static final Map<String, String> BUF_READS = Map.ofEntries(
        Map.entry("readBoolean", "BOOL"), Map.entry("readByte", "BYTE"), Map.entry("readUnsignedByte", "UNSIGNED_BYTE"),
        Map.entry("readShort", "SHORT"), Map.entry("readUnsignedShort", "UNSIGNED_SHORT"), Map.entry("readInt", "INT"),
        Map.entry("readUnsignedInt", "UNSIGNED_INT"), Map.entry("readLong", "LONG"), Map.entry("readFloat", "FLOAT"),
        Map.entry("readDouble", "DOUBLE"), Map.entry("readVarInt", "VAR_INT"), Map.entry("readVarLong", "VAR_LONG"),
        Map.entry("readUtf", "STRING"), Map.entry("readUUID", "UUID"), Map.entry("readIdentifier", "IDENTIFIER"),
        Map.entry("readResourceLocation", "IDENTIFIER"), Map.entry("readByteArray", "BYTE_ARRAY"),
        Map.entry("readVarIntArray", "VAR_INT_ARRAY"), Map.entry("readLongArray", "LONG_ARRAY"),
        Map.entry("readBitSet", "BIT_SET"), Map.entry("readFixedBitSet", "FIXED_BIT_SET"), Map.entry("readInstant", "INSTANT"),
        Map.entry("readBlockPos", "BLOCK_POS"), Map.entry("readChunkPos", "CHUNK_POS"), Map.entry("readGlobalPos", "GLOBAL_POS"),
        Map.entry("readVec3", "VEC3"), Map.entry("readLpVec3", "LP_VEC3"), Map.entry("readVector3f", "VECTOR3F"),
        Map.entry("readQuaternion", "QUATERNIONF"), Map.entry("readNbt", "NBT"), Map.entry("readGameProfile", "GAME_PROFILE"),
        Map.entry("readPublicKey", "PUBLIC_KEY"), Map.entry("readBytes", "RAW_BYTES"), Map.entry("readIntIdList", "VAR_INT_LIST"),
        Map.entry("readChar", "CHAR"), Map.entry("readComponent", "TEXT"), Map.entry("readComponentTrusted", "TEXT"),
        Map.entry("readJsonWithCodec", "JSON"), Map.entry("readWithCodec", "NBT"),
        Map.entry("readContainerId", "CONTAINER_ID"), Map.entry("readRegistryKey", "REGISTRY_KEY"),
        Map.entry("readBlockHitResult", "BLOCK_HIT_RESULT"), Map.entry("readableBytes", "REST_BYTES"),
        Map.entry("readSectionPos", "SECTION_POS"), Map.entry("readDate", "LONG")
    );

    // ---- entry point --------------------------------------------------------------

    record Entry(String key, String state, String className, Map<String, Object> type) {}

    public static void main(String[] args) throws Exception {
        // Enum/record reflection initialises MC classes that touch the registries.
        net.minecraft.SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
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
                    : codecFieldNode(packetClass.getName().replace('.', '/'), "STREAM_CODEC", 0);
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

        StringBuilder sb = new StringBuilder("{\n  \"version\": 2,\n  \"packets\": {\n");
        writeEntries(sb, packets);
        sb.append("  },\n  \"structs\": {\n");
        writeEntries(sb, structs);
        sb.append("  }\n}\n");
        Files.writeString(Path.of("packet_schema.json"), sb.toString(), StandardCharsets.UTF_8);
        long full = packets.stream().filter(e -> coverage(e.type).equals("full")).count();
        System.err.printf("GenPacketSchema: %d packets (%d fully typed, %d partial) + %d structs written to packet_schema.json%n",
            packets.size(), full, packets.size() - full, structs.size());
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
    static String shortName(String internalName) { return internalName.substring(internalName.lastIndexOf('/') + 1); }
    static Class<?> loadClass(String internalName) {
        try { return Class.forName(internalName.replace('/', '.'), false, GenPacketSchema.class.getClassLoader()); }
        catch (Throwable t) { return null; }
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
    static List<String> ctorParamNames(String internalName, String desc) {
        Class<?> c = loadClass(internalName);
        if (c == null) return null;
        // Unobfuscated 26.x jars keep parameter names (MethodParameters attribute).
        ClassModel cm = classModel(internalName);
        if (cm == null) return null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals("<init>") || !m.methodType().stringValue().equals(desc)) continue;
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
        String known = KNOWN_CODEC_FIELDS.get(owner + "." + field);
        if (known != null) return prim(known);
        if (field.equals("STREAM_CODEC") && !hasStaticField(owner, field)) {
            ClassModel cm0 = classModel(owner);
            if (cm0 != null && cm0.fields().isEmpty()) return node("unit");   // e.g. bundle delimiter: no payload
        }
        if (owner.endsWith("ByteBufCodecs")) {
            String t = BYTEBUF_CODECS.get(field);
            return t != null ? prim(t) : opaque("ByteBufCodecs." + field);
        }
        String key = owner + "." + field;
        if (codecFieldCache.containsKey(key)) return codecFieldCache.get(key);
        if (depth > MAX_DEPTH) return opaque("depth:" + key);
        codecFieldCache.put(key, opaque("recursive:" + key)); // cycle guard
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
        codecFieldCache.put(key, result);
        return result;
    }

    /** One instruction of the codec-building interpreter. */
    static void step(CodeElement el, Deque<Value> stack, String self, int depth) {
        switch (el) {
            case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> {
                String o = fi.owner().asInternalName(), n = fi.name().stringValue(), d = fi.typeSymbol().descriptorString();
                if (o.endsWith("core/registries/Registries") || d.contains("ResourceKey")) stack.push(new KeyV(registryName(n)));
                else if (d.contains("Codec") || o.endsWith("ByteBufCodecs")) stack.push(new CodecV(codecFieldNode(o, n, depth + 1)));
                else if (d.contains("IdMap") || d.contains("Registry;")) stack.push(new OtherV("idmap:" + shortName(o) + "." + n));
                else stack.push(new OtherV(shortName(o) + "." + n));
            }
            case InvokeDynamicInstruction idi -> stack.push(lambdaOf(idi));
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
        for (ConstantDesc cd : idi.bootstrapArgs()) {
            if (cd instanceof DirectMethodHandleDesc dmh) {
                return new LambdaV(dmh.owner().descriptorString().replaceAll("^L|;$", ""), dmh.methodName(), dmh.lookupDescriptor());
            }
        }
        return new LambdaV("?", "?", "");
    }

    static String registryName(String field) { return field.toLowerCase(Locale.ROOT); }

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

        // --- StreamCodec combinators -------------------------------------------
        if (owner.endsWith("codec/StreamCodec") || owner.endsWith("codec/StreamCodec$CodecOperation")) {
            switch (name) {
                case "composite" -> { stack.push(new CodecV(composite(args, self))); return; }
                case "unit" -> { stack.push(new CodecV(node("unit"))); return; }
                case "apply" -> { stack.push(new CodecV(applyFn(recv, arg(args, 0)))); return; }
                case "map", "cast", "mapStream", "dispatchToStream" -> { stack.push(recv instanceof CodecV cv ? cv : new CodecV(opaque("StreamCodec." + name))); return; }
                case "dispatch" -> { stack.push(new CodecV(node("dispatch", "key", nodeOf(recv)))); return; }
                case "of", "ofMember" -> { stack.push(new CodecV(readerNode(arg(args, args.size() - 1), depth))); return; }
                case "recursive" -> { stack.push(new CodecV(opaque("StreamCodec.recursive"))); return; }
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
                case "fromCodec", "fromCodecTrusted", "fromCodecWithRegistries", "fromCodecWithRegistriesTrusted", "compoundTagCodec", "tagCodec" -> { stack.push(new CodecV(nbtOrText(args.isEmpty() ? null : arg(args, 0)))); return; }
                case "lengthPrefixed" -> { stack.push(new FnV("lengthPrefixed", constOf(arg(args, 0)))); return; }
                case "either" -> { stack.push(new CodecV(node("either", "left", nodeOf(arg(args, 0)), "right", nodeOf(arg(args, 1))))); return; }
                case "optionalTagCodec" -> { stack.push(new CodecV(node("optional", "elem", node("nbt")))); return; }
                case "lenientJson" -> { stack.push(new CodecV(node("prim", "t", "JSON_TEXT", "max", constOf(arg(args, 0))))); return; }
                default -> { stack.push(new CodecV(opaque("ByteBufCodecs." + name))); return; }
            }
        }
        if (owner.endsWith("resources/ResourceKey") && name.equals("streamCodec")) { stack.push(new CodecV(node("resourcekey", "registry", keyOf(arg(args, 0))))); return; }
        if (owner.endsWith("world/item/ItemStack") && name.equals("validatedStreamCodec")) { stack.push(new CodecV(prim("ITEM_STACK"))); return; }
        if (owner.endsWith("resources/ResourceKey") && name.equals("createRegistryKey")) { stack.push(new KeyV("dynamic")); return; }
        if (name.equals("enumStreamCodec") || (name.equals("streamCodec") && ret.contains("StreamCodec"))) {
            // e.g. XyzEnum.enumStreamCodec(...) or Foo.streamCodec(...) — a codec of the owner
            stack.push(new CodecV(codecOfOwner(owner, name, depth)));
            return;
        }
        // --- anything else returning a codec: expand the owner's field if it is a plain forwarder, else opaque
        if (ret.contains("StreamCodec")) { stack.push(new CodecV(opaque(o + "." + name))); return; }
        if (!ret.equals("V")) stack.push(new OtherV(o + "." + name));
    }

    static Map<String, Object> codecOfOwner(String owner, String method, int depth) {
        List<String> ev = enumValues(owner);
        if (ev != null) return node("enum", "name", shortName(owner), "values", ev);
        return opaque(shortName(owner) + "." + method);
    }

    static Map<String, Object> idMapper(List<Value> args) {
        // idMapper(IntFunction byId, ToIntFunction toId) → enum of the lambda owner; idMapper(IdMap) → registry-like
        for (Value a : args) if (a instanceof LambdaV l) {
            List<String> ev = enumValues(l.owner());
            if (ev != null) return node("enum", "name", shortName(l.owner()), "values", ev);
            return node("enum", "name", shortName(l.owner()), "values", null, "java", l.owner() + "." + l.name());
        }
        for (Value a : args) if (a instanceof OtherV ov && ov.what().startsWith("idmap:")) return node("registry", "registry", ov.what().substring(6));
        return opaque("ByteBufCodecs.idMapper");
    }

    static Map<String, Object> applyFn(Value recv, Value fn) {
        Map<String, Object> base = nodeOf(recv);
        if (fn instanceof FnV f) {
            return switch (f.kind()) {
                case "list" -> node("list", "elem", base, "max", f.arg());
                case "lengthPrefixed" -> base;
                default -> opaque("apply:" + f.kind());
            };
        }
        if (fn instanceof LambdaV l && l.owner().endsWith("ByteBufCodecs")) {
            return switch (l.name()) {
                case "optional" -> node("optional", "elem", base);
                case "list", "collection" -> node("list", "elem", base);
                case "lengthPrefixed", "lenientJson" -> base;
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
            String fname = args.get(i + 1) instanceof LambdaV l ? l.name() : "f" + (i / 2);
            f.put("name", fname);
            f.put("type", nodeOf(args.get(i)));
            fields.add(f);
        }
        return node("struct", "name", structName, "fields", fields);
    }

    static Map<String, Object> nodeOf(Value v) {
        if (v instanceof CodecV c) return c.n();
        if (v instanceof LambdaV l) return opaque("lambda:" + shortName(l.owner()) + "." + l.name());
        if (v == null) return opaque("null");
        return opaque(v.toString());
    }
    static Value arg(List<Value> a, int i) { return i < a.size() ? a.get(i) : null; }
    static Object constOf(Value v) { return v instanceof ConstV c ? c.v() : null; }
    static String keyOf(Value v) { return v instanceof KeyV k ? k.registry() : "?"; }
    static String lambdaOrOther(Value v) { return v instanceof LambdaV l ? shortName(l.owner()) + "." + l.name() : v == null ? null : v.toString(); }

    // nbtOrText is the node for ByteBufCodecs.fromCodec*(codec) and buf.readWithCodec(codec):
    // an NBT payload, except when the codec is ComponentSerialization.CODEC (a chat
    // component), which has the same wire form as ComponentSerialization.STREAM_CODEC → TEXT.
    static Map<String, Object> nbtOrText(Value codec) {
        if (codec instanceof CodecV cv && "opaque".equals(cv.n().get("k"))
                && String.valueOf(cv.n().get("java")).endsWith("network/chat/ComponentSerialization.CODEC")) return prim("TEXT");
        return node("nbt", "java", lambdaOrOther(codec));
    }

    // ---- reader walking (Packet.codec / StreamCodec.of / buffer constructors) -----------

    static final Map<String, Map<String, Object>> readerCache = new HashMap<>();

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
        String key = owner + "." + name + desc;
        if (readerCache.containsKey(key)) return readerCache.get(key);
        if (depth > MAX_DEPTH) return opaque("depth:" + shortName(owner) + "." + name);
        readerCache.put(key, opaque("recursive:" + shortName(owner) + "." + name));
        Map<String, Object> result = interpretReader(owner, name, desc, depth);
        readerCache.put(key, result);
        return result;
    }

    static Map<String, Object> interpretReader(String owner, String name, String desc, int depth) {
        ClassModel cm = classModel(owner);
        if (cm == null) return opaque("no-class:" + shortName(owner) + "." + name);
        MethodModel target = null;
        for (MethodModel m : cm.methods()) {
            if (!m.methodName().stringValue().equals(name)) continue;
            if (desc.isEmpty() || m.methodType().stringValue().equals(desc)) { target = m; break; }
        }
        if (target == null) return opaque("no-method:" + shortName(owner) + "." + name);
        boolean isCtor = name.equals("<init>");
        List<Map<String, Object>> fields = new ArrayList<>();   // for constructors: PUTFIELD order
        List<Map<String, Object>> values = new ArrayList<>();   // for static readers: values produced in order
        Deque<Value> stack = new ArrayDeque<>();
        Map<Integer, Value> locals = new HashMap<>();   // slot → last stored value (array sizes, read values)
        boolean conditional = false;
        Map<String, Object> returned = null;
        for (CodeElement el : target.code().map(c -> (Iterable<CodeElement>) c).orElse(List.of())) {
            switch (el) {
                case BranchInstruction bi -> conditional = true;
                case LookupSwitchInstruction ls -> conditional = true;
                case TableSwitchInstruction ts -> conditional = true;
                case FieldInstruction fi when fi.opcode() == Opcode.PUTFIELD -> {
                    Value v = stack.isEmpty() ? new OtherV("underflow") : stack.pop();
                    if (v instanceof CodecV c) {
                        Map<String, Object> f = new LinkedHashMap<>();
                        f.put("name", fi.name().stringValue()); f.put("type", c.n()); fields.add(f);
                    }
                }
                case FieldInstruction fi when fi.opcode() == Opcode.GETSTATIC -> step(el, stack, owner, depth);
                case InvokeInstruction ii -> readerInvoke(ii, stack, owner, depth, values);
                case InvokeDynamicInstruction idi -> stack.push(lambdaOf(idi));
                case ConstantInstruction ci -> step(el, stack, owner, depth);
                case NewObjectInstruction no -> stack.push(new OtherV("new:" + no.className().asInternalName()));
                case LoadInstruction li -> stack.push(locals.getOrDefault(li.slot(), new OtherV("local:" + li.slot())));
                case StoreInstruction st -> { if (!stack.isEmpty()) locals.put(st.slot(), stack.pop()); }
                case NewPrimitiveArrayInstruction na -> { /* new byte[N]: the size constant stays as the array's stand-in, so readBytes(array) is a fixed block */ }
                case StackInstruction si when si.opcode() == Opcode.DUP -> { if (!stack.isEmpty()) stack.push(stack.peek()); }
                case StackInstruction si when si.opcode() == Opcode.POP -> { if (!stack.isEmpty()) stack.pop(); }
                case ReturnInstruction ri -> { if (!stack.isEmpty() && stack.peek() instanceof CodecV c) returned = c.n(); }
                default -> { }
            }
        }
        Map<String, Object> result;
        if (isCtor && !fields.isEmpty()) {
            result = node("struct", "name", shortName(owner), "fields", fields);
        } else if (returned != null && !isCtor) {
            result = returned;
        } else if (!values.isEmpty() && !isCtor) {
            // static X.read(buf) building new X(v1, v2, ...): name the values by record components / ctor params
            List<String> names = recordComponents(owner);
            List<Map<String, Object>> fs = new ArrayList<>();
            for (int i = 0; i < values.size(); i++) {
                Map<String, Object> f = new LinkedHashMap<>();
                f.put("name", names != null && i < names.size() && names.size() == values.size() ? names.get(i) : "v" + i);
                f.put("type", values.get(i)); fs.add(f);
            }
            result = fs.size() == 1 ? fs.get(0).get("type") instanceof Map<?, ?> m ? castNode(m) : opaque("?") : node("struct", "name", shortName(owner), "fields", fs);
        } else if (isCtor && !values.isEmpty()) {
            result = node("struct", "name", shortName(owner), "fields", namedByCtor(owner, desc, values));
        } else {
            result = opaque("empty-reader:" + shortName(owner) + "." + name);
        }
        if (conditional && result.get("k").equals("struct")) result.put("conditional", true);
        return result;
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> castNode(Map<?, ?> m) { return (Map<String, Object>) m; }

    static List<Map<String, Object>> namedByCtor(String owner, String desc, List<Map<String, Object>> values) {
        List<String> names = recordComponents(owner);
        List<Map<String, Object>> fs = new ArrayList<>();
        for (int i = 0; i < values.size(); i++) {
            Map<String, Object> f = new LinkedHashMap<>();
            f.put("name", names != null && names.size() == values.size() ? names.get(i) : "v" + i);
            f.put("type", values.get(i)); fs.add(f);
        }
        return fs;
    }

    /** Invocations inside a reader: buffer reads, codec decodes, nested readers, constructors. */
    static void readerInvoke(InvokeInstruction ii, Deque<Value> stack, String self, int depth, List<Map<String, Object>> values) {
        String owner = ii.owner().asInternalName(), name = ii.name().stringValue(), desc = ii.typeSymbol().descriptorString();
        boolean isStatic = ii.opcode() == Opcode.INVOKESTATIC;
        int n = arity(desc);
        List<Value> args = popArgs(stack, n);
        Value recv = isStatic ? null : (stack.isEmpty() ? new OtherV("underflow") : stack.pop());
        String ret = desc.substring(desc.indexOf(')') + 1);
        boolean isBuf = owner.endsWith("FriendlyByteBuf") || owner.endsWith("RegistryFriendlyByteBuf") || owner.endsWith("io/netty/buffer/ByteBuf")
            || owner.endsWith("codec/VarInt") || owner.endsWith("codec/VarLong") || owner.endsWith("codec/Utf8String");
        Map<String, Object> produced = null;
        // Wrappers that keep the value: Optional.of(x) (a branch-guarded read), List.of(x), requireNonNull(x)…
        if (owner.equals("java/util/Optional") && (name.equals("of") || name.equals("ofNullable")) && arg(args, 0) instanceof CodecV c) {
            Map<String, Object> opt = node("optional", "elem", c.n()); stack.push(new CodecV(opt)); return;
        }
        if ((owner.equals("java/util/Objects") && name.equals("requireNonNull")) || (owner.endsWith("ImmutableList") && name.equals("copyOf"))
                || (owner.equals("java/util/List") && name.equals("copyOf"))) {
            if (arg(args, 0) instanceof CodecV c) { stack.push(c); return; }
        }
        if (isBuf && name.startsWith("read")) {
            switch (name) {
                case "readList", "readCollection" -> produced = node("list", "elem", elemOf(args, depth));
                case "readNullable", "readOptional" -> produced = node("optional", "elem", elemOf(args, depth));
                case "readMap" -> produced = node("map", "key", readerOf(args.size() > 1 ? arg(args, args.size() - 2) : null, depth), "val", readerOf(arg(args, args.size() - 1), depth));
                case "readEnum" -> produced = arg(args, 0) instanceof ClassV cv ? node("enum", "name", shortName(cv.internal()), "values", enumValues(cv.internal())) : opaque("readEnum");
                case "readResourceKey" -> produced = node("resourcekey", "registry", keyOf(arg(args, 0)));
                case "readById" -> produced = node("registry", "registry", arg(args, 0) instanceof OtherV ov && ov.what().startsWith("idmap:") ? ov.what().substring(6) : "?");
                case "readJsonWithCodec", "readWithCodec" -> produced = nbtOrText(args.isEmpty() ? null : arg(args, 0));
                case "readEnumSet" -> produced = arg(args, 0) instanceof ClassV cv ? node("enumset", "name", shortName(cv.internal()), "values", enumValues(cv.internal())) : opaque("readEnumSet");
                case "readFixedBitSet" -> produced = node("prim", "t", "FIXED_BIT_SET", "bits", constOf(arg(args, 0)));
                case "readBytes" -> produced = constOf(arg(args, 0)) != null
                    ? node("prim", "t", "FIXED_BYTES", "len", constOf(arg(args, 0)))   // readBytes(256): a fixed-size block
                    : prim("RAW_BYTES");
                case "readEither" -> produced = node("either", "left", readerOf(arg(args, 0), depth), "right", readerOf(arg(args, 1), depth));
                case "readUtf" -> produced = node("string", "max", args.isEmpty() ? null : constOf(arg(args, 0)));
                case "read" -> produced = prim(owner.endsWith("VarInt") ? "VAR_INT" : owner.endsWith("VarLong") ? "VAR_LONG" : "STRING");
                default -> { String t = BUF_READS.get(name); produced = t != null ? prim(t) : opaque("buf." + name); }
            }
        } else if (name.equals("decode") && recv instanceof CodecV c) {
            produced = c.n();
        } else if (name.equals("<init>") && recv instanceof OtherV ov && ov.what().startsWith("new:")) {
            String cls = ov.what().substring(4);
            // `new X; dup; <args>; invokespecial <init>` leaves the dup'd reference on the
            // stack: the constructed value replaces it rather than sitting on top of it.
            if (!stack.isEmpty() && stack.peek() instanceof OtherV top && top.what().equals(ov.what())) stack.pop();
            if (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf")) {
                produced = readerNode(cls, "<init>", desc, depth + 1);
            } else if (!args.isEmpty() && args.stream().allMatch(a -> a instanceof CodecV)) {
                // new X(v1, v2, ...) with values read before: a struct named X with those fields
                List<Map<String, Object>> vals = new ArrayList<>();
                for (Value a : args) vals.add(((CodecV) a).n());
                produced = node("struct", "name", shortName(cls), "fields", namedByCtor(cls, desc, vals));
            } else if (cls.equals(self)) {
                produced = null; // the packet's own constructor from already-collected values (handled by caller)
            } else {
                produced = opaque("new:" + shortName(cls));
            }
        } else if (owner.startsWith("net/minecraft/") && (desc.contains("FriendlyByteBuf") || desc.contains("io/netty/buffer/ByteBuf")) && !ret.equals("V")) {
            produced = readerNode(owner, name, desc, depth + 1);
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
        } else if (!ret.equals("V")) {
            // Some other call. If exactly one argument (or the receiver) is a wire value it
            // is a conversion of that value (Type.byId(buf.readByte()), Optional.ofNullable(x),
            // Objects.requireNonNull(x)…): keep the wire node. Two wire arguments combined
            // into one value cannot be described, so that becomes a hole.
            List<Map<String, Object>> wire = new ArrayList<>();
            for (Value a : args) if (a instanceof CodecV c) wire.add(c.n());
            if (recv instanceof CodecV c) wire.add(c.n());
            if (wire.size() == 1) { stack.push(new CodecV(wire.get(0))); return; }
            if (wire.size() > 1) { stack.push(new CodecV(opaque("combined:" + shortName(owner) + "." + name))); return; }
            stack.push(new OtherV(shortName(owner) + "." + name));
            return;
        } else {
            return;
        }
        if (produced != null) {
            stack.push(new CodecV(produced));
            values.add(produced);
        }
    }

    static Map<String, Object> elemOf(List<Value> args, int depth) {
        for (int i = args.size() - 1; i >= 0; i--) if (args.get(i) instanceof LambdaV) return readerOf(args.get(i), depth);
        for (Value a : args) if (a instanceof CodecV c) return c.n();
        return opaque("elem");
    }
    static Map<String, Object> readerOf(Value v, int depth) {
        if (v instanceof LambdaV l) {
            if (l.owner().endsWith("FriendlyByteBuf") || l.owner().endsWith("RegistryFriendlyByteBuf")) {
                String t = BUF_READS.get(l.name()); return t != null ? prim(t) : opaque("buf." + l.name());
            }
            if (l.owner().endsWith("UUIDUtil")) return prim("UUID");
            return readerNode(l.owner(), l.name(), l.desc(), depth + 1);
        }
        if (v instanceof CodecV c) return c.n();
        return opaque("reader:" + v);
    }

    // ---- coverage, tokens, json ------------------------------------------------------

    static String coverage(Map<String, Object> n) { return hasHole(n) ? "partial" : "full"; }
    @SuppressWarnings("unchecked")
    static boolean hasHole(Map<String, Object> n) {
        String k = (String) n.get("k");
        if (k.equals("opaque") || k.equals("dispatch") || k.equals("either")) return true;
        if (Boolean.TRUE.equals(n.get("conditional"))) return true;
        if (k.equals("enum") && n.get("values") == null) return true;
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
            case "registry" -> out.add("registry:" + n.get("registry"));
            case "holder" -> out.add("holder:" + n.get("registry"));
            case "holderset" -> out.add("holderset:" + n.get("registry"));
            case "resourcekey" -> out.add("resourcekey:" + n.get("registry"));
            case "nbt" -> out.add("NBT");
            case "list" -> { List<String> in = new ArrayList<>(); tokens((Map<String, Object>) n.get("elem"), in, false); out.add("list{" + String.join(", ", in) + "}"); }
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
