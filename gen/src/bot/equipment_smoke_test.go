package bot_test

import (
	"errors"
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

// errEquipmentDone stops HandleGame once the equipment has been seen.
var errEquipmentDone = errors.New("equipment smoke test satisfied")

// TestSmokeEquipment summons an armed mob over RCON and checks the equipment
// the server sends for it. set_equipment is the one packet whose list has no
// count: entries follow each other while the top bit of the slot byte is set,
// and the generated list reads until that bit is clear. Reading one entry too
// few loses the rest of the packet without any error, so this asserts that a
// real server's two-slot packet arrives with both slots in it.
func TestSmokeEquipment(t *testing.T) {
	addr := os.Getenv("MC26_SMOKE_ADDR")
	rconAddr := os.Getenv("MC26_SMOKE_RCON")
	if addr == "" || rconAddr == "" {
		t.Skip("MC26_SMOKE_ADDR or MC26_SMOKE_RCON not set")
	}

	c := bot.NewClient()
	c.Auth.Name = "SmokeEquip"

	var mu sync.Mutex
	var problems []string
	most := 0 // the most slots one packet carried

	// A dead player is sent no chunks and tracks no entities, and the smoke
	// server keeps its world between runs — so a bot that died once would fail
	// this test for ever after. Respawning on death is what a client does.
	var player *basic.Player
	player = basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		Death:      func() error { return player.Respawn() },
		Disconnect: func(reason chat.Message) error { return bot.DisconnectErr(reason) },
	})

	c.Events.AddListener(
		bot.PacketHandler{Priority: 32, ID: packetid.ClientboundSetEquipment, F: func(p pk.Packet) error {
			var eq play.SetEquipment
			mu.Lock()
			defer mu.Unlock()
			if err := p.Scan(&eq); err != nil {
				problems = append(problems, fmt.Sprintf("set_equipment: %v", err))
				return nil
			}
			// Every entry but the last carried the continuation bit; the reader
			// takes it off, so no slot may still have it.
			for _, s := range eq.Slots {
				if s.Slot < 0 {
					problems = append(problems, fmt.Sprintf("slot %d still carries the continuation bit", s.Slot))
				}
			}
			if len(eq.Slots) > most {
				most = len(eq.Slots)
			}
			if most >= 2 {
				return errEquipmentDone
			}
			return nil
		}},
	)

	loaded := inTheWorld(c)

	if err := c.JoinServer(addr); err != nil {
		t.Fatalf("join %s: %v", addr, err)
	}
	defer c.Close()
	result := make(chan error, 1)
	go func() { result <- c.HandleGame() }()

	rcon, err := mcnet.DialRCON(rconAddr, os.Getenv("MC26_SMOKE_RCON_PASSWORD"))
	if err != nil {
		t.Fatalf("rcon %s: %v", rconAddr, err)
	}
	defer rcon.Close()
	select {
	case <-loaded:
	case <-time.After(30 * time.Second):
		t.Fatal("no chunk arrived: the bot is not in the world, so nothing would be tracked for it")
	}
	// An armor stand holding a sword and wearing a helmet: two slots, so the list
	// has an entry with the continuation bit and one without. It is a passive
	// entity on purpose — the smoke server runs on peaceful, where a mob is
	// removed before anything is ever sent about it.
	const nbt = `{equipment:{mainhand:{id:"minecraft:diamond_sword",count:1},head:{id:"minecraft:iron_helmet",count:1}}}`
	resp, err := commandResp(rcon, fmt.Sprintf("execute at %s run summon minecraft:armor_stand ~ ~ ~ %s", c.Auth.Name, nbt))
	if err != nil {
		t.Fatalf("summon: %v", err)
	}
	if !contains(resp, "Summoned") {
		t.Fatalf("the server did not summon the armor stand: %s", resp)
	}

	select {
	case err := <-result:
		if !errors.Is(err, errEquipmentDone) {
			t.Fatalf("HandleGame: %v", err)
		}
	case <-time.After(20 * time.Second):
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range problems {
		t.Error(p)
	}
	// One packet has to carry both slots: a reader that stopped at the first
	// entry would still see two if it counted across packets, and the rest of
	// the packet would be silently dropped rather than desynchronising anything.
	if most < 2 {
		t.Fatalf("the longest equipment list read was %d entries; the armed armor stand wears 2", most)
	}
	if len(problems) == 0 {
		t.Logf("ok: one set_equipment carried %d slots, read from a list with no count", most)
	}
}
