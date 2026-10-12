// Package lang is Minecraft's translations: the languages a program has, and the format their files are in.
//
// English (en_us) is built in: the language every client has, and the one a key missing from another
// language falls back to. Any other language is registered by the program (Register), from one of two
// places:
//
//   - a module of github.com/mj41/go-mc26-lang, one per language, tagged per Minecraft version:
//     go get github.com/mj41/go-mc26-lang/cs-cz@v0.263.0, then lang.Register(cs_cz.Locale, cs_cz.Data);
//   - langget (go run github.com/mj41/go-mc26/cmd/langget, usually from a //go:generate line), which reads
//     the languages a project's mc26-langs file lists — some, or "all" — fetches each by its checksum and
//     writes them into one of the project's packages, registered when that package is imported.
//
// Either way a language's file must be this version's: Register checks it against Manifest, the checksum of
// every language of this Minecraft version, so a file of another version is refused, not half used. A
// language's strings are parsed the first time they are asked for (Get). chat.SetLocale renders chat in one.
//
// A .lang file is the language's keys and strings, sorted by key, each followed by a NUL byte; Java's
// positional placeholders (%2$s) are written as Go's (%[2]s). ParseJSON reads the game's own .json files.
package lang

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed en_us.lang
var englishFile []byte

var (
	mu     sync.RWMutex
	files  = map[string][]byte{"en_us": englishFile} // registered, not yet parsed
	parsed = map[string]map[string]string{}
)

var english = sync.OnceValue(func() map[string]string { return MustParse(englishFile) })

// English returns en_us's translations. The map is shared by every caller and by chat's rendering: read it,
// never write it.
func English() map[string]string { return english() }

// Register adds a language from its .lang file. The file must be this version's: its checksum is the one
// Manifest has for the locale. Registering a locale again replaces it (with the same file: nothing changes).
func Register(locale string, data []byte) error {
	want, ok := Manifest[locale]
	if !ok {
		return fmt.Errorf("lang: %q is no language of Minecraft %s", locale, Version)
	}
	if got := Sum(data); got != want {
		return fmt.Errorf("lang: %s's file is not Minecraft %s's (its checksum %s, want %s): get it again for this version (go generate, or the go-mc26-lang module's %s tag)", locale, Version, got, want, Version)
	}
	mu.Lock()
	defer mu.Unlock()
	files[locale] = data
	delete(parsed, locale)
	return nil
}

// MustRegister is Register that panics: for a package's init, whose files a build put there.
func MustRegister(locale string, data []byte) {
	if err := Register(locale, data); err != nil {
		panic(err)
	}
}

// Get returns a registered language's translations, parsed the first time. The map is shared (every caller,
// chat's rendering): read it, never write it.
func Get(locale string) (map[string]string, error) {
	if locale == "en_us" {
		return English(), nil
	}
	mu.RLock()
	m, ok := parsed[locale]
	data, registered := files[locale]
	mu.RUnlock()
	if ok {
		return m, nil
	}
	if !registered {
		return nil, fmt.Errorf("lang: %q is not registered (registered: %s)", locale, strings.Join(Locales(), ", "))
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("lang: %s: %w", locale, err)
	}
	mu.Lock()
	parsed[locale] = m
	mu.Unlock()
	return m, nil
}

// Locales returns the registered languages, en_us among them, sorted.
func Locales() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(files))
	for l := range files {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Sum is a language file's checksum, as Manifest lists it: its content address.
func Sum(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// Parse reads a .lang file.
func Parse(data []byte) (map[string]string, error) {
	s := string(data) // one copy: the keys and strings are slices of it
	n := strings.Count(s, "\x00")
	if n%2 != 0 || (len(s) > 0 && s[len(s)-1] != 0) {
		return nil, errors.New("not a .lang file: its keys and strings are not NUL-terminated pairs")
	}
	m := make(map[string]string, n/2)
	for len(s) > 0 {
		i := strings.IndexByte(s, 0)
		k := s[:i]
		s = s[i+1:]
		j := strings.IndexByte(s, 0)
		m[k] = s[:j]
		s = s[j+1:]
	}
	return m, nil
}

// MustParse is Parse that panics, for a file this module or a build put in place.
func MustParse(data []byte) map[string]string {
	m, err := Parse(data)
	if err != nil {
		panic("lang: " + err.Error())
	}
	return m
}

var javaArg = regexp.MustCompile(`%(\d+)\$s`)

// ParseJSON reads a language file as the game ships it (assets/minecraft/lang/<locale>.json), its
// placeholders written as Go's, as in a .lang file.
func ParseJSON(data []byte) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	for k, v := range m {
		if strings.Contains(v, "$s") {
			m[k] = javaArg.ReplaceAllString(v, "%[$1]s")
		}
	}
	return m, nil
}
