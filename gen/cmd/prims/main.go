// prims reports whether every primitive a version's schema uses has a
// definition in the schema's own "prims" section, and whether the definitions
// form a set a reader can resolve. `mc26 build` runs the same check and fails
// on it; this command is for looking.
//
//	go run ./gen/cmd/prims 26.2
//	go run ./gen/cmd/prims -show ITEM_STACK 26.2
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mj41/mc26/gen/internal/paths"
	"github.com/mj41/mc26/gen/internal/prims"
)

func main() {
	show := flag.String("show", "", "print one primitive's definition and stop")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: prims [-show TOKEN] <version|dir>")
		os.Exit(2)
	}
	dir := paths.Data(flag.Arg(0))
	schema := filepath.Join(dir, "packet_schema.json")
	path := schema
	defs, err := prims.Load(path)
	if err != nil {
		fail("%v", err)
	}
	if *show != "" {
		d, ok := defs[*show]
		if !ok {
			fail("%s has no definition in %s", *show, path)
		}
		b, _ := json.MarshalIndent(d, "", " ")
		fmt.Printf("%s\n%s\n", *show, b)
		return
	}

	nodes, err := prims.LoadNodes(filepath.Join(paths.MustRoot(), "gen", "hand-crafted", "nodes.json"))
	if err != nil {
		fail("%v", err)
	}
	used, err := prims.Used(schema)
	if err != nil {
		fail("%v", err)
	}
	kinds, err := prims.Kinds(schema)
	if err != nil {
		fail("%v", err)
	}
	r := prims.Check(defs, used)
	undefinedKinds := prims.CheckKinds(nodes, kinds)
	fmt.Printf("%s: %d primitives used of %d defined, %d node kinds used of %d defined\n",
		filepath.Base(dir), len(r.Used), len(defs), len(kinds), len(nodes))
	report := func(what string, list []string) {
		if len(list) == 0 {
			return
		}
		fmt.Printf("\n%s:\n", what)
		for _, s := range list {
			fmt.Println("  " + s)
		}
	}
	report("node kinds used but not defined", undefinedKinds)
	report("used but not defined", r.Undefined)
	report("defined but referring to something undefined", r.Dangling)
	report("circular, which no reader can resolve", r.Cycles)
	report("defined but not used by this version", r.Unused)
	if !r.OK() || len(undefinedKinds) > 0 {
		os.Exit(1)
	}
	fmt.Println("\nevery primitive and every node kind of this version is defined")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "prims: "+format+"\n", args...)
	os.Exit(1)
}
