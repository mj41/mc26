package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorldFrom(t *testing.T) {
	for _, c := range []struct{ env, version, want string }{
		{"", "26.3", worldsFrom},
		{"", "26.1", "26.1"},
		{"", "1.21.11", ""}, // older than the world: its own
		{"own", "26.3", ""},
		{"26.2", "26.3", "26.2"},
		{"26.2", "26.1", ""},
	} {
		t.Setenv("MC26_WORLD_FROM", c.env)
		if got := worldFrom(c.version); got != c.want {
			t.Errorf("MC26_WORLD_FROM=%q, %s: %q, want %q", c.env, c.version, got, c.want)
		}
	}
}

func TestCopyDir(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "world")
	os.MkdirAll(filepath.Join(src, "region"), 0o755)
	os.WriteFile(filepath.Join(src, "level.dat"), []byte("level"), 0o644)
	os.WriteFile(filepath.Join(src, "region", "r.0.0.mca"), []byte("chunks"), 0o644)
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"level.dat": "level", "region/r.0.0.mca": "chunks"} {
		if b, err := os.ReadFile(filepath.Join(dst, f)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", f, b, err)
		}
	}
}
