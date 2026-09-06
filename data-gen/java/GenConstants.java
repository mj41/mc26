/**
 * GenConstants — Extracts the compile-time constants of a few Minecraft classes the library
 * mirrors by hand otherwise: inventory slot layout, section geometry, level limits, the NBT
 * keys of living entities.
 *
 * Output: constants.json in the current directory:
 *   { "net.minecraft.world.inventory.InventoryMenu": {"CRAFT_SLOT_START": 1, "SHIELD_SLOT": 45, …}, … }
 *
 * How: the ConstantValue attribute of every static final field of the listed classes (the
 * compile-time constants javap -constants shows), read with java.lang.classfile — no class is
 * initialised, so nothing of the game needs to run.
 */
import java.lang.classfile.*;
import java.lang.classfile.attribute.ConstantValueAttribute;
import java.lang.classfile.constantpool.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public class GenConstants {
    static final String[] CLASSES = {
        "net.minecraft.world.entity.player.Inventory",
        "net.minecraft.world.inventory.InventoryMenu",
        "net.minecraft.world.inventory.AbstractContainerMenu",
        "net.minecraft.core.SectionPos",
        "net.minecraft.world.level.chunk.LevelChunkSection",
        "net.minecraft.world.level.Level",
        "net.minecraft.world.entity.player.Player",
        "net.minecraft.world.entity.LivingEntity",
    };

    public static void main(String[] args) throws Exception {
        StringBuilder sb = new StringBuilder("{\n");
        int classes = 0, fields = 0;
        for (String name : CLASSES) {
            ClassModel cm = GenPacketSchema.classModel(name.replace('.', '/'));
            if (cm == null) { System.err.println("GenConstants: no class " + name + " (skipped)"); continue; }
            Map<String, Object> values = new LinkedHashMap<>();
            for (FieldModel f : cm.fields()) {
                if ((f.flags().flagsMask() & 0x0008) == 0 || (f.flags().flagsMask() & 0x0010) == 0) continue;   // static final
                Optional<ConstantValueAttribute> cv = f.findAttribute(Attributes.constantValue());
                if (cv.isEmpty()) continue;
                ConstantValueEntry e = cv.get().constant();
                Object v = switch (e) {
                    case IntegerEntry i -> f.fieldType().stringValue().equals("Z") ? (Object) (i.intValue() != 0) : (Object) i.intValue();
                    case LongEntry l -> l.longValue();
                    case FloatEntry fl -> fl.floatValue();
                    case DoubleEntry d -> d.doubleValue();
                    case StringEntry st -> st.stringValue();
                    default -> null;
                };
                if (v != null) values.put(f.fieldName().stringValue(), v);
            }
            if (classes++ > 0) sb.append(",\n");
            sb.append("  ").append(GenPacketSchema.json(name)).append(": ").append(GenPacketSchema.jsonNode(values));
            fields += values.size();
        }
        sb.append("\n}\n");
        Files.writeString(Path.of("constants.json"), sb.toString(), StandardCharsets.UTF_8);
        System.err.printf("GenConstants: %d constants of %d classes written to constants.json%n", fields, classes);
    }
}
