package server_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/version"
	mcnet "github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/registry"
	"github.com/mj41/go-mc26/server"
	"github.com/mj41/go-mc26/yggdrasil/user"
)

const kickReason = "smoke test done"

// kickGame is a GamePlay that records who logged in and disconnects them at
// once, so a test sees the whole handshake, login and configuration exchange
// plus the play-state disconnect packet.
type kickGame struct{ joined chan string }

func (g kickGame) AcceptPlayer(name string, _ uuid.UUID, _ *user.PublicKey, _ []user.Property, _ int32, conn *mcnet.Conn) {
	d := play.Disconnect{Value: chat.Text(kickReason)}
	_ = conn.WritePacket(pk.Marshal(d.PacketID(), d))
	g.joined <- name
}

// startServer serves an offline-mode framework server on a random local port
// until the test ends and returns its address.
func startServer(t *testing.T, game server.GamePlay) string {
	t.Helper()
	l, err := mcnet.ListenMC("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	players := server.NewPlayerList(5)
	s := &server.Server{
		ListPingHandler: struct {
			*server.PingInfo
			*server.PlayerList
		}{server.NewPingInfo("go-mc test", version.ProtocolVersion, chat.Text("smoke"), nil), players},
		LoginHandler:  &server.MojangLoginHandler{OnlineMode: false, Threshold: 256, LoginChecker: players},
		ConfigHandler: &server.Configurations{Registries: registry.NewNetworkCodec()},
		GamePlay:      game,
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.AcceptConn(&conn)
		}
	}()
	return l.Addr().String()
}

func TestListPing(t *testing.T) {
	addr := startServer(t, kickGame{joined: make(chan string, 1)})
	data, _, err := bot.PingAndListTimeout(addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct{ Max, Online int } `json:"players"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("status %s: %v", data, err)
	}
	if status.Version.Name != "go-mc test" || status.Version.Protocol != version.ProtocolVersion {
		t.Fatalf("status version %+v, want go-mc test/%d", status.Version, version.ProtocolVersion)
	}
}

func TestOfflineLogin(t *testing.T) {
	game := kickGame{joined: make(chan string, 1)}
	addr := startServer(t, game)

	c := bot.NewClient()
	c.Auth.Name = "Smoke"
	var got chat.Message
	basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		Disconnect: func(reason chat.Message) error { got = reason; return bot.DisconnectErr(reason) },
	})
	if err := c.JoinServer(addr); err != nil {
		t.Fatalf("join: %v", err)
	}
	select {
	case name := <-game.joined:
		if name != "Smoke" {
			t.Fatalf("server saw player %q, want Smoke", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never reached AcceptPlayer")
	}

	err := c.HandleGame()
	var disconnect bot.DisconnectErr
	if !errors.As(err, &disconnect) {
		t.Fatalf("HandleGame returned %v, want DisconnectErr", err)
	}
	if got.ClearString() != kickReason {
		t.Fatalf("disconnect reason %q, want %q", got.ClearString(), kickReason)
	}
}
