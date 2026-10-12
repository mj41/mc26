package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// harness packages: what the library's command must not link — the harness's init code would run in the
// step (and the delivery's key would have to hold all of it).
var harness = []string{"internal/e2e", "internal/smoke", "internal/capture", "internal/report", "internal/extract", "internal/fixtures"}

func TestNoHarness(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		for _, h := range harness {
			if strings.HasSuffix(pkg, "/gen/"+h) || strings.Contains(pkg, "/gen/"+h+"/") {
				t.Errorf("mc26lib links %s (the harness stays out of the library's key)", pkg)
			}
		}
	}
}

func TestRequiresItsFlags(t *testing.T) {
	if err := run([]string{"--out", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("no --data: %v", err)
	}
	if err := run([]string{"--data", t.TempDir(), "--out", t.TempDir(), "--version", "26.3", "--root", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "--root") {
		t.Errorf("a root with no gen/src: %v", err)
	}
}

// With the version's data at hand (temp/data/<v>, not in a clean checkout): the library is built, records the
// generator it is told (the library's key, in a delivery), and is complete.
func TestBuildsALibrary(t *testing.T) {
	if testing.Short() || os.Getenv("MC26LIB_BUILD") == "" {
		t.Skip("long (40 s): MC26LIB_BUILD=1 builds the library from temp/data/26.3")
	}
	root, _ := filepath.Abs("../../..")
	data := filepath.Join(root, "temp", "data", "26.3")
	if _, err := os.Stat(filepath.Join(data, "version.json")); err != nil {
		t.Skip("no temp/data/26.3")
	}
	out := filepath.Join(t.TempDir(), "lib")
	if err := run([]string{"--root", root, "--data", data, "--out", out, "--version", "26.3", "--generator", "mc26lib key-abc"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "data", "version", "version.go"))
	if err != nil || !strings.Contains(string(b), `Generator = "mc26lib key-abc"`) {
		t.Errorf("version.go: %v %.300s", err, b)
	}
	if _, err := os.Stat(filepath.Join(out, "go.mod")); err != nil {
		t.Error(err)
	}
}
