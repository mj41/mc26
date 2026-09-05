import net.minecraft.SharedConstants;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.server.Bootstrap;

import java.io.*;
import java.util.*;

/**
 * GenBiomes — Extracts biome names from MC runtime.
 *
 * Biomes are a data-driven registry. This extractor uses VanillaRegistries
 * to enumerate all vanilla biome names, then outputs them sorted by
 * ResourceLocation (namespace:path alphabetical order).
 *
 * Protocol IDs are assigned alphabetically because the vanilla server's
 * RegistryDataLoader reads biome JSONs from data packs via
 * ResourceManager.listResources() which returns ResourceLocations in
 * Comparable order (namespace then path, both alphabetical).
 *
 * Note: The actual protocol IDs are sent by the server to each client
 * during login (Configuration phase, Registry Data packet). This file
 * provides the _default vanilla_ ordering for go-mc's built-in biome
 * list. If the server sends different IDs, go-mc should use those.
 *
 * Output: biomes.json in the current directory.
 *
 * Fields per biome:
 *   id   — protocol registry ID (alphabetical order)
 *   name — registry name (e.g., "minecraft:plains")
 */
public class GenBiomes {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // Load all vanilla registries including data-driven ones (biomes, etc.).
        var lookup = vanillaLookup();
        var biomes = lookup.lookupOrThrow(Registries.BIOME);

        // Collect all biome names.
        List<String> names = new ArrayList<>();
        biomes.listElements().forEach(holder ->
            names.add(holder.getRegisteredName())
        );

        // Sort alphabetically — this matches RegistryDataLoader's file enumeration
        // order, which determines the vanilla server's protocol IDs.
        Collections.sort(names);

        // Write JSON manually (no external deps).
        try (PrintWriter pw = new PrintWriter(new FileWriter("biomes.json"))) {
            pw.println("[");
            for (int i = 0; i < names.size(); i++) {
                pw.printf("  {\"id\": %d, \"name\": %s}%s%n",
                    i,
                    jsonStr(names.get(i)),
                    i < names.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        System.out.printf("GenBiomes: wrote biomes.json (%d biomes)%n", names.size());
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    /**
     * VanillaRegistries.createLookup() (≤ 26.2) became createWorldLookup() in 26.3;
     * resolve whichever exists so one extractor serves every 26.x.
     */
    static net.minecraft.core.HolderLookup.Provider vanillaLookup() throws Exception {
        Class<?> vr = Class.forName("net.minecraft.data.registries.VanillaRegistries");
        for (String name : new String[] {"createLookup", "createWorldLookup"}) {
            try {
                return (net.minecraft.core.HolderLookup.Provider) vr.getMethod(name).invoke(null);
            } catch (NoSuchMethodException e) {
                // try the next name
            }
        }
        throw new IllegalStateException("VanillaRegistries has neither createLookup() nor createWorldLookup()");
    }
}
