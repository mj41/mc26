package server

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mj41/go-mc26/chat"
	"github.com/mj41/go-mc26/data/packetid"
	"github.com/mj41/go-mc26/net"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/offline"
	"github.com/mj41/go-mc26/protocol/login"
	"github.com/mj41/go-mc26/protocol/types"
	"github.com/mj41/go-mc26/server/auth"
	"github.com/mj41/go-mc26/yggdrasil/user"

	"github.com/google/uuid"
)

// LoginHandler is used to handle player login process, that is,
// from clientbound "LoginStart" packet to serverbound "LoginSuccess" packet.
type LoginHandler interface {
	AcceptLogin(conn *net.Conn, protocol int32) (name string, id uuid.UUID, profilePubKey *user.PublicKey, properties []user.Property, err error)
}

// LoginChecker is the interface to check if a player is allowed to log in the server.
// The checking could be anything, server player number, protocol version, blacklist or whitelist.
// If a player is not allowed to, the reason should be returned and will be sent to the client by "LoginDisconnect" packet.
type LoginChecker interface {
	CheckPlayer(name string, id uuid.UUID, protocol int32) (ok bool, reason chat.Message)
}

// Make sure MojangLoginHandler implement LoginHandler
var _ LoginHandler = (*MojangLoginHandler)(nil)

// MojangLoginHandler is a standard LoginHandler that implement both online and offline login progress.
// This implementation also supports custom LoginChecker.
// None of Custom login packets (also called LoginPluginRequest/Response) is supported for this implementation.
// To do that, implement your own LoginHandler imitate this code.
type MojangLoginHandler struct {
	// OnlineMode enables to check player's account.
	// And also encrypt the connection after login.
	OnlineMode bool

	// EnforceSecureProfile enforce to check the player's profile public key
	EnforceSecureProfile bool

	// Threshold set the smallest size of raw network payload to compress.
	// Set to 0 to compress all packets. Set to -1 to disable compression.
	Threshold int

	// LoginChecker is used to apply some checks before sending "LoginSuccess" packet
	// (e.g., blacklist or is server full).
	// This is an optional field and can be set to nil.
	LoginChecker

	// PrivateKey is the key used by encrypt the connection.
	privateKey     atomic.Pointer[rsa.PrivateKey]
	lockPrivateKey sync.Mutex
}

func (d *MojangLoginHandler) getPrivateKey() (key *rsa.PrivateKey, err error) {
	key = d.privateKey.Load()
	if key != nil {
		return
	}

	d.lockPrivateKey.Lock()
	defer d.lockPrivateKey.Unlock()

	key = d.privateKey.Load()
	if key == nil {
		key, err = rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			return
		}
		d.privateKey.Store(key)
	}
	return
}

/*
	var verifyToken [verifyTokenLen]byte
	_, err := rand.Read(verifyToken[:])
	if err != nil {
		return nil, err
	}
*/

// AcceptLogin implement LoginHandler for MojangLoginHandler
func (d *MojangLoginHandler) AcceptLogin(conn *net.Conn, protocol int32) (name string, id uuid.UUID, profilePubKey *user.PublicKey, properties []user.Property, err error) {
	// login start
	var p pk.Packet
	err = conn.ReadPacket(&p)
	if err != nil {
		return
	}
	var hello login.ServerboundHello
	if packetid.ServerboundPacketID(p.ID) != hello.PacketID() {
		err = wrongPacketErr{expect: int32(hello.PacketID()), get: p.ID}
		return
	}
	if err = p.Scan(&hello); err != nil {
		return
	}
	name, id = string(hello.Name), uuid.UUID(hello.ProfileID)

	// auth
	if d.OnlineMode {
		var serverKey *rsa.PrivateKey
		serverKey, err = d.getPrivateKey()
		if err != nil {
			return
		}
		var resp *auth.Resp
		// Auth, Encrypt
		resp, err = auth.Encrypt(conn, name, serverKey)
		if err != nil {
			return
		}
		name = resp.Name
		id = resp.ID
		properties = resp.Properties
	} else {
		// offline-mode UUID
		id = offline.NameToUUID(name)
	}

	// set compression
	if d.Threshold >= 0 {
		compression := login.LoginCompression{CompressionThreshold: pk.VarInt(d.Threshold)}
		err = conn.WritePacket(pk.Marshal(compression.PacketID(), compression))
		if err != nil {
			return
		}
		conn.SetThreshold(d.Threshold)
	}

	// check if player can join (whitelist, blacklist, server full or something else)
	if d.LoginChecker != nil {
		if ok, result := d.CheckPlayer(name, id, protocol); !ok {
			// player is not allowed to join the server
			err = LoginFailErr{reason: result}
			return
		}
	}
	// send login success (26.1: the game profile only)
	finished := login.LoginFinished{
		GameProfile: types.GameProfile{
			ID:         pk.UUID(id),
			Name:       pk.String(name),
			Properties: properties,
		},
	}
	err = conn.WritePacket(pk.Marshal(finished.PacketID(), finished))
	if err != nil {
		return
	}

	// receive login ack
	err = conn.ReadPacket(&p)
	if ack := (login.LoginAcknowledged{}); err == nil && packetid.ServerboundPacketID(p.ID) != ack.PacketID() {
		err = wrongPacketErr{expect: int32(ack.PacketID()), get: p.ID}
	}
	return
}

type GameProfile struct {
	ID   uuid.UUID
	Name string
}

type wrongPacketErr struct {
	expect, get int32
}

func (w wrongPacketErr) Error() string {
	return fmt.Sprintf("wrong packet id: expect %#02X, get %#02X", w.expect, w.get)
}

type LoginFailErr struct {
	reason chat.Message
}

func (l LoginFailErr) Error() string {
	return "login error: " + l.reason.ClearString()
}
