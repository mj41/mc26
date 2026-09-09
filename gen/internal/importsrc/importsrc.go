// Package importsrc copies the hand-written parts of a go-mc working tree
// into gen/src (the library sources), rewriting the module path. It was the
// one-time bridge from the fork; gen/src is maintained by hand (the kit in its own repository).
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
