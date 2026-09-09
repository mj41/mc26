package sign

import (
	"time"

	"github.com/mj41/go-mc26/protocol/types"
)

// MessageBody is a signed message's body with every last-seen signature
// resolved: what the wire carries as types.SignedMessageBodyPacked, once the
// signatures it only referred to by index are taken from the cache.
type MessageBody struct {
	PlainMsg  string
	Timestamp time.Time
	Salt      int64
	LastSeen  []*Signature
}

// Signature is a message signature: 256 bytes.
type Signature [256]byte

// Unpack resolves a packed body: an entry with id 0 carries its signature, any
// other refers to the cache entry id-1.
func Unpack(m *types.SignedMessageBodyPacked, cache *SignatureCache) (*MessageBody, error) {
	lastSeen := make([]*Signature, len(m.LastSeen.Entries))
	for i, v := range m.LastSeen.Entries {
		if v.ID == 0 {
			s := Signature(v.FullSignature)
			lastSeen[i] = &s
		} else if id := int(v.ID) - 1; id >= 0 && id < len(cache.signatures) {
			lastSeen[i] = cache.signatures[id]
		} else {
			return nil, UncachedSignature
		}
	}
	return &MessageBody{
		PlainMsg:  string(m.Content),
		Timestamp: time.UnixMilli(int64(m.TimeStamp)),
		Salt:      int64(m.Salt),
		LastSeen:  lastSeen,
	}, nil
}
