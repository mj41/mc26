package bot_test

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/entitydata"
	"github.com/mj41/go-mc26/data/packetid"
	"github.com/mj41/go-mc26/data/registryid"
	mcnet "github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

// summoned are the entities the test asks the server for. A server only sends
// the metadata that differs from the entity's defaults, so each one is summoned
// with NBT that moves the fields this test wants to see: the shared ones every
// entity has, and the type's own. Between them they use most of the entity data
// serializers a bot meets — flags, floats, optional text, item stacks,
// rotations, registry holders, villager data.
var summoned = []struct{ id, nbt string }{
	{"minecraft:villager", `{VillagerData:{profession:"minecraft:farmer",type:"minecraft:plains",level:3}}`},
	{"minecraft:sheep", `{Color:4b,Sheared:1b}`},
	{"minecraft:cat", `{variant:"minecraft:siamese",CollarColor:5b}`},
	{"minecraft:armor_stand", `{ShowArms:1b,NoBasePlate:1b,Small:1b}`},
	{"minecraft:arrow", `{crit:1b,PierceLevel:2b}`},
	{"minecraft:item_frame", `{ItemRotation:2b,Invisible:1b}`},
	{"minecraft:allay", `{CanDuplicate:0b}`},
	{"minecraft:slime", `{Size:3}`},
}

// commonNBT moves the fields every entity shares (Entity.<clinit>): the shared
// flags byte, the custom name and its visibility, silence and gravity.
const commonNBT = `CustomName:'"smoke"',CustomNameVisible:1b,Silent:1b,NoGravity:1b,Glowing:1b`

// errEntityDataDone stops HandleGame once enough entities have been seen.
var errEntityDataDone = errors.New("entity data smoke test satisfied")

