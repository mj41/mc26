import io.netty.buffer.Unpooled;
import net.minecraft.SharedConstants;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.component.DataComponentType;
import net.minecraft.core.component.DataComponents;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.RegistryFriendlyByteBuf;
import net.minecraft.network.codec.StreamCodec;
import net.minecraft.resources.Identifier;
import net.minecraft.server.Bootstrap;
import net.minecraft.sounds.SoundEvent;
import net.minecraft.tags.TagKey;
import net.minecraft.util.Unit;
import net.minecraft.world.item.ItemStack;

import java.io.*;
import java.lang.reflect.*;
import java.util.*;
import java.util.stream.*;

/**
 * GenComponentSchema — Extracts wire-format schema for each data component type
 * from MC runtime via reflection and StreamCodec probing.
 *
 * Output: component_schema.json in the current directory.
 *
 * Classification algorithm:
 *   - Unit → "empty"
 *   - Integer → probe VarInt/Int via StreamCodec → "embed"
 *   - Float → "embed pk.Float"
 *   - Boolean → "embed pk.Boolean"
 *   - Identifier → "embed pk.String"
 *   - TagKey<X> → "embed pk.String"
 *   - Component (chat) → "custom"
 *   - Holder<X> → "embed pk.VarInt", or "holder_data" when X has DIRECT_STREAM_CODEC
 *   - HolderSet<X> → "embed IDSet"
 *   - Enum → "embed pk.VarInt"
 *   - Record, 1 List field → "array"
 *   - Record, 1 simple field → "embed"
 *   - Record, >1 fields → "tuple"
 *   - Non-Record/non-enum fallback → "custom"
 *
 * For int fields, a probe-based approach determines VarInt vs Int:
 *   encode value 0 and value 128 through the component's StreamCodec,
 *   compare byte counts. VarInt(0)=1 byte, VarInt(128)=2 bytes (diff=1).
 *   Int(0)=Int(128)=4 bytes (diff=0).
 */
public class GenComponentSchema {

    // -----------------------------------------------------------------------
    // Java → Go type mapping
    // -----------------------------------------------------------------------

    /** Maps a Java class to its Go schema type string. */
    static String mapSimpleType(Class<?> cls) {
        if (cls == int.class || cls == Integer.class) return "pk.VarInt";
        if (cls == float.class || cls == Float.class) return "pk.Float";
        if (cls == boolean.class || cls == Boolean.class) return "pk.Boolean";
        if (cls == double.class || cls == Double.class) return "pk.Double";
        if (cls == long.class || cls == Long.class) return "pk.Long";
        if (cls == String.class) return "pk.String";
        return null;
    }

