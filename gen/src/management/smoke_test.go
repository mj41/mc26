package management_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mj41/go-mc26/data/version"
	"github.com/mj41/go-mc26/management"
)

// TestSmokeManagement drives the generated client against a vanilla server's
// management protocol: mc26 smoke sets MC26_SMOKE_MGMT to the address and
// MC26_SMOKE_MGMT_SECRET to the secret of the server it started.
func TestSmokeManagement(t *testing.T) {
	addr := os.Getenv("MC26_SMOKE_MGMT")
	secret := os.Getenv("MC26_SMOKE_MGMT_SECRET")
	if addr == "" || secret == "" {
		t.Skip("MC26_SMOKE_MGMT / MC26_SMOKE_MGMT_SECRET not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := management.Dial(ctx, addr, secret)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()

	// The status names the server the data was extracted from.
	st, err := c.ServerStatus(ctx)
	if err != nil {
		t.Fatalf("server/status: %v", err)
	}
	if !st.Started {
		t.Errorf("server/status: started = false")
	}
	if st.Version.Protocol != version.ProtocolVersion {
		t.Errorf("server/status: protocol %d, the library is built for %d", st.Version.Protocol, version.ProtocolVersion)
	}
	t.Logf("server %s (protocol %d), %d players online", st.Version.Name, st.Version.Protocol, len(st.Players))

	// A setting read, changed and read back.
	motd, err := c.ServersettingsMotd(ctx)
	if err != nil {
		t.Fatalf("serversettings/motd: %v", err)
	}
	if got, err := c.ServersettingsMotdSet(ctx, "managed by go-mc26"); err != nil {
		t.Fatalf("serversettings/motd/set: %v", err)
	} else if got != "managed by go-mc26" {
		t.Errorf("motd/set returned %q", got)
	}
	if got, err := c.ServersettingsMotd(ctx); err != nil || got != "managed by go-mc26" {
		t.Errorf("motd after set: %q, %v", got, err)
	}
	if _, err := c.ServersettingsMotdSet(ctx, motd); err != nil {
		t.Errorf("motd restore: %v", err)
	}

	// A list changed, with the notification the change causes.
	before, err := c.Allowlist(ctx)
	if err != nil {
		t.Fatalf("allowlist: %v", err)
	}
	after, err := c.AllowlistAdd(ctx, []management.Player{{Name: "Steve"}})
	if err != nil {
		t.Fatalf("allowlist/add: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Errorf("allowlist/add: %d entries before, %d after", len(before), len(after))
	}
	var added *management.AllowlistAddedNotification
	deadline := time.After(10 * time.Second)
wait:
	for {
		select {
		case n, ok := <-c.Notifications():
			if !ok {
				t.Fatalf("connection ended: %v", c.Err())
			}
			t.Logf("notification %s: %+v", n.NotificationMethod(), n)
			if a, ok := n.(management.AllowlistAddedNotification); ok {
				added = &a
				break wait
			}
		case <-deadline:
			break wait
		}
	}
	if added == nil {
		t.Errorf("no allowlist/added notification within 10s")
	} else if added.Player.Name != "Steve" {
		t.Errorf("allowlist/added names %q", added.Player.Name)
	}
	if _, err := c.AllowlistRemove(ctx, []management.Player{{Name: "Steve"}}); err != nil {
		t.Errorf("allowlist/remove: %v", err)
	}

	// The game rules, typed by the server.
	rules, err := c.Gamerules(ctx)
	if err != nil {
		t.Fatalf("gamerules: %v", err)
	}
	if len(rules) == 0 {
		t.Errorf("gamerules: none")
	}
	var typed int
	for _, r := range rules {
		if r.Type == "boolean" || r.Type == "integer" {
			typed++
		}
	}
	if typed != len(rules) {
		t.Errorf("gamerules: %d of %d carry a known type", typed, len(rules))
	}

	// An error the server reports as one.
	var out any
	if err := c.Call(ctx, "minecraft:no_such_method", nil, &out); err == nil {
		t.Errorf("an unknown method did not fail")
	} else if _, ok := err.(*management.Error); !ok {
		t.Errorf("an unknown method failed with %T: %v", err, err)
	} else {
		t.Logf("unknown method: %v", err)
	}
}
