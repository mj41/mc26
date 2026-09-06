/**
 * GenEntityData — Extracts the entity metadata layout (SynchedEntityData) from the
 * unobfuscated server jar: the serializers with their wire form, and for every entity type
 * the synched fields it carries with their index and serializer.
 *
 * Output: entity_data.json in the current directory:
 *   { "version": 1,
 *     "serializers": [ {"id": 0, "name": "BYTE", "coverage": "full", "type": {"k": "prim", "t": "BYTE"}}, … ],
 *     "entities": { "minecraft:zombie": { "id": 152, "class": "net.minecraft.world.entity.monster.Zombie",
 *                     "fields": [ {"index": 0, "name": "DATA_SHARED_FLAGS_ID", "class": "Entity", "serializer": 0}, … ] } } }
 *
 * How: reflection after bootstrap — EntityTypes' static fields are EntityType<Zombie> and so on,
 * their generic argument names the entity class; that class and its superclasses declare static
 * EntityDataAccessor fields whose id() and serializer() are read (the ids are cumulative along
 * the hierarchy, so a superclass's fields keep their index in every subclass); the serializer ids
 * are EntityDataSerializers' registration order. The serializers' codecs are typed by
 * GenPacketSchema's interpreter over EntityDataSerializers.<clinit> (the same node kinds as
 * packet_schema.json), so the generator can decode a value by its serializer id.
 */
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public class GenEntityData {
    static final String SERIALIZERS = "net/minecraft/network/syncher/EntityDataSerializers";

    public static void main(String[] args) throws Exception {
        net.minecraft.SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();

        Class<?> serializersClass = Class.forName(SERIALIZERS.replace('/', '.'));
        Class<?> serializerType = Class.forName("net.minecraft.network.syncher.EntityDataSerializer");
        Method getSerializedId = serializersClass.getMethod("getSerializedId", serializerType);
        Class<?> accessorType = Class.forName("net.minecraft.network.syncher.EntityDataAccessor");
        Method accessorId = accessorType.getMethod("id");
        Method accessorSerializer = accessorType.getMethod("serializer");

        // serializers: registration order = wire id
        List<Map<String, Object>> serializers = new ArrayList<>();
        for (Field f : serializersClass.getDeclaredFields()) {
            if (!Modifier.isStatic(f.getModifiers()) || !serializerType.isAssignableFrom(f.getType())) continue;
            f.setAccessible(true);
            int id = (int) getSerializedId.invoke(null, f.get(null));
            if (id < 0) continue;
            Map<String, Object> node = GenPacketSchema.codecFieldNode(SERIALIZERS, f.getName(), 0);
            Map<String, Object> e = new LinkedHashMap<>();
            e.put("id", id); e.put("name", f.getName()); e.put("coverage", GenPacketSchema.coverage(node)); e.put("type", node);
            serializers.add(e);
        }
        serializers.sort(Comparator.comparingInt(e -> (Integer) e.get("id")));

        // entity types: EntityTypes' static fields are EntityType<Zombie> etc.; the generic
        // argument names the entity class, whose accessor fields (and its superclasses') are the layout
        Object registry = Class.forName("net.minecraft.core.registries.BuiltInRegistries").getField("ENTITY_TYPE").get(null);
        Class<?> registryType = Class.forName("net.minecraft.core.Registry");
        Method getKey = registryType.getMethod("getKey", Object.class);
        Method getId = registryType.getMethod("getId", Object.class);
        Class<?> entityTypeClass = Class.forName("net.minecraft.world.entity.EntityType");
        Map<Class<?>, List<Map<String, Object>>> ownFields = new HashMap<>();
        Map<String, Map<String, Object>> entities = new TreeMap<>();
        int failed = 0;
        for (String holder : new String[]{"net.minecraft.world.entity.EntityTypes", "net.minecraft.world.entity.EntityType"}) {
            Class<?> hc;
            try { hc = Class.forName(holder); } catch (ClassNotFoundException e) { continue; }
            for (Field tf : hc.getDeclaredFields()) {
                if (!Modifier.isStatic(tf.getModifiers()) || !entityTypeClass.isAssignableFrom(tf.getType())) continue;
                if (!(tf.getGenericType() instanceof ParameterizedType pt) || pt.getActualTypeArguments().length != 1) continue;
                Type arg = pt.getActualTypeArguments()[0];
                Class<?> base = arg instanceof Class<?> c ? c : arg instanceof ParameterizedType ap && ap.getRawType() instanceof Class<?> c2 ? c2 : null;
                if (base == null) continue;
                tf.setAccessible(true);
                Object type = tf.get(null);
                String key = String.valueOf(getKey.invoke(registry, type));
                if (entities.containsKey(key)) continue;
                List<Class<?>> chain = new ArrayList<>();
                for (Class<?> c = base; c != null && c != Object.class; c = c.getSuperclass()) chain.add(0, c);
                List<Map<String, Object>> fields = new ArrayList<>();
                for (Class<?> c : chain) {
                    List<Map<String, Object>> own = ownFields.get(c);
                    if (own == null) {
                        own = new ArrayList<>();
                        for (Field f : c.getDeclaredFields()) {
                            if (!Modifier.isStatic(f.getModifiers()) || !accessorType.isAssignableFrom(f.getType())) continue;
                            try {
                                f.setAccessible(true);
                                Object acc = f.get(null);
                                Map<String, Object> e = new LinkedHashMap<>();
                                e.put("index", accessorId.invoke(acc));
                                e.put("name", f.getName());
                                e.put("class", c.getSimpleName());
                                e.put("serializer", getSerializedId.invoke(null, accessorSerializer.invoke(acc)));
                                own.add(e);
                            } catch (Throwable t) {
                                failed++;
                                System.err.println("GenEntityData: " + c.getName() + "." + f.getName() + ": " + t);
                            }
                        }
                        ownFields.put(c, own);
                    }
                    fields.addAll(own);
                }
                fields.sort(Comparator.comparingInt(e -> (Integer) e.get("index")));
                Map<String, Object> e = new LinkedHashMap<>();
                e.put("id", getId.invoke(registry, type));
                e.put("class", base.getName());
                e.put("fields", fields);
                entities.put(key, e);
            }
        }

        StringBuilder sb = new StringBuilder("{\n  \"version\": 1,\n  \"serializers\": [\n");
        for (int i = 0; i < serializers.size(); i++) sb.append("    ").append(GenPacketSchema.jsonNode(serializers.get(i))).append(i + 1 < serializers.size() ? ",\n" : "\n");
        sb.append("  ],\n  \"entities\": {\n");
        int i = 0;
        for (Map.Entry<String, Map<String, Object>> en : entities.entrySet()) {
            sb.append("    ").append(GenPacketSchema.json(en.getKey())).append(": ").append(GenPacketSchema.jsonNode(en.getValue())).append(++i < entities.size() ? ",\n" : "\n");
        }
        sb.append("  }\n}\n");
        Files.writeString(Path.of("entity_data.json"), sb.toString(), StandardCharsets.UTF_8);
        long full = serializers.stream().filter(e -> "full".equals(e.get("coverage"))).count();
        System.err.printf("GenEntityData: %d serializers (%d fully typed), %d entity types, %d fields unreadable, written to entity_data.json%n",
            serializers.size(), full, entities.size(), failed);
    }
}
