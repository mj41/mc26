import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.entity.BlockEntityType;

import java.io.*;
import java.util.*;

/**
 * GenBlockEntities — Extracts block entity types with their valid blocks from MC runtime.
 *
 * Output: block_entities.json in the current directory.
 *
 * Fields per block entity type:
 *   name         — registry name (e.g., "minecraft:furnace")
 *   valid_blocks — sorted list of block registry names this entity type applies to
 */
public class GenBlockEntities {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        List<Map<String, Object>> entities = new ArrayList<>();

        for (BlockEntityType<?> beType : BuiltInRegistries.BLOCK_ENTITY_TYPE) {
            var key = BuiltInRegistries.BLOCK_ENTITY_TYPE.getKey(beType);

            // Collect valid blocks by checking isValid() against all registered blocks.
            List<String> validBlocks = new ArrayList<>();
            for (Block block : BuiltInRegistries.BLOCK) {
                if (beType.isValid(block.defaultBlockState())) {
                    var blockKey = BuiltInRegistries.BLOCK.getKey(block);
                    validBlocks.add(blockKey.toString());
                }
            }
            Collections.sort(validBlocks);

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("name", key.toString());
            entry.put("valid_blocks", validBlocks);

            entities.add(entry);
        }

        // Write JSON manually (no external deps).
        try (PrintWriter pw = new PrintWriter(new FileWriter("block_entities.json"))) {
            pw.println("[");
            for (int i = 0; i < entities.size(); i++) {
                var e = entities.get(i);
                @SuppressWarnings("unchecked")
                var blocks = (List<String>) e.get("valid_blocks");

                pw.printf("  {\"name\": %s, \"valid_blocks\": [", jsonStr((String) e.get("name")));
                for (int j = 0; j < blocks.size(); j++) {
                    if (j > 0) pw.print(", ");
                    pw.print(jsonStr(blocks.get(j)));
                }
                pw.printf("]}%s%n", i < entities.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        System.out.printf("GenBlockEntities: wrote block_entities.json (%d block entity types)%n", entities.size());
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
