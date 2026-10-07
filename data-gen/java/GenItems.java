import net.minecraft.SharedConstants;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.component.DataComponents;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.network.chat.Component;
import net.minecraft.network.chat.contents.TranslatableContents;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.item.Item;

import java.io.*;
import java.lang.reflect.Method;
import java.util.*;

/**
 * GenItems — Extracts per-item default components needed by gen_item.go.
 *
 * Output: items.json in the current directory, in the same shape as the
 * `items.json` report that the MC data generator (--all) produced up to
 * 1.21.11 and dropped in 26.x:
 *
 *   { "minecraft:stone": { "components": {
 *       "minecraft:max_stack_size": 64,
 *       "minecraft:item_name": { "translate": "block.minecraft.stone" } } }, ... }
 *
 * The two components gen_item.go types are written as JSON; all of the item's default
 * components are also written as "wire": a DataComponentPatch in its network encoding
 * (DataComponentPatch.STREAM_CODEC, every component added, none removed), base64 — what the
 * library reads with the same decoder it reads an item stack's patch with, and so the tool
 * rules, the food, the consumable, the attribute modifiers of every item, exactly. A component
 * whose network encoding needs a registry only a server's data packs make (a jukebox song)
 * cannot be written from the built-in registries; it is left out and named in "wire_skipped".
 *
 * 26.x binds item default components lazily through
 * BuiltInRegistries.DATA_COMPONENT_INITIALIZERS (the server does this when it
 * loads its data packs). That field does not exist in 1.21.11, where
 * Item.components() is available right after bootstrap, so the binding step is
 * done reflectively to keep this extractor compiling against both jar generations.
 */
public class GenItems {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        bindDefaultComponents();

        // Sort by registry name for stable output.
        List<String> names = new ArrayList<>();
        Map<String, Item> byName = new HashMap<>();
        for (Item item : BuiltInRegistries.ITEM) {
            String name = BuiltInRegistries.ITEM.getKey(item).toString();
            names.add(name);
            byName.put(name, item);
        }
        Collections.sort(names);

        try (PrintWriter pw = new PrintWriter(new FileWriter("items.json"))) {
            pw.println("{");
            for (int i = 0; i < names.size(); i++) {
                String name = names.get(i);
                Item item = byName.get(name);
                var comps = item.components();

                Integer maxStack = comps.get(DataComponents.MAX_STACK_SIZE);
                if (maxStack == null) maxStack = 64;

                String translate = "item." + name.replace(':', '.');
                Component itemName = comps.get(DataComponents.ITEM_NAME);
                if (itemName != null && itemName.getContents() instanceof TranslatableContents tc) {
                    translate = tc.getKey();
                }

                pw.printf("  %s: {%n", jsonStr(name));
                pw.println("    \"components\": {");
                pw.printf("      \"minecraft:max_stack_size\": %d,%n", maxStack);
                pw.printf("      \"minecraft:item_name\": {\"translate\": %s}%n", jsonStr(translate));
                pw.println("    },");
                List<String> skipped = new ArrayList<>();
                pw.printf("    \"wire\": \"%s\"", wire(comps, skipped));
                if (!skipped.isEmpty()) {
                    pw.printf(",%n    \"wire_skipped\": [%s]", String.join(", ", skipped.stream().map(GenItems::jsonStr).toList()));
                    skippedTotal += skipped.size();
                }
                pw.println();
                pw.printf("  }%s%n", i < names.size() - 1 ? "," : "");
            }
            pw.println("}");
        }

        System.out.printf("GenItems: wrote items.json (%d items, %d components left out of the wire form)%n", names.size(), skippedTotal);
    }

    /**
     * 26.x: run BuiltInRegistries.DATA_COMPONENT_INITIALIZERS.build(provider) and apply every
     * PendingComponents so Holder.Reference.components() (and thus Item.components()) is bound.
     * No-op on versions without that field (1.21.11 and earlier).
     */
    static void bindDefaultComponents() throws Exception {
        Object initializers;
        try {
            initializers = BuiltInRegistries.class.getField("DATA_COMPONENT_INITIALIZERS").get(null);
        } catch (NoSuchFieldException e) {
            return; // pre-26.x: components are bound at registration time
        }
        HolderLookup.Provider provider = vanillaLookup();
        Method build = initializers.getClass().getMethod("build", HolderLookup.Provider.class);
        List<?> pending = (List<?>) build.invoke(initializers, provider);
        Class<?> pendingType = Class.forName("net.minecraft.core.component.DataComponentInitializers$PendingComponents");
        Method apply = pendingType.getMethod("apply");
        for (Object p : pending) {
            apply.invoke(p);
        }
        System.out.printf("GenItems: bound default components for %d registries%n", pending.size());
    }

    static int skippedTotal;
    /** the built-in registries, made after the bootstrap */
    static net.minecraft.core.RegistryAccess access;

    /** wire encodes the default components as a DataComponentPatch, base64. */
    @SuppressWarnings({"unchecked", "rawtypes"})
    static String wire(net.minecraft.core.component.DataComponentMap comps, List<String> skipped) {
        if (access == null) access = net.minecraft.core.RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY);
        List<byte[]> entries = new ArrayList<>();
        List<net.minecraft.core.component.TypedDataComponent<?>> sorted = new ArrayList<>();
        comps.forEach(sorted::add);
        sorted.sort(Comparator.comparingInt(c -> BuiltInRegistries.DATA_COMPONENT_TYPE.getId(c.type())));
        for (var c : sorted) {
            var buf = new net.minecraft.network.RegistryFriendlyByteBuf(io.netty.buffer.Unpooled.buffer(), access);
            try {
                net.minecraft.core.component.DataComponentType.STREAM_CODEC.encode(buf, c.type());
                ((net.minecraft.network.codec.StreamCodec) c.type().streamCodec()).encode(buf, c.value());
            } catch (RuntimeException e) {
                skipped.add(BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(c.type()).toString());
                continue;
            }
            byte[] b = new byte[buf.readableBytes()];
            buf.readBytes(b);
            entries.add(b);
        }
        var out = new net.minecraft.network.FriendlyByteBuf(io.netty.buffer.Unpooled.buffer());
        out.writeVarInt(entries.size());
        out.writeVarInt(0);
        for (byte[] b : entries) out.writeBytes(b);
        byte[] all = new byte[out.readableBytes()];
        out.readBytes(all);
        return Base64.getEncoder().encodeToString(all);
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
