import com.mojang.brigadier.StringReader;
import com.mojang.serialization.Lifecycle;
import net.minecraft.SharedConstants;
import net.minecraft.core.Holder;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.MappedRegistry;
import net.minecraft.core.Registry;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.component.DataComponentType;
import net.minecraft.core.component.TypedDataComponent;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.RegistryFriendlyByteBuf;
import net.minecraft.network.codec.StreamCodec;
import net.minecraft.resources.ResourceKey;
import net.minecraft.server.Bootstrap;
import net.minecraft.commands.arguments.item.ItemParser;
import net.minecraft.util.HashOps;

import java.io.*;
import java.util.*;

/**
 * GenComponentHashes — what a vanilla client sends for the components of a stack it clicks.
 *
 * A container click carries a hash of every component of a changed stack's patch: CRC32C over
 * the component as its (persistent) codec encodes it with HashOps (ClientPacketListener's
 * decoratedHashOpsGenerator). The library decodes components from their network encoding;
 * to hash one it has to rebuild what the codec would write. This extractor gives it the
 * answers to check against: sample stacks, written as a give command writes them, parsed with
 * the game's ItemParser; for every component of each, its network encoding
 * (TypedDataComponent: the type id, then the value's stream codec), base64, and the hash.
 *
 * The holders the components name (an enchantment, a trim material) are written by id on
 * the wire: the ids of the vanilla registries this extractor builds, listed in "registries"
 * per registry name in id order, so the test can name them as a server's registry data would.
 *
 * Output: component_hashes.json
 *   { "registries": { "minecraft:enchantment": ["minecraft:aqua_affinity", ...], ... },
 *     "samples": [ { "stack": "diamond_sword[custom_name=...]", "component": "minecraft:custom_name",
 *                    "wire": "...", "hash": 123 }, ... ] }
 */
public class GenComponentHashes {
    /** The stacks: every kind of codec a component has — values, records, lists, texts, holders, nested stacks. */
    static final String[] STACKS = {
        "diamond_pickaxe[damage=7,repair_cost=2,unbreakable={},enchantment_glint_override=true]",
        "diamond_pickaxe[enchantments={\"minecraft:efficiency\":4,\"minecraft:unbreaking\":3}]",
        "enchanted_book[stored_enchantments={\"minecraft:sharpness\":5}]",
        "diamond_sword[custom_name=\"Excalibur\",lore=[\"first line\",{text:\"red\",color:\"red\",italic:false}]]",
        "diamond_sword[custom_name={text:\"Bold\",bold:true,extra:[\" and \",{translate:\"item.minecraft.stick\"}]}]",
        "diamond_sword[item_name={translate:\"item.minecraft.stick\",with:[\"x\",{text:\"y\",underlined:true}]}]",
        "diamond_sword[rarity=\"epic\",max_stack_size=4,max_damage=10]",
        "diamond_sword[tooltip_display={hide_tooltip:false,hidden_components:[\"minecraft:enchantments\",\"minecraft:damage\"]}]",
        "diamond_sword[custom_data={a:1b,b:\"s\",c:[I;1,2],d:{e:2.5d,f:[1.0f,2.0f]},g:3L,h:4s}]",
        "written_book[written_book_content={title:\"T\",author:\"Robot\",pages:[\"one\",\"two\"]}]",
        "written_book[written_book_content={title:{raw:\"T\",filtered:\"t\"},author:\"Robot\",generation:2,resolved:true,pages:[{raw:\"one\",filtered:\"1\"}]}]",
        "writable_book[writable_book_content={pages:[\"draft\",{raw:\"a\",filtered:\"b\"}]}]",
        "firework_rocket[fireworks={flight_duration:2,explosions:[{shape:\"star\",colors:[I;16711680],has_trail:true}]}]",
        "firework_rocket[fireworks={flight_duration:1,explosions:[{shape:\"burst\",colors:[I;1,2],fade_colors:[I;3],has_twinkle:true}]}]",
        "firework_star[firework_explosion={shape:\"large_ball\",colors:[I;255]}]",
        "potion[potion_contents={potion:\"minecraft:strength\"}]",
        "potion[potion_contents={custom_color:255,custom_effects:[{id:\"minecraft:speed\",amplifier:1b,duration:200,show_particles:false}],custom_name:\"fast\"}]",
        "tipped_arrow[potion_contents={potion:\"minecraft:swiftness\"}]",
        "leather_chestplate[dyed_color=16711680]",
        "iron_chestplate[trim={material:\"minecraft:gold\",pattern:\"minecraft:coast\"}]",
        "player_head[profile={name:\"Robot\"}]",
        "player_head[profile={name:\"Robot\",id:[I;1,2,3,4]}]",
        "white_banner[banner_patterns=[{pattern:\"minecraft:stripe_top\",color:\"red\"},{pattern:\"minecraft:border\",color:\"blue\"}]]",
        "shield[base_color=\"red\"]",
        "bundle[bundle_contents=[{id:\"minecraft:stone\",count:2},{id:\"minecraft:diamond_sword\",components:{\"minecraft:damage\":3}}]]",
        "shulker_box[container=[{slot:0,item:{id:\"minecraft:dirt\",count:3}},{slot:5,item:{id:\"minecraft:stick\"}}]]",
        "crossbow[charged_projectiles=[{id:\"minecraft:arrow\"}]]",
        "goat_horn[instrument=\"minecraft:ponder_goat_horn\"]",
        "compass[lodestone_tracker={target:{pos:[I;0,64,0],dimension:\"minecraft:overworld\"},tracked:false}]",
        "compass[lodestone_tracker={}]",
        "suspicious_stew[suspicious_stew_effects=[{id:\"minecraft:speed\",duration:100},{id:\"minecraft:night_vision\"}]]",
        "filled_map[map_id=3]",
        "golden_apple[rarity=\"rare\"]",
        "diamond_sword[attribute_modifiers=[{type:\"minecraft:attack_damage\",id:\"minecraft:x\",amount:3.0d,operation:\"add_value\",slot:\"mainhand\"}]]",
        "stone[custom_model_data={floats:[1.5f],flags:[true],strings:[\"a\"],colors:[I;7]}]",
        "diamond_sword[!minecraft:damage]",
    };

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        GenItems.bindDefaultComponents();
        HolderLookup.Provider lookup = GenItems.vanillaLookup();
        RegistryAccess access = registries(lookup);
        var hashOps = lookup.createSerializationContext(HashOps.CRC32C_INSTANCE);
        var parser = new ItemParser(lookup);

