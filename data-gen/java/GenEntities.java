import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.entity.EntityType;

import java.io.*;
import java.util.*;

/**
 * GenEntities — Extracts entity types with dimensions from MC runtime.
 *
 * Output: entities.json in the current directory.
 *
 * Fields per entity:
 *   id          — protocol registry ID
 *   name        — registry name (e.g., "minecraft:allay")
 *   width       — collision box width
 *   height      — collision box height
 *   category    — entity category name (e.g., "misc", "monster", "creature")
 *   passengers  — the points its passengers sit at, by passenger (EntityAttachment.PASSENGER),
 *                 relative to its position before its rotation (a boat places its own)
 *   vehicle     — the point of it that sits at a vehicle's passenger point (VEHICLE)
 */
public class GenEntities {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        List<Map<String, Object>> entities = new ArrayList<>();

        for (EntityType<?> type : BuiltInRegistries.ENTITY_TYPE) {
            var key = BuiltInRegistries.ENTITY_TYPE.getKey(type);
            int id = BuiltInRegistries.ENTITY_TYPE.getId(type);

            var dims = type.getDimensions();

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("id", id);
            entry.put("name", key.toString());
            entry.put("width", dims.width());
            entry.put("height", dims.height());
            entry.put("category", type.getCategory().getName());
            entry.put("passengers", points(dims.attachments(), "PASSENGER"));
            entry.put("vehicle", points(dims.attachments(), "VEHICLE"));

            entities.add(entry);
        }

        // Sort by id.
        entities.sort(Comparator.comparingInt(e -> (int) e.get("id")));

        // Write JSON manually (no external deps).
        try (PrintWriter pw = new PrintWriter(new FileWriter("entities.json"))) {
            pw.println("[");
            for (int i = 0; i < entities.size(); i++) {
                var e = entities.get(i);
                pw.printf("  {\"id\": %d, \"name\": %s, \"width\": %s, \"height\": %s, \"category\": %s, \"passengers\": %s, \"vehicle\": %s}%s%n",
                    e.get("id"),
                    jsonStr((String) e.get("name")),
                    formatNum(e.get("width")),
                    formatNum(e.get("height")),
                    jsonStr((String) e.get("category")),
                    e.get("passengers"),
                    e.get("vehicle"),
                    i < entities.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        System.out.printf("GenEntities: wrote entities.json (%d entities)%n", entities.size());
    }

    /** points lists the attachment points of a kind, as JSON: [[x, y, z], …]. */
    @SuppressWarnings("unchecked")
    static String points(net.minecraft.world.entity.EntityAttachments attachments, String kind) throws Exception {
        var f = net.minecraft.world.entity.EntityAttachments.class.getDeclaredField("attachments");
        f.setAccessible(true);
        var map = (Map<net.minecraft.world.entity.EntityAttachment, List<net.minecraft.world.phys.Vec3>>) f.get(attachments);
        List<String> out = new ArrayList<>();
        for (var v : map.getOrDefault(net.minecraft.world.entity.EntityAttachment.valueOf(kind), List.of())) {
            out.add("[" + v.x + ", " + v.y + ", " + v.z + "]");
        }
        return "[" + String.join(", ", out) + "]";
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    static String formatNum(Object num) {
        if (num instanceof Float f) return String.valueOf(f.doubleValue());
        if (num instanceof Double d) return String.valueOf(d);
        return String.valueOf(num);
    }
}
