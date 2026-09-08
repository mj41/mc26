package bot

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/configuration"
	"github.com/mj41/go-mc26/protocol/types"
)

// packetReadWriter abstracts both *net.Conn (initial login) and *Conn (config re-entry)
// so that joinConfiguration can be used in both contexts.
type packetReadWriter interface {
	ReadPacket(*pk.Packet) error
	WritePacket(pk.Packet) error
}

type ConfigHandler interface {
	EnableFeature(features []pk.Identifier)

	PushResourcePack(res ResourcePack)
	PopResourcePack(id pk.UUID)
	PopAllResourcePack()

	SelectDataPacks(packs []DataPack) []DataPack
}

type ResourcePack struct {
	ID            pk.UUID
	URL           string
	Hash          string
	Forced        bool
	PromptMessage *chat.Message // Optional
}

// DataPack is a known pack offered by the server (namespace, id, version).
type DataPack = types.KnownPack

type ConfigErr struct {
	Stage string
	Err   error
}

func (l ConfigErr) Error() string {
	return "bot: configuration error: [" + l.Stage + "] " + l.Err.Error()
}

func (l ConfigErr) Unwrap() error {
	return l.Err
}

// send marshals a serverbound configuration packet and writes it.
func sendConfig[P interface {
	pk.FieldEncoder
	PacketID() packetid.ServerboundPacketID
}](conn packetReadWriter, p P) error {
	return conn.WritePacket(pk.Marshal(p.PacketID(), p))
}

func (c *Client) joinConfiguration(conn packetReadWriter) error {
	for {
		var p pk.Packet
		if err := conn.ReadPacket(&p); err != nil {
			return ConfigErr{"config custom payload", err}
		}

		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundConfigurationCookieRequest:
			var req configuration.CookieRequest
			if err := p.Scan(&req); err != nil {
				return ConfigErr{"cookie request", err}
			}
			cookieContent := c.Cookies[string(req.Key)]
			resp := configuration.CookieResponse{Key: req.Key}
			resp.Payload.Has = cookieContent != nil
			resp.Payload.Val = pk.ByteArray(cookieContent)
			if err := sendConfig(conn, resp); err != nil {
				return ConfigErr{"cookie response", err}
			}

		case packetid.ClientboundConfigurationCustomPayload:
			var payload configuration.ClientboundCustomPayload
			if err := p.Scan(&payload); err != nil {
				return ConfigErr{"custom payload", err}
			}
			// TODO: Provide configuration custom data handling interface
			//
			// There are two types of Custom packet.
			// One for Login stage, the other for config and play stage.
			// The first one called "Custom Query", and the second one called "Custom Payload".
			// We can know the different by their name, the "query" is one request to one response, paired.
			// But the second one can be sent in any order.
			//
			// And the custome payload packet seems to be same in config stage and play stage.
			// How do we provide API for that?

		case packetid.ClientboundConfigurationDisconnect:
			const ErrStage = "disconnect"
			var reason chat.Message
			if err := p.Scan(&reason); err != nil {
				return ConfigErr{ErrStage, err}
			}
			return ConfigErr{ErrStage, DisconnectErr(reason)}

		case packetid.ClientboundConfigurationFinishConfiguration:
			if err := sendConfig(conn, configuration.ServerboundFinishConfiguration{}); err != nil {
				return ConfigErr{"finish config", err}
			}
			return nil

		case packetid.ClientboundConfigurationKeepAlive:
			const ErrStage = "keep alive"
			var ka configuration.ClientboundKeepAlive
			if err := p.Scan(&ka); err != nil {
				return ConfigErr{ErrStage, err}
			}
			if err := sendConfig(conn, configuration.ServerboundKeepAlive{ID: ka.ID}); err != nil {
				return ConfigErr{ErrStage, err}
			}

		case packetid.ClientboundConfigurationPing:
			var ping configuration.Ping
			if err := p.Scan(&ping); err != nil {
				return ConfigErr{"ping", err}
			}
			if err := sendConfig(conn, configuration.Pong{ID: ping.ID}); err != nil {
				return ConfigErr{"pong", err}
			}

		case packetid.ClientboundConfigurationResetChat:
			// TODO

		case packetid.ClientboundConfigurationRegistryData:
			// The registry system decodes the entries straight into c.Registries.
			const ErrStage = "registry"
			var registryID pk.Identifier

			r := bytes.NewReader(p.Data)
			_, err := registryID.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, err}
			}

			registry := c.Registries.Registry(string(registryID))

			_, err = registry.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, fmt.Errorf("failed to read registry %s: %w", registryID, err)}
			}

		case packetid.ClientboundConfigurationResourcePackPop:
			var pop configuration.ResourcePackPop
			if err := p.Scan(&pop); err != nil {
				return ConfigErr{"resource pack pop", err}
			}
			if pop.ID.Has {
				c.ConfigHandler.PopResourcePack(pop.ID.Val)
			} else {
				c.ConfigHandler.PopAllResourcePack()
			}

		case packetid.ClientboundConfigurationResourcePackPush:
			var push configuration.ResourcePackPush
			if err := p.Scan(&push); err != nil {
				return ConfigErr{"resource pack", err}
			}
			res := ResourcePack{
				ID:     push.ID,
				URL:    string(push.URL),
				Hash:   string(push.Hash),
				Forced: bool(push.Required),
			}
			if push.Prompt.Has {
				res.PromptMessage = push.Prompt.Pointer()
			}
			c.ConfigHandler.PushResourcePack(res)

		case packetid.ClientboundConfigurationStoreCookie:
			var store configuration.StoreCookie
			if err := p.Scan(&store); err != nil {
				return ConfigErr{"store cookie", err}
			}
			c.Cookies[string(store.Key)] = []byte(store.Payload)

		case packetid.ClientboundConfigurationTransfer:
			var transfer configuration.Transfer
			if err := p.Scan(&transfer); err != nil {
				return ConfigErr{"transfer", err}
			}
			// TODO: trnasfer to the specific server
			// How does it work? Just connect the new server, and re-start at handshake?

		case packetid.ClientboundConfigurationUpdateEnabledFeatures:
			var features configuration.UpdateEnabledFeatures
			if err := p.Scan(&features); err != nil {
				return ConfigErr{"update enabled features", err}
			}
			c.ConfigHandler.EnableFeature(features.Features)

		case packetid.ClientboundConfigurationUpdateTags:
			const ErrStage = "update tags"
			r := bytes.NewReader(p.Data)

			var length pk.VarInt
			_, err := length.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, err}
			}

			var registryID pk.Identifier
			for i := 0; i < int(length); i++ {
				_, err = registryID.ReadFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, err}
				}

				registry := c.Registries.Registry(string(registryID))
				if registry == nil {
					// TODO: Sice our registry system is incompelted, ignore all tags bind to non-exist registry
					_, err = idleTagsDecoder{}.ReadFrom(r)
					if err != nil {
						return ConfigErr{ErrStage, err}
					}
					continue
					// return ConfigErr{ErrStage, errors.New("unknown registry: " + string(registryID))}
				}

				_, err = registry.ReadTagsFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, err}
				}
			}

		case packetid.ClientboundConfigurationSelectKnownPacks:
			const ErrStage = "select known packs"
			var offered configuration.ClientboundSelectKnownPacks
			if err := p.Scan(&offered); err != nil {
				return ConfigErr{ErrStage, err}
			}
			known := c.ConfigHandler.SelectDataPacks(offered.KnownPacks)
			if err := sendConfig(conn, configuration.ServerboundSelectKnownPacks{KnownPacks: known}); err != nil {
				return ConfigErr{ErrStage, err}
			}

		case packetid.ClientboundConfigurationCustomReportDetails:
			var details configuration.CustomReportDetails
			if err := p.Scan(&details); err != nil {
				return ConfigErr{"custom report details", err}
			}
			for _, d := range details.Details {
				c.CustomReportDetails[string(d.Key)] = string(d.Val)
			}

		case packetid.ClientboundConfigurationServerLinks:
			// TODO

		case packetid.ClientboundConfigurationClearDialog:
			// No UI to clear; ignore.

		case packetid.ClientboundConfigurationShowDialog:
			// Bot has no UI; ignore the dialog.

		case packetid.ClientboundConfigurationCodeOfConduct:
			// Server waits for acceptance before continuing configuration.
			if err := sendConfig(conn, configuration.AcceptCodeOfConduct{}); err != nil {
				return ConfigErr{"accept code of conduct", err}
			}
		}
	}
}

