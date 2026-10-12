// Command mc26lib builds the library from a version's data and nothing else: the generator and its checks (build.Run
// with no kit, no tests), without the harness that the mc26 command links. It is what a delivery's library container
// runs, from a copy of mc26 holding only the files that decide the library (the private stack's delivery build): a generator that read a file outside the copy would fail to build, not serve a stale library.
//
//	mc26lib --data DIR --out DIR --version V [--root DIR] [--generator TEXT] [--source TEXT]
//
// It imports internal/build and what that needs, and no package of the harness (internal/e2e, smoke, capture,
// report, extract, fixtures: TestNoHarness fails otherwise).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mj41/mc26/gen/internal/build"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mc26lib:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("mc26lib", flag.ContinueOnError)
	data := fs.String("data", "", "the version's extracted data directory (its version.json)")
	out := fs.String("out", "", "the library tree to produce")
	version := fs.String("version", "", "the version the data must be")
	root := fs.String("root", ".", "the mc26 tree: gen/ is read from it")
	generator := fs.String("generator", "mc26lib", "what the library records as its generator (version.go, the README)")
	source := fs.String("source", "", "what the library records as its data source (default: the data's path)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" || *out == "" || *version == "" {
		return fmt.Errorf("--data, --out and --version are required")
	}
	gen, err := filepath.Abs(filepath.Join(*root, "gen"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(gen, "src", "go.mod")); err != nil {
		return fmt.Errorf("no %s: --root names the mc26 tree", filepath.Join(gen, "src"))
	}
	src := *source
	if src == "" {
		src = "data " + *data
	}
	v, err := build.Run(build.Options{
		GenRoot: gen, DataDir: *data, OutDir: *out, Version: *version,
		DataSource: src, Generator: *generator,
		Log: func(f string, a ...any) { fmt.Printf(f+"\n", a...) },
	})
	if err != nil {
		return err
	}
	fmt.Printf("built %s -> %s\n", v.ID, *out)
	return nil
}
