package kit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKit is a kit checkout of one package that does not compile, and a library tree beside it.
func fakeKit(t *testing.T) (src, lib string) {
	t.Helper()
	dir := t.TempDir()
	src, lib = filepath.Join(dir, "kit-src"), filepath.Join(dir, "lib")
	for path, body := range map[string]string{
		filepath.Join(src, "go.mod"):        "module github.com/mj41/go-mc26-kit\n\ngo 1.25\n",
		filepath.Join(src, "bot", "bot.go"): "package bot\n\nfunc F() int { return \"not an int\" }\n",
		filepath.Join(lib, "go.mod"):        "module github.com/mj41/go-mc26\n\ngo 1.25\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src, lib
}

// The kit is assembled without being built or vetted when asked (NoCheck: the delivery's check container
// does that, against a Go cache that outlives the build); the default still refuses a kit that does not build.
func TestAssembleNoCheck(t *testing.T) {
	src, lib := fakeKit(t)
	out := filepath.Join(t.TempDir(), "kit")
	err := Assemble(Options{SrcDir: src, LibDir: lib, OutDir: out})
	if err == nil || !strings.Contains(err.Error(), "go build") {
		t.Fatalf("a kit that does not compile was assembled by default: %v", err)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Errorf("a failed assembly left %s", out)
	}
	var log []string
	if err := Assemble(Options{SrcDir: src, LibDir: lib, OutDir: out, NoCheck: true, Log: func(f string, a ...any) { log = append(log, f) }}); err != nil {
		t.Fatalf("NoCheck: %v", err)
	}
	for _, f := range []string{"go.work", filepath.Join("bot", "bot.go")} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("the assembled kit lacks %s: %v", f, err)
		}
	}
	w, _ := os.ReadFile(WorkFile(out))
	if !strings.Contains(string(w), "replace "+LibModule+" => ") || !strings.Contains(string(w), lib) {
		t.Errorf("the workspace does not name the library: %s", w)
	}
	if !strings.Contains(strings.Join(log, "\n"), "not built or vetted") {
		t.Errorf("the log does not say the check was skipped: %v", log)
	}
}