type DefaultConfigHandler struct {
	resourcesPack []ResourcePack
}

func NewDefaultConfigHandler() *DefaultConfigHandler {
	return &DefaultConfigHandler{
		resourcesPack: make([]ResourcePack, 0),
	}
}

func (d *DefaultConfigHandler) EnableFeature(features []pk.Identifier) {}

func (d *DefaultConfigHandler) PushResourcePack(res ResourcePack) {
	d.resourcesPack = append(d.resourcesPack, res)
}

func (d *DefaultConfigHandler) PopResourcePack(id pk.UUID) {
	for i, v := range d.resourcesPack {
		if id == v.ID {
			d.resourcesPack = append(d.resourcesPack[:i], d.resourcesPack[i+1:]...)
			break
		}
	}
}

func (d *DefaultConfigHandler) PopAllResourcePack() {
	d.resourcesPack = d.resourcesPack[:0]
}

func (d *DefaultConfigHandler) SelectDataPacks(packs []DataPack) []DataPack {
	return []DataPack{}
}

type idleTagsDecoder struct{}

func (idleTagsDecoder) ReadFrom(r io.Reader) (int64, error) {
	var count pk.VarInt
	var tag pk.Identifier
	var length pk.VarInt
	n, err := count.ReadFrom(r)
	if err != nil {
		return n, err
	}
	for i := 0; i < int(count); i++ {
		var n1, n2, n3 int64
		n1, err = tag.ReadFrom(r)
		if err != nil {
			return n + n1, err
		}
		n2, err = length.ReadFrom(r)
		if err != nil {
			return n + n1 + n2, err
		}
		n += n1 + n2

		var id pk.VarInt
		for i := 0; i < int(length); i++ {
			n3, err = id.ReadFrom(r)
			if err != nil {
				return n + n3, err
			}
			n += n3
		}
	}
	return n, nil
}