    /**
     * Maps a Java type (including generics) to a Go schema type string.
     * Returns null if the type cannot be automatically mapped.
     */
    static String mapFieldType(Type type) {
        if (type instanceof Class<?> cls) {
            // Simple/primitive types.
            String simple = mapSimpleType(cls);
            if (simple != null) return simple;

            // MC types with known Go equivalents.
            if (Identifier.class.isAssignableFrom(cls)) return "pk.String";
            if (cls.getName().equals("net.minecraft.network.chat.Component")) return "chat.Message";
            if (ItemStack.class.isAssignableFrom(cls)) return "SlotData";
            if (SoundEvent.class.isAssignableFrom(cls)) return "SoundEvent";
            if (cls.getSimpleName().equals("CompoundTag")) return "dynbt.Value";

            // Holder<X> as a field → check inner type.
            // (handled below in ParameterizedType)

            // Enum → VarInt on wire.
            if (cls.isEnum()) return "pk.VarInt";

            // TagKey<X> → String on wire.
            if (TagKey.class.isAssignableFrom(cls)) return "pk.String";

            // Nested Record types → use flattened simple name.
            if (cls.isRecord()) {
                return flattenClassName(cls);
            }

            // Other known types by simple name.
            String name = cls.getSimpleName();
            return name; // fall through with class name
        }

        if (type instanceof ParameterizedType pt) {
            Type rawType = pt.getRawType();
            if (rawType instanceof Class<?> rawCls) {
                // List<X> → pk.Array[GoType(X)]
                if (List.class.isAssignableFrom(rawCls)) {
                    Type elemType = pt.getActualTypeArguments()[0];
                    String goElem = mapFieldType(elemType);
                    if (goElem != null) {
                        return "pk.Array[" + goElem + "]";
                    }
                    return null;
                }

                // Optional<X> → pk.Option[GoType(X)]
                if (Optional.class.isAssignableFrom(rawCls)) {
                    Type innerType = pt.getActualTypeArguments()[0];
                    String goInner = mapFieldType(innerType);
                    if (goInner != null) {
                        return "pk.Option[" + goInner + "]";
                    }
                    return null;
                }

                // Holder<X> → pk.VarInt (simple registry ref) or SoundEvent
                if (rawCls.getSimpleName().equals("Holder")) {
                    Type innerType = pt.getActualTypeArguments()[0];
                    Class<?> innerCls = resolveRawClass(innerType);
                    if (innerCls != null && SoundEvent.class.isAssignableFrom(innerCls)) {
                        return "SoundEvent";
                    }
                    return "pk.VarInt";
                }

                // HolderSet<X> → IDSet
                if (rawCls.getSimpleName().equals("HolderSet")) {
                    return "IDSet";
                }

                // TagKey<X> → pk.String
                if (TagKey.class.isAssignableFrom(rawCls)) {
                    return "pk.String";
                }

                // DataComponentType<X> (unlikely in field, but handle)
                // ResourceKey<X>
                if (rawCls.getSimpleName().equals("ResourceKey")) {
                    return "pk.String";
                }

                // TypedEntityData<X> — this is a tuple-like record
                // Fall through to simple name
                return rawCls.getSimpleName();
            }
        }

        if (type instanceof WildcardType wt) {
            Type[] upper = wt.getUpperBounds();
            if (upper.length > 0) return mapFieldType(upper[0]);
        }

        return null;
    }

    /** Resolves the raw Class<?> from a Type (handles ParameterizedType, WildcardType). */
    static Class<?> resolveRawClass(Type type) {
        if (type instanceof Class<?> cls) return cls;
        if (type instanceof ParameterizedType pt) {
            Type raw = pt.getRawType();
            if (raw instanceof Class<?> cls) return cls;
        }
        if (type instanceof WildcardType wt) {
            Type[] upper = wt.getUpperBounds();
            if (upper.length > 0) return resolveRawClass(upper[0]);
        }
        return null;
    }

    /** Converts camelCase to PascalCase (capitalize first letter). */
    static String toPascalCase(String camelCase) {
        if (camelCase == null || camelCase.isEmpty()) return camelCase;
        return Character.toUpperCase(camelCase.charAt(0)) + camelCase.substring(1);
    }

    /**
     * Flattens a nested class name: e.g. "Tool$Rule" → "ToolRule",
     * "BlocksAttacks$DamageReduction" → "DamageReduction".
     * For top-level classes, returns the simple name.
     */
    static String flattenClassName(Class<?> cls) {
        String simpleName = cls.getSimpleName();
        Class<?> enclosing = cls.getEnclosingClass();
        if (enclosing != null) {
            // For nested classes, use just the inner class name if it's
            // descriptive enough, otherwise prepend the outer class name.
            // Most MC inner record classes are descriptive (Rule, DamageReduction, etc.)
            return simpleName;
        }
        return simpleName;
    }

    // -----------------------------------------------------------------------
    // VarInt / Int probe
    // -----------------------------------------------------------------------

    /**
     * Probes whether an Integer-typed component uses VarInt or Int encoding
     * by encoding two values through the component's StreamCodec and comparing
     * byte counts.
     *
     * VarInt(0) = 1 byte, VarInt(128) = 2 bytes → diff = 1
     * Int(0) = 4 bytes, Int(128) = 4 bytes → diff = 0
     *
     * @return "pk.VarInt" or "pk.Int"
     */
    @SuppressWarnings("unchecked")
    static String probeIntType(DataComponentType<?> type, RegistryAccess registryAccess) {
        try {
            StreamCodec codec = type.streamCodec();

            RegistryFriendlyByteBuf buf0 = new RegistryFriendlyByteBuf(
                Unpooled.buffer(), registryAccess);
            codec.encode(buf0, Integer.valueOf(0));
            int bytes0 = buf0.readableBytes();
            buf0.release();

            RegistryFriendlyByteBuf buf128 = new RegistryFriendlyByteBuf(
                Unpooled.buffer(), registryAccess);
            codec.encode(buf128, Integer.valueOf(128));
            int bytes128 = buf128.readableBytes();
            buf128.release();

            // VarInt encoding differs for 0 vs 128; Int encoding is always 4 bytes.
            return (bytes128 != bytes0) ? "pk.VarInt" : "pk.Int";
        } catch (Exception e) {
            // Can't probe (non-networkable) → default to VarInt.
            return "pk.VarInt";
        }
    }

