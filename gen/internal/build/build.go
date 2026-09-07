// Package build assembles the go-mc26 library for one Minecraft version: the
// hand-written sources of gen/src plus the packages generated from a data
// directory, the rendered README and CI workflow, then gofmt, go build, go vet
// and go test inside the result.
package build

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/mj41/mc26/gen/internal/generate"
	"github.com/mj41/mc26/gen/internal/gitx"
	"github.com/mj41/mc26/gen/internal/prims"
)

// Options configures one build.
type Options struct {
	GenRoot    string // the gen/ directory: src/, templates/, hand-crafted/
	DataDir    string // extracted JSON of one version (an mc26-data branch checkout works)
	OutDir     string // the library tree to produce; a .git inside is kept
	Version    string // expected version id; "" accepts whatever the data says
	DataSource string // recorded in version.go and the README, e.g. "mc26-data v0.262.0"
	Generator  string // recorded likewise, e.g. "mc26 1a2b3c4d5e6f"
	Test       bool   // run go test ./... in the result
	Log        func(format string, args ...any)
}

// Info is the version.json of the data the library was built from.
type Info struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	WorldVersion    int    `json:"world_version"`
	ProtocolVersion int    `json:"protocol_version"`
	JavaVersion     int    `json:"java_version"`
	Stable          bool   `json:"stable"`
}

// ReadVersion reads version.json of a data directory.
func ReadVersion(dataDir string) (*Info, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "version.json"))
	if err != nil {
		return nil, fmt.Errorf("not a data directory: %w", err)
	}
	var v Info
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// ReadVersionGo returns the Minecraft version a built library tree records in
// data/version/version.go.
func ReadVersionGo(libDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(libDir, "data", "version", "version.go"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "Name = "); ok {
			return strings.Trim(name, "\""), nil
		}
	}
	return "", fmt.Errorf("no Name in %s/data/version/version.go", libDir)
}

// Run builds the library and returns the version it was built for.
func Run(o Options) (*Info, error) {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	v, err := ReadVersion(o.DataDir)
	if err != nil {
		return nil, err
	}
	if o.Version != "" && v.ID != o.Version {
		return nil, fmt.Errorf("data in %s is %s, not %s", o.DataDir, v.ID, o.Version)
	}
	src := filepath.Join(o.GenRoot, "src")
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return nil, fmt.Errorf("no library sources in %s (run import-src first)", src)
	}

	o.Log("build %s: sources", v.ID)
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return nil, err
	}
	if err := gitx.ReplaceTree(o.OutDir, src); err != nil {
		return nil, fmt.Errorf("copying sources: %w", err)
	}
	// Files a version needs different from the newest one (a packet that
	// gained a field, …) live under src/_versions/<version>/ with the same
	// relative paths and replace the common sources; the directory itself
	// (ignored by the go tool because of the underscore) is not shipped.
	if err := os.RemoveAll(filepath.Join(o.OutDir, "_versions")); err != nil {
		return nil, err
	}
	overlay := filepath.Join(src, "_versions", v.ID)
	if fi, err := os.Stat(overlay); err == nil && fi.IsDir() {
		if err := gitx.CopyTree(overlay, o.OutDir, nil); err != nil {
			return nil, fmt.Errorf("applying %s: %w", overlay, err)
		}
		o.Log("build %s: overlay %s applied", v.ID, overlay)
	}

	// Every primitive the schema leaves as a name has to have a definition, or
	// the JSON describes the protocol only to a reader that already owns a Go
	// library. A version that introduces one nobody has defined stops here.
	primsFile := filepath.Join(o.GenRoot, "hand-crafted", "prims.json")
	defs, err := prims.Load(primsFile)
	if err != nil {
		return nil, err
	}
	used, err := prims.Used(filepath.Join(o.DataDir, "packet_schema.json"))
	if err != nil {
		return nil, err
	}
	if r := prims.Check(defs, used); !r.OK() {
		return nil, fmt.Errorf("primitives: %w", r.Err())
	}
	nodes, err := prims.LoadNodes(filepath.Join(o.GenRoot, "hand-crafted", "nodes.json"))
	if err != nil {
		return nil, err
	}
	kinds, err := prims.Kinds(filepath.Join(o.DataDir, "packet_schema.json"))
	if err != nil {
		return nil, err
	}
	if missing := prims.CheckKinds(nodes, kinds); len(missing) > 0 {
		return nil, fmt.Errorf("node kinds used but not defined in hand-crafted/nodes.json: %s", strings.Join(missing, ", "))
	}
	o.Log("build %s: %d primitives and %d node kinds, all defined", v.ID, len(used), len(kinds))

	o.Log("build %s: generate", v.ID)
	generate.Build = generate.BuildInfo{DataSource: o.DataSource, Generator: o.Generator}
	if err := generate.Run(generate.Config{
		JSONDir: o.DataDir, OutRoot: o.OutDir, AssetsDir: o.GenRoot, Log: o.Log,
	}); err != nil {
		return nil, err
	}

	o.Log("build %s: README and workflow", v.ID)
	if err := renderFile(filepath.Join(o.GenRoot, "templates", "README.md.tmpl"), filepath.Join(o.OutDir, "README.md"), map[string]any{
		"V": v, "DataSource": o.DataSource, "Generator": o.Generator, "Module": generate.Module,
	}); err != nil {
		return nil, err
	}
	if err := copyFile(filepath.Join(o.GenRoot, "templates", "lib-ci.yml"), filepath.Join(o.OutDir, ".github", "workflows", "ci.yml")); err != nil {
		return nil, err
	}

	o.Log("build %s: go mod tidy, gofmt, build, vet%s", v.ID, map[bool]string{true: ", test", false: ""}[o.Test])
	if err := goRun(o.OutDir, "mod", "tidy"); err != nil {
		return nil, err
	}
	// Generated files are formatted here rather than by every generator.
	if out, err := exec.Command("gofmt", "-w", o.OutDir).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("gofmt: %v: %s", err, out)
	}
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		if err := goRun(o.OutDir, args...); err != nil {
			return nil, err
		}
	}
	if o.Test {
		if err := goRun(o.OutDir, "test", "./..."); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// goRun runs the go command in dir and returns its output as the error text.
func goRun(dir string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func renderFile(tmplPath, out string, data any) error {
	t, err := template.ParseFiles(tmplPath)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("%s: %w", tmplPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
