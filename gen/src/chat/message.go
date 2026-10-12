// Package chat is Minecraft's text component, Message: text or a translation with
// its arguments, its style, click and hover events, and the components that follow
// it (Extra). Build one with Text or TranslateMsg. The protocol carries it as NBT
// (Message's ReadFrom and WriteTo); JsonMessage is the same message where the
// protocol still writes JSON.
//
// String renders a message with ANSI colours, ClearString without them. A
// translation is looked up the way the game looks it up: the language set with
// SetLanguage, then en_us, then the component's fallback, then the key itself.
package chat

import (
	"regexp"
	"strings"

	"github.com/mj41/go-mc26/data/lang"
	"github.com/mj41/go-mc26/nbt"
)

const (
	Chat = iota
	System
	GameInfo
	SayCommand
	MsgCommand
	TeamMsgCommand
	EmoteCommand
	TellrawCommand
)

// Colors
const (
	Black       = "black"
	DarkBlue    = "dark_blue"
	DarkGreen   = "dark_green"
	DarkAqua    = "dark_aqua"
	DarkRed     = "dark_red"
	DarkPurple  = "dark_purple"
	Gold        = "gold"
	Gray        = "gray"
	DarkGray    = "dark_gray"
	Blue        = "blue"
	Green       = "green"
	Aqua        = "aqua"
	Red         = "red"
	LightPurple = "light_purple"
	Yellow      = "yellow"
	White       = "white"
)

// Message is a text component: its own text or translation, its style, and the
// components after it (Extra), which inherit the style.
type Message struct {
	Text string `json:"text" nbt:"text"`

	Bold          bool `json:"bold,omitempty" nbt:"bold,omitempty"`
	Italic        bool `json:"italic,omitempty" nbt:"italic,omitempty"`
	UnderLined    bool `json:"underlined,omitempty" nbt:"underlined,omitempty"`
	StrikeThrough bool `json:"strikethrough,omitempty" nbt:"strikethrough,omitempty"`
	Obfuscated    bool `json:"obfuscated,omitempty" nbt:"obfuscated,omitempty"` // shown as changing random characters
	// Font is the font's resource location: minecraft:default, minecraft:uniform,
	// minecraft:alt, or one of a resource pack.
	Font string `json:"font,omitempty" nbt:"font,omitempty"`
	// Color is a color name (Black … White, as the constants of this package) or
	// "#rrggbb".
	Color string `json:"color,omitempty" nbt:"color,omitempty"`

	// Insertion contains text to insert. Only used for messages in chat.
	// When shift is held, clicking the component inserts the given text
	// into the chat box at the cursor (potentially replacing selected text).
	Insertion  string      `json:"insertion,omitempty" nbt:"insertion,omitempty"`
	ClickEvent *ClickEvent `json:"click_event,omitempty" nbt:"click_event,omitempty"`
	HoverEvent *HoverEvent `json:"hover_event,omitempty" nbt:"hover_event,omitempty"`

	Translate string        `json:"translate,omitempty" nbt:"translate,omitempty"`
	Fallback  string        `json:"fallback,omitempty" nbt:"fallback,omitempty"` // shown when no language has Translate
	With      TranslateArgs `json:"with,omitempty" nbt:"with,omitempty"`
	Extra     []Message     `json:"extra,omitempty" nbt:"extra,omitempty"`

	raw nbt.RawMessage // what it was read from (Raw)
}

type TranslateArgs []any

// Same as Message, but "Text" is omitempty
type translateMsg struct {
	Text string `json:"text,omitempty" nbt:"text,omitempty"`

	Bold          bool `json:"bold,omitempty" nbt:"bold,omitempty"`
	Italic        bool `json:"italic,omitempty" nbt:"italic,omitempty"`
	UnderLined    bool `json:"underlined,omitempty" nbt:"underlined,omitempty"`
	StrikeThrough bool `json:"strikethrough,omitempty" nbt:"strikethrough,omitempty"`
	Obfuscated    bool `json:"obfuscated,omitempty" nbt:"obfuscated,omitempty"`

	Font  string `json:"font,omitempty" nbt:"font,omitempty"`
	Color string `json:"color,omitempty" nbt:"color,omitempty"`

	Insertion  string      `json:"insertion,omitempty" nbt:"insertion,omitempty"`
	ClickEvent *ClickEvent `json:"click_event,omitempty" nbt:"click_event,omitempty"`
	HoverEvent *HoverEvent `json:"hover_event,omitempty" nbt:"hover_event,omitempty"`

	Translate string        `json:"translate,omitempty" nbt:"translate,omitempty"`
	Fallback  string        `json:"fallback,omitempty" nbt:"fallback,omitempty"` // shown when no language has Translate
	With      TranslateArgs `json:"with,omitempty" nbt:"with,omitempty"`
	Extra     []Message     `json:"extra,omitempty" nbt:"extra,omitempty"`

	raw nbt.RawMessage
}

