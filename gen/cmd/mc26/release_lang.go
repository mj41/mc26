package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mj41/mc26/gen/internal/generate"
	"github.com/mj41/mc26/gen/internal/gitx"
	"github.com/mj41/mc26/gen/internal/langmods"
	"github.com/mj41/mc26/gen/internal/paths"
)

// A release publishes go-mc26-lang itself (docs/release.md, "go-mc26-lang"): the library's manifest names every
// language file by its checksum, and langget and the modules fetch them from go-mc26-lang, so its blobs, its
// branch mc-<version> and the tags of the languages that changed must be there before go-mc26 is pushed.

// langPlan is what a release has made in the go-mc26-lang checkout and has still to push.
type langPlan struct {
	repo, version string
	files         map[string][]byte // by locale, English among them (go-mc26 carries it)
	refs          []string          // blobs, mc-<version>, then the tags made by this release
	base          string            // the tags' version line ("v0.263.")
}

// prepareLang makes the languages of this extraction into go-mc26-lang's modules (temp/lang/<version>) and commits
// and tags them in the checkout o.langRepo: the same as `mc26 lang --repo`, nothing pushed. It checks that the
// checkout's blobs hold every file the library's manifest will name. Run for a dry run too: everything that can
// go wrong before a push goes wrong here.
func prepareLang(o releaseOpts, dataDir string) (*langPlan, error) {
	base, err := tagBase(o.version)
	if err != nil {
		return nil, err
	}
	out := o.langOut
	if out == "" {
		out = filepath.Join(paths.Temp(), "lang", o.version)
	}
	lo := langmods.Options{DataDir: dataDir, Version: o.version, TagBase: base, OutDir: out,
		Generator: gitx.ShortHead(paths.MustRoot()), Log: logf}
	files, err := langmods.Write(lo)
	if err != nil {
		return nil, err
	}
	tags, err := langmods.Release(lo, o.langRepo, files)
	if err != nil {
		return nil, err
	}
	all, err := generate.LangFiles(dataDir) // (English too: Write leaves it out of the modules)
	if err != nil {
		return nil, err
	}
	if missing := blobsMissing(o.langRepo, all); len(missing) > 0 {
		return nil, fmt.Errorf("go-mc26-lang: the branch blobs of %s lacks the files of %s (%s)", o.langRepo, o.version, strings.Join(missing, " "))
	}
	p := &langPlan{repo: o.langRepo, version: o.version, files: all, base: base,
		refs: append([]string{"blobs", "mc-" + o.version}, tags...)}
	logf("release %s: go-mc26-lang prepared in %s: blobs and mc-%s, %d new tags", o.version, o.langRepo, o.version, len(tags))
	return p, nil
}

// blobsMissing lists the locales, English aside, whose file is not on the checkout's branch blobs by its checksum.
func blobsMissing(repo string, files map[string][]byte) []string {
	var missing []string
	for _, l := range sortedLocales(files) {
		if l == "en_us" {
			continue
		}
		name := strings.TrimPrefix(generate.LangSum(files[l]), "sha256:") + ".lang"
		if _, err := gitx.Run(repo, "cat-file", "-e", "blobs:"+name); err != nil {
			missing = append(missing, l)
		}
	}
	return missing
}

func sortedLocales(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for l := range files {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

// push pushes what the release made: the blobs first (what langget fetches), then the version's branch, then the
// tags, which point into it. Tags of this version's line that an earlier release made in this checkout and did not
// push (the origin has not got them) go with them. Everything is only ever added to: a release that changed
// nothing pushes nothing new.
func (p *langPlan) push() error {
	refs := slices.Clone(p.refs)
	remote, err := gitx.Run(p.repo, "ls-remote", "--tags", "origin")
	if err != nil {
		return fmt.Errorf("go-mc26-lang: %w", err)
	}
	local, err := gitx.Tags(p.repo, "*/"+p.base+"*")
	if err != nil {
		return err
	}
	for _, t := range local {
		if !strings.Contains(remote, "refs/tags/"+t) && !slices.Contains(refs, t) {
			refs = append(refs, t)
		}
	}
	if err := gitx.Push(p.repo, refs...); err != nil {
		return fmt.Errorf("go-mc26-lang: %w", err)
	}
	logf("pushed go-mc26-lang blobs, mc-%s and %d tags", p.version, len(refs)-2)
	return nil
}

// awaitLang waits until every file of the release is served under base (go-mc26-lang's blobs on the raw host:
// a pushed file is there within seconds, a moment longer when the host's cache has not caught up), and stops
// the release before go-mc26 is pushed when some are not there after limit.
func awaitLang(base string, files map[string][]byte, limit, every time.Duration) error {
	client := &http.Client{Timeout: time.Minute}
	deadline := time.Now().Add(limit)
	for {
		missing, err := langmods.Missing(client, base, files)
		if err == nil && len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("go-mc26-lang under %s: %w", base, err)
			}
			return fmt.Errorf("go-mc26-lang: after %s %d languages are not served under %s (%s)", limit, len(missing), base, strings.Join(missing, " "))
		}
		time.Sleep(every)
	}
}
