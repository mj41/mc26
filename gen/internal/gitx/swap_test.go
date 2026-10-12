package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The stage takes the place of the final directory in one step; the old tree is left at the stage's path.
func TestSwapDir(t *testing.T) {
	d := t.TempDir()
	final, stage := filepath.Join(d, "lib"), filepath.Join(d, "lib.new")
	put(t, final, "v", "old")
	put(t, stage, "v", "new")
	if err := SwapDir(final, stage); err != nil {
		t.Fatal(err)
	}
	if read(t, final, "v") != "new" || read(t, stage, "v") != "old" {
		t.Errorf("after the swap: final %q, stage %q", read(t, final, "v"), read(t, stage, "v"))
	}
}

// With no final yet it is a rename.
func TestSwapDirNoFinal(t *testing.T) {
	d := t.TempDir()
	final, stage := filepath.Join(d, "lib"), filepath.Join(d, "lib.new")
	put(t, stage, "v", "new")
	if err := SwapDir(final, stage); err != nil {
		t.Fatal(err)
	}
	if read(t, final, "v") != "new" {
		t.Errorf("final %q", read(t, final, "v"))
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("the stage is still there: %v", err)
	}
}
