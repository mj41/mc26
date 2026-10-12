package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mj41/go-mc26/data/lang"
	"github.com/mj41/go-mc26/nbt"
)

func TestTranslate(t *testing.T) {
	for _, c := range []struct {
		tmpl string
		args []string
		want string
	}{
		{"plain", []string{"a"}, "plain"}, // an argument no placeholder names is not shown
		{"%s and %s", []string{"a", "b"}, "a and b"},
		{"%2$s before %1$s", []string{"a", "b"}, "b before a"},   // the game's form, as a fallback carries it
		{"%[2]s before %[1]s", []string{"a", "b"}, "b before a"}, // gen_lang's form
		// a positional argument does not move the next %s on (translation.test.complex)
		{"Prefix, %s%[2]s again %s and %[1]s lastly %s and also %[1]s again!", []string{"1", "2", "3"},
			"Prefix, 12 again 2 and 1 lastly 3 and also 1 again!"},
		{"100%% sure, %s", []string{"a"}, "100% sure, a"},
		{"%%s", []string{"a"}, "%s"},
		{"Grüße, %s 你好", []string{"Ünï"}, "Grüße, Ünï 你好"},
		// the template as it is, without the arguments
		{"%s and %s", []string{"a"}, "%s and %s"},
		{"%3$s", []string{"a", "b"}, "%3$s"},
		{"%0$s", []string{"a"}, "%0$s"},
		{"%d apples", []string{"5"}, "%d apples"},
		{"%2$d apples", []string{"5", "6"}, "%2$d apples"},
		{"50% off %s", []string{"a"}, "50% off %s"},
		{"%s ends in %", []string{"a"}, "%s ends in %"},
	} {
		if got := translate(c.tmpl, c.args); got != c.want {
			t.Errorf("translate(%q, %q) = %q, want %q", c.tmpl, c.args, got, c.want)
		}
	}
}

func TestTranslation_lookup(t *testing.T) {
	t.Cleanup(func() { SetLanguage(lang.English()) })
	SetLanguage(map[string]string{"translation.test.world": "Welt"})
	for _, c := range []struct {
		msg  Message
		want string
	}{
		{Message{Translate: "translation.test.world"}, "Welt"},                                                     // the selected language
		{Message{Translate: "translation.test.none", Fallback: "unused"}, "Hello, world!"},                         // then en_us
		{Message{Translate: "custom.greeting", Fallback: "Hi %s", With: TranslateArgs{Text("Steve")}}, "Hi Steve"}, // then the fallback
		{Message{Translate: "custom.greeting", With: TranslateArgs{Text("Steve")}}, "custom.greeting"},             // then the key, without the arguments
	} {
		if got := c.msg.ClearString(); got != c.want {
			t.Errorf("%+v: ClearString() = %q, want %q", c.msg, got, c.want)
		}
	}
}

// A component as /tellraw sends one (vanilla's own components carry no
// fallback): decoded from JSON and from the network NBT, shown by its fallback,
// and written back with it.
func TestMessage_fallback(t *testing.T) {
	SetLanguage(lang.English())
	const want = "Steve found 3 diamonds"

	var fromJSON Message
	if err := json.Unmarshal([]byte(`{"translate":"custom.found","fallback":"%1$s found %2$s diamonds","with":["Steve","3"]}`), &fromJSON); err != nil {
		t.Fatal(err)
	}
	if fromJSON.Fallback != "%1$s found %2$s diamonds" {
		t.Errorf("JSON: Fallback = %q", fromJSON.Fallback)
	}
	if got := fromJSON.ClearString(); got != want {
		t.Errorf("JSON: ClearString() = %q, want %q", got, want)
	}
	out, err := json.Marshal(fromJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"fallback":"%1$s found %2$s diamonds"`) {
		t.Errorf("JSON: written back without its fallback: %s", out)
	}

	// a string and an int in one list: the server wraps each in a compound under "", as NbtOps does
	data, err := nbt.Marshal(nbt.StringifiedMessage(`{translate: "custom.found", fallback: "%1$s found %2$s diamonds", with: [{"": "Steve"}, {"": 3}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var fromNBT Message
	if err := nbt.Unmarshal(data, &fromNBT); err != nil {
		t.Fatal(err)
	}
	if got := fromNBT.ClearString(); got != want {
		t.Errorf("NBT: ClearString() = %q, want %q", got, want)
	}
	if got := fromNBT.String(); got != want {
		t.Errorf("NBT: String() = %q, want %q", got, want)
	}
}

// The arguments of a JSON component may be numbers and booleans, as the
// game's ARG_CODEC takes them: each shown as its text, written back as it came.
func TestTranslateArgs_UnmarshalJSON_primitives(t *testing.T) {
	SetLanguage(lang.English())
	for _, c := range []struct{ json, text, back string }{
		{`{"translate":"commands.fill.success","with":[512]}`, "Successfully filled 512 block(s)", `"with":[512]`},
		{`{"translate":"custom.mixed","fallback":"%s, %s, %s, %s","with":["Steve",-3.50,true,{"text":"x"}]}`,
			"Steve, -3.50, true, x", `"with":[{"text":"Steve"},-3.50,true,{"text":"x"}]`},
	} {
		var m Message
		if err := json.Unmarshal([]byte(c.json), &m); err != nil {
			t.Errorf("%s: %v", c.json, err)
			continue
		}
		if got := m.ClearString(); got != c.text {
			t.Errorf("%s: ClearString() = %q, want %q", c.json, got, c.text)
		}
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), c.back) {
			t.Errorf("%s: written back as %s, want it to contain %s", c.json, out, c.back)
		}
	}
	var m Message
	if err := json.Unmarshal([]byte(`{"translate":"k","with":[null]}`), &m); err == nil {
		t.Errorf("a null argument decoded (%#v); the game rejects it", m.With)
	}
}
