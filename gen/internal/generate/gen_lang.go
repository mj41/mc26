// gen_lang generates data/lang/<locale>/<locale>.go for every language.
//
// Language JSONs are downloaded by the Go host from the Mojang asset index
// CDN into <jsonDir>/lang/ before the container extraction phase runs.
//
// Java format strings (%2$s) are converted to Go format strings (%[2]s).
//
// The en_us package is the "default" language (no chat.SetLanguage init).
// All other packages call chat.SetLanguage(Map) in their init() function.
package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var javaFmtArg = regexp.MustCompile(`%(\d+)\$s`)

// transJavaFormat converts Java positional format strings to Go format.
// e.g., "%2$s" → "%[2]s"
func transJavaFormat(s string) string {
	return javaFmtArg.ReplaceAllStringFunc(s, func(m string) string {
		var idx int
		fmt.Sscanf(m, "%%%d$s", &idx)
		return fmt.Sprintf("%%[%d]s", idx)
	})
}

func genLang(jsonDir, goMCRoot string) error {
	langJsonDir := filepath.Join(jsonDir, "lang")

	entries, err := os.ReadDir(langJsonDir)
	if err != nil {
		return fmt.Errorf("genLang: reading lang dir: %w", err)
	}

	generated := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json") // e.g. "en_us", "de_de"
		if err := genOneLang(langJsonDir, goMCRoot, name); err != nil {
			return err
		}
		generated++
	}

	logf("genLang: generated %d language files", generated)
	return nil
}

// genOneLang generates data/lang/<dir>/<name>.go for a single language.
func genOneLang(langJsonDir, goMCRoot, name string) error {
	jsonPath := filepath.Join(langJsonDir, name+".json")

	// Directory name: underscores → hyphens (e.g. "en_us" → "en-us").
	dirName := strings.ReplaceAll(name, "_", "-")
	outPath := filepath.Join(goMCRoot, "data", "lang", dirName, name+".go")

	var langMap map[string]string
	if err := readJSON(jsonPath, &langMap); err != nil {
		return fmt.Errorf("genLang(%s): %w", name, err)
	}

	// Apply Java→Go format string conversion.
	for k, v := range langMap {
		if javaFmtArg.MatchString(v) {
			langMap[k] = transJavaFormat(v)
		}
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(langMap))
	for k := range langMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Package name is the locale with hyphens removed (Go package names
	// can't contain hyphens). For most locales name == package name since
	// they use underscores (e.g. "de_de"). For special cases like "bar"
	// or "brb" it's already fine.
	pkgName := name

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_lang.go", name+".json"))

	buf.WriteString("\npackage ")
	buf.WriteString(pkgName)
	buf.WriteString("\n")

	// Non-English languages register themselves via init().
	if name != "en_us" {
		buf.WriteString("\nimport \"github.com/mj41/go-mc26/chat\"\n\nfunc init() { chat.SetLanguage(Map) }\n")
	}

	buf.WriteString("\nvar Map = map[string]string{\n")
	for _, k := range keys {
		fmt.Fprintf(&buf, "\t%q: %q,\n", k, langMap[k])
	}
	buf.WriteString("}\n")

	if err := writeFile(outPath, []byte(buf.String())); err != nil {
		return fmt.Errorf("genLang(%s): %w", name, err)
	}
	return nil
}
