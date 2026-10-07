package chat

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/mj41/go-mc26/nbt"
	pk "github.com/mj41/go-mc26/net/packet"
)

// ReadFrom decode Message in a Text component
func (m *Message) ReadFrom(r io.Reader) (int64, error) {
	return pk.NBT(m).ReadFrom(r)
}

// WriteTo encode Message into a Text component
func (m Message) WriteTo(w io.Writer) (int64, error) {
	return pk.NBT(&m).WriteTo(w)
}

func (m Message) TagType() byte {
	return nbt.TagCompound
}

// MarshalNBT writes the compound payload only: the caller has already written
// the tag type (network format, no name), so the marshalled bytes must not
// start with another type byte.
func (m Message) MarshalNBT(w io.Writer) error {
	var buf bytes.Buffer
	enc := nbt.NewEncoder(&buf)
	enc.NetworkFormat(true)
	var err error
	if m.Translate != "" {
		err = enc.Encode(translateMsg(m), "")
	} else {
		err = enc.Encode(rawMsgStruct(m), "")
	}
	if err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes()[1:]) // strip the TagCompound type byte
	return err
}

func (m *Message) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	var raw nbt.RawMessage
	if err := raw.UnmarshalNBT(tagType, r); err != nil {
		return err
	}
	if err := m.fromRaw(raw); err != nil {
		return err
	}
	m.raw = raw
	return nil
}

// Raw is the NBT the message was read from, as the server encoded it: the
// component's exact form (an explicit "italic: false" included), which its
// hash in a container click is made of. Type is 0 for a message made in Go.
func (m Message) Raw() nbt.RawMessage { return m.raw }

func (m *Message) fromRaw(raw nbt.RawMessage) error {
	// Re-combine the tagType into the reader, and create a nbt decoder
	tagReader := bytes.NewReader([]byte{raw.Type})
	decoder := nbt.NewDecoder(io.MultiReader(tagReader, bytes.NewReader(raw.Data)))
	decoder.NetworkFormat(true) // TagType directlly followed the body

	switch raw.Type {
	case nbt.TagString:
		_, err := decoder.Decode(&m.Text)
		return err
	case nbt.TagCompound:
		// An element of a list whose elements are not all compounds is
		// wrapped in one, under the empty key (NBT lists hold one type).
		var wrapped map[string]nbt.RawMessage
		if err := raw.Unmarshal(&wrapped); err == nil && len(wrapped) == 1 {
			if inner, ok := wrapped[""]; ok {
				return m.fromRaw(inner)
			}
		}
		_, err := decoder.Decode((*rawMsgStruct)(m))
		return err
	case nbt.TagList:
		_, err := decoder.Decode(&m.Extra)
		return err
	case nbt.TagByte, nbt.TagShort, nbt.TagInt, nbt.TagLong, nbt.TagFloat, nbt.TagDouble:
		// A bare number, as the server sends for a translatable component's
		// numeric arguments ("Successfully filled %s block(s)"): shown as text.
		var v any
		if _, err := decoder.Decode(&v); err != nil {
			return err
		}
		m.Text = fmt.Sprint(v)
		return nil
	default:
		return errors.New("unknown chat message type: '" + strconv.FormatUint(uint64(raw.Type), 16) + "'")
	}
}

func (t *TranslateArgs) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	tagReader := bytes.NewReader([]byte{tagType})
	decoder := nbt.NewDecoder(io.MultiReader(tagReader, r))
	decoder.NetworkFormat(true) // TagType directlly followed the body

	switch tagType {
	case nbt.TagList:
		var value []Message
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, v)
		}
		return nil
	case nbt.TagByteArray:
		var value []int8
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, strconv.FormatInt(int64(v), 10))
		}
		return nil
	case nbt.TagIntArray:
		var value []int32
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, strconv.FormatInt(int64(v), 10))
		}
		return nil
	case nbt.TagLongArray:
		var value []int64
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, strconv.FormatInt(int64(v), 10))
		}
		return nil
	default:
		return errors.New("unknown translation args type: '" + strconv.FormatUint(uint64(tagType), 16) + "'")
	}
}
