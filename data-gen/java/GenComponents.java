import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.component.DataComponentType;

import java.io.*;
import java.util.*;

/**
 * GenComponents — Extracts data component types with their registry IDs and
 * network-serializability from MC runtime.
 *
 * Output: components.json in the current directory.
 *
 * Fields per component:
 *   id          — registry protocol_id (= wire ID for DataComponentPatch)
 *   name        — registry name (e.g., "minecraft:custom_data")
 *   networkable — true if the type has a streamCodec (can be sent over wire)
 */
public class GenComponents {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        List<Map<String, Object>> components = new ArrayList<>();

        for (DataComponentType<?> type : BuiltInRegistries.DATA_COMPONENT_TYPE) {
            var key = BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(type);
            int id = BuiltInRegistries.DATA_COMPONENT_TYPE.getId(type);

            boolean networkable = false;
            try {
                var codec = type.streamCodec();
                networkable = (codec != null);
            } catch (Exception e) {
                // streamCodec() throws if not networkable.
            }

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("id", id);
            entry.put("name", key.toString());
            entry.put("networkable", networkable);

            components.add(entry);
        }

        // Sort by id.
        components.sort(Comparator.comparingInt(e -> (int) e.get("id")));

        // Write JSON manually (no external deps).
        try (PrintWriter pw = new PrintWriter(new FileWriter("components.json"))) {
            pw.println("[");
            for (int i = 0; i < components.size(); i++) {
                var e = components.get(i);
                pw.printf("  {\"id\": %d, \"name\": %s, \"networkable\": %s}%s%n",
                    e.get("id"),
                    jsonStr((String) e.get("name")),
                    e.get("networkable"),
                    i < components.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        long networkable = components.stream().filter(e -> (boolean) e.get("networkable")).count();
        System.out.printf("GenComponents: wrote components.json (%d total, %d networkable)%n",
            components.size(), networkable);
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
