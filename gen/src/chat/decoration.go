package chat

import (
	"fmt"
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

// ChatTypeDecoration (style_gen.go) is the chat type decoration: a translation key,
// the parameters it takes and a style. This file is its wire form inside an
// inline chat type, and Decorate.

// Type is the chat type bound to a player chat message: a registry id, or an
// inline chat type when the server did not reference the registry.
type Type struct {
	ID         int32       // index into the minecraft:chat_type registry; -1 when Inline is set
	Inline     *InlineType // inline chat type when the server did not reference the registry
	SenderName Message
	TargetName *Message
}

type InlineType struct {
	Chat      ChatTypeDecoration
	Narration ChatTypeDecoration
}

func (d *ChatTypeDecoration) ReadFrom(r io.Reader) (n int64, err error) {
	var params []pk.VarInt
	var style Style
	n, err = pk.Tuple{
		(*pk.String)(&d.TranslationKey),
		pk.Array(&params),
		pk.NBTField{V: &style, AllowUnknownFields: true},
	}.ReadFrom(r)
	if err != nil {
		return
	}
	d.Style = &style
	d.Parameters = make([]ChatTypeDecorationParameter, len(params))
	for i, v := range params {
		if v < 0 || int(v) >= len(ChatTypeDecorationParameterValues) {
			return n, fmt.Errorf("unknown chat decoration parameter %d", v)
		}
		d.Parameters[i] = ChatTypeDecorationParameterValues[v]
	}
	return
}

func (d ChatTypeDecoration) WriteTo(w io.Writer) (n int64, err error) {
	params := make([]pk.VarInt, len(d.Parameters))
	for i, p := range d.Parameters {
		idx := -1
		for j, name := range ChatTypeDecorationParameterValues {
			if name == p {
				idx = j
			}
		}
		if idx < 0 {
			return 0, fmt.Errorf("unknown chat decoration parameter %q", p)
		}
		params[i] = pk.VarInt(idx)
	}
	style := d.Style
	if style == nil {
		style = &Style{}
	}
	return pk.Tuple{
		pk.String(d.TranslationKey),
		pk.Array(params),
		pk.NBTField{V: style},
	}.WriteTo(w)
}

func (i *InlineType) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&i.Chat, &i.Narration}.ReadFrom(r)
}

func (i InlineType) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{i.Chat, i.Narration}.WriteTo(w)
}

// Decorate renders content through the decoration d: the translation with the
// sender, target and content in the places the parameters name, in d's style.
func (t *Type) Decorate(content Message, d *ChatTypeDecoration) (msg Message) {
	with := make([]any, len(d.Parameters))
	for i, para := range d.Parameters {
		switch para {
		case ChatTypeDecorationParameterSender:
			with[i] = t.SenderName
		case ChatTypeDecorationParameterTarget:
			if t.TargetName != nil {
				with[i] = *t.TargetName
			} else {
				with[i] = Text("")
			}
		case ChatTypeDecorationParameterContent:
			with[i] = content
		default:
			with[i] = Text("<nil>")
		}
	}
	msg = Message{
		Translate: d.TranslationKey,
		With:      with,
	}
	if s := d.Style; s != nil {
		msg.Bold = s.Bold
		msg.Italic = s.Italic
		msg.UnderLined = s.Underlined
		msg.StrikeThrough = s.Strikethrough
		msg.Obfuscated = s.Obfuscated
		msg.Font = s.Font
		msg.Color = s.Color
		msg.Insertion = s.Insertion
		msg.ClickEvent = s.ClickEvent
		msg.HoverEvent = s.HoverEvent
	}
	return msg
}

func (t *Type) ReadFrom(r io.Reader) (n int64, err error) {
	var hasTargetName pk.Boolean
	var holder pk.VarInt
	n1, err := holder.ReadFrom(r)
	if err != nil {
		return n1, err
	}
	if holder == 0 {
		t.ID = -1
		t.Inline = new(InlineType)
		n0, err := t.Inline.ReadFrom(r)
		n1 += n0
		if err != nil {
			return n1, fmt.Errorf("read inline chat type error: %w", err)
		}
	} else {
		t.ID = int32(holder) - 1
		t.Inline = nil
	}
	n2, err := t.SenderName.ReadFrom(r)
	if err != nil {
		return n1 + n2, fmt.Errorf("read sender name error: %w", err)
	}
	n3, err := hasTargetName.ReadFrom(r)
	if err != nil {
		return n1 + n2 + n3, fmt.Errorf("read has target name error: %w", err)
	}
	if hasTargetName {
		t.TargetName = new(Message)
		n4, err := t.TargetName.ReadFrom(r)
		if err != nil {
			return n1 + n2 + n3 + n4, fmt.Errorf("read target name error: %w", err)
		}
		return n1 + n2 + n3 + n4, nil
	}
	t.TargetName = nil
	return n1 + n2 + n3, nil
}

func (t Type) WriteTo(w io.Writer) (int64, error) {
	if t.Inline != nil {
		return pk.Tuple{
			pk.VarInt(0),
			*t.Inline,
			t.SenderName,
			pk.OptionEncoder[Message]{Has: t.TargetName != nil, Val: derefMessage(t.TargetName)},
		}.WriteTo(w)
	}
	return pk.Tuple{
		pk.VarInt(t.ID + 1),
		t.SenderName,
		pk.OptionEncoder[Message]{Has: t.TargetName != nil, Val: derefMessage(t.TargetName)},
	}.WriteTo(w)
}

func derefMessage(m *Message) Message {
	if m == nil {
		return Message{}
	}
	return *m
}
