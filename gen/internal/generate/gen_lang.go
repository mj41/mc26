// gen_lang writes the library's languages: data/lang/en_us.lang (English, built in) and
// data/lang/manifest_gen.go, the checksum of every language of the version — what package lang checks a
// language a program registers against. The other languages are not in the library: mc26 lang writes them
// as the modules of go-mc26-lang, from the same bytes (LangFiles).
//
// Language JSONs are downloaded by the Go host from the Mojang asset index
// CDN into <jsonDir>/lang/ before the container extraction phase runs.
//
// A .lang file is a language's keys and strings, sorted by key, each followed by a NUL byte, Java's
// positional placeholders (%2$s) written as Go's (%[2]s). package lang (gen/src/data/lang) reads it.
package generate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var javaFmtArg = regexp.MustCompile(`%(\d+)\$s`)

// EncodeLang is a language's strings as a .lang file. A NUL inside a key or a string is an error: the
// format has none.
func EncodeLang(m map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		v := m[k]
		if strings.ContainsRune(k, 0) || strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("a NUL in %q", k)
		}
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(javaFmtArg.ReplaceAllString(v, "%[$1]s"))
		b.WriteByte(0)
	}
	return b.Bytes(), nil
}

// LangSum is a .lang file's checksum, as package lang's Manifest has it.
func LangSum(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// LangFiles is every language of an extraction as .lang files, by locale ("en_us").
func LangFiles(jsonDir string) (map[string][]byte, error) {
	dir := filepath.Join(jsonDir, "lang")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("languages: %w", err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		locale := strings.TrimSuffix(e.Name(), ".json")
		var m map[string]string
		if err := readJSON(filepath.Join(dir, e.Name()), &m); err != nil {
			return nil, fmt.Errorf("language %s: %w", locale, err)
		}
		if out[locale], err = EncodeLang(m); err != nil {
			return nil, fmt.Errorf("language %s: %w", locale, err)
		}
	}
	if out["en_us"] == nil {
		return nil, fmt.Errorf("languages: no en_us in %s", dir)
	}
	return out, nil
}

func genLang(jsonDir, goMCRoot string) error {
	files, err := LangFiles(jsonDir)
	if err != nil {
		return fmt.Errorf("genLang: %w", err)
	}
	dir := filepath.Join(goMCRoot, "data", "lang")
	if err := writeFile(filepath.Join(dir, "en_us.lang"), files["en_us"]); err != nil {
		return fmt.Errorf("genLang: %w", err)
	}

	locales := make([]string, 0, len(files))
	for l := range files {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	var b strings.Builder
	b.WriteString(generatedHeader("gen_lang.go", "lang/*.json"))
	b.WriteString("package lang\n\n")
	fmt.Fprintf(&b, "// Version is the Minecraft version the languages are of.\nconst Version = %q\n\n", jsonVersion)
	b.WriteString("// Manifest is the checksum (Sum) of every language of this version, by locale: a language's file is\n")
	b.WriteString("// this version's when its checksum is the one here (Register).\nvar Manifest = map[string]string{\n")
	for _, l := range locales {
		fmt.Fprintf(&b, "\t%q: %q,\n", l, LangSum(files[l]))
	}
	b.WriteString("}\n")
	if err := writeGo(filepath.Join(dir, "manifest_gen.go"), b.String()); err != nil {
		return fmt.Errorf("genLang: %w", err)
	}
	logf("genLang: en_us built in, %d languages in the manifest", len(locales))
	return nil
}
