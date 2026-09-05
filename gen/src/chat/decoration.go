package chat

import (
	"fmt"
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

type Decoration struct {
	TranslationKey string   `nbt:"translation_key"`
	Parameters     []string `nbt:"parameters"`
	Style          struct {
		Bold          bool   `nbt:"bold,omitempty"`
		Italic        bool   `nbt:"italic,omitempty"`
		UnderLined    bool   `nbt:"underlined,omitempty"`
		StrikeThrough bool   `nbt:"strikethrough,omitempty"`
		Obfuscated    bool   `nbt:"obfuscated,omitempty"`
		Color         string `nbt:"color,omitempty"`
		Insertion     string `nbt:"insertion,omitempty"`
		Font          string `nbt:"font,omitempty"`
	} `nbt:"style,omitempty"`
}

// Type is the bound chat type sent with player/disguised chat packets.
//
// Wire (ChatType.Bound): chatType:registryEntryHolder, name:Component, targetName:Option<Component>.
// The holder is VarInt(id+1) for a minecraft:chat_type registry entry, or 0 followed by
// an inline ChatType (chat + narration decorations).
type Type struct {
	ID         int32       // index into the minecraft:chat_type registry; -1 when Inline is set
	Inline     *InlineType // inline chat type when the server did not reference the registry
	SenderName Message
	TargetName *Message
}

// InlineType is an inline ChatType (ChatType.DIRECT_STREAM_CODEC): chat + narration decorations.
type InlineType struct {
	Chat      Decoration
	Narration Decoration
}

// decorationParameters is the wire enum ChatTypeDecoration.Parameter.
var decorationParameters = []string{"sender", "target", "content"}

func (d *Decoration) ReadFrom(r io.Reader) (n int64, err error) {
	var params []pk.VarInt
	n, err = pk.Tuple{
		(*pk.String)(&d.TranslationKey),
		pk.Array(&params),
		pk.NBTField{V: &d.Style, AllowUnknownFields: true},
	}.ReadFrom(r)
	if err != nil {
		return
	}
	d.Parameters = make([]string, len(params))
	for i, v := range params {
		if v < 0 || int(v) >= len(decorationParameters) {
			return n, fmt.Errorf("unknown chat decoration parameter %d", v)
		}
		d.Parameters[i] = decorationParameters[v]
	}
	return
}

func (d Decoration) WriteTo(w io.Writer) (n int64, err error) {
	params := make([]pk.VarInt, len(d.Parameters))
	for i, p := range d.Parameters {
		idx := -1
		for j, name := range decorationParameters {
			if name == p {
				idx = j
			}
		}
		if idx < 0 {
			return 0, fmt.Errorf("unknown chat decoration parameter %q", p)
		}
		params[i] = pk.VarInt(idx)
	}
	return pk.Tuple{
		pk.String(d.TranslationKey),
		pk.Array(params),
		pk.NBTField{V: &d.Style},
	}.WriteTo(w)
}

func (i *InlineType) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&i.Chat, &i.Narration}.ReadFrom(r)
}

func (i InlineType) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{i.Chat, i.Narration}.WriteTo(w)
}

func (t *Type) Decorate(content Message, d *Decoration) (msg Message) {
	with := make([]any, len(d.Parameters))
	for i, para := range d.Parameters {
		switch para {
		case "sender":
			with[i] = t.SenderName
		case "target":
			if t.TargetName != nil {
				with[i] = *t.TargetName
			} else {
				with[i] = Text("")
			}
		case "content":
			with[i] = content
		default:
			with[i] = Text("<nil>")
		}
	}
	return Message{
		Translate: d.TranslationKey,
		With:      with,

		Bold:          d.Style.Bold,
		Italic:        d.Style.Italic,
		UnderLined:    d.Style.UnderLined,
		StrikeThrough: d.Style.StrikeThrough,
		Obfuscated:    d.Style.Obfuscated,
		Font:          d.Style.Font,
		Color:         d.Style.Color,
		Insertion:     d.Style.Insertion,
	}
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

// WriteTo encodes the bound chat type. Inline is written when set, otherwise the registry ID.
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
