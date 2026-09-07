// mc26 runs the chain from Mojang's server jar to a tagged go-mc26 library —
// locally and in GitHub Actions alike:
//
//	mc26 extract  --version 26.2                 # jar → temp/data/26.2 (JSON + _meta.json)
//	mc26 build    --data temp/data/26.2 --out temp/lib/26.2   # data → library tree, built and tested
//	mc26 smoke    --version 26.2 --lib temp/lib/26.2          # vanilla server (container) ↔ bot smoke test
//	mc26 e2e      --version 26.2 --examples ../go-mc26-examples   # the example bots against a vanilla server
//	mc26 pipeline --version 26.2 [--smoke] [--e2e]   # extract + build + test (+ smoke, + e2e), nothing committed
//	mc26 release  --version 26.2 [--push]        # extract → mc26-data branch+tag → build → smoke → go-mc26 branch+tag
//	mc26 commit   --repo DIR --branch B --from TREE --message M [--tag-base v0.262.]
//	mc26 latest                                  # newest release and snapshot in Mojang's manifest
//	mc26 import-src --from <go-mc tree>          # one-time: fill gen/src from the fork
//	mc26 import-examples --from <go-mc tree> --to <examples checkout> --lib-version v0.262.0
//
// Scratch files live under <root>/temp (see internal/paths). Nothing is pushed
// unless --push is given.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/mj41/mc26/gen/internal/build"
	"github.com/mj41/mc26/gen/internal/capture"
	"github.com/mj41/mc26/gen/internal/e2e"
	"github.com/mj41/mc26/gen/internal/extract"
	"github.com/mj41/mc26/gen/internal/gitx"
	"github.com/mj41/mc26/gen/internal/importsrc"
	"github.com/mj41/mc26/gen/internal/paths"
	"github.com/mj41/mc26/gen/internal/report"
	"github.com/mj41/mc26/gen/internal/smoke"
)

