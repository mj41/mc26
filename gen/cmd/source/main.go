// source decompiles classes of a Minecraft jar into readable Java under
// temp/source/<version>/, for the code a bytecode listing is too slow to read:
// a movement tick, a digging loop, the order of the packets a client sends.
//
//	go run ./gen/cmd/source 26.3 LocalPlayer MultiPlayerGameMode   # the client jar: client and shared classes
//	go run ./gen/cmd/source 26.3 net.minecraft.world.entity.Entity # a fully qualified name when a simple one is ambiguous
//	go run ./gen/cmd/source -server 26.3 DedicatedServer           # the server jar, for what only the dedicated server has
//	go run ./gen/cmd/source -l 26.3 GameMode                       # just the matching class names
//	go run ./gen/cmd/source -all 26.3                              # every class of the jar (minutes, ~1 GB)
//
// It prints the path of every file it wrote. A class comes with its nested
// classes; the rest of the jar is on the decompiler's library path, so types
// and signatures resolve. The output is cached: asking again costs nothing
// unless -f.
//
// The client jar is the default because it holds the shared classes
// (net.minecraft.world, the integrated server) as well as the client's own.
// What is decompiled is for reading only: temp/ is not committed, and the
// kit's code is written from what was read, not copied from it.
//
// The decompiler is Vineflower, downloaded once into temp/cache and checked
// against a pinned SHA-256; it runs in the same JDK container as the
// extraction.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mj41/mc26/gen/internal/extract"
	"github.com/mj41/mc26/gen/internal/jarclass"
	"github.com/mj41/mc26/gen/internal/limits"
	"github.com/mj41/mc26/gen/internal/paths"
)

const (
	vineflowerVersion = "1.12.0"
	vineflowerURL     = "https://repo1.maven.org/maven2/org/vineflower/vineflower/" + vineflowerVersion + "/vineflower-" + vineflowerVersion + ".jar"
	vineflowerSHA256  = "1dfcfe974395734fa467ce620661c7623d05ba83670de0529b1fbd63ff548b9d"
)

func main() {
	list := flag.Bool("l", false, "list the matching class names and stop")
	server := flag.Bool("server", false, "read the server jar instead of the client's")
	all := flag.Bool("all", false, "decompile every class of the jar")
	force := flag.Bool("f", false, "decompile again even if it is cached")
	flag.Parse()
	if flag.NArg() < 1 || (flag.NArg() < 2 && !*all) {
		fmt.Fprintln(os.Stderr, "usage: source [-l] [-server] [-f] <version> <class-suffix>...\n       source [-server] -all <version>")
		os.Exit(2)
	}
	version := flag.Arg(0)
	logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }

	jar, outDir := filepath.Join(paths.Cache(), version+"-inner.jar"), filepath.Join(paths.Temp(), "source", version)
	if *server {
		outDir = filepath.Join(outDir, "server")
	} else {
		var err error
		if jar, err = extract.ClientJar(paths.Cache(), version, logf); err != nil {
			fail("%v", err)
		}
	}
	if _, err := os.Stat(jar); err != nil {
		fail("%s not found; run `mc26 extract --version %s` first", jar, version)
	}
	classes, err := jarclass.Names(jar)
	if err != nil {
		fail("%s: %v", jar, err)
	}

	var wanted []string // top-level classes, in Java form
	if *all {
		for _, c := range classes {
			if !strings.Contains(c, "$") && strings.HasPrefix(c, "net.minecraft.") {
				wanted = append(wanted, c)
			}
		}
	}
	for _, want := range flag.Args()[1:] {
		found := jarclass.Match(classes, want, *list)
		if len(found) == 0 {
			fail("no class of %s ends in %q", version, want)
		}
		if *list {
			for _, c := range found {
				fmt.Println(c)
			}
			continue
		}
		if len(found) > 1 {
			fail("%q matches %d classes; name one of them:\n  %s", want, len(found), strings.Join(found, "\n  "))
		}
		wanted = append(wanted, topLevel(found[0]))
	}
	if *list {
		return
	}

	var todo []string
	for _, c := range wanted {
		if _, err := os.Stat(javaPath(outDir, c)); err != nil || *force {
			todo = append(todo, c)
		}
	}
	if len(todo) > 0 {
		if err := decompile(jar, outDir, todo, logf); err != nil {
			fail("%v", err)
		}
	}
	for _, c := range wanted {
		p := javaPath(outDir, c)
		if _, err := os.Stat(p); err != nil {
			fail("the decompiler wrote no %s", p)
		}
		fmt.Println(p)
	}
}

