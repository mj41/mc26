package bot_test

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/packetid"
	mcnet "github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

// TestSmokeCommands round-trips the command tree a real server sends. The
// packet is the most conditional one there is: every node carries a flags byte
// whose two low bits say whether it is the root, a literal or an argument, and
// whose other bits say whether a redirect and a suggestion id follow, and an
// argument node then reads whatever its type in command_argument_type reads.
// Decoding it and encoding it again has to give the same bytes back — anything
// read at the wrong width or under the wrong condition shows up here, where a
// decode that merely does not fail would not.
func TestSmokeCommands(t *testing.T) {
	addr := os.Getenv("MC26_SMOKE_ADDR")
	rconAddr := os.Getenv("MC26_SMOKE_RCON")
	if addr == "" || rconAddr == "" {
		t.Skip("MC26_SMOKE_ADDR or MC26_SMOKE_RCON not set")
	}

	// how many nodes say the whole tree arrived rather than a plain player's
	const wantNodes = 500

	c := bot.NewClient()
	c.Auth.Name = "SmokeCmds"

	var mu sync.Mutex
	var problems []string
	most := 0 // the most nodes one tree carried
	trees := make(chan int, 8)

	var player *basic.Player
	player = basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		Death:      func() error { return player.Respawn() },
		Disconnect: func(reason chat.Message) error { return bot.DisconnectErr(reason) },
	})

	c.Events.AddListener(
		bot.PacketHandler{Priority: 32, ID: packetid.ClientboundPlayCommands, F: func(p pk.Packet) error {
			mu.Lock()
			defer mu.Unlock()
			var cmds play.Commands
			if err := p.Scan(&cmds); err != nil {
				problems = append(problems, fmt.Sprintf("decode: %v", err))
				trees <- 0
				return nil
			}
			var back bytes.Buffer
			if _, err := cmds.WriteTo(&back); err != nil {
				problems = append(problems, fmt.Sprintf("encode: %v", err))
			} else if !bytes.Equal(p.Data, back.Bytes()) {
				problems = append(problems, fmt.Sprintf("round trip differs: %d bytes in, %d out", len(p.Data), back.Len()))
			}
			if len(cmds.Entries) > most {
				most = len(cmds.Entries)
			}
			trees <- len(cmds.Entries)
			return nil
		}},
	)

	if err := c.JoinServer(addr); err != nil {
		t.Fatalf("join %s: %v", addr, err)
	}
	defer c.Close()
	result := make(chan error, 1)
	go func() { result <- c.HandleGame() }()

	waitTree := func(what string) int {
		select {
		case n := <-trees:
			return n
		case err := <-result:
			t.Fatalf("HandleGame while waiting for %s: %v", what, err)
		case <-time.After(30 * time.Second):
			t.Fatalf("the server sent no command tree %s", what)
		}
		return 0
	}

	// A server sends each player only the commands that player may use, so an
	// ordinary bot gets a couple of dozen nodes. Opping it makes the server send
	// the whole vanilla tree, which is where the argument types worth reading
	// live — but the server only accepts the name once it knows it, so this
	// happens after joining, and only if the bot is not an operator already from
	// an earlier run against the same world.
	if waitTree("on joining") < wantNodes {
		rcon, err := mcnet.DialRCON(rconAddr, os.Getenv("MC26_SMOKE_RCON_PASSWORD"))
		if err != nil {
			t.Fatalf("rcon %s: %v", rconAddr, err)
		}
		defer rcon.Close()
		resp, err := commandResp(rcon, "op "+c.Auth.Name)
		if err != nil {
			t.Fatalf("rcon op: %v", err)
		}
		if !contains(resp, "Made "+c.Auth.Name) {
			t.Fatalf("the server did not op the bot: %q", resp)
		}
		waitTree("after opping the bot")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range problems {
		t.Error(p)
	}
	// The whole vanilla tree is thousands of nodes; a couple of dozen would mean
	// the op never took and the argument types were never exercised.
	if most < wantNodes {
		t.Errorf("the largest command tree read was %d nodes, too few to have exercised the argument types", most)
	}
	if len(problems) == 0 {
		t.Logf("ok: %d command tree nodes read and written back byte for byte", most)
	}
}
