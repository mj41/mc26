// Package importsrc copies the hand-written parts of a go-mc working tree
// into gen/src (the library sources) or an examples checkout, rewriting the
// module path. It is the one-time bridge from the fork; afterwards gen/src is
// maintained by hand.
package importsrc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const oldModule = "github.com/Tnze/go-mc"

// Options for ImportSrc.
type Options struct {
	From        string // a go-mc working tree
	To          string // gen/src
	Module      string // the library module path
	UpstreamRef string // the upstream commit the fork started from, for COPIED
	Owner       string // copyright holder added to LICENSE
	Log         func(format string, args ...any)
}

// skipTop are top-level entries of the fork that do not belong to the library.
var skipTop = map[string]bool{
	".git": true, ".github": true, ".gitignore": true, ".idea": true, ".vscode": true,
	"temp": true, "bin": true, "cmd": true, "tools": true, "examples": true, "realms": true,
	"docs": true, "README.md": true, "go.work": true, "go.work.sum": true, "wip": true,
}

// isGenerated reports files the generators produce (skipped: build recreates them).
func isGenerated(rel string, data []byte) bool {
	switch {
	case strings.HasPrefix(rel, "data/lang/"):
		return true
	case rel == "level/block/block_states.nbt":
		return true
	case strings.HasPrefix(rel, "data/packetid/") && strings.HasSuffix(rel, "_string.go"):
		return true
	}
	head := data
	if len(head) > 400 {
		head = head[:400]
	}
	return bytes.Contains(head, []byte("Code generated")) && bytes.Contains(head, []byte("DO NOT EDIT"))
}

// ImportSrc populates o.To.
func ImportSrc(o Options) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if err := os.RemoveAll(o.To); err != nil {
		return err
	}
	var copied []string
	err := filepath.WalkDir(o.From, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(o.From, path)
		if rel == "." {
			return nil
		}
		top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if skipTop[top] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isGenerated(rel, data) {
			return nil
		}
		if strings.HasSuffix(rel, ".go") || rel == "go.mod" {
			data = bytes.ReplaceAll(data, []byte(oldModule), []byte(o.Module))
		}
		if rel == "LICENSE" {
			data = bytes.Replace(data, []byte("Copyright (c) 2019 Tnze"), []byte("Copyright (c) 2019 Tnze\nCopyright (c) 2026 "+o.Owner), 1)
		}
		target := filepath.Join(o.To, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		copied = append(copied, rel)
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return err
	}
	sort.Strings(copied)
	o.Log("import-src: %d files copied to %s", len(copied), o.To)
	return writeCopied(o, copied)
}

// writeCopied records where every file came from: identical to upstream at
// UpstreamRef, modified since, or new in this project.
func writeCopied(o Options, files []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Origin of every file in this tree, one per line: path<TAB>origin.\n")
	fmt.Fprintf(&b, "# upstream = byte-identical to Tnze/go-mc at %s (MIT, Copyright (c) 2019 Tnze),\n", o.UpstreamRef)
	fmt.Fprintf(&b, "# modified = derived from that file, new = written for this project.\n")
	fmt.Fprintf(&b, "# Import paths were rewritten from %s to %s in every case.\n", oldModule, o.Module)
	counts := map[string]int{}
	for _, f := range files {
		origin := "new"
		if exec.Command("git", "-C", o.From, "cat-file", "-e", o.UpstreamRef+":"+f).Run() == nil {
			origin = "modified"
			if exec.Command("git", "-C", o.From, "diff", "--quiet", o.UpstreamRef, "--", f).Run() == nil {
				origin = "upstream"
			}
		}
		counts[origin]++
		fmt.Fprintf(&b, "%s\t%s\n", f, origin)
	}
	o.Log("import-src: upstream %d, modified %d, new %d", counts["upstream"], counts["modified"], counts["new"])
	return os.WriteFile(filepath.Join(o.To, "COPIED"), []byte(b.String()), 0o644)
}

// ExamplesOptions for ImportExamples.
type ExamplesOptions struct {
	From       string // a go-mc working tree
	To         string // the examples checkout (branch already selected)
	Module     string // module path of the examples repo
	LibModule  string
	LibVersion string // required library version, e.g. v0.262.0
	Log        func(format string, args ...any)
}

// ImportExamples copies examples/* into a checkout, rewriting imports and
// writing go.mod. README.md and LICENSE of the checkout are kept.
func ImportExamples(o ExamplesOptions) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	src := filepath.Join(o.From, "examples")
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	// Remove old example directories (keep README, LICENSE, .git, .github).
	old, _ := os.ReadDir(o.To)
	for _, e := range old {
		if e.IsDir() && e.Name() != ".git" && e.Name() != ".github" {
			if err := os.RemoveAll(filepath.Join(o.To, e.Name())); err != nil {
				return err
			}
		}
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "test" {
			continue
		}
		dir := filepath.Join(src, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				return err
			}
			if strings.HasSuffix(f.Name(), ".go") {
				data = bytes.ReplaceAll(data, []byte(oldModule), []byte(o.LibModule))
			}
			target := filepath.Join(o.To, e.Name(), f.Name())
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return err
			}
			n++
		}
	}
	// Direct dependencies of the examples themselves; the library's own are
	// resolved through it. go.sum is completed by `go mod tidy` once the
	// library version is published.
	gomod := fmt.Sprintf("module %s\n\ngo 1.25\n\nrequire (\n\t%s %s\n\tgithub.com/google/uuid v1.3.0\n)\n", o.Module, o.LibModule, o.LibVersion)
	if err := os.WriteFile(filepath.Join(o.To, "go.mod"), []byte(gomod), 0o644); err != nil {
		return err
	}
	o.Log("import-examples: %d files into %s", n, o.To)
	return nil
}
