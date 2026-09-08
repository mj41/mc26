package msg

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/bot/basic"
	"github.com/mj41/go-mc26/bot/playerlist"
	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/chat/sign"
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
)

// The Manager is used to receive and send chat messages.
type Manager struct {
	c      *bot.Client
	p      *basic.Player
	pl     *playerlist.PlayerList
	events EventsHandler

	sign.SignatureCache
}

// New returns a new chat manager.
func New(c *bot.Client, p *basic.Player, pl *playerlist.PlayerList, events EventsHandler) *Manager {
	m := &Manager{
		c:              c,
		p:              p,
		pl:             pl,
		events:         events,
		SignatureCache: sign.NewSignatureCache(),
	}
	if events.SystemChat != nil {
		c.Events.AddListener(bot.PacketHandler{
			Priority: 64, ID: packetid.ClientboundPlaySystemChat,
			F: m.handleSystemChat,
		})
	}
	if events.PlayerChatMessage != nil {
		c.Events.AddListener(bot.PacketHandler{
			Priority: 64, ID: packetid.ClientboundPlayPlayerChat,
			F: m.handlePlayerChat,
		})
	}
	if events.DisguisedChat != nil {
		c.Events.AddListener(bot.PacketHandler{
			Priority: 64, ID: packetid.ClientboundPlayDisguisedChat,
			F: m.handleDisguisedChat,
		})
	}
	return m
}

func (m *Manager) handleSystemChat(p pk.Packet) error {
	var sc play.SystemChat
	if err := p.Scan(&sc); err != nil {
		return err
	}
	return m.events.SystemChat(sc.Content, bool(sc.Overlay))
}

func (m *Manager) handlePlayerChat(packet pk.Packet) error {
	var pc play.PlayerChat
	if err := packet.Scan(&pc); err != nil {
		return err
	}
	sender, index, signature, body := pc.Sender, pc.Index, pc.Signature, pc.Body
	unsignedContent, filter, chatType := pc.UnsignedContent, pc.FilterMask, pc.ChatType

	unpackedMsg, err := body.Unpack(&m.SignatureCache)
	if err != nil {
		return InvalidChatPacket{err}
	}
	senderInfo, ok := m.pl.PlayerInfos[uuid.UUID(sender)]
	if !ok {
		return InvalidChatPacket{ErrUnknownPlayer}
	}
	decoration, err := m.chatDecoration(&chatType)
	if err != nil {
		return err
	}

	var message sign.Message
	if senderInfo.ChatSession != nil {
		message.Prev = sign.Prev{
			Index:   int(index),
			Sender:  uuid.UUID(sender),
			Session: senderInfo.ChatSession.SessionID,
		}
	} else {
		message.Prev = sign.Prev{
			Index:   0,
			Sender:  uuid.UUID(sender),
			Session: uuid.Nil,
		}
	}
	message.Signature = (*sign.Signature)(signature.Pointer()) // the same 256 bytes as types.MessageSignature
	message.MessageBody = unpackedMsg
	message.Unsigned = unsignedContent.Pointer()
	message.FilterMask = filter

	var validated bool
	if senderInfo.ChatSession != nil {
		if !senderInfo.ChatSession.VerifyAndUpdate(&message) {
			return ErrValidationFailed
		}
		validated = true
		// store signature into signatureCache
		m.PopOrInsert(message.Signature, message.LastSeen)
	}

	var content chat.Message
	if unsignedContent.Has {
		content = unsignedContent.Val
	} else {
		content = chat.Text(body.PlainMsg)
	}
	msg := chatType.Decorate(content, decoration)
	return m.events.PlayerChatMessage(msg, validated)
}

// chatDecoration resolves the chat decoration for a bound chat type: the inline
// definition if the server sent one, otherwise the minecraft:chat_type registry entry.
func (m *Manager) chatDecoration(t *chat.Type) (*chat.ChatTypeDecoration, error) {
	if t.Inline != nil {
		return &t.Inline.Chat, nil
	}
	ct := m.c.Registries.ChatType.GetByID(t.ID)
	if ct == nil {
		return nil, InvalidChatPacket{ErrUnknwonChatType}
	}
	return &ct.Chat, nil
}

func (m *Manager) handleDisguisedChat(packet pk.Packet) error {
	var dc play.DisguisedChat
	if err := packet.Scan(&dc); err != nil {
		return err
	}

	decoration, err := m.chatDecoration(&dc.ChatType)
	if err != nil {
		return err
	}
	msg := dc.ChatType.Decorate(dc.Message, decoration)

	return m.events.DisguisedChat(msg)
}

// SendMessage send chat message to server.
// Doesn't support sending message with signature currently.
func (m *Manager) SendMessage(msg string) error {
	if len(msg) > 256 {
		return errors.New("message length greater than 256")
	}

	var salt int64
	if err := binary.Read(rand.Reader, binary.BigEndian, &salt); err != nil {
		return err
	}

	c := play.Chat{
		Message:   pk.String(msg),
		TimeStamp: types.Instant(time.Now().UnixMilli()),
		Salt:      pk.Long(salt),
		// Signature: none (unsigned chat); LastSeenMessages: nothing acknowledged
	}
	return m.c.Conn.WritePacket(pk.Marshal(c.PacketID(), c))
}

// SendMessage send chat message to server.
// Doesn't support sending message with signature currently.
func (m *Manager) SendCommand(command string) error {
	if len(command) > 256 {
		return errors.New("message length greater than 256")
	}
	var salt int64
	if err := binary.Read(rand.Reader, binary.BigEndian, &salt); err != nil {
		return err
	}

	// 26.x: an unsigned command is just the command string (chat_command);
	// timestamp/salt/signatures belong to chat_command_signed.
	_ = salt
	c := play.ChatCommand{Command: pk.String(command)}
	return m.c.Conn.WritePacket(pk.Marshal(c.PacketID(), c))
}

type InvalidChatPacket struct {
	err error
}

func (i InvalidChatPacket) Error() string {
	if i.err == nil {
		return "invalid chat packet"
	}
	return "invalid chat packet: " + i.err.Error()
}

func (i InvalidChatPacket) Unwrap() error {
	return i.err
}

var (
	ErrUnknownPlayer          = errors.New("unknown player")
	ErrUnknwonChatType        = errors.New("unknown chat type")
	ErrValidationFailed error = bot.DisconnectErr(chat.TranslateMsg("multiplayer.disconnect.chat_validation_failed"))
)