    /**
     * Probes whether a single-int-field Record component uses VarInt or Int
     * by constructing two instances (with int=0 and int=128), encoding both
     * through the component's StreamCodec, and comparing byte counts.
     *
     * @return "pk.VarInt" or "pk.Int"
     */
    @SuppressWarnings("unchecked")
    static String probeSingleIntRecord(DataComponentType<?> type, Class<?> recordClass,
                                        RegistryAccess registryAccess) {
        try {
            StreamCodec codec = type.streamCodec();
            Constructor<?>[] ctors = recordClass.getConstructors();
            if (ctors.length == 0) return "pk.VarInt";

            // Find the canonical constructor (single int parameter).
            Constructor<?> ctor = null;
            for (Constructor<?> c : ctors) {
                Class<?>[] params = c.getParameterTypes();
                if (params.length == 1 && params[0] == int.class) {
                    ctor = c;
                    break;
                }
            }
            if (ctor == null) return "pk.VarInt";

            Object inst0 = ctor.newInstance(0);
            Object inst128 = ctor.newInstance(128);

            RegistryFriendlyByteBuf buf0 = new RegistryFriendlyByteBuf(
                Unpooled.buffer(), registryAccess);
            codec.encode(buf0, inst0);
            int bytes0 = buf0.readableBytes();
            buf0.release();

            RegistryFriendlyByteBuf buf128 = new RegistryFriendlyByteBuf(
                Unpooled.buffer(), registryAccess);
            codec.encode(buf128, inst128);
            int bytes128 = buf128.readableBytes();
            buf128.release();

            return (bytes128 != bytes0) ? "pk.VarInt" : "pk.Int";
        } catch (Exception e) {
            // Can't probe → default to VarInt.
            return "pk.VarInt";
        }
    }

    // -----------------------------------------------------------------------
    // Schema entry builders
    // -----------------------------------------------------------------------

    static Map<String, Object> embedEntry(String name, String embedType) {
        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", name);
        entry.put("pattern", "embed");
        entry.put("embedType", embedType);
        return entry;
    }

    static Map<String, Object> emptyEntry(String name) {
        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", name);
        entry.put("pattern", "empty");
        return entry;
    }

    /** True if cls declares a DIRECT_STREAM_CODEC (inline-capable registry holder). */
    static boolean hasDirectStreamCodec(Class<?> cls) {
        try {
            cls.getDeclaredField("DIRECT_STREAM_CODEC");
            return true;
        } catch (NoSuchFieldException e) {
            return false;
        }
    }

    /**
     * holder_data entry for Holder<X> with inline data. The inline type name is the
     * flattened Java record name; inline fields are emitted when every record component
     * maps to a Go type (otherwise the hand-crafted overrides must supply the inline type).
     */
    static Map<String, Object> holderDataEntry(String name, Class<?> innerCls) {
        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", name);
        entry.put("pattern", "holder_data");
        entry.put("inlineType", flattenClassName(innerCls) + "Data");
        if (innerCls.isRecord()) {
            List<Map<String, String>> fields = new ArrayList<>();
            for (RecordComponent rc : innerCls.getRecordComponents()) {
                String goType = mapFieldType(rc.getGenericType());
                if (goType == null || goType.equals(rc.getType().getSimpleName())) {
                    fields = null; // unmappable field → leave inline fields to overrides
                    break;
                }
                fields.add(tupleField(toPascalCase(rc.getName()), goType));
            }
            if (fields != null) entry.put("inlineFields", fields);
        }
        return entry;
    }

    static Map<String, Object> customEntry(String name) {
        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", name);
        entry.put("pattern", "custom");
        return entry;
    }

