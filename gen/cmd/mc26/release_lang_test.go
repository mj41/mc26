package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mj41/mc26/gen/internal/generate"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// langOrigin is a go-mc26-lang as GitHub has it (a bare repository with a main) and a way to clone it.
func langOrigin(t *testing.T) (origin string, clone func() string) {
	t.Helper()
	origin = filepath.Join(t.TempDir(), "go-mc26-lang.git")
	seed := t.TempDir()
	gitIn(t, seed, "init", "-q", "-b", "main")
	gitIn(t, seed, "config", "user.name", "test")
	gitIn(t, seed, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("go-mc26-lang\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-q", "-m", "init")
	if out, err := exec.Command("git", "clone", "-q", "--bare", seed, origin).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return origin, func() string {
		dir := filepath.Join(t.TempDir(), "go-mc26-lang")
		if out, err := exec.Command("git", "clone", "-q", "--no-single-branch", origin, dir).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		gitIn(t, dir, "config", "user.name", "mc26 release")
		gitIn(t, dir, "config", "user.email", "mc26-release@users.noreply.github.com")
		return dir
	}
}

func langData(t *testing.T, langs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lang"), 0o755); err != nil {
		t.Fatal(err)
	}
	for l, json := range langs {
		if err := os.WriteFile(filepath.Join(dir, "lang", l+".json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func remoteRefs(t *testing.T, origin string) string {
	t.Helper()
	return gitIn(t, origin, "for-each-ref", "--format=%(refname:short)")
}

// A release with a go-mc26-lang checkout makes the languages' modules itself: a dry run commits and tags them in the
// checkout and verifies the blobs, and pushes nothing; the push sends blobs, mc-<version> and the new tags; a release
// run again with the same data makes no new tags, one with a changed language tags that language alone.
func TestLangReleasePublishes(t *testing.T) {
	origin, clone := langOrigin(t)
	data := langData(t, map[string]string{
		"en_us": `{"language.name":"English","chat.type.text":"<%s> %s"}`,
		"cs_cz": `{"language.name":"Čeština","language.region":"Česko","death.attack.arrow":"%1$s byl zastřelen"}`,
		"de_de": `{"language.name":"Deutsch"}`,
	})
	opts := func(repo string) releaseOpts {
		return releaseOpts{version: "26.3", langRepo: repo, langOut: filepath.Join(t.TempDir(), "out")}
	}

	// a dry run: made in the checkout, nothing at the origin
	work := clone()
	plan, err := prepareLang(opts(work), data)
	if err != nil {
		t.Fatalf("the dry run: %v", err)
	}
	if want := []string{"blobs", "mc-26.3", "cs-cz/v0.263.0", "de-de/v0.263.0"}; !slices.Equal(plan.refs, want) {
		t.Errorf("the refs to push: %v, want %v", plan.refs, want)
	}
	if got := remoteRefs(t, origin); strings.Contains(got, "blobs") || strings.Contains(got, "mc-26.3") || strings.Contains(got, "cs-cz") {
		t.Errorf("a dry run pushed: %s", got)
	}
	if got := gitIn(t, work, "tag", "--list"); !strings.Contains(got, "cs-cz/v0.263.0") {
		t.Errorf("no tag made in the checkout: %q", got)
	}

	// the push
	if err := plan.push(); err != nil {
		t.Fatal(err)
	}
	got := remoteRefs(t, origin)
	for _, want := range []string{"blobs", "mc-26.3", "cs-cz/v0.263.0", "de-de/v0.263.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s not pushed: %s", want, got)
		}
	}
	if strings.Contains(got, "en-us") {
		t.Errorf("English, built into go-mc26, has a module: %s", got)
	}

	// the same release again from a fresh clone: nothing new
	again, err := prepareLang(opts(clone()), data)
	if err != nil {
		t.Fatalf("the same release again: %v", err)
	}
	if want := []string{"blobs", "mc-26.3"}; !slices.Equal(again.refs, want) {
		t.Errorf("the same data made refs %v, want only the branches %v", again.refs, want)
	}
	if err := again.push(); err != nil {
		t.Errorf("a push of nothing new: %v", err)
	}

	// Mojang changed one language without a new version: its file is new on blobs, its module has the next tag
	data2 := langData(t, map[string]string{
		"en_us": `{"language.name":"English","chat.type.text":"<%s> %s"}`,
		"cs_cz": `{"language.name":"Čeština","language.region":"Česko","death.attack.arrow":"%1$s byl zastřelen střelou"}`,
		"de_de": `{"language.name":"Deutsch"}`,
	})
	changed, err := prepareLang(opts(clone()), data2)
	if err != nil {
		t.Fatalf("the changed release: %v", err)
	}
	if want := []string{"blobs", "mc-26.3", "cs-cz/v0.263.1"}; !slices.Equal(changed.refs, want) {
		t.Errorf("a changed language: refs %v, want %v", changed.refs, want)
	}
	if err := changed.push(); err != nil {
		t.Fatal(err)
	}
	if got := remoteRefs(t, origin); !strings.Contains(got, "cs-cz/v0.263.1") || strings.Contains(got, "de-de/v0.263.1") {
		t.Errorf("tags after the change: %s", got)
	}
}

// Tags a release made in the checkout and did not push (a run that failed after the commit, in a checkout kept) go
// with the next push.
func TestLangReleasePushesEarlierTags(t *testing.T) {
	origin, clone := langOrigin(t)
	data := langData(t, map[string]string{"en_us": `{"a":"A"}`, "cs_cz": `{"a":"Á"}`})
	work := clone()
	o := releaseOpts{version: "26.3", langRepo: work, langOut: filepath.Join(t.TempDir(), "out")}
	if _, err := prepareLang(o, data); err != nil { // (made, never pushed)
		t.Fatal(err)
	}
	again, err := prepareLang(o, data) // the same checkout, nothing changed: no new tag, the old one unpushed
	if err != nil {
		t.Fatal(err)
	}
	if err := again.push(); err != nil {
		t.Fatal(err)
	}
	if got := remoteRefs(t, origin); !strings.Contains(got, "cs-cz/v0.263.0") {
		t.Errorf("the earlier tag was not pushed: %s", got)
	}
}

// A file the manifest will name that the checkout's blobs lack stops the release (it would be a go-mc26 whose
// languages cannot be fetched).
func TestLangBlobsMissing(t *testing.T) {
	_, clone := langOrigin(t)
	data := langData(t, map[string]string{"en_us": `{"a":"A"}`, "cs_cz": `{"a":"Á"}`, "de_de": `{"a":"Ä"}`})
	work := clone()
	o := releaseOpts{version: "26.3", langRepo: work, langOut: filepath.Join(t.TempDir(), "out")}
	plan, err := prepareLang(o, data)
	if err != nil {
		t.Fatal(err)
	}
	if m := blobsMissing(work, plan.files); len(m) != 0 {
		t.Fatalf("missing right after the release: %v", m)
	}
	files, _ := generate.LangFiles(data)
	gitIn(t, work, "checkout", "-q", "blobs")
	gitIn(t, work, "rm", "-q", strings.TrimPrefix(generate.LangSum(files["de_de"]), "sha256:")+".lang")
	gitIn(t, work, "commit", "-q", "-m", "lost")
	if m := blobsMissing(work, plan.files); !slices.Equal(m, []string{"de_de"}) {
		t.Errorf("a lost file: missing %v, want de_de", m)
	}
}

// awaitLang goes on while the host does not serve a file yet (it was pushed a moment ago) and returns when it does;
// a file that does not come is an error that names it.
func TestAwaitLang(t *testing.T) {
	data := langData(t, map[string]string{"en_us": `{"a":"A"}`, "cs_cz": `{"a":"Á"}`})
	files, err := generate.LangFiles(data)
	if err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asked.Add(1) <= 2 { // not served yet
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if err := awaitLang(srv.URL, files, 10*time.Second, 10*time.Millisecond); err != nil {
		t.Fatalf("served after two tries: %v (asked %d times)", err, asked.Load())
	}
	never := httptest.NewServer(http.NotFoundHandler())
	defer never.Close()
	err = awaitLang(never.URL, files, 50*time.Millisecond, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "cs_cz") {
		t.Errorf("a file never served: %v", err)
	}
}
