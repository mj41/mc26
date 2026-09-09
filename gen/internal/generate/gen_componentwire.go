// gen_componentwire generates level/component/wire_gen.go: the Go types of the
// primitives whose definition the schema carries and whose value is a data
// component: an item stack, a component patch, one typed component, and the
// delimited forms of the last two.
//
// Every one of them used to be written by hand from Mojang's bytecode. The
// extractor reads their definitions out of the jar now (packet_schema.json's
// "prims" section), and goType renders a primitive that has no Go type from its
// definition, so what is left here is naming them and saying which package they
// belong to: level/component, because the value of a typed component is the
// DataComponent interface generated there.
package generate

import (
	"fmt"
	"path/filepath"

	"github.com/mj41/mc26/gen/internal/prims"
)

// componentWire are the primitives rendered into level/component, with the Go
// name each takes. The names are the ones the library had while these types
// were hand-written, so nothing that uses them has to change.
var componentWire = []struct{ prim, goName string }{
	{"TYPED_DATA_COMPONENT", "Typed"},
	{"COMPONENT_PATCH", "Patch"},
	{"DELIMITED_COMPONENT_PATCH", "DelimitedPatch"},
	{"OPTIONAL_ITEM_STACK", "SlotData"},
	{"UNTRUSTED_ITEM_STACK", "UntrustedSlotData"},
}

func genComponentWire(jsonDir, goMCRoot string) error {
	defs, err := prims.Load(filepath.Join(jsonDir, "packet_schema.json"))
	if err != nil {
		return fmt.Errorf("genComponentWire: %w", err)
	}
	if primDefs == nil {
		primDefs = map[string]map[string]any{}
		for name, d := range defs {
			primDefs[name] = d.Def
		}
	}
	var regs registriesJSON
	if err := readJSON(filepath.Join(jsonDir, "registries.json"), &regs); err != nil {
		return fmt.Errorf("genComponentWire: %w", err)
	}

	// The component package's own view: the primitives rendered here have no Go
	// type, so they resolve to their definitions; the rest keep theirs.
	prims := map[string]string{}
	for k, v := range primTypes {
		prims[k] = v
	}
	names := map[string]string{}
	for _, c := range componentWire {
		delete(prims, c.prim)
		names[c.prim] = c.goName
	}
	gs := &genState{
		enums: map[string]*pktEnumDef{}, structs: map[string]*structDef{}, unions: map[string]*unionDef{},
		whiles: map[string]*whileListDef{}, bits: map[string]*bitsDef{}, factories: map[string]*factoryDef{},
		fixedBits: map[int]bool{}, fixedBytes: map[int]bool{},
		pkg: "component", q: "", wire: "wire.", prims: prims, primNames: names,
		hand: map[string]string{}, widthFns: map[string]*widthFn{}, registryIDs: regs,
	}
	for _, c := range componentWire {
		def, ok := primDefs[c.prim]
		if !ok {
			logf("genComponentWire: %s is not a primitive of this version, skipped", c.prim)
			continue
		}
		gs.primRename = c.goName
		if _, _, err := gs.goType(schemaNode(def), c.prim); err != nil {
			return fmt.Errorf("genComponentWire: %s: %w", c.prim, err)
		}
		gs.primRename = ""
	}
	out := gs.renderStructs()
	if err := writeGo(filepath.Join(goMCRoot, "level", "component", "wire_gen.go"), out); err != nil {
		return fmt.Errorf("genComponentWire: %w", err)
	}
	logf("genComponentWire: wrote level/component/wire_gen.go (%d types)", len(gs.order)+len(gs.factoryOrder))
	return nil
}
