package bot

import (
	"errors"
	"fmt"

	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

// HandleGame receive server packet and response them correctly.
// Note that HandleGame will block if you don't receive from Events.
func (c *Client) HandleGame() error {
	for {
		var p pk.Packet
		// Read packets
		if err := c.Conn.ReadPacket(&p); err != nil {
			return err
		}

		if p.ID == int32(packetid.ClientboundPlayBundleDelimiter) {
			err := c.handleBundlePackets()
			if err != nil {
				return err
			}
		} else if p.ID == int32(packetid.ClientboundPlayStartConfiguration) {
			// MC 1.21.2+: server-initiated config-phase re-entry during play.
			if err := c.handleStartConfiguration(); err != nil {
				return err
			}
		} else {
			// handle packets
			err := c.handlePacket(p)
			if err != nil {
				return err
			}

			// return the packet buffer
			c.Conn.pool.Put(p.Data)
		}
	}
}

type PacketHandlerError struct {
	ID  packetid.ClientboundPacketID
	Err error
}

func (d PacketHandlerError) Error() string {
	return fmt.Sprintf("handle packet %v error: %v", d.ID, d.Err)
}

func (d PacketHandlerError) Unwrap() error {
	return d.Err
}

func (c *Client) handleBundlePackets() (err error) {
	var packets []pk.Packet
	for i := 0; i < 4096; i++ {
		var p pk.Packet
		// Read packets
		if err := c.Conn.ReadPacket(&p); err != nil {
			return err
		}

		if p.ID == int32(packetid.ClientboundPlayBundleDelimiter) {
			// bundle finished
			goto handlePackets
		}

		packets = append(packets, p)
	}
	return errors.New("packet number of a bundle out of limit")

handlePackets:
	for i := range packets {
		if err := c.handlePacket(packets[i]); err != nil {
			return err
		}
	}
	return nil
}

// handleStartConfiguration handles a server-initiated config-phase re-entry (MC 1.21.2+).
// The server sends ClientboundStartConfiguration during play; the client must ACK in play
// phase, then run the config loop, and the server resumes play when done.
func (c *Client) handleStartConfiguration() error {
	ack := play.ConfigurationAcknowledged{}
	if err := c.Conn.WritePacket(pk.Marshal(ack.PacketID(), ack)); err != nil {
		return err
	}
	// c.Conn is queue-based: the background goroutine pushes config-phase packets into
	// the queue, so reading from c.Conn here is safe and correct.
	return c.joinConfiguration(c.Conn)
}

func (c *Client) handlePacket(p pk.Packet) (err error) {
	packetID := packetid.ClientboundPacketID(p.ID)
	for _, handler := range c.Events.generic {
		if err = handler.F(p); err != nil {
			return PacketHandlerError{ID: packetID, Err: err}
		}
	}
	for _, handler := range c.Events.handlers[packetID] {
		err = handler.F(p)
		if err != nil {
			return PacketHandlerError{ID: packetID, Err: err}
		}
	}
	return
}
