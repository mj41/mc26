package bot_test

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/bot/world"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/packetid"
	mcnet "github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
)

// TestSmokeTraffic drives a session against a real server: it joins, sends what
// a bot can send without playing the game, and asks the server by rcon for as
// many different packets as a standing bot can provoke. It checks nothing about
// the bytes itself.
//
// The checking belongs to `mc26 crosscheck`, which runs this through a
// recording proxy: the proxy sees every state and both directions, including
// the login and configuration packets that are over before an event handler
// could see them, and the packets this bot sends, which nothing used to check
// at all. What it records is then read back by TestCaptureCheck and by a
// decoder that has only the extracted JSON.
func TestSmokeTraffic(t *testing.T) {
	addr := os.Getenv("MC26_SMOKE_ADDR")
	rconAddr := os.Getenv("MC26_SMOKE_RCON")
	if addr == "" || rconAddr == "" {
		t.Skip("MC26_SMOKE_ADDR or MC26_SMOKE_RCON not set")
	}

	c := bot.NewClient()
	c.Auth.Name = "SmokeTraffic"

	var mu sync.Mutex
	seen := map[int32]int{}
	var at atomic.Pointer[struct{ x, y, z float64 }]
	at.Store(&struct{ x, y, z float64 }{})

	var player *basic.Player
	player = basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		Death: func() error { return player.Respawn() },
		Teleported: func(x, y, z float64, _, _ float32, _ int32, id int32) error {
			at.Store(&struct{ x, y, z float64 }{x, y, z})
			return player.AcceptTeleportation(pk.VarInt(id))
		},
		Disconnect: func(reason chat.Message) error { return bot.DisconnectErr(reason) },
	})
	world.NewWorld(c, player, world.EventsListener{})

	c.Events.AddGeneric(bot.PacketHandler{Priority: 100, F: func(p pk.Packet) error {
		mu.Lock()
		seen[p.ID]++
		mu.Unlock()
		return nil
	}})

	loaded := inTheWorld(c)
	if err := c.JoinServer(addr); err != nil {
		t.Fatalf("join %s: %v", addr, err)
	}
	defer c.Close()
	result := make(chan error, 1)
	go func() { result <- c.HandleGame() }()
	select {
	case <-loaded:
	case err := <-result:
		t.Fatalf("HandleGame: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("no chunk arrived")
	}

	// Ask the server for as many different packets as a bot can provoke without
	// playing the game: the command tree, entities and their metadata, equipment,
	// chat, the passage of time, the scoreboard, the boss bar.
	rcon, err := mcnet.DialRCON(rconAddr, os.Getenv("MC26_SMOKE_RCON_PASSWORD"))
	if err != nil {
		t.Fatalf("rcon %s: %v", rconAddr, err)
	}
	defer rcon.Close()
	name := c.Auth.Name
	for _, cmd := range []string{
		"op " + name,
		"difficulty easy",
		// The item components first: each give sends the whole inventory again, and only
		// the first few of any packet id are kept, so a component given last is a component
		// the recording never sees.
		"give " + name + " minecraft:diamond_sword[attribute_modifiers=[{type:\"minecraft:attack_damage\"," +
			"id:\"minecraft:base_attack_damage\",amount:5,operation:\"add_value\",slot:\"mainhand\"," +
			"display:{type:\"override\",value:\"five\"}}],tooltip_display={hide_tooltip:false," +
			"hidden_components:[\"minecraft:attribute_modifiers\"]},custom_name='\"cap\"'] 1",
		"give " + name + " minecraft:potion[potion_contents={potion:\"minecraft:strength\"," +
			"custom_effects:[{id:\"minecraft:speed\",amplifier:2,duration:200}]}] 1",
		"give " + name + " minecraft:player_head[profile={name:\"Steve\"}] 1",
		"give " + name + " minecraft:diamond_pickaxe[can_break=[{blocks:\"minecraft:stone\"}," +
			"{blocks:\"minecraft:furnace\",state:{lit:\"false\"}}]] 1",
		"give " + name + " minecraft:shears[can_place_on=[{blocks:\"minecraft:dirt\"," +
			"predicates:{\"minecraft:damage\":{durability:{min:1}}}}]] 1",
		"give " + name + " minecraft:apple[consumable={consume_seconds:1.6f,animation:\"eat\"," +
			"on_consume_effects:[{type:\"minecraft:apply_effects\",effects:[{id:\"minecraft:speed\"," +
			"duration:100}],probability:1.0f},{type:\"minecraft:teleport_randomly\",diameter:8.0f}," +
			"{type:\"minecraft:clear_all_effects\"}]}] 1",
		"give " + name + " minecraft:totem_of_undying[death_protection={death_effects:" +
			"[{type:\"minecraft:remove_effects\",effects:\"minecraft:poison\"}]}] 1",
		"time set noon",
		"weather rain",
		"execute at " + name + " run summon minecraft:armor_stand ~ ~ ~ {equipment:{mainhand:{id:\"minecraft:diamond_sword\",count:1},head:{id:\"minecraft:iron_helmet\",count:1}},CustomName:'\"cap\"',CustomNameVisible:1b}",
		"execute at " + name + " run summon minecraft:villager ~ ~ ~ {VillagerData:{profession:\"minecraft:farmer\",type:\"minecraft:plains\",level:3}}",
		"execute at " + name + " run summon minecraft:sheep ~ ~ ~ {Color:4b}",
		"execute at " + name + " run summon minecraft:allay ~ ~ ~",
		"execute at " + name + " run summon minecraft:arrow ~ ~ ~ {crit:1b}",
		"say hello from the capture",
		"title " + name + " title {\"text\":\"captured\"}",
		"bossbar add cap {\"text\":\"cap\"}",
		"bossbar set cap players " + name,
		"scoreboard objectives add cap dummy",
		"scoreboard objectives setdisplay sidebar cap",
		"scoreboard players set " + name + " cap 7",
		"team add capteam",
		"team join capteam " + name,
		"experience add " + name + " 30",
		"effect give " + name + " minecraft:speed 30 1",
		"playsound minecraft:entity.pig.ambient master " + name,
		"particle minecraft:flame ~ ~ ~ 1 1 1 0 5",
		"execute at " + name + " run setblock ~2 ~ ~2 minecraft:chest",
		"advancement grant " + name + " only minecraft:story/root",
		"weather clear",
	} {
		resp, err := commandResp(rcon, cmd)
		if err != nil {
			t.Fatalf("rcon %q: %v", cmd, err)
		}
		// A command the server refuses answers with the reason instead of failing, and a
		// give that did not happen is coverage silently lost rather than a test failure.
		for _, bad := range []string{"Unknown", "Expected", "Incorrect", "Invalid", "Unable"} {
			if strings.Contains(resp, bad) {
				t.Errorf("the server refused %q: %s", cmd, strings.TrimSpace(resp))
			}
		}
		time.Sleep(120 * time.Millisecond)
	}

	// What a bot sends is described by the same JSON as what it receives, and
	// nothing checked it until the recording covered both directions. These are
	// packets a standing bot can send truthfully, and they exist in every version
	// this repository builds.
	//
	// One position per client tick: a second one before the tick has ended is a
	// disconnect (ServerGamePacketListenerImpl.handleMovePlayer refuses when
	// receivedPositionThisTick is set). The flag is cleared by handleClientTickEnd
	// and by nothing else -- not by the server's own tick -- so a client that
	// never says its tick ended may send exactly one position ever. Hence the
	// ClientTickEnd after each of them, which is what a real client does.
	pos := at.Load()
	for _, p := range []struct {
		id packetid.ServerboundPacketID
		v  pk.FieldEncoder
	}{
		{id: packetid.ServerboundPlayerLoaded, v: &play.PlayerLoaded{}},
		{id: packetid.ServerboundMovePlayerStatusOnly, v: &play.MovePlayerStatusOnly{
			Value: types.MovePlayerPacked{OnGround: true}}},
		{id: packetid.ServerboundMovePlayerRot, v: &play.MovePlayerRot{
			YRot: 180, XRot: 10, Flags: types.MovePlayerPacked{OnGround: true}}},
		{id: packetid.ServerboundMovePlayerPos, v: &play.MovePlayerPos{
			X: pk.Double(pos.x), Y: pk.Double(pos.y), Z: pk.Double(pos.z),
			Flags: types.MovePlayerPacked{OnGround: true}}},
		{id: packetid.ServerboundClientTickEnd, v: &play.ClientTickEnd{}},
		{id: packetid.ServerboundMovePlayerPosRot, v: &play.MovePlayerPosRot{
			X: pk.Double(pos.x), Y: pk.Double(pos.y), Z: pk.Double(pos.z),
			YRot: 90, XRot: 0, Flags: types.MovePlayerPacked{OnGround: true}}},
		{id: packetid.ServerboundClientTickEnd, v: &play.ClientTickEnd{}},
		{id: packetid.ServerboundSetCarriedItem, v: &play.SetCarriedItem{Slot: 1}},
		{id: packetid.ServerboundPlayerCommand, v: &play.PlayerCommand{
			Action: types.PlayerCommandActionStartSprinting}},
		{id: packetid.ServerboundPlayerCommand, v: &play.PlayerCommand{
			Action: types.PlayerCommandActionStopSprinting}},
		{id: packetid.ServerboundPlayerInput, v: &play.PlayerInput{Input: 0}},
		{id: packetid.ServerboundPingRequest, v: &play.PingRequest{Time: 1}},
		{id: packetid.ServerboundCommandSuggestion, v: &play.CommandSuggestion{ID: 1, Command: "/g"}},
		{id: packetid.ServerboundClientCommand, v: &play.ClientCommand{
			Action: types.ClientCommandActionRequestStats}},
	} {
		if err := c.Conn.WritePacket(pk.Marshal(int32(p.id), p.v)); err != nil {
			t.Fatalf("sending %T: %v", p.v, err)
		}
		time.Sleep(150 * time.Millisecond)
		// A server that refused what was sent closes the connection, and a
		// recording of a session that ended early is a recording of less than
		// was meant to be in it.
		select {
		case err := <-result:
			t.Fatalf("the server ended the session after %T: %v", p.v, err)
		default:
		}
	}

	// Let what the commands set off arrive.
	select {
	case err := <-result:
		t.Errorf("the session ended before the recording was done: %v", err)
	case <-time.After(5 * time.Second):
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("the server sent nothing")
	}
	n := 0
	for _, c := range seen {
		n += c
	}
	t.Logf("ok: the session carried %d packets of %d distinct play clientbound ids", n, len(seen))
}
