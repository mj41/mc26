package bot_test

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/bot/msg"
	"github.com/mj41/go-mc26/bot/playerlist"
	"github.com/mj41/go-mc26/bot/world"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/level"
)

// errSmokeDone stops HandleGame once the smoke test has seen enough.
var errSmokeDone = errors.New("smoke test satisfied")

// TestSmoke joins the vanilla server at MC26_SMOKE_ADDR (offline mode), waits
// for the login to complete, for a number of chunks to arrive and for its own
// chat message to come back, then disconnects. It is skipped when the address
// is not set; `mc26 smoke` starts the server and sets it.
func TestSmoke(t *testing.T) {
	addr := os.Getenv("MC26_SMOKE_ADDR")
	if addr == "" {
		t.Skip("MC26_SMOKE_ADDR not set")
	}
	const wantChunks = 25
	const hello = "smoke test hello"

	c := bot.NewClient()
	c.Auth.Name = "Smoke"
	var chunks, started, echoed atomic.Int32
	var chatManager *msg.Manager
	done := func() error {
		if started.Load() == 1 && chunks.Load() >= wantChunks && echoed.Load() == 1 {
			return errSmokeDone
		}
		return nil
	}
	player := basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		GameStart: func() error {
			started.Store(1)
			return chatManager.SendMessage(hello)
		},
		Disconnect: func(reason chat.Message) error {
			return bot.DisconnectErr(reason)
		},
	})
	world.NewWorld(c, player, world.EventsListener{
		LoadChunk: func(level.ChunkPos) error {
			chunks.Add(1)
			return done()
		},
	})
	chatManager = msg.New(c, player, playerlist.New(c), msg.EventsHandler{
		PlayerChatMessage: func(m chat.Message, validated bool) error {
			if m.ClearString() != "" && contains(m.ClearString(), hello) {
				echoed.Store(1)
			}
			return done()
		},
		SystemChat: func(chat.Message, bool) error { return done() },
	})

	if err := c.JoinServer(addr); err != nil {
		t.Fatalf("join %s: %v", addr, err)
	}
	t.Logf("logged in to %s", addr)
	result := make(chan error, 1)
	go func() { result <- c.HandleGame() }()
	select {
	case err := <-result:
		if !errors.Is(err, errSmokeDone) {
			t.Fatalf("HandleGame: %v (chunks %d, started %d, echoed %d)", err, chunks.Load(), started.Load(), echoed.Load())
		}
	case <-time.After(90 * time.Second):
		t.Fatalf("timeout: chunks %d/%d, started %d, echoed %d", chunks.Load(), wantChunks, started.Load(), echoed.Load())
	}
	_ = c.Close()
	t.Logf("ok: %d chunks, chat echoed", chunks.Load())
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
