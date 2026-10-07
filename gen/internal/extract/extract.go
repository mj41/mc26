// Package extract produces the JSON data set of one Minecraft version:
//
//  1. the Go host downloads the server jar and the language files from
//     Mojang (the only steps that need the network);
//  2. a JDK 25 container runs the data generator and the Java extractors of
//     data-gen/java against the jar, writing <out>/*.json;
//  3. _meta.json records what was extracted by what.
//
// Minecraft 26.1 and later only: the jars are unobfuscated, so the extractors
// read real names.
package extract

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/mj41/mc26/gen/internal/limits"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	manifestURL  = "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json"
	assetCDNBase = "https://resources.download.minecraft.net"
	// JDKImage runs the extractors; Minecraft 26.x needs Java 25.
	JDKImage = "docker.io/library/eclipse-temurin:25-jdk"
)

// Options configures one extraction.
type Options struct {
	Version  string // Mojang version id: "26.2", "26.3-pre-2", …
	OutDir   string // receives the JSON; its base name must equal Version
	CacheDir string // server jars, one per version
	JavaDir  string // data-gen/java
	Runtime  string // "podman" or "docker"; detected when empty
	DryRun   bool
	// Only runs just these extractors (GenNbtSchema, …) into an existing extraction: no
	// downloads, no data generator; for iterating on one extractor.
	Only []string
	// Extractor identifies the code that ran, for _meta.json.
	ExtractorRepo, ExtractorCommit string
	Log                            func(format string, args ...any)
}

// Meta is _meta.json: what was extracted, from what, by what.
type Meta struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	WorldVersion    int    `json:"world_version"`
	ProtocolVersion int    `json:"protocol_version"`
	JavaVersion     int    `json:"java_version"`
	Stable          bool   `json:"stable"`
	ServerJarURL    string `json:"server_jar_url"`
	ServerJarSHA1   string `json:"server_jar_sha1"`
	ExtractorRepo   string `json:"extractor_repo"`
	ExtractorCommit string `json:"extractor_commit"`
	ExtractedAt     string `json:"extracted_at"`
}

