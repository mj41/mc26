package chat_test

import (
	"testing"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/lang"
	"github.com/mj41/go-mc26/nbt"
)

func TestMessage_UnmarshalJSON_string(t *testing.T) {
	snbts := []string{
		"{translate: sleep.players_sleeping, with: [I; 1, 37]}",
		// the server's command feedback carries a list of plain int tags
		"{translate: commands.fill.success, with: [512]}",
		// a list of texts not all compounds: NbtOps wraps the others under ""
		`{text: "Bold", extra: [{"": " and "}, {translate: "item.minecraft.stick"}]}`,
	}

	texts := []string{
		"1/37 players sleeping",
		"Successfully filled 512 block(s)",
		"Bold and Stick",
	}

	chat.SetLanguage(lang.English())
	for i, v := range snbts {
		bytes, err := nbt.Marshal(nbt.StringifiedMessage(v))
		if err != nil {
			t.Errorf("Invalid SNBT: %v", err)
			continue
		}

		var cm chat.Message
		if err := nbt.Unmarshal(bytes, &cm); err != nil {
			t.Error(err)
		}
		if str := cm.String(); str != texts[i] {
			t.Errorf("gets %q, wants %q", str, texts[i])
		}
	}
}
