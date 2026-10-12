package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mj41/go-mc26/data/lang"
)

// translation is the template a translatable component shows, found the way
// the game finds it: the selected language, then en_us (the client loads
// en_us under every language, LanguageManager's language stack), then the
// component's fallback, then the key itself (Language.getOrDefault).
func translation(key, fallback string) string {
	if s, ok := translateMap[key]; ok {
		return s
	}
	if s, ok := lang.English()[key]; ok {
		return s
	}
	if fallback != "" {
		return fallback
	}
	return key
}

// translate renders a translatable component's text the way the game does
// (TranslatableContents.decomposeTemplate): %s is the next argument, %2$s
// (%[2]s once gen_lang has converted a language file) the second, and %% a
// percent sign; a positional argument does not move the next %s on, and an
// argument no placeholder names is not shown. Any other % — a verb but s, a
// percent sign alone, a placeholder past the arguments — and the game shows
// the template as it is, without the arguments.
func translate(tmpl string, args []string) string {
	if !strings.Contains(tmpl, "%") {
		return tmpl
	}
	var b strings.Builder
	next := 0
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		rest := tmpl[i+1:]
		switch {
		case strings.HasPrefix(rest, "%"):
			b.WriteByte('%')
			i++
		case strings.HasPrefix(rest, "s"):
			if next >= len(args) {
				return tmpl
			}
			b.WriteString(args[next])
			next++
			i++
		default:
			// %2$s as the game writes it, %[2]s as gen_lang writes it
			var digits string
			var n int
			if j := strings.Index(rest, "$s"); j > 0 && isDigits(rest[:j]) {
				digits, n = rest[:j], j+2
			} else if strings.HasPrefix(rest, "[") {
				if j := strings.Index(rest, "]s"); j > 1 && isDigits(rest[1:j]) {
					digits, n = rest[1:j], j+2
				}
			}
			if digits == "" {
				return tmpl
			}
			idx, err := strconv.Atoi(digits)
			if err != nil || idx < 1 || idx > len(args) {
				return tmpl
			}
			b.WriteString(args[idx-1])
			i += n
		}
	}
	return b.String()
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// translateArgs renders a component's arguments: a Message by render (String
// or ClearString), a value from a numeric NBT list as its text.
func translateArgs(with TranslateArgs, render func(Message) string) []string {
	args := make([]string, len(with))
	for i, v := range with {
		switch v := v.(type) {
		case Message:
			args[i] = render(v)
		case nil:
			args[i] = "null"
		default:
			args[i] = fmt.Sprint(v)
		}
	}
	return args
}
