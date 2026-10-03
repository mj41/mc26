// Package gitx wraps the few git operations the release flow needs: put a
// produced tree on a branch of a checkout, commit it, tag it, push it.
package gitx

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Run runs git in dir and returns its trimmed combined output.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, s)
	}
	return s, nil
}

// Head returns the full sha of HEAD, or "" when dir is not a repository.
func Head(dir string) string {
	out, err := Run(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// ShortHead returns the abbreviated sha of HEAD, or "unknown".
func ShortHead(dir string) string {
	if h := Head(dir); len(h) >= 12 {
		return h[:12]
	}
	return "unknown"
}

// IsClean reports whether the working tree has no uncommitted changes.
func IsClean(dir string) (bool, error) {
	out, err := Run(dir, "status", "--porcelain")
	return out == "", err
}

// BranchExists reports whether a local branch exists.
func BranchExists(dir, name string) bool {
	_, err := Run(dir, "rev-parse", "--verify", "-q", "refs/heads/"+name)
	return err == nil
}

// Checkout switches to branch, creating it from `from` when it does not exist.
func Checkout(dir, branch, from string) error {
	if BranchExists(dir, branch) {
		_, err := Run(dir, "checkout", "-q", branch)
		return err
	}
	// --no-track: with branch.autoSetupMerge=always the new branch would track
	// `from`, and a push under push.default=upstream would land on it
	_, err := Run(dir, "checkout", "-q", "--no-track", "-b", branch, from)
	return err
}

// ReplaceTree makes the working tree of dir equal to src: everything except
// .git is removed, then src is copied in.
func ReplaceTree(dir, src string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return CopyTree(src, dir, func(rel string) bool { return rel == ".git" })
}

// CopyTree copies src into dst (created if needed); skip(rel) excludes paths.
func CopyTree(src, dst string, skip func(rel string) bool) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Commit stages everything and commits; changed is false when the tree was
// already committed as is.
func Commit(dir, message string) (sha string, changed bool, err error) {
	if _, err := Run(dir, "add", "-A"); err != nil {
		return "", false, err
	}
	out, err := Run(dir, "status", "--porcelain")
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return Head(dir), false, nil
	}
	if _, err := Run(dir, "commit", "-q", "-m", message); err != nil {
		return "", false, err
	}
	return Head(dir), true, nil
}

// Tag creates an annotated tag at HEAD.
func Tag(dir, name, message string) error {
	_, err := Run(dir, "tag", "-a", name, "-m", message)
	return err
}

// TagExists reports whether a tag exists.
func TagExists(dir, name string) bool {
	_, err := Run(dir, "rev-parse", "--verify", "-q", "refs/tags/"+name)
	return err == nil
}

// NextTag returns base + the smallest number n such that base+n is not yet a
// tag (base like "v0.262."), i.e. the next patch of that line.
func NextTag(dir, base string) (string, error) {
	out, err := Run(dir, "tag", "--list", base+"*")
	if err != nil {
		return "", err
	}
	next := 0
	for _, t := range strings.Fields(out) {
		rest := strings.TrimPrefix(t, base)
		if i := strings.IndexAny(rest, "-+"); i >= 0 {
			rest = rest[:i]
		}
		if n, err := strconv.Atoi(rest); err == nil && n >= next {
			next = n + 1
		}
	}
	return base + strconv.Itoa(next), nil
}

// Tags lists the tags matching pattern, sorted.
func Tags(dir, pattern string) ([]string, error) {
	out, err := Run(dir, "tag", "--list", pattern)
	if err != nil {
		return nil, err
	}
	tags := strings.Fields(out)
	sort.Strings(tags)
	return tags, nil
}

// Push pushes refs (branches and tags) to origin, each to the ref of its own
// name: a bare name would follow the user's push.default, which may send a
// branch to its upstream instead.
func Push(dir string, refs ...string) error {
	args := []string{"push", "origin"}
	for _, r := range refs {
		switch {
		case strings.HasPrefix(r, "refs/"):
			args = append(args, r+":"+r)
		case BranchExists(dir, r):
			args = append(args, "refs/heads/"+r+":refs/heads/"+r)
		default:
			if _, err := Run(dir, "rev-parse", "--verify", "-q", "refs/tags/"+r); err != nil {
				return fmt.Errorf("push %s: neither a local branch nor a tag", r)
			}
			args = append(args, "refs/tags/"+r+":refs/tags/"+r)
		}
	}
	_, err := Run(dir, args...)
	return err
}
