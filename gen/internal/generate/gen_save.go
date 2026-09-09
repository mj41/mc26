// gen_save generates the Go types of the save formats from save_schema.json:
// the shapes GenSaveSchema read out of the readers Mojang writes by hand,
// key by key, because the save formats have no codec.
//
// It uses the same renderer as the registry elements (gen_nbt.go): the schema
// vocabulary is the same, and so is what a Go type for it looks like — a struct
// whose fields carry `nbt:"<key>"` tags, with a pointer or omitempty where the
// key may be absent.
package generate

import (
	"fmt"
	"path/filepath"
	"strings"
)

type saveSchemaFile struct {
	Version int                 `json:"version"`
	Formats map[string]nbtEntry `json:"formats"`
}

func genSave(jsonDir, outRoot string) error {
	var schema saveSchemaFile
	if err := readJSON(filepath.Join(jsonDir, "save_schema.json"), &schema); err != nil {
		return fmt.Errorf("genSave: %w (re-run extraction; the data must include GenSaveSchema's output)", err)
	}
	g := &nbtGen{pkgs: map[string]*nbtPkg{"save": {name: "save", taken: map[string]*nbtType{}}}, byJava: map[string]string{}, byName: map[string]string{}}
	// The codec-built records of a world save are in nbt_schema.json (the
	// world options, the dimensions, the respawn data, the data-pack lists):
	// they belong to the same package, so they are rendered with the formats.
	var nbtSchema nbtSchemaFile
	if err := readJSON(filepath.Join(jsonDir, "nbt_schema.json"), &nbtSchema); err != nil {
		return fmt.Errorf("genSave: %w", err)
	}
	records := map[string]nbtEntry{}
	for key, e := range nbtSchema.Types {
		if strings.HasPrefix(e.Class, "net.minecraft.world.") && e.Type["k"] != "recursive" {
			records[key] = e
		}
	}
	var roots []map[string]any
	for _, key := range sortedKeys(schema.Formats) {
		roots = append(roots, schema.Formats[key].Type)
	}
	for _, key := range sortedKeys(records) {
		roots = append(roots, records[key].Type)
	}
	g.findGenericsIn(roots)
	var holes []string
	for _, key := range sortedKeys(schema.Formats) {
		e := schema.Formats[key]
		if _, _, err := g.typeOf(e.Type, "save", "the saved "+key); err != nil {
			return fmt.Errorf("genSave: %s: %w", key, err)
		}
		holes = append(holes, holePaths(e.Type, key)...)
	}
	for _, key := range sortedKeys(records) {
		e := records[key]
		if _, _, err := g.typeOf(e.Type, "save", "a record of level.dat, "+shortJava(e.Class)+"."+e.Field); err != nil {
			return fmt.Errorf("genSave: %s: %w", key, err)
		}
		holes = append(holes, holePaths(e.Type, key)...)
	}
	doc := "// The save formats of this Minecraft version, as they are on disk: every key one of\n" +
		"// Mojang's readers takes off the tag, with the tag type that reader implies. They have no\n" +
		"// codec, so this is read from the readers themselves (data-gen/java/GenSaveSchema.java).\n"
	if len(holes) > 0 {
		doc += "//\n// Not described, kept as raw NBT: " + joinLimit(holes, 6) + ".\n"
	}
	out := g.renderFrom(g.pkgs["save"], doc, "gen_save.go", "save_schema.json")
	if err := writeGo(filepath.Join(outRoot, "save", "save_gen.go"), out); err != nil {
		return fmt.Errorf("genSave: %w", err)
	}
	logf("genSave: wrote save/save_gen.go (%d formats, %d level.dat records, %d types)", len(schema.Formats), len(records), len(g.pkgs["save"].types))
	return nil
}

// joinLimit lists at most n of xs, saying how many were left out.
func joinLimit(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(" and %d more", len(xs)-n)
}