// Run extracts o.Version into o.OutDir and returns the recorded metadata.
func Run(o Options) (*Meta, error) {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if err := CheckVersion(o.Version); err != nil {
		return nil, err
	}
	if filepath.Base(o.OutDir) != o.Version {
		return nil, fmt.Errorf("out dir %s must be named after the version %s (the container writes /jsons/<version>)", o.OutDir, o.Version)
	}
	if _, err := os.Stat(filepath.Join(o.JavaDir, "ExtractAll.java")); err != nil {
		return nil, fmt.Errorf("ExtractAll.java not found in %s", o.JavaDir)
	}
	for _, d := range []string{o.CacheDir, o.OutDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if o.Runtime == "" {
		o.Runtime = DetectRuntime()
		if o.Runtime == "" {
			return nil, fmt.Errorf("neither podman nor docker found in PATH")
		}
	}

	o.Log("Phase 1: downloads (host)")
	jar, err := ServerJar(o.CacheDir, o.Version, o.Log)
	if err != nil {
		return nil, err
	}
	if len(o.Only) == 0 {
		if err := downloadLangFiles(filepath.Join(o.OutDir, "lang"), o.Version, o.Log); err != nil {
			return nil, err
		}
	}

	o.Log("Phase 2: extraction in %s (%s)", JDKImage, o.Runtime)
	args := containerArgs(o.Runtime, o.Version, o.CacheDir, filepath.Dir(o.OutDir), o.JavaDir)
	if len(o.Only) > 0 {
		args = append(args, "--only="+strings.Join(o.Only, ","))
	}
	if o.DryRun {
		o.Log("  %s %s", o.Runtime, strings.Join(args, " "))
		return nil, nil
	}
	pull := exec.Command(o.Runtime, "pull", "--quiet", JDKImage)
	pull.Stdout, pull.Stderr = os.Stderr, os.Stderr
	if err := pull.Run(); err != nil {
		o.Log("  pull failed (%v), using the local image if any", err)
	}
	cmd := exec.Command(o.Runtime, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("extraction container: %w", err)
	}

	meta, err := writeMeta(o, jar)
	if err != nil {
		return nil, err
	}
	report(o.OutDir, o.Log)
	return meta, nil
}

// CheckVersion accepts Minecraft 26.1 and later ("26.2", "26.3-pre-2", "26.10-rc-1").
func CheckVersion(v string) error {
	major, rest, _ := strings.Cut(v, ".")
	m, err := strconv.Atoi(major)
	if err != nil || m < 26 || rest == "" {
		return fmt.Errorf("version %q: only Minecraft 26.1 and later are supported (unobfuscated jars)", v)
	}
	if m == 26 {
		minor := rest
		if i := strings.IndexAny(minor, "-"); i >= 0 {
			minor = minor[:i]
		}
		if n, err := strconv.Atoi(minor); err != nil || n < 1 {
			return fmt.Errorf("version %q: only Minecraft 26.1 and later are supported", v)
		}
	}
	return nil
}

// DetectRuntime returns "podman" or "docker", whichever is in PATH.
func DetectRuntime() string {
	for _, r := range []string{"podman", "docker"} {
		if _, err := exec.LookPath(r); err == nil {
			return r
		}
	}
	return ""
}

func containerArgs(runtime, version, cacheDir, jsonsDir, javaDir string) []string {
	args := []string{
		"run", "--rm",
		"--memory", limits.ExtractContainerMemory,
		"-e", "JAVA_TOOL_OPTIONS=" + limits.ExtractJavaHeap, // every JVM in it: the data generator and the extractors
		"--security-opt", "label=disable", // SELinux: allow the bind mounts
		"-v", cacheDir + ":/cache",
		"-v", jsonsDir + ":/jsons",
		"-v", javaDir + ":/java:ro",
		"-v", filepath.Clean(filepath.Join(javaDir, "..", "..", "gen", "hand-crafted")) + ":/hand:ro",
		"-e", "MC_PRIMS_JSON=/hand/prims.json", // which Java members the schema's primitive names stand for
		"-e", "MC26_TRACE=" + os.Getenv("MC26_TRACE"),
	}
	if runtime != "podman" { // docker: write the output as the host user
		if uid := os.Getuid(); uid > 0 {
			args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
		}
	}
	return append(args, JDKImage, "java", "--source", "21", "/java/ExtractAll.java", version)
}

// ---- Mojang downloads ----------------------------------------------------------

type manifest struct {
	Versions []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"versions"`
}

type versionDetail struct {
	Downloads struct {
		Server struct {
			URL  string `json:"url"`
			SHA1 string `json:"sha1"`
		} `json:"server"`
		Client struct {
			URL  string `json:"url"`
			SHA1 string `json:"sha1"`
		} `json:"client"`
	} `json:"downloads"`
	AssetIndex struct {
		URL string `json:"url"`
	} `json:"assetIndex"`
}

type assetIndex struct {
	Objects map[string]struct {
		Hash string `json:"hash"`
	} `json:"objects"`
}

// ServerJar returns the cached server jar of version, downloading it from
// Mojang's manifest when missing, and verifying the sha1 either way. The sha1
// the manifest gave is kept beside the jar (<version>-server.jar.sha1): a jar
// that matches it is used without asking Mojang, so a cache filled once (a
// test bench's image) runs with no way out.
func ServerJar(cacheDir, version string, log func(string, ...any)) (string, error) {
	path := filepath.Join(cacheDir, version+"-server.jar")
	if want, err := os.ReadFile(path + ".sha1"); err == nil {
		if sum, err := fileSHA1(path); err == nil && sum == strings.TrimSpace(string(want)) {
			log("  server jar cached: %s", path)
			return path, nil
		}
	}
	detail, err := fetchDetail(version)
	if err != nil {
		return "", err
	}
	if detail.Downloads.Server.URL == "" {
		return "", fmt.Errorf("version %s has no server download", version)
	}
	sha1 := detail.Downloads.Server.SHA1
	if sum, err := fileSHA1(path); err == nil && sum == sha1 {
		log("  server jar cached: %s", path)
		return path, os.WriteFile(path+".sha1", []byte(sha1+"\n"), 0o644)
	}
	log("  downloading server jar %s", detail.Downloads.Server.URL)
	if err := download(detail.Downloads.Server.URL, path); err != nil {
		return "", err
	}
	if sum, err := fileSHA1(path); err != nil || sum != sha1 {
		return "", fmt.Errorf("server jar sha1 %s, manifest says %s", sum, sha1)
	}
	return path, os.WriteFile(path+".sha1", []byte(sha1+"\n"), 0o644)
}

// ClientJar returns the cached client jar of version, downloading it from
// Mojang's manifest when missing. Nothing in the pipeline needs it: it is there
// for reading, when a rule has to be checked against the code on the other end
// of the wire.
func ClientJar(cacheDir, version string, log func(string, ...any)) (string, error) {
	detail, err := fetchDetail(version)
	if err != nil {
		return "", err
	}
	if detail.Downloads.Client.URL == "" {
		return "", fmt.Errorf("version %s has no client download", version)
	}
	path := filepath.Join(cacheDir, version+"-client.jar")
	if sum, err := fileSHA1(path); err == nil && sum == detail.Downloads.Client.SHA1 {
		log("  client jar cached: %s", path)
		return path, nil
	}
	log("  downloading client jar %s", detail.Downloads.Client.URL)
	if err := download(detail.Downloads.Client.URL, path); err != nil {
		return "", err
	}
	if sum, err := fileSHA1(path); err != nil || sum != detail.Downloads.Client.SHA1 {
		return "", fmt.Errorf("client jar sha1 %s, manifest says %s", sum, detail.Downloads.Client.SHA1)
	}
	return path, nil
}

// LatestVersions returns the newest release and the newest snapshot ids of
// Mojang's manifest.
func LatestVersions() (release, snapshot string, err error) {
	var m manifest
	if err := getJSON(manifestURL, &m); err != nil {
		return "", "", err
	}
	for _, v := range m.Versions {
		if release == "" && v.Type == "release" {
			release = v.ID
		}
		if snapshot == "" && v.Type == "snapshot" {
			snapshot = v.ID
		}
		if release != "" && snapshot != "" {
			break
		}
	}
	return release, snapshot, nil
}

func fetchDetail(version string) (*versionDetail, error) {
	var m manifest
	if err := getJSON(manifestURL, &m); err != nil {
		return nil, fmt.Errorf("version manifest: %w", err)
	}
	for _, v := range m.Versions {
		if v.ID == version {
			var d versionDetail
			if err := getJSON(v.URL, &d); err != nil {
				return nil, fmt.Errorf("version %s: %w", version, err)
			}
			return &d, nil
		}
	}
	return nil, fmt.Errorf("version %s not in Mojang's manifest", version)
}

func downloadLangFiles(langDir, version string, log func(string, ...any)) error {
	if err := os.MkdirAll(langDir, 0o755); err != nil {
		return err
	}
	detail, err := fetchDetail(version)
	if err != nil {
		return err
	}
	var idx assetIndex
	if err := getJSON(detail.AssetIndex.URL, &idx); err != nil {
		return fmt.Errorf("asset index: %w", err)
	}
	var total, downloaded int
	for key, obj := range idx.Objects {
		if !strings.HasPrefix(key, "minecraft/lang/") || !strings.HasSuffix(key, ".json") {
			continue
		}
		total++
		dest := filepath.Join(langDir, strings.TrimPrefix(key, "minecraft/lang/"))
		if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
			continue
		}
		if err := download(assetCDNBase+"/"+obj.Hash[:2]+"/"+obj.Hash, dest); err != nil {
			return fmt.Errorf("language %s: %w", key, err)
		}
		downloaded++
	}
	log("  languages: %d files, %d downloaded", total, downloaded)
	return nil
}

func getJSON(url string, v any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func download(url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func fileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- metadata -------------------------------------------------------------------

func writeMeta(o Options, jar string) (*Meta, error) {
	var v struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		WorldVersion    int    `json:"world_version"`
		ProtocolVersion int    `json:"protocol_version"`
		JavaVersion     int    `json:"java_version"`
		Stable          bool   `json:"stable"`
	}
	data, err := os.ReadFile(filepath.Join(o.OutDir, "version.json"))
	if err != nil {
		return nil, fmt.Errorf("extraction produced no version.json: %w", err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	detail, err := fetchDetail(o.Version)
	if err != nil {
		return nil, err
	}
	sum, _ := fileSHA1(jar)
	meta := &Meta{
		ID: v.ID, Name: v.Name, WorldVersion: v.WorldVersion, ProtocolVersion: v.ProtocolVersion,
		JavaVersion: v.JavaVersion, Stable: v.Stable,
		ServerJarURL: detail.Downloads.Server.URL, ServerJarSHA1: sum,
		ExtractorRepo: o.ExtractorRepo, ExtractorCommit: o.ExtractorCommit,
		ExtractedAt: time.Now().UTC().Format(time.RFC3339),
	}
	out, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	return meta, os.WriteFile(filepath.Join(o.OutDir, "_meta.json"), append(out, '\n'), 0o644)
}

// ReadMeta reads _meta.json of an extracted directory.
func ReadMeta(dir string) (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "_meta.json"))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s/_meta.json: %w", dir, err)
	}
	return &m, nil
}

func report(dir string, log func(string, ...any)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
			log("  %-28s %8.1f KB", e.Name(), float64(fi.Size())/1024)
		}
	}
	log("  %-28s %8.1f MB", "total (without lang/)", float64(total)/1024/1024)
}