// topLevel strips the nested part of a class name: Foo$Bar → Foo. A nested
// class is decompiled inside the file of the class it is nested in.
func topLevel(class string) string {
	top, _, _ := strings.Cut(class, "$")
	return top
}

func javaPath(outDir, class string) string {
	return filepath.Join(outDir, strings.ReplaceAll(class, ".", "/")+".java")
}

// decompile copies the class files of the wanted classes and of their nested
// classes out of the jar into a staging directory, and runs the decompiler on
// that directory with the whole jar as its library: only those classes are
// written, every type they mention still resolves.
func decompile(jar, outDir string, classes []string, logf func(string, ...any)) error {
	runtime := extract.DetectRuntime()
	if runtime == "" {
		return fmt.Errorf("neither podman nor docker found in PATH")
	}
	vf, err := vineflower(logf)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(paths.Temp(), "source-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	n, err := stageClasses(jar, filepath.Join(stage, "in"), classes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	logf("decompiling %d classes (%d class files) into %s", len(classes), n, outDir)
	args := []string{
		"run", "--rm",
		"--memory", limits.ExtractContainerMemory,
		"-e", "JAVA_TOOL_OPTIONS=" + limits.ExtractJavaHeap,
		"--security-opt", "label=disable",
		"-v", filepath.Dir(jar) + ":/jar:ro",
		"-v", filepath.Dir(vf) + ":/vf:ro",
		"-v", filepath.Join(stage, "in") + ":/in:ro",
		"-v", outDir + ":/out",
	}
	if runtime != "podman" { // docker: write the output as the host user
		if uid := os.Getuid(); uid > 0 {
			args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
		}
	}
	args = append(args, extract.JDKImage,
		"java", "-jar", "/vf/"+filepath.Base(vf),
		"--log-level=warn", "--folder", "-e=/jar/"+filepath.Base(jar), "/in", "/out")
	cmd := exec.Command(runtime, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// stageClasses extracts the class files of classes and of the classes nested
// in them (Outer$Inner, Outer$1) into dir, and returns how many it wrote.
func stageClasses(jar, dir string, classes []string) (int, error) {
	want := make([]string, len(classes))
	for i, c := range classes {
		want[i] = strings.ReplaceAll(c, ".", "/")
	}
	sort.Strings(want)
	zr, err := zip.OpenReader(jar)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	n := 0
	for _, f := range zr.File {
		name, ok := strings.CutSuffix(f.Name, ".class")
		if !ok || !wanted(want, name) {
			continue
		}
		if err := copyEntry(f, filepath.Join(dir, f.Name)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// wanted reports whether the class file name (a/b/C or a/b/C$D) is one of the
// sorted top-level classes or nested in one.
func wanted(sorted []string, name string) bool {
	top, _, _ := strings.Cut(name, "$")
	i := sort.SearchStrings(sorted, top)
	return i < len(sorted) && sorted[i] == top
}

func copyEntry(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// vineflower returns the cached decompiler jar, downloading it when missing;
// either way its SHA-256 must be the pinned one.
func vineflower(logf func(string, ...any)) (string, error) {
	path := filepath.Join(paths.Cache(), "vineflower-"+vineflowerVersion+".jar")
	if sum, err := fileSHA256(path); err == nil && sum == vineflowerSHA256 {
		return path, nil
	}
	logf("downloading %s", vineflowerURL)
	resp, err := http.Get(vineflowerURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, vineflowerURL)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if sum, err := fileSHA256(tmp); err != nil || sum != vineflowerSHA256 {
		os.Remove(tmp)
		return "", fmt.Errorf("%s: sha256 %s, pinned %s", vineflowerURL, sum, vineflowerSHA256)
	}
	return path, os.Rename(tmp, path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "source: "+format+"\n", args...)
	os.Exit(1)
}
