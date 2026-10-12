package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mj41/mc26/gen/internal/generate"
)

func TestTagBase(t *testing.T) {
	for in, want := range map[string]string{"26.2": "v0.262.", "26.3-pre-2": "v0.263.0-pre2.", "26.3-rc-1": "v0.263.0-rc1."} {
		got, err := tagBase(in)
		if err != nil || got != want {
			t.Errorf("tagBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// A release stops before any push while go-mc26-lang lacks a language as this extraction has it, and says
// what to run; with the file there it goes on. English is never looked for.
func TestLangPublished(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, "lang"), 0o755)
	for l, json := range map[string]string{"en_us": `{"a":"A"}`, "cs_cz": `{"a":"Á"}`} {
		if err := os.WriteFile(filepath.Join(dataDir, "lang", l+".json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := generate.LangFiles(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cs := "/" + strings.TrimPrefix(generate.LangSum(files["cs_cz"]), "sha256:") + ".lang"
	published := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !published || r.URL.Path != cs {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err = langPublished(srv.URL, "26.3", dataDir)
	if err == nil || !strings.Contains(err.Error(), "cs_cz") || !strings.Contains(err.Error(), "mc26 lang --version 26.3") {
		t.Fatalf("cs_cz not published: %v", err)
	}
	published = true
	if err := langPublished(srv.URL, "26.3", dataDir); err != nil {
		t.Fatalf("cs_cz published: %v", err)
	}
}
