package basic

import (
	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

// EventsListener is a collection of event handlers.
// Fill the fields with your handler functions and pass it to [NewPlayer] to create the Player manager.
// For the event you don't want to handle, just leave it nil.
type EventsListener struct {
	// GameStart event is called when the login process is completed and the player is ready to play.
	//
	// If you want to do some action when the bot joined the server like sending a chat message,
	// this event is the right place to do it.
	GameStart func() error

	// Disconnect event is called before the server disconnects your client.
	// When the server willfully disconnects the client, it will send a ClientboundDisconnect packet and tell you why.
	// On vanilla client, the reason is displayed in the disconnect screen.
	//
	// This information may be very useful for debugging, and generally you should record it into the log.
	//
	// If the connection is disconnected due to network reasons or the client's initiative,
	// this event will not be triggered.
	Disconnect func(reason chat.Message) error

	// HealthChange event is called when the player's health or food changed.
	HealthChange func(health float32, foodLevel int32, foodSaturation float32) error

	// Death event is a special case of HealthChange.
	// It will be called after HealthChange handler called (if it isn't nil)
	// when the player's health is less than or equal to 0.
	//
	// Typically, you should call [Player.Respawn] in this handler.
	Death func() error

	// Teleported event is called when the server think the player position in the client side is wrong,
	// and send a ClientboundPlayerPosition packet to correct the client.
	//
	// Typically, you need to do two things in this handler:
	// - Update the player's position and rotation you tracked to the correct position.
	// - Call [Player.AcceptTeleportation] to send a teleport confirmation packet to the server.
	//
	// Before you confirm the teleportation, the server will not accept any player motion packets.
	//
	// The position coordinates and rotation are absolute or relative depends on the flags.
	// The flags field is a u32 bitfield (PositionUpdateRelatives) with bits:
	//   0x01=X, 0x02=Y, 0x04=Z, 0x08=Yaw, 0x10=Pitch, 0x20=DX, 0x40=DY, 0x80=DZ, 0x100=YawDelta
	// For more information, see https://wiki.vg/Protocol#Synchronize_Player_Position
	Teleported func(x, y, z float64, yaw, pitch float32, flags int32, teleportID int32) error
}

// attach your event listener to the client.
// The functions are copied when attaching, and modify on [EventListener] doesn't affect after that.
func (e EventsListener) attach(p *Player) {
	if e.GameStart != nil {
		attachJoinGameHandler(p.c, e.GameStart)
	}
	if e.Disconnect != nil {
		attachDisconnect(p.c, e.Disconnect)
	}
	if e.HealthChange != nil || e.Death != nil {
		attachUpdateHealth(p.c, e.HealthChange, e.Death)
	}
	if e.Teleported != nil {
		attachPlayerPosition(p.c, e.Teleported)
	}
}

func attachJoinGameHandler(c *bot.Client, handler func() error) {
	c.Events.AddListener(bot.PacketHandler{
		Priority: 64, ID: packetid.ClientboundPlayLogin,
		F: func(_ pk.Packet) error {
			return handler()
		},
	})
}

func attachDisconnect(c *bot.Client, handler func(reason chat.Message) error) {
	c.Events.AddListener(bot.PacketHandler{
		Priority: 64, ID: packetid.ClientboundPlayDisconnect,
		F: func(p pk.Packet) error {
			var d play.Disconnect
			if err := p.Scan(&d); err != nil {
				return Error{err}
			}
			return handler(d.Value)
		},
	})
}

func attachUpdateHealth(c *bot.Client, healthChangeHandler func(health float32, food int32, saturation float32) error, deathHandler func() error) {
	c.Events.AddListener(bot.PacketHandler{
		Priority: 64, ID: packetid.ClientboundPlaySetHealth,
		F: func(p pk.Packet) error {
			var sh play.SetHealth
			if err := p.Scan(&sh); err != nil {
				return Error{err}
			}
			var healthChangeErr, deathErr error
			if healthChangeHandler != nil {
				healthChangeErr = healthChangeHandler(float32(sh.Health), int32(sh.Food), float32(sh.Saturation))
			}
			if deathHandler != nil && sh.Health <= 0 {
				deathErr = deathHandler()
			}
			if healthChangeErr != nil || deathErr != nil {
				return updateHealthError{healthChangeErr, deathErr}
			}
			return nil
		},
	})
}

func attachPlayerPosition(c *bot.Client, handler func(x, y, z float64, yaw, pitch float32, flags int32, teleportID int32) error) {
	c.Events.AddListener(bot.PacketHandler{
		Priority: 64, ID: packetid.ClientboundPlayPlayerPosition,
		F: func(p pk.Packet) error {
			var pos play.PlayerPosition
			if err := p.Scan(&pos); err != nil {
				return Error{err}
			}
			c := pos.Change
			return handler(float64(c.Position.X), float64(c.Position.Y), float64(c.Position.Z),
				float32(c.YRot), float32(c.XRot), int32(pos.Relatives), int32(pos.ID))
		},
	})
}

type updateHealthError struct {
	healthChangeErr, deathErr error
}

func (u updateHealthError) Unwrap() error {
	if u.healthChangeErr != nil {
		return u.healthChangeErr
	}
	if u.deathErr != nil {
		return u.deathErr
	}
	return nil
}

func (u updateHealthError) Error() string {
	switch {
	case u.healthChangeErr != nil && u.deathErr != nil:
		return "[" + u.healthChangeErr.Error() + ", " + u.deathErr.Error() + "]"
	case u.healthChangeErr != nil:
		return u.healthChangeErr.Error()
	case u.deathErr != nil:
		return u.deathErr.Error()
	default:
		return "nil"
	}
}
