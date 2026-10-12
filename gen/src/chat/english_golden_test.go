package chat_test

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/lang"
	"github.com/mj41/go-mc26/data/version"
)

// Every English string renders as it did when the languages were Go tables in the library (until
// 2026-10-12): each key through chat, with no arguments and with five, against the golden recorded from
// that build (testdata/en_us_<version>.tsv.gz: the key, the two renders, quoted, one key a line). A change to
// the .lang format, its placeholders or chat's lookup fails here; programs that read English server text
// (deaths, joins, the server's messages) would otherwise see it change silently. A version without a golden
// is skipped.
func TestEnglishGolden(t *testing.T) {
	path := filepath.Join("testdata", "en_us_"+version.Name+".tsv.gz")
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no golden for Minecraft %s", version.Name)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	chat.SetLanguage(nil) // English alone
	args := []chat.Message{chat.Text("A1"), chat.Text("B2"), chat.Text("C3"), chat.Text("D4"), chat.Text("E5")}
	seen, bad := map[string]bool{}, 0
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) != 3 {
			t.Fatalf("a golden line of %d fields: %q", len(fields), sc.Text())
		}
		var v [3]string
		for i, q := range fields {
			if v[i], err = strconv.Unquote(q); err != nil {
				t.Fatalf("golden %q: %v", q, err)
			}
		}
		key := v[0]
		seen[key] = true
		if got := chat.TranslateMsg(key).ClearString(); got != v[1] {
			bad++
			if bad <= 10 {
				t.Errorf("%s with no arguments: %q, the golden %q", key, got, v[1])
			}
		}
		if got := chat.TranslateMsg(key, args...).ClearString(); got != v[2] {
			bad++
			if bad <= 10 {
				t.Errorf("%s with five arguments: %q, the golden %q", key, got, v[2])
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if bad > 10 {
		t.Errorf("… %d renders differ in all", bad)
	}
	english := lang.English()
	if len(seen) != len(english) {
		t.Errorf("the golden has %d keys, en_us %d", len(seen), len(english))
	}
	for k := range english {
		if !seen[k] {
			t.Errorf("en_us has %q, the golden has not", k)
			break
		}
	}
}

// English inside components: a translation among another's arguments, styles, the components that follow.
func TestEnglishNested(t *testing.T) {
	chat.SetLanguage(nil)
	for _, c := range []struct {
		msg         chat.Message
		clear, ansi string
	}{
		{chat.TranslateMsg("chat.type.text", chat.Text("Steve"), chat.TranslateMsg("item.minecraft.diamond")), "<Steve> Diamond", "<Steve> Diamond"},
		{chat.TranslateMsg("death.attack.arrow", chat.Text("Steve"), chat.TranslateMsg("entity.minecraft.skeleton")), "Steve was shot by Skeleton", "Steve was shot by Skeleton"},
		{chat.TranslateMsg("multiplayer.player.joined", chat.Text("Steve")).SetColor(chat.Yellow), "Steve joined the game", "\033[93mSteve joined the game\033[0m"},
		{chat.Text("[server] ").Append(chat.TranslateMsg("multiplayer.player.left", chat.Text("Alex"))), "[server] Alex left the game", "[server] Alex left the game"},
	} {
		if got := c.msg.ClearString(); got != c.clear {
			t.Errorf("ClearString %q, want %q", got, c.clear)
		}
		if got := c.msg.String(); got != c.ansi {
			t.Errorf("String %q, want %q", got, c.ansi)
		}
	}
}
