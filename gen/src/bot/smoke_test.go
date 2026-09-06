package bot_test

import (
	"errors"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/bot/msg"
	"github.com/mj41/go-mc26/bot/playerlist"
	"github.com/mj41/go-mc26/bot/world"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/packetid"
	"github.com/mj41/go-mc26/level"
	pk "github.com/mj41/go-mc26/net/packet"
)

// inTheWorld closes its channel when the first chunk arrives. A server sends
// chunks only to a living, spawned player, and tracks entities for it from the
// same moment — so waiting for one is what says a test may summon something and
// expect to hear about it. Appearing in the player list is not enough: a dead
// player is listed and told nothing.
func inTheWorld(c *bot.Client) <-chan struct{} {
	ch := make(chan struct{})
	var once sync.Once
	c.Events.AddListener(bot.PacketHandler{
		Priority: 16, ID: packetid.ClientboundLevelChunkWithLight,
		F: func(pk.Packet) error {
			once.Do(func() { close(ch) })
			return nil
		},
	})
	return ch
}

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
	// Keep the registry NBT so the generated element types can be checked
	// against what this server actually sends.
	c.Registries.KeepRaw(true)
	var chunks, started, echoed atomic.Int32
	var chatManager *msg.Manager
	done := func() error {
		if started.Load() == 1 && chunks.Load() >= wantChunks && echoed.Load() == 1 {
			return errSmokeDone
		}
		return nil
	}
	// A dead player is sent no chunks, and the smoke server keeps its world
	// between runs: without respawning, a bot that died once would wait for
	// chunks that never come.
	var player *basic.Player
	player = basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		GameStart: func() error {
			started.Store(1)
			return chatManager.SendMessage(hello)
		},
		Death: func() error { return player.Respawn() },
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

	// Every registry entry the server sent must fit its generated element type:
	// a key the type does not have means the type is missing a field or a
	// generated nbt tag is wrong, which would otherwise decode to a zero value
	// in silence.
	unexpected := c.Registries.Unexpected()
	if len(unexpected) > 0 {
		ids := make([]string, 0, len(unexpected))
		for id := range unexpected {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			for i, err := range unexpected[id] {
				if i >= 3 {
					t.Errorf("%s: %d more entries with unknown keys", id, len(unexpected[id])-3)
					break
				}
				t.Errorf("%s %v", id, err)
			}
		}
	}
	if len(unexpected) == 0 {
		t.Logf("ok: %d chunks, chat echoed, registries cover what the server sent", chunks.Load())
	}
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
