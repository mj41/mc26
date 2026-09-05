import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.state.properties.*;
import net.minecraft.util.StringRepresentable;

import java.io.*;
import java.lang.reflect.*;
import java.util.*;

/**
 * GenBlockProperties — Extracts block state property definitions from BlockStateProperties
 * using reflection, including boolean, integer, and enum property types.
 *
 * Output: block_properties.json in the current directory.
 *
 * Structure:
 *   properties — array of property definitions, sorted by field name
 *   enums      — map of enum class name → full list of enum constants (all values, not just
 *                the subset used by a specific property), sorted by key
 */
public class GenBlockProperties {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        List<Map<String, Object>> properties = new ArrayList<>();
        Map<String, List<String>> enums = new TreeMap<>();

        Field[] fields = BlockStateProperties.class.getDeclaredFields();
        for (Field field : fields) {
            int mods = field.getModifiers();
            if (!Modifier.isStatic(mods) || !Modifier.isFinal(mods)) continue;
            if (!Property.class.isAssignableFrom(field.getType())) continue;

            field.setAccessible(true);
            Property<?> prop = (Property<?>) field.get(null);

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("field", field.getName());
            entry.put("name", prop.getName());

            if (prop instanceof BooleanProperty) {
                entry.put("type", "boolean");
            } else if (prop instanceof IntegerProperty intProp) {
                entry.put("type", "integer");
                List<Integer> vals = new ArrayList<>(intProp.getPossibleValues());
                entry.put("min", vals.get(0));
                entry.put("max", vals.get(vals.size() - 1));
            } else if (prop instanceof EnumProperty<?> enumProp) {
                entry.put("type", "enum");
                Class<?> enumClass = enumProp.getValueClass();
                String simpleName = enumClass.getSimpleName();
                entry.put("enum_class", simpleName);

                // Values for this specific property (may be a subset).
                List<String> propValues = new ArrayList<>();
                for (Object val : enumProp.getPossibleValues()) {
                    propValues.add(((StringRepresentable) val).getSerializedName());
                }
                entry.put("values", propValues);

                // Collect full enum constants for the enums map.
                if (!enums.containsKey(simpleName)) {
                    List<String> allValues = new ArrayList<>();
                    for (Object c : enumClass.getEnumConstants()) {
                        allValues.add(((StringRepresentable) c).getSerializedName());
                    }
                    enums.put(simpleName, allValues);
                }
            }

            properties.add(entry);
        }

        // Sort properties by field name.
        properties.sort(Comparator.comparing(e -> (String) e.get("field")));

        // Write JSON manually (no external deps).
        try (PrintWriter pw = new PrintWriter(new FileWriter("block_properties.json"))) {
            pw.println("{");

            // properties array
            pw.println("  \"properties\": [");
            for (int i = 0; i < properties.size(); i++) {
                var e = properties.get(i);
                String type = (String) e.get("type");
                pw.print("    {");
                pw.printf("\"field\": %s, ", jsonStr((String) e.get("field")));
                pw.printf("\"name\": %s, ", jsonStr((String) e.get("name")));
                pw.printf("\"type\": %s", jsonStr(type));

                if ("integer".equals(type)) {
                    pw.printf(", \"min\": %d, \"max\": %d", e.get("min"), e.get("max"));
                } else if ("enum".equals(type)) {
                    pw.printf(", \"enum_class\": %s", jsonStr((String) e.get("enum_class")));
                    @SuppressWarnings("unchecked")
                    var values = (List<String>) e.get("values");
                    pw.print(", \"values\": [");
                    for (int j = 0; j < values.size(); j++) {
                        if (j > 0) pw.print(", ");
                        pw.print(jsonStr(values.get(j)));
                    }
                    pw.print("]");
                }

                pw.printf("}%s%n", i < properties.size() - 1 ? "," : "");
            }
            pw.println("  ],");

            // enums object
            pw.println("  \"enums\": {");
            var enumKeys = new ArrayList<>(enums.keySet());
            for (int i = 0; i < enumKeys.size(); i++) {
                String key = enumKeys.get(i);
                List<String> vals = enums.get(key);
                pw.printf("    %s: [", jsonStr(key));
                for (int j = 0; j < vals.size(); j++) {
                    if (j > 0) pw.print(", ");
                    pw.print(jsonStr(vals.get(j)));
                }
                pw.printf("]%s%n", i < enumKeys.size() - 1 ? "," : "");
            }
            pw.println("  }");

            pw.println("}");
        }

        System.out.printf("GenBlockProperties: wrote block_properties.json (%d properties, %d enum types)%n",
            properties.size(), enums.size());
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
