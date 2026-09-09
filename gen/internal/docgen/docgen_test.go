package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDirectives(t *testing.T) {
	d := &data{ver: map[string]any{"id": "26.3-pre-3", "protocol_version": float64(1073742159)}}
	tmpl := strings.Join([]string{
		"# Minecraft <!-- mc26 value: id -->",
		"<!-- mc26 version: >= 26.3-pre-1 -->",
		"since pre-1",
		"<!-- mc26 end -->",
		"<!-- mc26 version: 26.1 26.2 -->",
		"old only",
		"<!-- mc26 end -->",
		"<!-- mc26 internal -->",
		"for the repository",
		"<!-- mc26 end -->",
		"protocol <!-- mc26 value: protocol -->",
	}, "\n")
	out, err := render(tmpl, Options{Version: "26.3-pre-3"}, d)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Minecraft 26.3-pre-3\nsince pre-1\nprotocol 1073742159"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
	out, err = render(tmpl, Options{Version: "26.2", Internal: true}, d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "old only") || !strings.Contains(out, "for the repository") || strings.Contains(out, "since pre-1") {
		t.Errorf("26.2 internal rendering: %q", out)
	}
	if _, err := render("<!-- mc26 version: 26.1 -->\nx", Options{Version: "26.1"}, d); err == nil {
		t.Error("an unended block must be an error")
	}
	if _, err := render("<!-- mc26 value: nothing -->", Options{Version: "26.1"}, d); err == nil {
		t.Error("an unknown value must be an error")
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	tmpl := "# P\n\n### list\n\ntext\n\n<!-- mc26 version: 26.1 -->\nold\n<!-- mc26 end -->\n<!-- mc26 include: prims -->\n"
	if err := os.WriteFile(filepath.Join(dir, "protocol.mc26tmpl.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := Check(dir, "", []string{"list"}); len(p) != 0 {
		t.Errorf("a complete template reported %v", p)
	}
	p := Check(dir, "", []string{"list", "map"})
	if len(p) != 1 || !strings.Contains(p[0], `node kind "map"`) {
		t.Errorf("a missing kind: %v", p)
	}
	bad := "<!-- mc26 include: nothing -->\n<!-- mc26 frob -->\n<!-- mc26 end -->\n"
	if err := os.WriteFile(filepath.Join(dir, "other.mc26tmpl.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	p = Check(dir, "", []string{"list"})
	if len(p) != 3 {
		t.Errorf("want 3 problems (unknown include, unknown directive, end without block), got %v", p)
	}
}
