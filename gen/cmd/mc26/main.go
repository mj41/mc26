// mc26 runs the chain from Mojang's server jar to a tagged go-mc26 library —
// locally and in GitHub Actions alike:
//
//	mc26 extract  --version 26.2                 # jar → temp/data/26.2 (JSON + _meta.json)
//	mc26 build    --data temp/data/26.2 --out temp/lib/26.2   # data → library tree, built and tested
//	mc26 smoke    --version 26.2 --lib temp/lib/26.2          # vanilla server (container) ↔ bot smoke test
//	mc26 e2e      --version 26.2                                  # the kit's example bots against a vanilla server
//	mc26 fixtures --version 26.2                                  # the save tests' small world, cut from the e2e run's
//	mc26 pipeline --version 26.2 [--smoke] [--e2e]   # extract + build + test (+ smoke, + e2e), nothing committed
//	mc26 release  --version 26.2 [--push]        # extract → mc26-data branch+tag → build → smoke → go-mc26 branch+tag
//	mc26 commit   --repo DIR --branch B --from TREE --message M [--tag-base v0.262.]
//	mc26 latest                                  # newest release and snapshot in Mojang's manifest
//	mc26 import-src --from <go-mc tree>          # one-time: fill gen/src from the fork
//
// Scratch files live under <root>/temp (see internal/paths). Nothing is pushed
// unless --push is given.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/mj41/mc26/gen/internal/build"
	"github.com/mj41/mc26/gen/internal/capture"
	"github.com/mj41/mc26/gen/internal/docgen"
	"github.com/mj41/mc26/gen/internal/e2e"
	"github.com/mj41/mc26/gen/internal/extract"
	"github.com/mj41/mc26/gen/internal/fixtures"
	"github.com/mj41/mc26/gen/internal/gitx"
	"github.com/mj41/mc26/gen/internal/importsrc"
	"github.com/mj41/mc26/gen/internal/kit"
	"github.com/mj41/mc26/gen/internal/limits"
	"github.com/mj41/mc26/gen/internal/mcver"
	"github.com/mj41/mc26/gen/internal/paths"
	"github.com/mj41/mc26/gen/internal/report"
	"github.com/mj41/mc26/gen/internal/schemacheck"
	"github.com/mj41/mc26/gen/internal/smoke"
)

const (
	repoName  = "github.com/mj41/mc26"
	libModule = "github.com/mj41/go-mc26"
	kitModule = "github.com/mj41/go-mc26-kit"
	// upstreamRef is the Tnze/go-mc commit the copied sources descend from.
	upstreamRef = "539b4a3"
	smokePort   = 25599
)

// logOut is where every command's progress goes; verify points it at a log file per step.
var logOut io.Writer = os.Stderr

