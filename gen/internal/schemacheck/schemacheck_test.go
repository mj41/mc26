package schemacheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComplete(t *testing.T) {
	p := write(t, t.TempDir(), "s.json", `{"packets": {
	  "a": {"coverage": "full", "type": {"k": "struct", "name": "A", "fields": [
	    {"name": "x", "type": {"k": "prim", "t": "INT"}},
	    {"name": "again", "type": {"k": "ref", "name": "A", "of": "struct"}},
	    {"name": "d", "type": {"k": "dispatch", "name": "D", "key": "type", "cases": [
	      {"k": "case", "id": "minecraft:one", "type": {"k": "ref", "name": "D", "of": "dispatch"}}]}}]}}}}`)
	r, err := CheckFile(p, "packets")
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || r.Entries != 1 || r.Refs != 2 {
		t.Fatalf("complete schema reported %+v", r)
	}
}

func TestHoles(t *testing.T) {
	p := write(t, t.TempDir(), "s.json", `{"registries": {
	  "b": {"coverage": "partial", "type": {"k": "struct", "name": "B", "fields": [
	    {"name": "o", "type": {"k": "opaque", "java": "X.codec"}},
	    {"name": "d", "type": {"k": "dispatch", "name": "D", "key": "type"}},
	    {"name": "r", "type": {"k": "ref", "name": "Elsewhere", "of": "struct"}},
	    {"name": "q", "type": {"k": "ref", "name": "B"}},
	    {"name": "c", "type": {"k": "dispatch", "name": "E", "key": "type", "cases": [{"k": "case", "id": "?", "type": {"k": "unit"}}]}}]}}}}`)
	r, err := CheckFile(p, "registries")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`coverage "partial"`, "opaque node (X.codec)", `dispatch D on "type" without cases`,
		`ref to struct "Elsewhere" with no enclosing`, `ref "B" says not what kind`, "a case without an id"}
	if len(r.Problems) != len(want) {
		t.Fatalf("got %d problems, want %d:\n%s", len(r.Problems), len(want), strings.Join(r.Problems, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(r.Problems[i], w) {
			t.Errorf("problem %d = %q, want it to mention %q", i, r.Problems[i], w)
		}
	}
	if err := r.Err(); err == nil || !strings.HasPrefix(err.Error(), "6 holes") {
		t.Errorf("Err() = %v", err)
	}
}

func TestMissingSection(t *testing.T) {
	p := write(t, t.TempDir(), "s.json", `{"packets": {}}`)
	r, err := CheckFile(p, "packets", "components")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], `no "components" section`) {
		t.Errorf("got %v", r.Problems)
	}
}
