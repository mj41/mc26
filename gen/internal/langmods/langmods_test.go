package langmods

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mj41/mc26/gen/internal/generate"
	"github.com/mj41/mc26/gen/internal/gitx"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeLangs(t *testing.T, dir string, langs map[string]string) {
	t.Helper()
	for l, json := range langs {
		if err := os.WriteFile(filepath.Join(dir, "lang", l+".json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A release puts each language's module on mc-<version>, its file on blobs by checksum, and a tag on each
// language whose file changed since its last tag: all of them the first time, then only the one that changed.
func TestRelease(t *testing.T) {
	data, repo := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "lang"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLangs(t, data, map[string]string{
		"en_us": `{"language.name":"English","chat.type.text":"<%s> %s"}`,
		"cs_cz": `{"language.name":"Čeština","language.region":"Česko","death.attack.arrow":"%1$s byl zastřelen entitou %2$s"}`,
		"de_de": `{"language.name":"Deutsch"}`,
	})
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "test")
	git(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "init")

	o := Options{DataDir: data, Version: "26.3", TagBase: "v0.263.", OutDir: filepath.Join(t.TempDir(), "out"), Generator: "test"}
	release := func() []string {
		t.Helper()
		files, err := Write(o)
		if err != nil {
			t.Fatal(err)
		}
		tags, err := Release(o, repo, files)
		if err != nil {
			t.Fatal(err)
		}
		return tags
	}

	if tags := release(); !slices.Equal(tags, []string{"cs-cz/v0.263.0", "de-de/v0.263.0"}) {
		t.Fatalf("the first release's tags: %v", tags)
	}
	if got := git(t, repo, "show", "cs-cz/v0.263.0:cs-cz/go.mod"); !strings.Contains(got, "module github.com/mj41/go-mc26-lang/cs-cz") {
		t.Errorf("cs-cz's go.mod: %s", got)
	}
	if got := git(t, repo, "show", "cs-cz/v0.263.0:cs-cz/cs_cz.lang"); !strings.Contains(got, "%[1]s byl zastřelen entitou %[2]s") {
		t.Errorf("cs-cz's file has not Go's placeholders: %q", got)
	}
	if ls := git(t, repo, "ls-tree", "--name-only", "mc-26.3"); strings.Contains(ls, "en-us") {
		t.Errorf("en_us, built into go-mc26, is a module too: %s", ls)
	}
	if n := len(strings.Fields(git(t, repo, "ls-tree", "--name-only", "blobs"))); n != 3 { // two files, the README
		t.Errorf("%d entries on blobs, want the two files and the README", n)
	}

	if tags := release(); len(tags) != 0 {
		t.Fatalf("a release of the same files tagged %v", tags)
	}
	writeLangs(t, data, map[string]string{"cs_cz": `{"language.name":"Čeština","language.region":"Česko","death.attack.arrow":"%1$s byl zastřelen %2$s"}`})
	if tags := release(); !slices.Equal(tags, []string{"cs-cz/v0.263.1"}) {
		t.Fatalf("with cs_cz changed, the tags: %v, want cs-cz/v0.263.1 alone", tags)
	}
	if clean, _ := gitx.IsClean(repo); !clean || git(t, repo, "rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Error("the checkout is not left as it was found (clean, on main)")
	}
}

// A release's languages against a blob host: a file it has not is missing, a host that answers neither
// found nor not found fails the check rather than passing or blaming the files.
func TestMissing(t *testing.T) {
	files := map[string][]byte{"en_us": []byte("a\x00A\x00"), "cs_cz": []byte("a\x00Á\x00"), "de_de": []byte("a\x00Ä\x00")}
	have := map[string]bool{}
	status := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.Method != http.MethodHead || !have[r.URL.Path] {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	path := func(l string) string {
		return "/blobs/" + strings.TrimPrefix(generate.LangSum(files[l]), "sha256:") + ".lang"
	}
	have[path("cs_cz")] = true // English is go-mc26's: never looked for
	base := srv.URL + "/blobs"

	got, err := Missing(srv.Client(), base, files)
	if err != nil || !slices.Equal(got, []string{"de_de"}) {
		t.Fatalf("de_de not on the host: missing %v, %v", got, err)
	}
	have[path("de_de")] = true
	if got, err := Missing(srv.Client(), base, files); err != nil || len(got) != 0 {
		t.Fatalf("all on the host: missing %v, %v", got, err)
	}
	status = http.StatusServiceUnavailable
	if got, err := Missing(srv.Client(), base, files); err == nil {
		t.Fatalf("a host answering 503: missing %v and no error", got)
	}
}
