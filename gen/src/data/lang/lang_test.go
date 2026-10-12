package lang

import (
	"strings"
	"testing"
)

// English is built in, whole, and is the file the manifest has for en_us.
func TestEnglish(t *testing.T) {
	if got, want := Sum(englishFile), Manifest["en_us"]; got != want {
		t.Fatalf("the built-in en_us is %s, the manifest has %s", got, want)
	}
	if len(English()) < 1000 {
		t.Fatalf("en_us has %d strings", len(English()))
	}
}

// The English server text bots read (joins, chat, deaths, a full server) is what the game has: a program
// that parses it must not see it change.
func TestEnglishServerText(t *testing.T) {
	for key, want := range map[string]string{
		"chat.type.text":                     "<%s> %s",
		"multiplayer.player.joined":          "%s joined the game",
		"multiplayer.player.left":            "%s left the game",
		"death.attack.arrow":                 "%[1]s was shot by %[2]s", // the game's %1$s, as Go writes it
		"multiplayer.disconnect.server_full": "The server is full!",
	} {
		if got := English()[key]; got != want {
			t.Errorf("en_us %s = %q, want %q", key, got, want)
		}
	}
}

// A language is registered only as this version's file, and parsed when it is first asked for.
func TestRegister(t *testing.T) {
	if err := Register("xx_yy", nil); err == nil || !strings.Contains(err.Error(), "no language") {
		t.Errorf("an unknown locale: %v", err)
	}
	if err := Register("cs_cz", []byte("a\x00b\x00")); err == nil || !strings.Contains(err.Error(), "not Minecraft") {
		t.Errorf("a file not this version's: %v", err)
	}
	if _, err := Get("cs_cz"); err == nil {
		t.Error("cs_cz is not registered, and Get found it")
	}
	if m, err := Get("en_us"); err != nil || m["chat.type.text"] == "" {
		t.Errorf("en_us: %v", err)
	}
	if ls := Locales(); len(ls) == 0 || ls[0] != "en_us" && !contains(ls, "en_us") {
		t.Errorf("the registered languages %v lack en_us", ls)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// The format: NUL-terminated pairs; anything else is refused. The game's JSON reads with its placeholders as
// Go's.
func TestParse(t *testing.T) {
	m, err := Parse([]byte("a\x00one\x00b\x00%[2]s %[1]s\x00"))
	if err != nil || len(m) != 2 || m["a"] != "one" || m["b"] != "%[2]s %[1]s" {
		t.Fatalf("Parse: %v %v", m, err)
	}
	for _, bad := range []string{"a\x00", "a\x00b", "a"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) took it", bad)
		}
	}
	j, err := ParseJSON([]byte(`{"k":"%1$s and %2$s","p":"100%%"}`))
	if err != nil || j["k"] != "%[1]s and %[2]s" || j["p"] != "100%%" {
		t.Fatalf("ParseJSON: %v %v", j, err)
	}
}
