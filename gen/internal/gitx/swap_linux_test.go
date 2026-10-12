//go:build linux

package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// The kernel's exchange itself works here (not only the fallback of two renames).
func TestExchange(t *testing.T) {
	d := t.TempDir()
	a, b := filepath.Join(d, "a"), filepath.Join(d, "b")
	put(t, a, "v", "A")
	put(t, b, "v", "B")
	if err := exchange(a, b); err != nil {
		t.Skipf("no renameat2 exchange here: %v", err)
	}
	if read(t, a, "v") != "B" || read(t, b, "v") != "A" {
		t.Errorf("a %q, b %q", read(t, a, "v"), read(t, b, "v"))
	}
}

// A reader of the final directory, while it is swapped again and again, always finds a whole tree.
func TestSwapNeverShowsAGap(t *testing.T) {
	d := t.TempDir()
	final, stage := filepath.Join(d, "lib"), filepath.Join(d, "lib.new")
	put(t, final, "v", "old")
	put(t, stage, "v", "new")
	done := make(chan struct{})
	gaps := make(chan string, 1)
	go func() {
		defer close(gaps)
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := os.ReadFile(filepath.Join(final, "v")); err != nil {
				gaps <- err.Error()
				return
			}
		}
	}()
	for i := 0; i < 3000; i++ {
		if err := SwapDir(final, stage); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	if g := <-gaps; g != "" {
		t.Errorf("a reader found %s", g)
	}
}