    static Map<String, Object> arrayEntry(String name, String fieldName, String elementType) {
        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", name);
        entry.put("pattern", "array");
        entry.put("fieldName", fieldName);
        entry.put("elementType", elementType);
        return entry;
    }

    static Map<String, Object> tupleEntry(String name, List<Map<String, String>> fields) {
        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", name);
        entry.put("pattern", "tuple");
        entry.put("fields", fields);
        return entry;
    }

    static Map<String, String> tupleField(String name, String type) {
        Map<String, String> field = new LinkedHashMap<>();
        field.put("name", name);
        field.put("type", type);
        return field;
    }

    // -----------------------------------------------------------------------
    // Record introspection helpers
    // -----------------------------------------------------------------------

    /**
     * Checks if a Record has exactly one field that is a List type.
     */
    static boolean isSingleListRecord(RecordComponent[] components) {
        if (components.length != 1) return false;
        Type genType = components[0].getGenericType();
        Class<?> fieldClass = components[0].getType();
        return List.class.isAssignableFrom(fieldClass);
    }

    /**
     * Extracts the element type from a List<X> generic type.
     */
    static Type getListElementType(Type listType) {
        if (listType instanceof ParameterizedType pt) {
            Type[] args = pt.getActualTypeArguments();
            if (args.length > 0) return args[0];
        }
        return Object.class;
    }

    /**
     * Checks if a Record has exactly one field that is a simple embeddable type
     * (int, float, boolean, String, Identifier, TagKey, Holder, HolderSet, etc.).
     */
    static boolean isSingleFieldEmbed(RecordComponent[] components) {
        if (components.length != 1) return false;
        Type genType = components[0].getGenericType();
        String mapped = mapFieldType(genType);
        return mapped != null && !List.class.isAssignableFrom(components[0].getType());
    }

    // -----------------------------------------------------------------------
    // Main classification
    // -----------------------------------------------------------------------