        Set<String> used = new TreeSet<>();
        List<String> samples = new ArrayList<>();
        int failed = 0;
        for (String stack : STACKS) {
            List<TypedDataComponent<?>> comps = new ArrayList<>();
            try {
                parser.parse(new StringReader(stack), new ItemParser.Visitor() {
                    @Override
                    public <T> void visitComponent(final DataComponentType<T> type, final T value) {
                        comps.add(new TypedDataComponent<>(type, value));
                    }
                });
            } catch (Exception e) {
                System.out.printf("GenComponentHashes: %s: %s%n", stack, e.getMessage());
                failed++;
                continue;
            }
            for (var c : comps) {
                String name = BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(c.type()).toString();
                String wire;
                int hash;
                try {
                    wire = wire(access, c);
                    hash = encodeHash(c, hashOps);
                } catch (RuntimeException e) {
                    System.out.printf("GenComponentHashes: %s %s: %s%n", stack, name, e);
                    failed++;
                    continue;
                }
                used.add(name);
                samples.add(String.format("    {\"stack\": %s, \"component\": %s, \"wire\": \"%s\", \"hash\": %d}",
                    GenItems.jsonStr(stack), GenItems.jsonStr(name), wire, hash));
            }
        }

        try (PrintWriter pw = new PrintWriter(new FileWriter("component_hashes.json"))) {
            pw.println("{");
            pw.println("  \"registries\": {");
            List<String> regs = new ArrayList<>();
            access.registries().sorted(Comparator.comparing(e -> e.key().identifier().toString())).forEach(e -> {
                if (BuiltInRegistries.REGISTRY.containsKey(e.key().identifier())) return; // the library has the built-in ones
                List<String> names = new ArrayList<>();
                for (var v : e.value()) names.add(GenItems.jsonStr(e.value().getKey(cast(v)).toString()));
                regs.add(String.format("    %s: [%s]", GenItems.jsonStr(e.key().identifier().toString()), String.join(", ", names)));
            });
            pw.println(String.join(",\n", regs));
            pw.println("  },");
            pw.println("  \"samples\": [");
            pw.println(String.join(",\n", samples));
            pw.println("  ]");
            pw.println("}");
        }
        System.out.printf("GenComponentHashes: wrote component_hashes.json (%d samples of %d components, %d failed)%n", samples.size(), used.size(), failed);
        if (failed > 0) throw new IllegalStateException("GenComponentHashes: " + failed + " samples failed");
    }

    @SuppressWarnings("unchecked")
    static <T> T cast(Object o) { return (T) o; }

    @SuppressWarnings({"unchecked", "rawtypes"})
    static int encodeHash(TypedDataComponent<?> c, com.mojang.serialization.DynamicOps<com.google.common.hash.HashCode> ops) {
        return ((com.google.common.hash.HashCode) ((TypedDataComponent) c).encodeValue(ops).getOrThrow()).asInt();
    }

    @SuppressWarnings({"unchecked", "rawtypes"})
    static String wire(RegistryAccess access, TypedDataComponent<?> c) {
        var buf = new RegistryFriendlyByteBuf(io.netty.buffer.Unpooled.buffer(), access);
        DataComponentType.STREAM_CODEC.encode(buf, c.type());
        ((StreamCodec) c.type().streamCodec()).encode(buf, c.value());
        byte[] b = new byte[buf.readableBytes()];
        buf.readBytes(b);
        return Base64.getEncoder().encodeToString(b);
    }

    /**
     * registries makes a RegistryAccess of the lookup: the built-in registries as they are, and
     * a registry with ids for each of the others (the lookup's data-driven registries have none),
     * in the order the lookup lists their elements. Holders are written by the id of their value.
     */
    @SuppressWarnings({"unchecked", "rawtypes"})
    static RegistryAccess registries(HolderLookup.Provider lookup) {
        List<Registry<?>> all = new ArrayList<>();
        lookup.listRegistries().forEach(reg -> {
            var builtIn = BuiltInRegistries.REGISTRY.getValue(reg.key().identifier());
            if (builtIn != null) {
                all.add(builtIn);
                return;
            }
            MappedRegistry m = new MappedRegistry((ResourceKey) reg.key(), Lifecycle.stable());
            try {
                reg.listElements().sorted(Comparator.comparing(h -> ((Holder.Reference<?>) h).key().identifier().toString())).forEach(h -> {
                    var ref = (Holder.Reference<?>) h;
                    Registry.register(m, (ResourceKey) ref.key(), ref.value());
                });
            } catch (IllegalStateException e) {
                // two entries of equal value (worldgen records): no component names these
                return;
            }
            m.freeze();
            all.add(m);
        });
        return new RegistryAccess.ImmutableRegistryAccess(all).freeze();
    }
}
