// gen_chunkstatus generates level/chunkstatus_gen.go from the
// minecraft:chunk_status registry: a chunk's generation stage, as it is
// written into a saved chunk's `Status` tag and read back.
package generate

import (
	"fmt"
	"path/filepath"
	"strings"
)

func genChunkStatus(jsonDir, goMCRoot string) error {
	var registries registriesJSON
	if err := readJSON(filepath.Join(jsonDir, "registries.json"), &registries); err != nil {
		return fmt.Errorf("genChunkStatus: %w", err)
	}
	reg, ok := registries["minecraft:chunk_status"]
	if !ok {
		return fmt.Errorf("genChunkStatus: no minecraft:chunk_status in registries.json")
	}
	// registry order is generation order: empty first, full last
	names := make([]string, len(reg.Entries))
	for name, v := range reg.Entries {
		if v.ProtocolID < 0 || v.ProtocolID >= len(names) {
			return fmt.Errorf("genChunkStatus: %s has id %d of %d", name, v.ProtocolID, len(names))
		}
		names[v.ProtocolID] = stripMinecraftPrefix(name)
	}

	var b strings.Builder
	b.WriteString(generatedHeader("gen_chunkstatus.go", "registries.json"))
	b.WriteString(`
package level

// ChunkStatus is how far a chunk's generation has come: the value of a saved
// chunk's ` + "`Status`" + ` tag, and an entry of the minecraft:chunk_status registry.
// The constants are in generation order, StatusEmpty first and StatusFull last;
// a version adds, removes and renames them, so compare with these rather than
// with a string literal.
type ChunkStatus string

const (
`)
	for _, n := range names {
		fmt.Fprintf(&b, "\tStatus%s ChunkStatus = %q\n", snakeToCamel(n), n)
	}
	b.WriteString(`)

// ChunkStatuses lists every status of this version in generation order.
var ChunkStatuses = []ChunkStatus{
`)
	for _, n := range names {
		fmt.Fprintf(&b, "\tStatus%s,\n", snakeToCamel(n))
	}
	b.WriteString("}\n")

	outPath := filepath.Join(goMCRoot, "level", "chunkstatus_gen.go")
	if err := writeFile(outPath, []byte(b.String())); err != nil {
		return fmt.Errorf("genChunkStatus: %w", err)
	}
	logf("genChunkStatus: wrote %s (%d statuses)", outPath, len(names))
	return nil
}
