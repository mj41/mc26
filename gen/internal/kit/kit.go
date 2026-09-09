// Package kit assembles go-mc26-kit — the bot, the server framework, the
// account flows and the examples, a module of its own on top of the library,
// in a repository of its own that this one consumes but does not own —
// against one built library tree, and checks it there: go build, go vet and
// the unit tests, through a workspace that points the library's import path
// at that tree. The kit's sources are version independent; this is where a
// version proves it. The pipeline needs the kit because its bot is the test
// client of the smoke test, the recorded session and the end-to-end run.
package kit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mj41/mc26/gen/internal/gitx"
	"github.com/mj41/mc26/gen/internal/limits"
)

// LibModule is the library's module path, what the workspace replaces.
const LibModule = "github.com/mj41/go-mc26"

// Options configures one assembly.
type Options struct {
	SrcDir string // a go-mc26-kit checkout: the kit's sources
	LibDir string // a built library tree of one version
	OutDir string // the kit tree to produce (temp/kit/<version>)
	Test   bool   // run go test ./... in the result
	Log    func(format string, args ...any)
}

// WorkFile is the workspace file of an assembled kit tree.
func WorkFile(kitDir string) string { return filepath.Join(kitDir, "go.work") }

// Env is the environment for a go command run inside an assembled kit tree:
// the workspace selected, the memory limits applied.
func Env(kitDir string, extra ...string) []string {
	return limits.GoEnv(append([]string{"GOWORK=" + WorkFile(kitDir)}, extra...)...)
}

// Assemble copies the sources, writes the workspace and checks the tree.
func Assemble(o Options) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if _, err := os.Stat(filepath.Join(o.SrcDir, "go.mod")); err != nil {
		return fmt.Errorf("no go-mc26-kit checkout in %s (clone github.com/mj41/go-mc26-kit there, or --kit-src DIR)", o.SrcDir)
	}
	lib, err := filepath.Abs(o.LibDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return err
	}
	if err := gitx.ReplaceTree(o.OutDir, o.SrcDir); err != nil {
		return fmt.Errorf("copying the kit sources: %w", err)
	}
	// `use` of the library tree would make the go command fetch the tag the
	// kit's go.mod requires, which is not published while this runs; a
	// replace to the directory does not.
	work := fmt.Sprintf("go 1.25\n\nuse .\n\nreplace %s => %s\n", LibModule, lib)
	if err := os.WriteFile(WorkFile(o.OutDir), []byte(work), 0o644); err != nil {
		return err
	}
	steps := [][]string{{"build", "./..."}, {"vet", "./..."}}
	if o.Test {
		steps = append(steps, []string{"test", "./..."})
	}
	o.Log("kit: build, vet%s against %s", map[bool]string{true: ", test", false: ""}[o.Test], lib)
	for _, args := range steps {
		cmd := exec.Command("go", args...)
		cmd.Dir = o.OutDir
		cmd.Env = Env(o.OutDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("kit: go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}
