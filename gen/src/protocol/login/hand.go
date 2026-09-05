package login

// Hand-written packets of the login state whose payload is "the rest of the
// packet" and therefore not describable by a codec tree.

import (
	"io"

	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/types"
)

// CustomQuery is clientbound minecraft:custom_query
// (ClientboundCustomQueryPacket): a transaction id, a channel and the payload.
type CustomQuery struct {
	TransactionID pk.VarInt
	Channel       pk.Identifier
	Data          types.RestBytes
}

func (CustomQuery) PacketID() packetid.ClientboundPacketID {
	return packetid.ClientboundLoginCustomQuery
}

func (p *CustomQuery) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.TransactionID, &p.Channel, &p.Data}.ReadFrom(r)
}

func (p CustomQuery) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.TransactionID, p.Channel, p.Data}.WriteTo(w)
}

// CustomQueryAnswer is serverbound minecraft:custom_query_answer
// (ServerboundCustomQueryAnswerPacket): the transaction id and, when the
// client understood the channel, the payload.
type CustomQueryAnswer struct {
	TransactionID pk.VarInt
	Data          pk.Option[types.RestBytes, *types.RestBytes]
}

func (CustomQueryAnswer) PacketID() packetid.ServerboundPacketID {
	return packetid.ServerboundLoginCustomQueryAnswer
}

func (p *CustomQueryAnswer) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.TransactionID, &p.Data}.ReadFrom(r)
}

func (p CustomQueryAnswer) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{p.TransactionID, p.Data}.WriteTo(w)
}