func logf(format string, args ...any) { fmt.Fprintf(logOut, format+"\n", args...) }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	commands := map[string]func([]string) error{
		"extract": cmdExtract, "build": cmdBuild, "commit": cmdCommit, "smoke": cmdSmoke, "e2e": cmdE2E,
		"pipeline": cmdPipeline, "release": cmdRelease, "latest": cmdLatest, "tag": cmdTag, "report": cmdReport,
		"import-src": cmdImportSrc,
		"crosscheck": cmdCrossCheck, "verify": cmdVerify, "update": cmdUpdate, "docs": cmdDocs, "index": cmdIndex,
		"fixtures": cmdFixtures,
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
  build     --data DIR --out DIR [--version V] [--data-source S] [--no-test] [--allow-holes]
  smoke     --version V [--kit DIR] [--port N] [--runtime podman|docker|host]
  e2e       --version V [--kit DIR] [--port N] [--runtime …]
  pipeline  --version V [--data DIR] [--out DIR] [--smoke] [--e2e] [--skip-extract]
  verify    [--versions 26.1,26.2] [--steps build,smoke,crosscheck,e2e] [--quiet] [--runtime …]
  update    [--version V | --pre] [--skip-extract] [--allow-holes] [--no-verify] [--no-commit] [--runtime …]
  release   --version V [--data-repo DIR] [--data-pre-repo DIR] [--lib-repo DIR] [--skip-extract] [--no-smoke] [--e2e] [--push]
  commit    --repo DIR --branch B --from TREE --message M [--tag-base v0.262.] [--push]
  report    --version V | --lib DIR      generated vs hand-written lines of a built library
  docs      --version V [--out DIR] [--internal]   render gen/docs/*.mc26tmpl.md for a version
  index     --repo DIR --kind data|data-pre|lib     rewrite the README on main: the branch and tag table
  latest
  tag       --version V
  import-src        --from GOMC [--owner NAME]

The kit (bot, server, accounts, examples) is a checkout of go-mc26-kit next to this repository
(--kit-src DIR to point elsewhere): the pipeline builds and tests it against every library it
builds, since its bot is the test client; it does not own or release it.
Scratch: <root>/temp (data/<version>, cache, lib/<version>, kit/<version>, smoke/<version>).`)
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

// allowHoles lets `build --allow-holes` through a schema with a hole; every
// other command builds strictly.
var allowHoles bool

// kitSrc is the go-mc26-kit checkout every build assembles and tests against
// the library it produced (--kit-src on the commands that build).
var kitSrc string

func kitSrcFlag(fs *flag.FlagSet) {
	fs.StringVar(&kitSrc, "kit-src", sibling("go-mc26-kit"), "checkout of go-mc26-kit, built and tested against the library")
}

// buildLib builds the library from dataDir into outDir, and the kit against
// it into temp/kit/<version>.
func buildLib(dataDir, outDir, version, dataSource string, test bool) (*build.Info, error) {
	root := paths.MustRoot()
	v, err := build.ReadVersion(dataDir)
	if err != nil {
		return nil, err
	}
	return build.Run(build.Options{
		GenRoot: filepath.Join(root, "gen"), DataDir: dataDir, OutDir: outDir, KitSrc: kitSrc, KitOut: paths.Kit(v.ID), Version: version,
		DataSource: dataSource, Generator: "mc26 " + gitx.ShortHead(root), Test: test, AllowHoles: allowHoles, Log: logf,
	})
}

// runSmoke starts a vanilla server of version and runs the smoke tests from
// the kit tree assembled against that version's library.
func runSmoke(version, kitDir string, port int, runtime string) error {
	jar, err := extract.ServerJar(paths.Cache(), version, logf)
	if err != nil {
		return err
	}
	return smoke.Run(smoke.Options{
		Version: version, KitDir: kitDir, JarPath: jar, WorkDir: filepath.Join(paths.Temp(), "smoke", version),
		Port: port, Runtime: runtime, Log: logf,
	})
}

// runE2E builds the kit's example bots and drives them against a vanilla
// server of version.
func runE2E(version, dataDir, kitDir string, port int, runtime string) error {
	jar, err := extract.ServerJar(paths.Cache(), version, logf)
	if err != nil {
		return err
	}
	root := paths.MustRoot()
	// a version's fixture world is cut once, by its first end-to-end run; every
	// later run leaves it, so verify does not rewrite committed worlds and the
	// build ships the world the sources hold (`mc26 fixtures` re-cuts it)
	fixtureDir := paths.Fixtures(version)
	if _, err := os.Stat(fixtureDir); err == nil {
		fixtureDir = ""
	}
	return e2e.Run(e2e.Options{
		Version: version, DataDir: dataDir, KitDir: kitDir, JarPath: jar,
		CrossLang: filepath.Join(root, "gen", "crosslang"), NodesPath: filepath.Join(root, "gen", "hand-crafted", "nodes.json"),
		WorkDir: filepath.Join(paths.Temp(), "e2e", version), Port: port, Runtime: runtime, Log: logf,
		Fixtures: fixtureDir,
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
	// the documentation of this version, rendered from gen/docs for a reader of the data
	if _, err := renderDocs(meta.ID, dataDir, filepath.Join(stage, "docs"), false); err != nil {
		return "", fmt.Errorf("docs: %w", err)
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
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
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
	holes := fs.Bool("allow-holes", false, "build although a schema has a hole (while the extractor is being fixed)")
	kitSrcFlag(fs)
	fs.Parse(args)
	allowHoles = *holes
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
	kitDir := fs.String("kit", "", "kit tree assembled against the built library (default temp/kit/<version>)")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *kitDir == "" {
		*kitDir = paths.Kit(*version)
	}
	return runSmoke(*version, *kitDir, *port, *runtime)
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
	kitDir := fs.String("kit", "", "kit tree assembled against the built library (default temp/kit/<version>)")
	capturePath := fs.String("capture", "", "capture file (default temp/capture/<version>.jsonl)")
	keep := fs.Bool("keep", false, "decode the capture that is already there, without starting a server")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *kitDir == "" {
		*kitDir = paths.Kit(*version)
	}
	if *data == "" {
		*data = paths.Data(*version)
	}
	if *capturePath == "" {
		*capturePath = filepath.Join(paths.Temp(), "capture", *version+".jsonl")
	}
	return runCrossCheck(*version, *data, *kitDir, *capturePath, *keep, *port, *runtime)
}

// runCrossCheck records a traffic session through the proxy (unless keep), checks
// this library reads every packet of it, and hands it to the JSON-only decoder.
func runCrossCheck(version, data, kitDir, capturePath string, keep bool, port int, runtime string) error {
	root := paths.MustRoot()
	if !keep {
		jar, err := extract.ServerJar(paths.Cache(), version, logf)
		if err != nil {
			return err
		}
		ids, err := capture.LoadIDs(filepath.Join(data, "packets.json"), filepath.Join(root, "gen", "hand-crafted", "nodes.json"))
		if err != nil {
			return err
		}
		proxy := &capture.Proxy{Target: fmt.Sprintf("127.0.0.1:%d", port), IDs: ids, PerID: 4, Log: logf}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		go proxy.Serve(l)
		logf("crosscheck: recording through %s to the server on %d", l.Addr(), port)
		err = smoke.Run(smoke.Options{
			Version: version, KitDir: kitDir, JarPath: jar,
			WorkDir: filepath.Join(paths.Temp(), "smoke", version),
			Port:    port, Runtime: runtime, Log: logf,
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
		if err := os.MkdirAll(filepath.Dir(capturePath), 0o755); err != nil {
			return err
		}
		if err := capture.Write(capturePath, ps); err != nil {
			return err
		}
		logf("crosscheck: %d packets of %d state/flow/id triples recorded to %s %v",
			len(ps), len(capture.Distinct(ps)), capturePath, proxy.Counts())

		// The same packets through this library's generated types, which is a
		// different question from whether the JSON describes them: a type short of
		// a field reads without complaint, so this checks each is read to its end.
		check := exec.Command("go", "test", "./bot", "-run", "TestCaptureCheck", "-v", "-count=1")
		check.Dir = kitDir
		check.Env = kit.Env(kitDir, "MC26_CHECK_CAPTURE="+capturePath)
		out, err := check.CombinedOutput()
		logf("%s", strings.TrimSpace(string(out)))
		if err != nil {
			return fmt.Errorf("this library did not read every packet of the session: %w", err)
		}
	}
	// The reader that has only the JSON: a Go module of its own under
	// gen/crosslang, which imports neither the library nor the generators.
	decoder := filepath.Join(root, "gen", "crosslang")
	if _, err := os.Stat(filepath.Join(decoder, "go.mod")); err != nil {
		return fmt.Errorf("%s is not a module: the cross-language reader is what this command runs", decoder)
	}
	logf("crosscheck: decoding %s with the JSON-only reader in %s", capturePath, decoder)
	vet := exec.Command("go", "vet", ".")
	vet.Dir = decoder
	vet.Env = limits.GoEnv()
	if out, err := vet.CombinedOutput(); err != nil {
		return fmt.Errorf("gen/crosslang: go vet: %v\n%s", err, out)
	}
	cmd := exec.Command("go", "run", ".",
		"--data", data,
		"--nodes", filepath.Join(root, "gen", "hand-crafted", "nodes.json"),
		"--capture", capturePath)
	cmd.Dir = decoder
	cmd.Env = limits.GoEnv()
	cmd.Stdout, cmd.Stderr = logOut, logOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the JSON-only reader did not read what this library reads: %w", err)
	}
	return nil
}

// cmdVerify runs the whole check — build, smoke, crosscheck, e2e — for every
// version, one step at a time (each holds one server or one compile), and
// prints one table. A failed step does not stop the other versions; a failed
// build skips the steps of that version that need the library.
func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	versions := fs.String("versions", "", "comma-separated Minecraft versions (default: every extracted one under temp/data)")
	steps := fs.String("steps", "build,smoke,crosscheck,e2e", "comma-separated steps to run, in this order")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	quiet := fs.Bool("quiet", false, "progress goes to the log files only, not to stderr")
	logDir := fs.String("log-dir", filepath.Join(paths.Temp(), "verify"), "one log file per version and step")
	kitSrcFlag(fs)
	fs.Parse(args)
	var vs []string
	if *versions != "" {
		vs = strings.Split(*versions, ",")
	} else {
		var err error
		if vs, err = extractedVersions(); err != nil {
			return err
		}
	}
	return runVerify(vs, strings.Split(*steps, ","), *port, *runtime, *quiet, *logDir)
}

// extractedVersions lists the versions under temp/data, oldest first.
func extractedVersions() ([]string, error) {
	entries, err := os.ReadDir(paths.DataRoot())
	if err != nil {
		return nil, fmt.Errorf("no extracted data: %w", err)
	}
	var vs []string
	for _, e := range entries {
		if e.IsDir() {
			vs = append(vs, e.Name())
		}
	}
	if len(vs) == 0 {
		return nil, fmt.Errorf("no versions: extract one first")
	}
	sort.Slice(vs, func(i, j int) bool { return mcver.Less(vs[i], vs[j]) })
	return vs, nil
}

// runVerify is verify's body: the steps for every version, then the table.
func runVerify(vs, wanted []string, port int, runtime string, quiet bool, logDir string) error {
	type result struct {
		status string // ok, FAIL, skipped
		took   time.Duration
		log    string
	}
	results := map[string]map[string]result{}
	var failed []string
	stderr := logOut
	defer func() { logOut = stderr }()
	for _, v := range vs {
		results[v] = map[string]result{}
		data := paths.Data(v)
		lib, kitDir := paths.Lib(v), paths.Kit(v)
		built := true
		for _, step := range wanted {
			logPath := filepath.Join(logDir, v, step+".log")
			if !built {
				results[v][step] = result{status: "skipped", log: logPath}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
				return err
			}
			f, err := os.Create(logPath)
			if err != nil {
				return err
			}
			if quiet {
				logOut = f
			} else {
				logOut = io.MultiWriter(stderr, f)
			}
			fmt.Fprintf(stderr, "########## %s %s\n", v, step)
			start := time.Now()
			switch step {
			case "build":
				_, err = buildLib(data, lib, v, "", true)
			case "smoke":
				err = runSmoke(v, kitDir, port, runtime)
			case "crosscheck":
				err = runCrossCheck(v, data, kitDir, filepath.Join(paths.Temp(), "capture", v+".jsonl"), false, port, runtime)
			case "e2e":
				err = runE2E(v, data, kitDir, port, runtime)
			default:
				err = fmt.Errorf("unknown step %q (build, smoke, crosscheck, e2e)", step)
			}
			took := time.Since(start).Round(time.Second)
			if err != nil {
				logf("verify: %s %s FAILED after %s: %v", v, step, took, err)
			}
			logOut = stderr
			f.Close()
			r := result{status: "ok", took: took, log: logPath}
			if err != nil {
				r.status = "FAIL"
				failed = append(failed, fmt.Sprintf("%s %s: %v (log: %s)", v, step, err, logPath))
				if step == "build" {
					built = false
				}
			}
			results[v][step] = r
		}
	}
	// the table, on stdout: what a reader of the run wants first
	fmt.Printf("%-14s", "version")
	for _, step := range wanted {
		fmt.Printf(" %-14s", step)
	}
	fmt.Println()
	for _, v := range vs {
		fmt.Printf("%-14s", v)
		for _, step := range wanted {
			r := results[v][step]
			cell := r.status
			if r.took > 0 {
				cell += " " + r.took.String()
			}
			fmt.Printf(" %-14s", cell)
		}
		fmt.Println()
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d steps failed:\n  %s", len(failed), len(vs)*len(wanted), strings.Join(failed, "\n  "))
	}
	fmt.Printf("verify: %d versions, %d steps each, all passed; logs in %s\n", len(vs), len(wanted), logDir)
	return nil
}

func cmdE2E(args []string) error {
	fs := flag.NewFlagSet("e2e", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version of the server jar")
	data := fs.String("data", "", "data directory (default temp/data/<version>)")
	kitDir := fs.String("kit", "", "kit tree assembled against the built library (default temp/kit/<version>)")
	port := fs.Int("port", smokePort, "server port")
	runtime := fs.String("runtime", "", "podman or docker for the server (detected), or host for the host's java")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *data == "" {
		*data = filepath.Join(paths.DataRoot(), *version)
	}
	if *kitDir == "" {
		*kitDir = paths.Kit(*version)
	}
	return runE2E(*version, *data, *kitDir, *port, *runtime)
}

// cmdFixtures cuts the save package's test world out of the world the
// end-to-end run's server wrote, so the library's own tests read what this
// version's server writes.
func cmdFixtures(args []string) error {
	fs := flag.NewFlagSet("fixtures", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version whose end-to-end world to cut from")
	world := fs.String("world", "", "world directory (default temp/e2e/<version>/server/world, what `mc26 e2e` leaves)")
	out := fs.String("out", "", "fixture directory (default gen/src/save/testdata/<version>; the build copies it to save/testdata/world)")
	chunks := fs.Int("chunks", 4, "chunks to keep around the spawn, and entity chunks")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *world == "" {
		*world = filepath.Join(paths.Temp(), "e2e", *version, "server", "world")
	}
	if _, err := os.Stat(filepath.Join(*world, "level.dat")); err != nil {
		return fmt.Errorf("no world at %s: run `mc26 e2e --version %s` first", *world, *version)
	}
	if *out == "" {
		*out = paths.Fixtures(*version)
	}
	note, err := fixtures.Make(fixtures.Options{Version: *version, World: *world, Out: *out, Chunks: *chunks, Log: logf})
	if err != nil {
		return err
	}
	fmt.Print(note)
	return nil
}

func cmdPipeline(args []string) error {
	fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version id")
	data := fs.String("data", "", "use this data directory instead of extracting")
	out := fs.String("out", "", "library tree to produce (default temp/lib/<version>)")
	doSmoke := fs.Bool("smoke", false, "run the vanilla-server smoke test after the build")
	doE2E := fs.Bool("e2e", false, "run the example bots against a vanilla server after the build")
	skipExtract := fs.Bool("skip-extract", false, "reuse temp/data/<version> when it exists")
	runtime := fs.String("runtime", "", "podman or docker (detected); host runs the server on the host's java")
	kitSrcFlag(fs)
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
		if err := runSmoke(*version, paths.Kit(*version), smokePort, containerRuntime(*runtime)); err != nil {
			return err
		}
	}
	if *doE2E {
		if err := runE2E(*version, dataDir, paths.Kit(*version), smokePort, containerRuntime(*runtime)); err != nil {
			return err
		}
	}
	logf("pipeline %s: ok in %s (data %s, library %s)", *version, time.Since(start).Round(time.Second), dataDir, *out)
	return nil
}

// containerRuntime maps the pipeline's --runtime to the server runner: the
// extraction always needs a container runtime, the server may use "host".
func containerRuntime(r string) string { return r }

// releaseOpts are release's flags; update fills them in too.
type releaseOpts struct {
	version, dataRepo, dataPreRepo, libRepo, runtime string
	skipExtract, noSmoke, dataOnly, e2e, push        bool
}

func releaseFlags(fs *flag.FlagSet, o *releaseOpts) {
	fs.StringVar(&o.version, "version", "", "Minecraft version id")
	fs.StringVar(&o.dataRepo, "data-repo", sibling("mc26-data"), "checkout of mc26-data")
	fs.StringVar(&o.dataPreRepo, "data-pre-repo", sibling("mc26-data-pre"), "checkout of mc26-data-pre (pre-releases and snapshots)")
	fs.StringVar(&o.libRepo, "lib-repo", sibling("go-mc26"), "checkout of go-mc26")
	fs.StringVar(&o.runtime, "runtime", "", "podman or docker (detected)")
	kitSrcFlag(fs)
}

func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	var o releaseOpts
	releaseFlags(fs, &o)
	fs.BoolVar(&o.skipExtract, "skip-extract", false, "reuse temp/data/<version> when it exists")
	fs.BoolVar(&o.noSmoke, "no-smoke", false, "skip the vanilla-server smoke test")
	fs.BoolVar(&o.dataOnly, "data-only", false, "stop after the data commit and tag (a pre-release whose library needs work)")
	fs.BoolVar(&o.e2e, "e2e", false, "also run the example bots against a vanilla server before committing the library")
	fs.BoolVar(&o.push, "push", false, "push the branches and tags to origin")
	fs.Parse(args)
	if o.version == "" {
		return fmt.Errorf("--version is required")
	}
	_, _, err := runRelease(o)
	return err
}

// runRelease is release's body: data commit and tag, the library built from
// that data and tested, its commit and tag, and the push when asked. It
// returns the two tags.
func runRelease(o releaseOpts) (dataTag, libTag string, err error) {
	version, dataRepo, dataPreRepo, libRepo, runtime := &o.version, &o.dataRepo, &o.dataPreRepo, &o.libRepo, &o.runtime
	skipExtract, noSmoke, dataOnly, doE2E, push := &o.skipExtract, &o.noSmoke, &o.dataOnly, &o.e2e, &o.push
	base, err := tagBase(*version)
	if err != nil {
		return "", "", err
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
			return "", "", err
		}
	}
	meta, err := extract.ReadMeta(dataDir)
	if err != nil {
		return "", "", err
	}
	stage, err := stageData(dataDir, repo)
	if err != nil {
		return "", "", err
	}
	dataMsg := fmt.Sprintf("Extract %s (data version %d, protocol %d; mc26 %s)", meta.ID, meta.WorldVersion, meta.ProtocolVersion, meta.ExtractorCommit)
	dataTag, err = commitTree(repo, branch, stage, dataMsg, base)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", dataName, err)
	}
	if err := updateIndex(repo, strings.TrimPrefix(dataName, "mc26-")); err != nil {
		return "", "", fmt.Errorf("%s: README: %w", dataName, err)
	}
	if *dataOnly {
		if *push {
			if err := gitx.Push(repo, "main", branch, dataTag); err != nil {
				return "", "", err
			}
			logf("pushed %s %s %s", dataName, branch, dataTag)
		} else {
			logf("release %s: %s %s committed locally (not pushed); library skipped (--data-only)", *version, dataName, dataTag)
		}
		return dataTag, "", nil
	}

	// 2. library, built from the data branch checkout, smoke-tested before it is committed
	libOut := filepath.Join(paths.Temp(), "lib", *version)
	if _, err := buildLib(repo, libOut, *version, dataName+" "+dataTag, true); err != nil {
		return "", "", err
	}
	if !*noSmoke {
		if err := runSmoke(*version, paths.Kit(*version), smokePort, *runtime); err != nil {
			return "", "", err
		}
	}
	if *doE2E {
		if err := runE2E(*version, repo, paths.Kit(*version), smokePort, *runtime); err != nil {
			return "", "", err
		}
	}
	libMsg := fmt.Sprintf("Build %s from %s %s (mc26 %s)", meta.ID, dataName, dataTag, gitx.ShortHead(paths.MustRoot()))
	libTag, err = commitTree(*libRepo, branch, libOut, libMsg, base)
	if err != nil {
		return "", "", fmt.Errorf("go-mc26: %w", err)
	}
	if err := updateIndex(*libRepo, "lib"); err != nil {
		return "", "", fmt.Errorf("go-mc26: README: %w", err)
	}

	// 3. push
	if *push {
		if err := gitx.Push(repo, "main", branch, dataTag); err != nil {
			return "", "", err
		}
		if err := gitx.Push(*libRepo, "main", branch, libTag); err != nil {
			return "", "", err
		}
		logf("pushed %s %s %s and go-mc26 %s %s", dataName, branch, dataTag, branch, libTag)
	} else {
		logf("release %s: %s %s / go-mc26 %s committed locally (not pushed)", *version, dataName, dataTag, libTag)
	}
	return dataTag, libTag, nil
}

// cmdUpdate is a new Minecraft version from the manifest to local commits:
// extract, the schemas checked, the wire diff against the previous version,
// a strict build, verify over every extracted version, then the data and
// library commits and tags — never a push. It stops where a person is
// needed: a hole in a schema (the extractor), a compile error (gen/src), a
// failed test.
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	var o releaseOpts
	releaseFlags(fs, &o)
	pre := fs.Bool("pre", false, "without --version: the newest snapshot or pre-release instead of the newest release")
	skipExtract := fs.Bool("skip-extract", false, "reuse temp/data/<version> when it exists (after fixing gen/src)")
	holes := fs.Bool("allow-holes", false, "go on although a schema has a hole")
	noVerify := fs.Bool("no-verify", false, "skip smoke, crosscheck and e2e")
	noCommit := fs.Bool("no-commit", false, "stop after verify: no data or library commit")
	fs.Parse(args)
	start := time.Now()
	if o.version == "" {
		release, snapshot, err := extract.LatestVersions()
		if err != nil {
			return err
		}
		o.version = release
		if *pre {
			o.version = snapshot
		}
		logf("update: Mojang's manifest says release %s, snapshot %s: taking %s", release, snapshot, o.version)
	}
	v := o.version

	// 1. the data
	dataDir := paths.Data(v)
	if _, err := os.Stat(filepath.Join(dataDir, "_meta.json")); err != nil || !*skipExtract {
		if _, _, err := extractVersion(v, o.runtime, false); err != nil {
			return err
		}
	} else {
		logf("update: reusing %s", dataDir)
	}

	// 2. the schemas: a hole is the extractor's to close
	sc, err := schemacheck.Check(dataDir)
	if err != nil {
		return err
	}
	if !sc.OK() {
		if !*holes {
			return fmt.Errorf("the extractor does not describe %s in full: %w\n(fix data-gen/java and re-extract, or --allow-holes)", v, sc.Err())
		}
		logf("update: %d holes in the schemas, going on", len(sc.Problems))
	} else {
		logf("update: schemas of %s: %d entries and %d refs, all described", v, sc.Entries, sc.Refs)
	}

	// 3. what changed on the wire, against the newest version before this one
	all, err := extractedVersions()
	if err != nil {
		return err
	}
	prev := ""
	for _, x := range all {
		if x != v && mcver.Less(x, v) {
			prev = x
		}
	}
	if prev != "" {
		logf("update: wire changes %s → %s (packetdiff)", prev, v)
		diff := exec.Command("go", "run", "./gen/cmd/packetdiff", prev, v)
		diff.Dir = paths.MustRoot()
		diff.Env = limits.GoEnv()
		diff.Stdout, diff.Stderr = logOut, logOut
		if err := diff.Run(); err != nil {
			return fmt.Errorf("packetdiff: %w", err)
		}
		logf("update: registry and shared-type changes %s → %s (nbtdiff)", prev, v)
		diff = exec.Command("go", "run", "./gen/cmd/nbtdiff", prev, v)
		diff.Dir = paths.MustRoot()
		diff.Env = limits.GoEnv()
		diff.Stdout, diff.Stderr = logOut, logOut
		if err := diff.Run(); err != nil {
			return fmt.Errorf("nbtdiff: %w", err)
		}
	}

	// 4. the library: a compile error names the field the sources have to follow
	allowHoles = *holes
	libOut := filepath.Join(paths.Temp(), "lib", v)
	if _, err := buildLib(dataDir, libOut, v, "data "+dataDir, true); err != nil {
		return fmt.Errorf("%w\n(fix gen/src for %s, then: mc26 update --version %s --skip-extract)", err, v, v)
	}

	// 5. every extracted version against a real server: the sources changed for all of them
	if !*noVerify {
		if err := runVerify(all, []string{"build", "smoke", "crosscheck", "e2e"}, smokePort, o.runtime, true, filepath.Join(paths.Temp(), "verify")); err != nil {
			return err
		}
	}
	if *noCommit {
		logf("update %s: ok in %s, nothing committed (--no-commit)", v, time.Since(start).Round(time.Second))
		return nil
	}

	// 6. the commits, verified already: no second smoke, no push
	o.skipExtract, o.noSmoke, o.push = true, true, false
	dataTag, libTag, err := runRelease(o)
	if err != nil {
		return err
	}
	logf("update %s: ok in %s — data %s, library %s, committed locally", v, time.Since(start).Round(time.Second), dataTag, libTag)
	logf("to publish: mc26 release --version %s --skip-extract --no-smoke --push", v)
	return nil
}

// cmdIndex rewrites the README of a generated repository's main branch: what
// the repository is, and the table of its branches with their latest tags.
func cmdIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	repo := fs.String("repo", "", "checkout of mc26-data, mc26-data-pre or go-mc26")
	kind := fs.String("kind", "", "data, data-pre or lib")
	fs.Parse(args)
	if *repo == "" || *kind == "" {
		return fmt.Errorf("--repo and --kind are required")
	}
	return updateIndex(*repo, *kind)
}

// indexRow is one branch of a generated repository.
type indexRow struct {
	Branch, Version, Protocol, DataVersion, Tag string
}

// updateIndex renders gen/templates/index-<kind>.md.tmpl with the repository's
// mc-* branches (newest first), the facts each records and its latest tag, and
// commits it on main when it changed. The branch that was checked out stays so.
func updateIndex(repo, kind string) error {
	if clean, err := gitx.IsClean(repo); err != nil || !clean {
		return fmt.Errorf("%s has uncommitted changes", repo)
	}
	// every mc-* branch, local or only on origin (a fresh clone checks out one):
	// the index lists them all, read from the local branch where there is one
	out, err := gitx.Run(repo, "for-each-ref", "--format=%(refname)", "refs/heads/mc-*", "refs/remotes/origin/mc-*")
	if err != nil {
		return err
	}
	refOf := map[string]string{}
	for _, r := range strings.Fields(out) {
		if name, ok := strings.CutPrefix(r, "refs/heads/"); ok {
			refOf[name] = r
		} else if name, ok := strings.CutPrefix(r, "refs/remotes/origin/"); ok && refOf[name] == "" {
			refOf[name] = r
		}
	}
	var branches []string
	for name := range refOf {
		branches = append(branches, name)
	}
	sort.Slice(branches, func(i, j int) bool { return mcver.Less(branches[j][3:], branches[i][3:]) })
	var rows []indexRow
	for _, name := range branches {
		b := refOf[name]
		row := indexRow{Branch: name, Version: name[3:], Tag: "—"}
		if kind == "lib" {
			src, err := gitx.Run(repo, "show", b+":data/version/version.go")
			if err != nil {
				return fmt.Errorf("%s %s: %w", repo, b, err)
			}
			for _, line := range strings.Split(src, "\n") {
				line = strings.TrimSpace(line)
				if v, ok := strings.CutPrefix(line, "ProtocolVersion = "); ok {
					row.Protocol = v
				}
				if v, ok := strings.CutPrefix(line, "DataVersion = "); ok {
					row.DataVersion = v
				}
			}
		} else {
			src, err := gitx.Run(repo, "show", b+":version.json")
			if err != nil {
				return fmt.Errorf("%s %s: %w", repo, b, err)
			}
			var v struct {
				Protocol int `json:"protocol_version"`
				World    int `json:"world_version"`
			}
			if err := json.Unmarshal([]byte(src), &v); err != nil {
				return fmt.Errorf("%s %s: version.json: %w", repo, b, err)
			}
			row.Protocol, row.DataVersion = strconv.Itoa(v.Protocol), strconv.Itoa(v.World)
		}
		base, err := tagBase(row.Version)
		if err == nil {
			if tags, err := gitx.Run(repo, "tag", "--merged", b, "--list", base+"*"); err == nil && tags != "" {
				ts := strings.Fields(tags)
				sort.Slice(ts, func(i, j int) bool { return tagLess(ts[i], ts[j], base) })
				row.Tag = ts[len(ts)-1]
			}
		}
		rows = append(rows, row)
	}
	current, _ := gitx.Run(repo, "branch", "--show-current")
	if err := gitx.Checkout(repo, "main", "main"); err != nil {
		return err
	}
	root := paths.MustRoot()
	if err := render(filepath.Join(root, "gen", "templates", "index-"+kind+".md.tmpl"), filepath.Join(repo, "README.md"), map[string]any{"Branches": rows}); err != nil {
		return err
	}
	_, changed, err := gitx.Commit(repo, fmt.Sprintf("README: %d branch%s", len(rows), map[bool]string{true: "", false: "es"}[len(rows) == 1]))
	if err != nil {
		return err
	}
	if changed {
		logf("%s: README on main rewritten (%d branches)", filepath.Base(repo), len(rows))
	}
	if current != "" && current != "main" {
		return gitx.Checkout(repo, current, "main")
	}
	return nil
}

// tagLess orders two tags of one base by their number.
func tagLess(a, b, base string) bool {
	n := func(t string) int {
		rest := strings.TrimPrefix(t, base)
		if i := strings.IndexAny(rest, "-+"); i >= 0 {
			rest = rest[:i]
		}
		v, _ := strconv.Atoi(rest)
		return v
	}
	return n(a) < n(b)
}

// cmdDocs renders the documentation templates for one version.
func cmdDocs(args []string) error {
	fs := flag.NewFlagSet("docs", flag.ExitOnError)
	version := fs.String("version", "", "Minecraft version id")
	data := fs.String("data", "", "data directory (default temp/data/<version>)")
	out := fs.String("out", "", "output directory (default temp/docs/<version>)")
	internal := fs.Bool("internal", false, "keep the blocks meant for this repository's readers (where things live, the Go types)")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	if *data == "" {
		*data = paths.Data(*version)
	}
	if *out == "" {
		*out = filepath.Join(paths.Temp(), "docs", *version)
	}
	n, err := renderDocs(*version, *data, *out, *internal)
	if err != nil {
		return err
	}
	logf("docs %s: %d documents in %s", *version, n, *out)
	return nil
}

// renderDocs renders every template of gen/docs into outDir.
func renderDocs(version, dataDir, outDir string, internal bool) (int, error) {
	root := paths.MustRoot()
	tmpls, err := filepath.Glob(filepath.Join(root, "gen", "docs", "*.mc26tmpl.md"))
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	o := docgen.Options{Version: version, DataDir: dataDir, HandDir: filepath.Join(root, "gen", "hand-crafted"), Internal: internal}
	for _, t := range tmpls {
		s, err := docgen.RenderFile(t, o)
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(outDir, docgen.OutputName(t)), []byte(s), 0o644); err != nil {
			return 0, err
		}
	}
	return len(tmpls), nil
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
	kitSrcFlag(fs)
	fs.Parse(args)
	if *lib == "" {
		if *version == "" {
			return fmt.Errorf("--lib or --version is required")
		}
		*lib = filepath.Join(paths.Temp(), "lib", *version)
	}
	r, err := report.Run(report.Options{LibDir: *lib, GenRoot: filepath.Join(paths.MustRoot(), "gen"), KitSrc: kitSrc})
	if err != nil {
		return err
	}
	name := *version
	if name == "" {
		if v, err := build.ReadVersionGo(*lib); err == nil {
			name = v
		}
	}
	if name != "" {
		if _, err := os.Stat(paths.Data(name)); err == nil {
			sc, err := schemacheck.Check(paths.Data(name))
			if err != nil {
				return err
			}
			if sc.OK() {
				fmt.Printf("schemas of %s: %d entries and %d refs, all described\n\n", name, sc.Entries, sc.Refs)
			} else {
				fmt.Printf("schemas of %s: %v\n\n", name, sc.Err())
			}
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

var _ = strconv.Itoa
