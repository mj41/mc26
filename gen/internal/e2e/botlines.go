package e2e

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// A downstream bot (a program built in the kit's workspace and run by botday, botflat and the like) is known to the
// harness by two things only, both configuration with neutral defaults:
//
//   - MC26_BOT, its package, relative to the kit's directory (default ./bot/cmd/bot);
//   - MC26_BOT_PREFIX, what its own log lines begin with (default "bot: "): the harness reads the lines that carry it
//     (what the bot decided, which skill ended, its tasks) and nothing else of its output.
//
// The rest of the contract is the bot's command line (-address, -name, -events, -debug, -explore-rings, -wait-at, -do)
// and the line "loaded" when it has joined. docs/testing.md has the table.

// DefaultBot and DefaultBotPrefix are used when the environment says nothing.
const (
	DefaultBot       = "./bot/cmd/bot"
	DefaultBotPrefix = "bot: "
)

// botPackage is the bot's package (MC26_BOT).
func botPackage() string {
	if v := os.Getenv("MC26_BOT"); v != "" {
		return v
	}
	return DefaultBot
}

// botPrefix is what the bot's own lines begin with (MC26_BOT_PREFIX).
func botPrefix() string {
	if v := os.Getenv("MC26_BOT_PREFIX"); v != "" {
		return v
	}
	return DefaultBotPrefix
}

// botSays is what the bot said on a line of its output (after its prefix, which a timestamp may precede), and whether
// the line is one of the bot's own.
func botSays(line string) (string, bool) {
	p := botPrefix()
	i := strings.Index(line, p)
	if i < 0 {
		return "", false
	}
	return line[i+len(p):], true
}

// botHas is whether the output holds a bot line that begins with what (after the prefix).
func botHas(out, what string) bool { return strings.Contains(out, botPrefix()+what) }

// botCount is how many times the output holds such a line.
func botCount(out, what string) int { return strings.Count(out, botPrefix()+what) }

// botLast is what follows the last bot line that begins with what ("" and false: none).
func botLast(out, what string) (string, bool) {
	i := strings.LastIndex(out, botPrefix()+what)
	if i < 0 {
		return "", false
	}
	return out[i+len(botPrefix()+what):], true
}

// waitBotPrefix fails fast when the bot has joined (it said "loaded") and, a little later, has printed no line that
// carries the prefix: the harness would read nothing of what it does and every stage would wait out its limit. The two
// sides agree on MC26_BOT_PREFIX, and a harness set differently from its bot's image says so at once.
func waitBotPrefix(out interface{ String() string }, limit time.Duration) error {
	for t0 := time.Now(); ; time.Sleep(250 * time.Millisecond) {
		if botHas(out.String(), "") {
			return nil
		}
		if time.Since(t0) >= limit {
			n := len(strings.Split(strings.TrimSpace(out.String()), "\n"))
			return fmt.Errorf("the bot printed %d lines and none holds %q: set MC26_BOT_PREFIX to what its own lines begin with", n, botPrefix())
		}
	}
}