const (
	repoName       = "github.com/mj41/mc26"
	libModule      = "github.com/mj41/go-mc26"
	examplesModule = "github.com/mj41/go-mc26-examples"
	// upstreamRef is the Tnze/go-mc commit the copied sources descend from.
	upstreamRef = "539b4a3"
	smokePort   = 25599
)

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	commands := map[string]func([]string) error{
		"extract": cmdExtract, "build": cmdBuild, "commit": cmdCommit, "smoke": cmdSmoke, "e2e": cmdE2E,
		"pipeline": cmdPipeline, "release": cmdRelease, "latest": cmdLatest, "tag": cmdTag, "report": cmdReport,
		"import-src": cmdImportSrc, "import-examples": cmdImportExamples,
		"crosscheck": cmdCrossCheck,
	}
	fn, ok := commands[cmd]
	if !ok {
		usage()
		os.Exit(2)
	}
	if err := fn(args); err != nil {
		logf("mc26 %s: %v", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: mc26 <command> [flags]

  extract   --version V [--runtime podman|docker] [--dry-run] [--only GenNbtSchema,…]
  build     --data DIR --out DIR [--version V] [--data-source S] [--no-test]
  smoke     --version V [--lib DIR] [--port N] [--runtime podman|docker|host]
  e2e       --version V [--lib DIR] [--examples DIR] [--port N] [--runtime …]
  pipeline  --version V [--data DIR] [--out DIR] [--smoke] [--e2e] [--skip-extract]
  release   --version V [--data-repo DIR] [--data-pre-repo DIR] [--lib-repo DIR] [--skip-extract] [--no-smoke] [--e2e] [--push]
  commit    --repo DIR --branch B --from TREE --message M [--tag-base v0.262.] [--push]
  report    --version V | --lib DIR      generated vs hand-written lines of a built library
  latest
  tag       --version V
  import-src        --from GOMC [--owner NAME]
  import-examples   --from GOMC --to DIR --lib-version vX.Y.Z

Scratch: <root>/temp (data/<version>, cache, lib/<version>, smoke/<version>).`)
}

// ---- pieces shared by the commands --------------------------------------------

// tagBase returns the tag prefix of a Minecraft version: "26.2" → "v0.262.",
// "26.3-pre-2" → "v0.263.0-pre2.", "26.3-rc-1" → "v0.263.0-rc1.".
func tagBase(version string) (string, error) {
	m := regexp.MustCompile(`^(\d+)\.(\d+)(?:-(.+))?$`).FindStringSubmatch(version)
	if m == nil {
		return "", fmt.Errorf("version %q is not <YY>.<N>[-suffix]", version)
	}
	base := "v0." + m[1] + m[2] + "."
	if m[3] == "" {
		return base, nil
	}
	suffix := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(m[3], "")
	return "v0." + m[1] + m[2] + ".0-" + suffix + ".", nil
}

func isPreRelease(version string) bool { return strings.Contains(version, "-") }

// extractVersion runs the extraction into temp/data/<version> and returns the directory.
func extractVersion(version, runtime string, dryRun bool, only ...string) (string, *extract.Meta, error) {
	root := paths.MustRoot()
	out := filepath.Join(paths.DataRoot(), version)
	meta, err := extract.Run(extract.Options{
		Version: version, OutDir: out, CacheDir: paths.Cache(),
		JavaDir: filepath.Join(root, "data-gen", "java"), Runtime: runtime, DryRun: dryRun, Only: only,
		ExtractorRepo: repoName, ExtractorCommit: gitx.ShortHead(root), Log: logf,
	})
	return out, meta, err
}

// buildLib builds the library from dataDir into outDir.
func buildLib(dataDir, outDir, version, dataSource string, test bool) (*build.Info, error) {
	root := paths.MustRoot()
	return build.Run(build.Options{
		GenRoot: filepath.Join(root, "gen"), DataDir: dataDir, OutDir: outDir, Version: version,
		DataSource: dataSource, Generator: "mc26 " + gitx.ShortHead(root), Test: test, Log: logf,
	})
}

// runSmoke starts a vanilla server of version and runs the library's smoke test.
func runSmoke(version, libDir string, port int, runtime string) error {
	jar, err := extract.ServerJar(paths.Cache(), version, logf)
	if err != nil {
		return err
	}
	return smoke.Run(smoke.Options{
		Version: version, LibDir: libDir, JarPath: jar, WorkDir: filepath.Join(paths.Temp(), "smoke", version),
		Port: port, Runtime: runtime, Log: logf,
	})
}

// runE2E builds the example bots against libDir and drives them against a
// vanilla server of version.
func runE2E(version, dataDir, libDir, examplesDir string, port int, runtime string) error {
	jar, err := extract.ServerJar(paths.Cache(), version, logf)
	if err != nil {
		return err
	}
	return e2e.Run(e2e.Options{
		Version: version, DataDir: dataDir, LibDir: libDir, ExamplesDir: examplesDir, JarPath: jar,
		WorkDir: filepath.Join(paths.Temp(), "e2e", version), Port: port, Runtime: runtime, Log: logf,
	})
}

// commitTree puts tree on branch of repo (created from main when new), commits
// it and tags it with the next tag of base unless HEAD already carries one and
// nothing changed. It returns the tag.
func commitTree(repo, branch, tree, message, base string) (string, error) {
	if gitx.Head(repo) == "" {
		return "", fmt.Errorf("%s is not a git repository with commits", repo)
	}
	if clean, err := gitx.IsClean(repo); err != nil || !clean {
		return "", fmt.Errorf("%s has uncommitted changes", repo)
	}
	if err := gitx.Checkout(repo, branch, "main"); err != nil {
		return "", err
	}
	if err := gitx.ReplaceTree(repo, tree); err != nil {
		return "", err
	}
	sha, changed, err := gitx.Commit(repo, message)
	if err != nil {
		return "", err
	}
	if !changed {
		if out, _ := gitx.Run(repo, "tag", "--points-at", "HEAD", "--list", base+"*"); out != "" {
			tag := strings.Fields(out)[0]
			logf("%s: %s unchanged, already %s", filepath.Base(repo), branch, tag)
			return tag, nil
		}
	}
	tag, err := gitx.NextTag(repo, base)
	if err != nil {
		return "", err
	}
	if err := gitx.Tag(repo, tag, message); err != nil {
		return "", err
	}
	logf("%s: %s at %s, tagged %s", filepath.Base(repo), branch, sha[:12], tag)
	return tag, nil
}

// stageData turns an extracted directory into the tree of a data branch:
// the JSON plus README, LICENSE (from the repo's main) and the verify workflow.
func stageData(dataDir, repo string) (string, error) {
	meta, err := extract.ReadMeta(dataDir)
	if err != nil {
		return "", err
	}
	stage := filepath.Join(paths.Temp(), "stage", "data-"+meta.ID)
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	// Only the data: no build products of the extraction run.
	skip := func(rel string) bool {
		return rel == "logs" || strings.HasSuffix(rel, ".class") || strings.HasSuffix(rel, ".log")
	}
	if err := gitx.CopyTree(dataDir, stage, skip); err != nil {
		return "", err
	}
	root := paths.MustRoot()
	if err := render(filepath.Join(root, "gen", "templates", "data-README.md.tmpl"), filepath.Join(stage, "README.md"), map[string]any{"Meta": meta}); err != nil {
		return "", err
	}
	if lic, err := gitx.Run(repo, "show", "main:LICENSE"); err == nil {
		if err := os.WriteFile(filepath.Join(stage, "LICENSE"), []byte(lic+"\n"), 0o644); err != nil {
			return "", err
		}
	}
	verify, err := os.ReadFile(filepath.Join(root, "gen", "templates", "data-verify.yml"))
	if err != nil {
		return "", err
	}
	wf := filepath.Join(stage, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		return "", err
	}
	return stage, os.WriteFile(filepath.Join(wf, "verify.yml"), verify, 0o644)
}

func render(tmplPath, out string, data any) error {
	t, err := template.ParseFiles(tmplPath)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.Execute(f, data)
}

func sibling(name string) string { return filepath.Join(filepath.Dir(paths.MustRoot()), name) }

// ---- commands --------------------------------------------------------------------

func cmdExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version id (26.1 or later)")
	runtime := fs.String("runtime", "", "podman or docker (detected)")
	dryRun := fs.Bool("dry-run", false, "print the container command only")
	only := fs.String("only", "", "run just these extractors (comma-separated, e.g. GenNbtSchema) into the existing temp/data/<version>")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	var onlyList []string
	if *only != "" {
		onlyList = strings.Split(*only, ",")
	}
	out, meta, err := extractVersion(*version, *runtime, *dryRun, onlyList...)
	if err != nil {
		return err
	}
	if meta != nil {
		logf("extracted %s (protocol %d, data version %d) → %s", meta.ID, meta.ProtocolVersion, meta.WorldVersion, out)
	}
	return nil
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	data := fs.String("data", "", "extracted data directory (or a version id under temp/data)")
	out := fs.String("out", "", "library tree to produce (default temp/lib/<version>)")
	version := fs.String("version", "", "expected version id")
	source := fs.String("data-source", "", "recorded in version.go (default: the data path)")
	noTest := fs.Bool("no-test", false, "skip go test")
	fs.Parse(args)
	if *data == "" {
		return fmt.Errorf("--data is required")
	}
	dataDir := paths.Data(*data)
	v, err := build.ReadVersion(dataDir)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(paths.Temp(), "lib", v.ID)
	}
	if *source == "" {
		*source = "data " + dataDir
	}
	if _, err := buildLib(dataDir, *out, *version, *source, !*noTest); err != nil {
		return err
	}
	logf("built %s → %s", v.ID, *out)
	return nil
}

func cmdSmoke(args []string) error {
	fs := flag.NewFlagSet("smoke", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version of the server jar")
	lib := fs.String("lib", "", "built library tree (default temp/lib/<version>)")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *lib == "" {
		*lib = filepath.Join(paths.Temp(), "lib", *version)
	}
	return runSmoke(*version, *lib, *port, *runtime)
}

// cmdCrossCheck records a real session with a vanilla server and reads it back
// twice: once with this library's generated types, and once with a decoder that
// has only the extracted JSON. It is the test of whether that JSON describes
// the protocol, rather than describing it to a reader who already has this
// library.
//
// The recording is made by a proxy between the bot and the server, so it covers
// every state and both directions — what a bot sends is described by the same
// JSON as what it receives, and until this went through the proxy only the play
// packets a bot happened to receive were ever checked.
func cmdCrossCheck(args []string) error {
	fs := flag.NewFlagSet("crosscheck", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version of the server jar")
	data := fs.String("data", "", "data directory (default temp/data/<version>)")
	lib := fs.String("lib", "", "built library tree (default temp/lib/<version>)")
	capturePath := fs.String("capture", "", "capture file (default temp/capture/<version>.jsonl)")
	keep := fs.Bool("keep", false, "decode the capture that is already there, without starting a server")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	root := paths.MustRoot()
	if *lib == "" {
		*lib = filepath.Join(paths.Temp(), "lib", *version)
	}
	if *data == "" {
		*data = paths.Data(*version)
	}
	if *capturePath == "" {
		*capturePath = filepath.Join(paths.Temp(), "capture", *version+".jsonl")
	}
	if !*keep {
		jar, err := extract.ServerJar(paths.Cache(), *version, logf)
		if err != nil {
			return err
		}
		ids, err := capture.LoadIDs(filepath.Join(*data, "packets.json"))
		if err != nil {
			return err
		}
		proxy := &capture.Proxy{Target: fmt.Sprintf("127.0.0.1:%d", *port), IDs: ids, PerID: 4, Log: logf}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		go proxy.Serve(l)
		logf("crosscheck: recording through %s to the server on %d", l.Addr(), *port)
		err = smoke.Run(smoke.Options{
			Version: *version, LibDir: *lib, JarPath: jar,
			WorkDir: filepath.Join(paths.Temp(), "smoke", *version),
			Port:    *port, Runtime: *runtime, Log: logf,
			TestRun:    "TestSmokeTraffic",
			ClientAddr: l.Addr().String(),
		})
		l.Close()
		if err != nil {
			return err
		}
		if err := proxy.Err(); err != nil {
			return fmt.Errorf("recording the session: %w", err)
		}
		ps := proxy.Packets()
		if len(ps) == 0 {
			return fmt.Errorf("nothing went through the proxy: the bot did not connect to it")
		}
		if err := os.MkdirAll(filepath.Dir(*capturePath), 0o755); err != nil {
			return err
		}
		if err := capture.Write(*capturePath, ps); err != nil {
			return err
		}
		logf("crosscheck: %d packets of %d state/flow/id triples recorded to %s %v",
			len(ps), len(capture.Distinct(ps)), *capturePath, proxy.Counts())

		// The same packets through this library's generated types, which is a
		// different question from whether the JSON describes them: a type short of
		// a field reads without complaint, so this checks each is read to its end.
		check := exec.Command("go", "test", "./bot", "-run", "TestCaptureCheck", "-v", "-count=1")
		check.Dir = *lib
		check.Env = append(os.Environ(), "MC26_CHECK_CAPTURE="+*capturePath)
		out, err := check.CombinedOutput()
		logf("%s", strings.TrimSpace(string(out)))
		if err != nil {
			return fmt.Errorf("this library did not read every packet of the session: %w", err)
		}
	}
	decoder := filepath.Join(root, "gen", "crosslang", "decode.py")
	if _, err := os.Stat(decoder); err != nil {
		return fmt.Errorf("%s not found: the cross-language decoder is what this command runs", decoder)
	}
	logf("crosscheck: decoding %s with %s", *capturePath, decoder)
	cmd := exec.Command("python3", decoder,
		"--data", *data,
		"--prims", filepath.Join(root, "gen", "hand-crafted", "prims.json"),
		"--nodes", filepath.Join(root, "gen", "hand-crafted", "nodes.json"),
		"--capture", *capturePath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the JSON-only decoder did not read what this library reads: %w", err)
	}
	return nil
}

func cmdE2E(args []string) error {
	fs := flag.NewFlagSet("e2e", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version of the server jar")
	data := fs.String("data", "", "data directory (default temp/data/<version>)")
	lib := fs.String("lib", "", "built library tree (default temp/lib/<version>)")
	examples := fs.String("examples", sibling("go-mc26-examples"), "go-mc26-examples checkout")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *data == "" {
		*data = filepath.Join(paths.DataRoot(), *version)
	}
	if *lib == "" {
		*lib = filepath.Join(paths.Temp(), "lib", *version)
	}
	return runE2E(*version, *data, *lib, *examples, *port, *runtime)
}

func cmdPipeline(args []string) error {
	fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version id")
	data := fs.String("data", "", "use this data directory instead of extracting")
	out := fs.String("out", "", "library tree to produce (default temp/lib/<version>)")
	doSmoke := fs.Bool("smoke", false, "run the vanilla-server smoke test after the build")
	doE2E := fs.Bool("e2e", false, "run the example bots against a vanilla server after the build")
	examples := fs.String("examples", sibling("go-mc26-examples"), "go-mc26-examples checkout for --e2e")
	skipExtract := fs.Bool("skip-extract", false, "reuse temp/data/<version> when it exists")
	runtime := fs.String("runtime", "", "podman or docker (detected); host runs the server on the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	start := time.Now()
	dataDir := *data
	if dataDir == "" {
		dataDir = filepath.Join(paths.DataRoot(), *version)
		if _, err := os.Stat(filepath.Join(dataDir, "_meta.json")); err != nil || !*skipExtract {
			if _, _, err := extractVersion(*version, *runtime, false); err != nil {
				return err
			}
		} else {
			logf("pipeline: reusing %s", dataDir)
		}
	}
	if *out == "" {
		*out = filepath.Join(paths.Temp(), "lib", *version)
	}
	if _, err := buildLib(dataDir, *out, *version, "data "+dataDir, true); err != nil {
		return err
	}
	if *doSmoke {
		if err := runSmoke(*version, *out, smokePort, containerRuntime(*runtime)); err != nil {
			return err
		}
	}
	if *doE2E {
		if err := runE2E(*version, dataDir, *out, *examples, smokePort, containerRuntime(*runtime)); err != nil {
			return err
		}
	}
	logf("pipeline %s: ok in %s (data %s, library %s)", *version, time.Since(start).Round(time.Second), dataDir, *out)
	return nil
}

// containerRuntime maps the pipeline's --runtime to the server runner: the
// extraction always needs a container runtime, the server may use "host".
func containerRuntime(r string) string { return r }

func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version id")
	dataRepo := fs.String("data-repo", sibling("mc26-data"), "checkout of mc26-data")
	dataPreRepo := fs.String("data-pre-repo", sibling("mc26-data-pre"), "checkout of mc26-data-pre (pre-releases and snapshots)")
	libRepo := fs.String("lib-repo", sibling("go-mc26"), "checkout of go-mc26")
	skipExtract := fs.Bool("skip-extract", false, "reuse temp/data/<version> when it exists")
	noSmoke := fs.Bool("no-smoke", false, "skip the vanilla-server smoke test")
	dataOnly := fs.Bool("data-only", false, "stop after the data commit and tag (a pre-release whose library needs work)")
	doE2E := fs.Bool("e2e", false, "also run the example bots against a vanilla server before committing the library")
	examples := fs.String("examples", sibling("go-mc26-examples"), "go-mc26-examples checkout for --e2e")
	push := fs.Bool("push", false, "push the branches and tags to origin")
	runtime := fs.String("runtime", "", "podman or docker (detected)")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	base, err := tagBase(*version)
	if err != nil {
		return err
	}
	branch := "mc-" + *version
	repo := *dataRepo
	dataName := "mc26-data"
	if isPreRelease(*version) {
		repo, dataName = *dataPreRepo, "mc26-data-pre"
	}

	// 1. data
	dataDir := filepath.Join(paths.DataRoot(), *version)
	if _, err := os.Stat(filepath.Join(dataDir, "_meta.json")); err != nil || !*skipExtract {
		if _, _, err := extractVersion(*version, *runtime, false); err != nil {
			return err
		}
	}
	meta, err := extract.ReadMeta(dataDir)
	if err != nil {
		return err
	}
	stage, err := stageData(dataDir, repo)
	if err != nil {
		return err
	}
	dataMsg := fmt.Sprintf("Extract %s (data version %d, protocol %d; mc26 %s)", meta.ID, meta.WorldVersion, meta.ProtocolVersion, meta.ExtractorCommit)
	dataTag, err := commitTree(repo, branch, stage, dataMsg, base)
	if err != nil {
		return fmt.Errorf("%s: %w", dataName, err)
	}
	if *dataOnly {
		if *push {
			if err := gitx.Push(repo, branch, dataTag); err != nil {
				return err
			}
			logf("pushed %s %s %s", dataName, branch, dataTag)
		} else {
			logf("release %s: %s %s committed locally (not pushed); library skipped (--data-only)", *version, dataName, dataTag)
		}
		return nil
	}

	// 2. library, built from the data branch checkout, smoke-tested before it is committed
	libOut := filepath.Join(paths.Temp(), "lib", *version)
	if _, err := buildLib(repo, libOut, *version, dataName+" "+dataTag, true); err != nil {
		return err
	}
	if !*noSmoke {
		if err := runSmoke(*version, libOut, smokePort, *runtime); err != nil {
			return err
		}
	}
	if *doE2E {
		if err := runE2E(*version, repo, libOut, *examples, smokePort, *runtime); err != nil {
			return err
		}
	}
	libMsg := fmt.Sprintf("Build %s from %s %s (mc26 %s)", meta.ID, dataName, dataTag, gitx.ShortHead(paths.MustRoot()))
	libTag, err := commitTree(*libRepo, branch, libOut, libMsg, base)
	if err != nil {
		return fmt.Errorf("go-mc26: %w", err)
	}

	// 3. push
	if *push {
		if err := gitx.Push(repo, branch, dataTag); err != nil {
			return err
		}
		if err := gitx.Push(*libRepo, branch, libTag); err != nil {
			return err
		}
		logf("pushed %s %s %s and go-mc26 %s %s", dataName, branch, dataTag, branch, libTag)
	} else {
		logf("release %s: %s %s / go-mc26 %s committed locally (not pushed)", *version, dataName, dataTag, libTag)
	}
	return nil
}

func cmdCommit(args []string) error {
	fs := flag.NewFlagSet("commit", flag.ExitOnError)
	repo := fs.String("repo", "", "checkout to commit into")
	branch := fs.String("branch", "", "branch (created from main when missing)")
	from := fs.String("from", "", "tree to commit")
	message := fs.String("message", "", "commit message")
	base := fs.String("tag-base", "", "tag prefix, e.g. v0.262. (next free number is used)")
	push := fs.Bool("push", false, "push branch and tag")
	fs.Parse(args)
	if *repo == "" || *branch == "" || *from == "" || *message == "" {
		return fmt.Errorf("--repo, --branch, --from and --message are required")
	}
	if *base == "" {
		*base = "untagged."
	}
	tag, err := commitTree(*repo, *branch, *from, *message, *base)
	if err != nil {
		return err
	}
	if *push {
		return gitx.Push(*repo, *branch, tag)
	}
	return nil
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	lib := fs.String("lib", "", "built library tree (default temp/lib/<version>)")
	version := fs.String("version", "", "Minecraft version, to find the default tree")
	fs.Parse(args)
	if *lib == "" {
		if *version == "" {
			return fmt.Errorf("--lib or --version is required")
		}
		*lib = filepath.Join(paths.Temp(), "lib", *version)
	}
	r, err := report.Run(report.Options{LibDir: *lib, GenRoot: filepath.Join(paths.MustRoot(), "gen")})
	if err != nil {
		return err
	}
	name := *version
	if name == "" {
		if v, err := build.ReadVersionGo(*lib); err == nil {
			name = v
		}
	}
	r.Print(os.Stdout, name)
	return nil
}

func cmdLatest(args []string) error {
	release, snapshot, err := extract.LatestVersions()
	if err != nil {
		return err
	}
	fmt.Printf("release  %s\nsnapshot %s\n", release, snapshot)
	return nil
}

func cmdTag(args []string) error {
	fs := flag.NewFlagSet("tag", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version id")
	fs.Parse(args)
	base, err := tagBase(*version)
	if err != nil {
		return err
	}
	fmt.Println(base + "0")
	return nil
}

func cmdImportSrc(args []string) error {
	fs := flag.NewFlagSet("import-src", flag.ExitOnError)
	from := fs.String("from", "", "go-mc working tree")
	owner := fs.String("owner", "mj41", "copyright holder added to LICENSE")
	fs.Parse(args)
	if *from == "" {
		return fmt.Errorf("--from is required")
	}
	return importsrc.ImportSrc(importsrc.Options{
		From: *from, To: filepath.Join(paths.MustRoot(), "gen", "src"), Module: libModule,
		UpstreamRef: upstreamRef, Owner: *owner, Log: logf,
	})
}

func cmdImportExamples(args []string) error {
	fs := flag.NewFlagSet("import-examples", flag.ExitOnError)
	from := fs.String("from", "", "go-mc working tree")
	to := fs.String("to", "", "examples checkout, on the branch to fill")
	libVersion := fs.String("lib-version", "", "go-mc26 version to require, e.g. v0.262.0")
	fs.Parse(args)
	if *from == "" || *to == "" || *libVersion == "" {
		return fmt.Errorf("--from, --to and --lib-version are required")
	}
	if err := importsrc.ImportExamples(importsrc.ExamplesOptions{
		From: *from, To: *to, Module: examplesModule, LibModule: libModule, LibVersion: *libVersion, Log: logf,
	}); err != nil {
		return err
	}
	ci, err := os.ReadFile(filepath.Join(paths.MustRoot(), "gen", "templates", "examples-ci.yml"))
	if err != nil {
		return err
	}
	wf := filepath.Join(*to, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(wf, "ci.yml"), ci, 0o644)
}

var _ = strconv.Itoa