    @SuppressWarnings("unchecked")
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // Get a RegistryAccess for creating RegistryFriendlyByteBuf (needed for probing).
        var registryAccess = RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY);

        List<Map<String, Object>> schema = new ArrayList<>();

        // Counters for summary.
        int total = 0, empty = 0, embed = 0, embedNbt = 0;
        int array = 0, tuple = 0, custom = 0, holderData = 0;
        int probedVarInt = 0, probedInt = 0;
        List<String> customNames = new ArrayList<>();

        // Iterate all declared fields of DataComponents.class.
        for (Field field : DataComponents.class.getDeclaredFields()) {
            // Only process DataComponentType<?> fields.
            if (field.getType() != DataComponentType.class) continue;

            field.setAccessible(true);
            DataComponentType<?> type = (DataComponentType<?>) field.get(null);

            // Get registry name.
            var key = BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(type);
            if (key == null) continue;
            String name = key.toString();

            // Extract generic type T from DataComponentType<T>.
            Type genericType = field.getGenericType();
            if (!(genericType instanceof ParameterizedType pt)) {
                schema.add(customEntry(name));
                custom++;
                customNames.add(name);
                total++;
                continue;
            }

            Type typeArg = pt.getActualTypeArguments()[0];
            Class<?> valueClass = resolveRawClass(typeArg);

            if (valueClass == null) {
                // Can't resolve the value class (e.g., List<ResourceKey<Recipe<?>>>).
                schema.add(customEntry(name));
                custom++;
                customNames.add(name);
                total++;
                continue;
            }

            // --- Classification ---
            Map<String, Object> entry = classifyComponent(name, type, valueClass,
                typeArg, registryAccess);

            schema.add(entry);
            total++;

            // Update counters.
            String pattern = (String) entry.get("pattern");
            switch (pattern) {
                case "empty" -> empty++;
                case "embed" -> {
                    embed++;
                    String et = (String) entry.get("embedType");
                    if ("pk.Int".equals(et)) probedInt++;
                    else if ("pk.VarInt".equals(et)) probedVarInt++;
                }
                case "embed_nbt" -> embedNbt++;
                case "array" -> array++;
                case "tuple" -> tuple++;
                case "holder_data" -> holderData++;
                case "custom" -> { custom++; customNames.add(name); }
            }
        }

        // Sort by name for stable output.
        schema.sort(Comparator.comparing(e -> (String) e.get("name")));

        // Write JSON.
        try (PrintWriter pw = new PrintWriter(new FileWriter("component_schema.json"))) {
            writeJson(pw, schema);
        }

        // Print comprehensive summary.
        System.out.println("GenComponentSchema: wrote component_schema.json");
        System.out.printf("  Total:        %d%n", total);
        System.out.printf("  empty:        %d%n", empty);
        System.out.printf("  embed:        %d (probed VarInt: %d, probed Int: %d)%n",
            embed, probedVarInt, probedInt);
        System.out.printf("  embed_nbt:    %d%n", embedNbt);
        System.out.printf("  array:        %d%n", array);
        System.out.printf("  tuple:        %d%n", tuple);
        System.out.printf("  holder_data:  %d%n", holderData);
        System.out.printf("  custom:       %d%n", custom);
        if (!customNames.isEmpty()) {
            System.out.println("  Custom components (need overrides):");
            for (String cn : customNames) {
                System.out.println("    - " + cn);
            }
        }
    }

    /**
     * Classifies a single data component type and returns its schema entry.
     */
    static Map<String, Object> classifyComponent(String name, DataComponentType<?> type,
                                                   Class<?> valueClass, Type typeArg,
                                                   RegistryAccess registryAccess) {

        // 1. Unit → empty
        if (valueClass == Unit.class) {
            return emptyEntry(name);
        }

        // 2. Direct Integer → probe VarInt/Int
        if (valueClass == Integer.class) {
            String intType = probeIntType(type, registryAccess);
            return embedEntry(name, intType);
        }

        // 3. Direct Float → embed pk.Float
        if (valueClass == Float.class) {
            return embedEntry(name, "pk.Float");
        }

        // 4. Direct Boolean → embed pk.Boolean
        if (valueClass == Boolean.class) {
            return embedEntry(name, "pk.Boolean");
        }

        // 5. Identifier (ResourceLocation) → embed pk.String
        if (Identifier.class.isAssignableFrom(valueClass)) {
            return embedEntry(name, "pk.String");
        }

        // 6. TagKey<X> → embed pk.String
        if (TagKey.class.isAssignableFrom(valueClass)) {
            return embedEntry(name, "pk.String");
        }

        // 7. Component (chat text) → custom (needs delegate override for chat.Message)
        if (valueClass.getName().equals("net.minecraft.network.chat.Component") ||
            valueClass.getSimpleName().equals("Component") &&
            valueClass.getName().contains("net.minecraft.network.chat")) {
            return customEntry(name);
        }

        // 9. Direct Holder<X> / HolderSet<X>.
        //    Holder<X> where X declares DIRECT_STREAM_CODEC is a registryEntryHolder on the
        //    wire (VarInt id+1, 0 = inline X) → holder_data. Otherwise a plain registry id.
        //    HolderSet<X> is an IDSet (26.x: provides_banner_patterns, damage_resistant).
        if (typeArg instanceof ParameterizedType holderPt) {
            Type rawType = holderPt.getRawType();
            if (rawType instanceof Class<?> rawCls && rawCls.getSimpleName().equals("Holder")) {
                Class<?> innerCls = resolveRawClass(holderPt.getActualTypeArguments()[0]);
                if (innerCls != null && hasDirectStreamCodec(innerCls)
                        && !SoundEvent.class.isAssignableFrom(innerCls)) {
                    return holderDataEntry(name, innerCls);
                }
                return embedEntry(name, "pk.VarInt");
            }
            if (rawType instanceof Class<?> rawCls && rawCls.getSimpleName().equals("HolderSet")) {
                return embedEntry(name, "IDSet");
            }
        }

        // 10. Enum → embed pk.VarInt
        //     (override for Rarity, MapPostProcessing → named_int)
        if (valueClass.isEnum()) {
            return embedEntry(name, "pk.VarInt");
        }

        // 11. Record types
        if (valueClass.isRecord()) {
            return classifyRecord(name, type, valueClass, registryAccess);
        }

        // 12. List<X> as top-level type → custom
        if (List.class.isAssignableFrom(valueClass)) {
            return customEntry(name);
        }

        // 13. Fallback: non-Record, non-enum, non-primitive → custom
        return customEntry(name);
    }

    /**
     * Classifies a Record-typed data component.
     */
    static Map<String, Object> classifyRecord(String name, DataComponentType<?> type,
                                                Class<?> recordClass,
                                                RegistryAccess registryAccess) {

        RecordComponent[] components = recordClass.getRecordComponents();

        if (components.length == 0) {
            // Empty record → treat as empty.
            return emptyEntry(name);
        }

        // --- Single-field Record ---
        if (components.length == 1) {
            RecordComponent rc = components[0];
            Type fieldGenType = rc.getGenericType();
            Class<?> fieldClass = rc.getType();

            // Single List<X> field → array pattern.
            if (List.class.isAssignableFrom(fieldClass)) {
                Type elemType = getListElementType(fieldGenType);
                String goElem = mapFieldType(elemType);
                if (goElem == null) goElem = resolveRawClass(elemType).getSimpleName();
                String fieldName = toPascalCase(rc.getName());
                return arrayEntry(name, fieldName, goElem);
            }

            // Single int field → probe VarInt/Int.
            if (fieldClass == int.class) {
                String intType = probeSingleIntRecord(type, recordClass, registryAccess);
                return embedEntry(name, intType);
            }

            // Single Holder<X> field where X has inline data (DIRECT_STREAM_CODEC) →
            // registryEntryHolder on the wire (e.g. instrument, jukebox_playable in 26.x).
            if (fieldGenType instanceof ParameterizedType hpt
                    && hpt.getRawType() instanceof Class<?> hraw
                    && hraw.getSimpleName().equals("Holder")) {
                Class<?> innerCls = resolveRawClass(hpt.getActualTypeArguments()[0]);
                if (innerCls != null && hasDirectStreamCodec(innerCls)
                        && !SoundEvent.class.isAssignableFrom(innerCls)) {
                    return holderDataEntry(name, innerCls);
                }
            }

            // Single simple/complex field → embed with mapped type.
            String goType = mapFieldType(fieldGenType);
            if (goType != null) {
                return embedEntry(name, goType);
            }

            // Can't map the single field → custom.
            return customEntry(name);
        }

        // --- Multi-field Record → attempt tuple ---
        return classifyMultiFieldRecord(name, type, recordClass, components, registryAccess);
    }

    /**
     * Classifies a multi-field Record as a tuple pattern.
     * Falls back to "custom" if any field type can't be mapped or if the
     * record contains Holder<X> fields with inline data (DIRECT_STREAM_CODEC).
     */
    static Map<String, Object> classifyMultiFieldRecord(String name, DataComponentType<?> type,
                                                          Class<?> recordClass,
                                                          RecordComponent[] components,
                                                          RegistryAccess registryAccess) {

        List<Map<String, String>> fields = new ArrayList<>();
        boolean hasComplexHolder = false;

        for (RecordComponent rc : components) {
            Type fieldGenType = rc.getGenericType();
            Class<?> fieldClass = rc.getType();
            String fieldName = toPascalCase(rc.getName());

            // Check for Holder<X> fields with inline data (DIRECT_STREAM_CODEC).
            if (fieldGenType instanceof ParameterizedType pt) {
                Type rawType = pt.getRawType();
                if (rawType instanceof Class<?> rawCls && rawCls.getSimpleName().equals("Holder")) {
                    Type innerType = pt.getActualTypeArguments()[0];
                    Class<?> innerCls = resolveRawClass(innerType);
                    if (innerCls != null) {
                        // Check if the holder type has inline data capability.
                        boolean hasDirectCodec = false;
                        try {
                            innerCls.getDeclaredField("DIRECT_STREAM_CODEC");
                            hasDirectCodec = true;
                        } catch (NoSuchFieldException e) {
                            // No direct codec → simple registry reference.
                        }

                        if (hasDirectCodec && !SoundEvent.class.isAssignableFrom(innerCls)) {
                            // Complex holder with inline data (not SoundEvent which
                            // go-mc already handles) → can't auto-template as tuple.
                            hasComplexHolder = true;
                        }
                    }
                }
            }

            String goType = mapFieldType(fieldGenType);
            if (goType == null) {
                // Can't map this field type → fall back to custom.
                return customEntry(name);
            }

            fields.add(tupleField(fieldName, goType));
        }

        if (hasComplexHolder) {
            // Record has Holder fields with inline data → custom.
            return customEntry(name);
        }

        return tupleEntry(name, fields);
    }

    // -----------------------------------------------------------------------
    // JSON output
    // -----------------------------------------------------------------------

    static void writeJson(PrintWriter pw, List<Map<String, Object>> schema) {
        pw.println("[");
        for (int i = 0; i < schema.size(); i++) {
            var entry = schema.get(i);
            String pattern = (String) entry.get("pattern");
            String suffix = (i < schema.size() - 1) ? "," : "";

            switch (pattern) {
                case "embed" -> writeEmbedEntry(pw, entry, suffix);
                case "embed_nbt" -> writeSimpleEntry(pw, entry, suffix);
                case "empty" -> writeSimpleEntry(pw, entry, suffix);
                case "custom" -> writeSimpleEntry(pw, entry, suffix);
                case "array" -> writeArrayEntry(pw, entry, suffix);
                case "tuple" -> writeTupleEntry(pw, entry, suffix);
                case "holder_data" -> writeHolderDataEntry(pw, entry, suffix);
                default -> writeSimpleEntry(pw, entry, suffix);
            }
        }
        pw.println("]");
    }

    static void writeSimpleEntry(PrintWriter pw, Map<String, Object> entry, String suffix) {
        pw.printf("  {\"name\": %s, \"pattern\": %s}%s%n",
            jsonStr((String) entry.get("name")),
            jsonStr((String) entry.get("pattern")),
            suffix);
    }

    static void writeEmbedEntry(PrintWriter pw, Map<String, Object> entry, String suffix) {
        pw.printf("  {\"name\": %s, \"pattern\": \"embed\", \"embedType\": %s}%s%n",
            jsonStr((String) entry.get("name")),
            jsonStr((String) entry.get("embedType")),
            suffix);
    }

    static void writeArrayEntry(PrintWriter pw, Map<String, Object> entry, String suffix) {
        pw.printf("  {\"name\": %s, \"pattern\": \"array\", \"fieldName\": %s, \"elementType\": %s}%s%n",
            jsonStr((String) entry.get("name")),
            jsonStr((String) entry.get("fieldName")),
            jsonStr((String) entry.get("elementType")),
            suffix);
    }

    @SuppressWarnings("unchecked")
    static void writeTupleEntry(PrintWriter pw, Map<String, Object> entry, String suffix) {
        var fields = (List<Map<String, String>>) entry.get("fields");
        pw.printf("  {\"name\": %s, \"pattern\": \"tuple\", \"fields\": [%n",
            jsonStr((String) entry.get("name")));
        for (int j = 0; j < fields.size(); j++) {
            var f = fields.get(j);
            String fSuffix = (j < fields.size() - 1) ? "," : "";
            pw.printf("    {\"name\": %s, \"type\": %s}%s%n",
                jsonStr(f.get("name")),
                jsonStr(f.get("type")),
                fSuffix);
        }
        pw.printf("  ]}%s%n", suffix);
    }

    @SuppressWarnings("unchecked")
    static void writeHolderDataEntry(PrintWriter pw, Map<String, Object> entry, String suffix) {
        var fields = (List<Map<String, String>>) entry.get("inlineFields");
        if (fields == null) {
            pw.printf("  {\"name\": %s, \"pattern\": \"holder_data\", \"inlineType\": %s}%s%n",
                jsonStr((String) entry.get("name")),
                jsonStr((String) entry.get("inlineType")),
                suffix);
            return;
        }
        pw.printf("  {\"name\": %s, \"pattern\": \"holder_data\", \"inlineType\": %s, \"inlineFields\": [%n",
            jsonStr((String) entry.get("name")),
            jsonStr((String) entry.get("inlineType")));
        for (int j = 0; j < fields.size(); j++) {
            var f = fields.get(j);
            String fSuffix = (j < fields.size() - 1) ? "," : "";
            pw.printf("    {\"name\": %s, \"type\": %s}%s%n",
                jsonStr(f.get("name")),
                jsonStr(f.get("type")),
                fSuffix);
        }
        pw.printf("  ]}%s%n", suffix);
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
