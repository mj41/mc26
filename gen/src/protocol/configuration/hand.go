package configuration

// Hand-written packets of the configuration state whose payload is "the rest
// of the packet" and therefore not describable by a codec tree.

import (
	"io"

	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/types"
)

// CustomPayload is clientbound/serverbound minecraft:custom_payload
// (ClientboundCustomPayloadPacket / ServerboundCustomPayloadPacket): a channel
// name followed by the rest of the packet.
type CustomPayload struct {
	Channel pk.Identifier
	Data    types.RestBytes
}

func (p *CustomPayload) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.Channel, &p.Data}.ReadFrom(r)
}
func (p CustomPayload) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.Channel, p.Data}.WriteTo(w)
}

// ClientboundCustomPayloadID and ServerboundCustomPayloadID are the ids of CustomPayload in each flow.
const (
	ClientboundCustomPayloadID = packetid.ClientboundConfigCustomPayload
	ServerboundCustomPayloadID = packetid.ServerboundConfigCustomPayload
)