// TestSmokeEntityData joins the server at MC26_SMOKE_ADDR, summons a few
// entity types over RCON and checks the generated entity metadata against
// them: every value must decode with the serializer the server names, and
// every index must be a field data/entitydata lists for that entity type with
// that same serializer. A generated table that disagrees with the server —
// an index that moved, a serializer that changed — fails here rather than
// giving a bot a plausible wrong value.
func TestSmokeEntityData(t *testing.T) {
	addr := os.Getenv("MC26_SMOKE_ADDR")
	rconAddr := os.Getenv("MC26_SMOKE_RCON")
	if addr == "" || rconAddr == "" {
		t.Skip("MC26_SMOKE_ADDR or MC26_SMOKE_RCON not set")
	}

	c := bot.NewClient()
	c.Auth.Name = "SmokeEntities"

	asked := map[string]bool{} // the types this test summoned
	for _, e := range summoned {
		asked[e.id] = true
	}

	var mu sync.Mutex
	var wanted atomic.Int32 // how many entity types the server accepted
	wanted.Store(int32(len(summoned)))
	entityType := map[int32]string{} // entity id → its type id
	seen := map[string]int{}         // type id → how many metadata packets it got
	checked := map[string]struct{}{} // type id and index actually compared
	var problems []string

	// A dead player is sent no chunks and tracks no entities, and the smoke
	// server keeps its world between runs, so a bot that died once would never
	// see another entity. Respawning on death is what a client does.
	var player *basic.Player
	player = basic.NewPlayer(c, basic.DefaultSettings, basic.EventsListener{
		Death:      func() error { return player.Respawn() },
		Disconnect: func(reason chat.Message) error { return bot.DisconnectErr(reason) },
	})
	_ = player

	c.Events.AddListener(
		bot.PacketHandler{Priority: 32, ID: packetid.ClientboundAddEntity, F: func(p pk.Packet) error {
			var add play.AddEntity
			if err := p.Scan(&add); err != nil {
				return fmt.Errorf("add_entity: %w", err)
			}
			name := ""
			if int(add.Type) < len(registryid.EntityType) {
				name = registryid.EntityType[add.Type]
			}
			mu.Lock()
			entityType[int32(add.ID)] = name
			mu.Unlock()
			return nil
		}},
		bot.PacketHandler{Priority: 32, ID: packetid.ClientboundSetEntityData, F: func(p pk.Packet) error {
			var data play.SetEntityData
			// A value the generated types cannot decode stops the whole
			// packet: that is the failure this test is looking for.
			if err := p.Scan(&data); err != nil {
				mu.Lock()
				problems = append(problems, fmt.Sprintf("set_entity_data: %v", err))
				mu.Unlock()
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			name, known := entityType[int32(data.ID)]
			if !known || name == "" {
				return nil // an entity that was there before the bot joined
			}
			fields := entitydata.Fields[name]
			for _, v := range data.PackedItems {
				checked[fmt.Sprintf("%s#%d", name, v.Index)] = struct{}{}
				var field *entitydata.Field
				for i := range fields {
					if fields[i].Index == int(v.Index) {
						field = &fields[i]
						break
					}
				}
				switch {
				case field == nil:
					problems = append(problems, fmt.Sprintf("%s: index %d is not a field of that type in data/entitydata", name, v.Index))
				case field.Serializer != int(v.Serializer):
					problems = append(problems, fmt.Sprintf("%s: index %d (%s.%s) is serializer %d in data/entitydata, the server sent %d",
						name, v.Index, field.Class, field.Name, field.Serializer, v.Serializer))
				}
			}
			// Only the types this test asked for count towards being done: a
			// creeper the world spawned by itself is checked like any other, but
			// finishing on it would leave a summoned type unexamined.
			if len(data.PackedItems) > 0 && asked[name] {
				seen[name]++
			}
			if int32(len(seen)) >= wanted.Load() {
				return errEntityDataDone
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

	// Summon the entities where the bot stands, once it is in the world.
	rcon, err := mcnet.DialRCON(rconAddr, os.Getenv("MC26_SMOKE_RCON_PASSWORD"))
	if err != nil {
		t.Fatalf("rcon %s: %v", rconAddr, err)
	}
	defer rcon.Close()
	if err := command(rcon, "difficulty easy"); err != nil {
		t.Fatalf("rcon: %v", err)
	}
	select {
	case <-loaded:
	case <-time.After(30 * time.Second):
		t.Fatal("no chunk arrived: the bot is not in the world, so nothing would be tracked for it")
	}
	// A type the server refuses to place here (an item frame needs a wall)
	// is not one this test can wait for.
	accepted := 0
	for _, e := range summoned {
		nbt := "{" + commonNBT + "}"
		if e.nbt != "" {
			nbt = "{" + commonNBT + "," + strings.TrimSuffix(strings.TrimPrefix(e.nbt, "{"), "}") + "}"
		}
		resp, err := commandResp(rcon, fmt.Sprintf("execute at %s run summon %s ~ ~ ~ %s", c.Auth.Name, e.id, nbt))
		if err != nil {
			t.Fatalf("summon %s: %v", e.id, err)
		}
		if contains(resp, "Summoned") {
			accepted++
		} else {
			t.Logf("the server did not summon %s (%s)", e.id, strings.TrimSpace(resp))
		}
	}
	wanted.Store(int32(accepted))

	select {
	case err := <-result:
		if !errors.Is(err, errEntityDataDone) {
			t.Fatalf("HandleGame: %v", err)
		}
	case <-time.After(60 * time.Second):
		// Not fatal by itself: the assertions below say whether what did
		// arrive was right.
		t.Logf("timeout waiting for metadata; saw %d entity types", len(seen))
	}

	mu.Lock()
	defer mu.Unlock()
	for i, p := range problems {
		if i >= 10 {
			t.Errorf("%d more entity data problems", len(problems)-10)
			break
		}
		t.Error(p)
	}
	if len(seen) == 0 {
		t.Fatalf("no entity metadata arrived; summoned %v", summoned)
	}
	total, types := 0, make([]string, 0, len(seen))
	for name, n := range seen {
		total += n
		types = append(types, name)
	}
	sort.Strings(types)
	if len(seen) < accepted {
		t.Errorf("metadata arrived for %d of the %d entity types the server summoned: %v", len(seen), accepted, types)
	}
	// The server only sends what differs from an entity's defaults, so the test
	// says how many fields it really compared and fails if that collapses:
	// otherwise it could pass while checking almost nothing.
	const wantChecked = 40
	if len(checked) < wantChecked {
		t.Errorf("only %d entity data fields were compared with data/entitydata, want at least %d", len(checked), wantChecked)
	}
	if len(problems) == 0 {
		t.Logf("ok: %d fields of %d metadata packets, %d entity types, match data/entitydata (%v)", len(checked), total, len(seen), types)
	}
}

// command sends an RCON command and reads its answer away.
func command(r mcnet.RCONClientConn, cmd string) error {
	_, err := commandResp(r, cmd)
	return err
}

func commandResp(r mcnet.RCONClientConn, cmd string) (string, error) {
	if err := r.Cmd(cmd); err != nil {
		return "", err
	}
	return r.Resp()
}
