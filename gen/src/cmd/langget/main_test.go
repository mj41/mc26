package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mj41/go-mc26/data/lang"
)

// blobs is a fake go-mc26-lang blobs host for a test: the files by checksum, the requests counted; any other
// host is refused, so a test never reaches the real one.
type blobs struct {
	files    map[string][]byte // by name, <sha256>.lang
	requests int
}

func (b *blobs) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "blobs.test" {
		return nil, fmt.Errorf("the test reached for %s", r.URL.Host)
	}
	b.requests++
	data, ok := b.files[filepath.Base(r.URL.Path)]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(data))}, nil
}

var (
	czech  = []byte("language.name\x00Čeština\x00multiplayer.player.joined\x00Hráč %s se připojil do hry\x00")
	german = []byte("language.name\x00Deutsch\x00multiplayer.player.joined\x00%s hat das Spiel betreten\x00")
)

func fixture(t *testing.T) (getter, *blobs) {
	t.Helper()
	b := &blobs{files: map[string][]byte{}}
	manifest := map[string]string{"en_us": "sha256:built-in"}
	for l, data := range map[string][]byte{"cs_cz": czech, "de_de": german} {
		manifest[l] = lang.Sum(data)
		b.files[strings.TrimPrefix(lang.Sum(data), "sha256:")+".lang"] = data
	}
	return getter{Dir: t.TempDir(), Pkg: "langs", Base: "http://blobs.test", Manifest: manifest, Version: "26.3",
		Client: &http.Client{Transport: b}, Log: io.Discard}, b
}

func list(t *testing.T, g getter, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.Dir, "mc26-langs"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(g getter, name string) bool {
	_, err := os.Stat(filepath.Join(g.Dir, name))
	return err == nil
}

// The languages listed are fetched by checksum, written and registered (langs_gen.go, valid Go); a shorter
// list removes what it no longer has and does not fetch again what is there; no list leaves English alone.
func TestLanggetList(t *testing.T) {
	g, b := fixture(t)
	list(t, g, "# the languages of the bot\ncs_cz de_de\n")
	if err := g.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(g.Dir, "cs_cz.lang")); !bytes.Equal(got, czech) {
		t.Error("cs_cz.lang is not the file fetched")
	}
	src, err := os.ReadFile(filepath.Join(g.Dir, generated))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), generated, src, 0); err != nil {
		t.Fatalf("langs_gen.go is no Go: %v\n%s", err, src)
	}
	for _, want := range []string{"package langs", `lang.MustRegister("cs_cz", lang_cs_cz)`, `lang.MustRegister("de_de", lang_de_de)`, "//go:embed de_de.lang"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("langs_gen.go lacks %q:\n%s", want, src)
		}
	}

	requests := b.requests
	list(t, g, "cs_cz\n")
	if err := g.run(); err != nil {
		t.Fatal(err)
	}
	if exists(g, "de_de.lang") {
		t.Error("de_de, no longer listed, is still there")
	}
	if b.requests != requests {
		t.Errorf("cs_cz, already there, fetched again (%d requests)", b.requests-requests)
	}

	list(t, g, "")
	if err := g.run(); err != nil {
		t.Fatal(err)
	}
	if exists(g, "cs_cz.lang") {
		t.Error("with no language listed, cs_cz.lang is still there")
	}
	// langs_gen.go stays, registering nothing: go generate fails on a file removed under it
	if src, err := os.ReadFile(filepath.Join(g.Dir, generated)); err != nil || strings.Contains(string(src), "MustRegister") {
		t.Errorf("with no language listed, langs_gen.go: %v\n%s", err, src)
	} else if _, err := parser.ParseFile(token.NewFileSet(), generated, src, 0); err != nil {
		t.Errorf("the empty langs_gen.go is no Go: %v", err)
	}
}

// "all" is every language of the version (en_us is go-mc26's own); MC26_LANGS wins over the file.
func TestLanggetAllAndEnv(t *testing.T) {
	g, _ := fixture(t)
	list(t, g, "all")
	if err := g.run(); err != nil {
		t.Fatal(err)
	}
	if !exists(g, "cs_cz.lang") || !exists(g, "de_de.lang") || exists(g, "en_us.lang") {
		t.Error("all: want cs_cz and de_de, and no en_us")
	}
	t.Setenv("MC26_LANGS", "de_de")
	if err := g.run(); err != nil {
		t.Fatal(err)
	}
	if exists(g, "cs_cz.lang") || !exists(g, "de_de.lang") {
		t.Error("MC26_LANGS=de_de: want de_de alone")
	}
}

// A file that is not the one the manifest has is refused and not written; an unknown locale is an error.
func TestLanggetRefuses(t *testing.T) {
	g, b := fixture(t)
	for name := range b.files {
		b.files[name] = append([]byte("tampered "), b.files[name]...)
	}
	list(t, g, "cs_cz")
	if err := g.run(); err == nil || !strings.Contains(err.Error(), "not the") {
		t.Errorf("a tampered file: %v", err)
	}
	if exists(g, "cs_cz.lang") {
		t.Error("the tampered file was written")
	}
	list(t, g, "xx_yy")
	if err := g.run(); err == nil || !strings.Contains(err.Error(), "no language") {
		t.Errorf("an unknown locale: %v", err)
	}
}

// -from takes the files from a directory (a checkout of the blobs), with no network.
func TestLanggetFrom(t *testing.T) {
	g, b := fixture(t)
	g.From, g.Client = t.TempDir(), nil
	for name, data := range b.files {
		if err := os.WriteFile(filepath.Join(g.From, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list(t, g, "de_de")
	if err := g.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(g.Dir, "de_de.lang")); !bytes.Equal(got, german) {
		t.Error("de_de.lang from the directory is not the file")
	}
}