type rawMsgStruct Message

// Append extra message to the end of the message and return the new one.
// The source message remains unchanged.
func (m Message) Append(extraMsg ...Message) Message {
	origLen := len(m.Extra)
	finalLen := origLen + len(extraMsg)
	var extra []Message
	if cap(m.Extra) < len(m.Extra)+len(extraMsg) {
		extra = make([]Message, finalLen)
		copy(extra, m.Extra)
	} else {
		extra = m.Extra[:finalLen]
	}
	copy(extra[origLen:], extraMsg)
	m.Extra = extra
	return m
}

func (m Message) SetColor(color string) Message {
	m.Color = color
	return m
}

func Text(str string) Message {
	return Message{Text: str}
}

func TranslateMsg(key string, with ...Message) (m Message) {
	m.Translate = key
	for _, v := range with {
		m.With = append(m.With, v)
	}
	return
}

var fmtCode = map[byte]string{
	'0': "30",
	'1': "34",
	'2': "32",
	'3': "36",
	'4': "31",
	'5': "35",
	'6': "33",
	'7': "37",
	'8': "90",
	'9': "94",
	'a': "92",
	'b': "96",
	'c': "91",
	'd': "95",
	'e': "93",
	'f': "97",

	// 'k':"",	//random
	'l': "1",
	'm': "9",
	'n': "4",
	'o': "3",
	'r': "0",
}

var colors = map[string]string{
	Black:       "30",
	DarkBlue:    "34",
	DarkGreen:   "32",
	DarkAqua:    "36",
	DarkRed:     "31",
	DarkPurple:  "35",
	Gold:        "33",
	Gray:        "37",
	DarkGray:    "90",
	Blue:        "94",
	Green:       "92",
	Aqua:        "96",
	Red:         "91",
	LightPurple: "95",
	Yellow:      "93",
	White:       "97",
}

// translateMap is the language String and ClearString render in, en_us under it (translation); nil: en_us
// alone.
var translateMap map[string]string

// SetLanguage sets the language String and ClearString render in: its translations (lang.Get, lang.Parse);
// a key it lacks is en_us's. nil goes back to en_us alone.
func SetLanguage(trans map[string]string) {
	translateMap = trans
}

// SetLocale sets the language String and ClearString render in by its locale ("cs_cz"): one lang has
// registered (en_us always; another a program registers, package lang).
func SetLocale(locale string) error {
	m, err := lang.Get(locale)
	if err != nil {
		return err
	}
	SetLanguage(m)
	return nil
}

// ClearString returns the message's text without color or style: what a player
// reads.
func (m Message) ClearString() string {
	var msg strings.Builder
	text, _ := TransCtrlSeq(m.Text, false)
	msg.WriteString(text)

	// handle translate
	if m.Translate != "" {
		msg.WriteString(translate(translation(m.Translate, m.Fallback), translateArgs(m.With, Message.ClearString)))
	}

	if m.Extra != nil {
		for i := range m.Extra {
			msg.WriteString(m.Extra[i].ClearString())
		}
	}
	return msg.String()
}

// String returns the message's text with ANSI escape sequences for its colors and
// styles; ClearString is the same without them. On Windows, print it through a
// writer that understands ANSI sequences (github.com/mattn/go-colorable, say).
func (m Message) String() string {
	var msg, format strings.Builder
	if m.Bold {
		format.WriteString("1;")
	}
	if m.Italic {
		format.WriteString("3;")
	}
	if m.UnderLined {
		format.WriteString("4;")
	}
	if m.StrikeThrough {
		format.WriteString("9;")
	}
	if m.Color != "" {
		format.WriteString(colors[m.Color] + ";")
	}
	if format.Len() > 0 {
		msg.WriteString("\033[" + format.String()[:format.Len()-1] + "m")
	}

	text, ok := TransCtrlSeq(m.Text, true)
	msg.WriteString(text)

	// handle translate
	if m.Translate != "" {
		msg.WriteString(translate(translation(m.Translate, m.Fallback), translateArgs(m.With, Message.String)))
	}

	if m.Extra != nil {
		for i := range m.Extra {
			msg.WriteString(m.Extra[i].String())
		}
	}

	if format.Len() > 0 || ok {
		msg.WriteString("\033[0m")
	}
	return msg.String()
}

var fmtPat = regexp.MustCompile(`(?i)§[\dA-FK-OR]`)

// TransCtrlSeq will transform control sequences into ANSI code
// or simply filter them. Depends on the second argument.
// if the str contains control sequences, returned change=true.
func TransCtrlSeq(str string, ansi bool) (dst string, change bool) {
	dst = fmtPat.ReplaceAllStringFunc(
		str,
		func(str string) string {
			f, ok := fmtCode[str[2]]
			if ok {
				if ansi {
					change = true
					return "\033[" + f + "m" // enable, add ANSI code
				}
				return "" // disable, remove the § code
			}
			return str // not a § code
		},
	)
	return
}
