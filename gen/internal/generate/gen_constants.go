// gen_constants generates data/constants/constants_gen.go from constants.json
// (GenConstants: the compile-time constants of a few Minecraft classes — the
// inventory slot layout, section geometry, level limits, the NBT keys of living
// entities), one Go constant per field named by class and field
// (InventoryMenuShieldSlot, LivingEntityTagHealth).
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func genConstants(jsonDir, outRoot string) error {
	var classes map[string]map[string]any
	if err := readJSON(filepath.Join(jsonDir, "constants.json"), &classes); err != nil {
		return fmt.Errorf("genConstants: %w (re-run extraction; the data must include GenConstants's output)", err)
	}
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_constants.go", "constants.json"))
	sb.WriteString("\n// Package constants holds compile-time constants of Minecraft " + jsonVersion + "'s classes that\n")
	sb.WriteString("// the library's hand-written code follows: the inventory slot layout, the section\n")
	sb.WriteString("// geometry, the level limits, the NBT keys of living entities.\n")
	sb.WriteString("package constants\n\n")
	total := 0
	for _, class := range sortedKeys(classes) {
		short := class[strings.LastIndex(class, ".")+1:]
		fmt.Fprintf(&sb, "// %s (%s)\nconst (\n", short, class)
		fields := classes[class]
		names := sortedKeys(fields)
		sort.SliceStable(names, func(i, j int) bool { return names[i] < names[j] })
		for _, name := range names {
			v := fields[name]
			var lit string
			switch x := v.(type) {
			case string:
				lit = fmt.Sprintf("%q", x)
			case bool:
				lit = fmt.Sprintf("%v", x)
			case float64:
				if x == float64(int64(x)) {
					lit = fmt.Sprintf("%d", int64(x))
				} else {
					lit = fmt.Sprintf("%g", x)
				}
			default:
				lit = fmt.Sprintf("%v", x)
			}
			fmt.Fprintf(&sb, "\t%s%s = %s\n", goTypeName(short), goEnumConst(name), lit)
			total++
		}
		sb.WriteString(")\n\n")
	}
	if err := writeGo(filepath.Join(outRoot, "data", "constants", "constants_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genConstants: %w", err)
	}
	logf("genConstants: %d constants of %d classes", total, len(classes))
	return nil
}
