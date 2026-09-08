// Package capture records a session between a bot and a vanilla server as the
// frames that crossed the wire, rather than as the packets a library made of
// them.
//
// It listens on one address and forwards to another, copying the bytes through
// untouched and splitting them the way the frame section of nodes.json says: a
// var int byte count, then — once the server has asked for compression — a var
// int uncompressed size and a zlib stream when that size is not zero, then the
// var int packet id and the body. It follows the state changes it sees, so
// every packet is recorded under the state and flow that name it in the schema,
// which is what a reader needs to look it up.
//
// Recording here rather than inside the library is the point of it. Both
// directions are recorded, so what a bot sends is checked against the same
// description as what it receives; and the frame itself is read from the
// description rather than from the library's own framing code, so a packet
// nothing in the library constructs is still recorded.
package capture

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"syscall"
)

// Packet is one packet as it travelled, with what a reader needs to find it in
// the schema.
type Packet struct {
	State string `json:"state"`
	Flow  string `json:"flow"`
	ID    int32  `json:"id"`
	Data  string `json:"data"` // hex of everything after the packet id
}

// IDs are the packets that change the state or the framing. They are read from
// packets.json rather than written down here, since their numbers move between
// versions like every other packet's.
type IDs struct {
	Compression       int32 // login/clientbound minecraft:login_compression
	EncryptionRequest int32 // login/clientbound minecraft:hello
	LoginAck          int32 // login/serverbound minecraft:login_acknowledged
	FinishConfig      int32 // configuration/serverbound minecraft:finish_configuration
	ConfigAck         int32 // play/serverbound minecraft:configuration_acknowledged
}

// LoadIDs reads the state-changing packet ids from a packets.json.
func LoadIDs(path string) (IDs, error) {
	var report map[string]map[string]map[string]struct {
		ProtocolID int32 `json:"protocol_id"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return IDs{}, err
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return IDs{}, fmt.Errorf("%s: %w", path, err)
	}
	var ids IDs
	for _, want := range []struct {
		state, flow, name string
		into              *int32
	}{
		{"login", "clientbound", "minecraft:login_compression", &ids.Compression},
		{"login", "clientbound", "minecraft:hello", &ids.EncryptionRequest},
		{"login", "serverbound", "minecraft:login_acknowledged", &ids.LoginAck},
		{"configuration", "serverbound", "minecraft:finish_configuration", &ids.FinishConfig},
		{"play", "serverbound", "minecraft:configuration_acknowledged", &ids.ConfigAck},
	} {
		p, ok := report[want.state][want.flow][want.name]
		if !ok {
			return IDs{}, fmt.Errorf("%s has no %s/%s %s, which says when the state changes",
				path, want.state, want.flow, want.name)
		}
		*want.into = p.ProtocolID
	}
	return ids, nil
}

// Proxy records every packet of every connection made through it.
type Proxy struct {
	Target string // host:port of the server
	IDs    IDs
	PerID  int                  // keep at most this many of each state/flow/id, 0 for all
	Log    func(string, ...any) //
	mu     sync.Mutex           //
	out    []Packet             //
	err    error                // the first frame this could not read
}

// Serve accepts connections until l is closed.
func (p *Proxy) Serve(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

// Packets are what was recorded, in the order it travelled.
func (p *Proxy) Packets() []Packet {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Packet(nil), p.out...)
}

// Err is the first frame the proxy could not read, if there was one. A session
// it could not follow is a hole in the recording, not a smaller recording, so
// it is an error rather than a shorter file.
func (p *Proxy) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Counts are how many packets were recorded per state and flow.
func (p *Proxy) Counts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]int{}
	for _, x := range p.out {
		out[x.State+"/"+x.Flow]++
	}
	return out
}

// Write writes a capture as one JSON object per line.
func Write(path string, ps []Packet) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, x := range ps {
		if err := enc.Encode(x); err != nil {
			return err
		}
	}
	return f.Close()
}

// Distinct is how many state/flow/id triples a capture holds, for reporting how
// much of the protocol a run reached.
func Distinct(ps []Packet) []string {
	seen := map[string]bool{}
	for _, x := range ps {
		seen[fmt.Sprintf("%s/%s/%d", x.State, x.Flow, x.ID)] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// session is one connection: both directions share the state and the
// compression threshold, because a packet in either direction changes them.
type session struct {
	p         *Proxy
	mu        sync.Mutex
	state     string
	threshold int // negative while compression is off
	// The per-id limit counts within one connection: every session logs in and is
	// configured, and its first chunk packet is the chunk the player stands in, so
	// a bot that rejoins after changing the world is recorded standing in the change.
	seen map[string]int
	last map[string]int // where the most recent packet of an id was put
}

func (p *Proxy) handle(client net.Conn) {
	defer client.Close()
	server, err := net.Dial("tcp", p.Target)
	if err != nil {
		p.fail(fmt.Errorf("dialling %s: %w", p.Target, err))
		return
	}
	defer server.Close()
	s := &session{p: p, state: "handshake", threshold: -1, seen: map[string]int{}, last: map[string]int{}}
	done := make(chan struct{}, 2)
	go func() { s.pump(server, client, "serverbound"); done <- struct{}{} }()
	go func() { s.pump(client, server, "clientbound"); done <- struct{}{} }()
	<-done
	// One side going away ends the session; closing both unblocks the other pump.
	client.Close()
	server.Close()
	<-done
}

func (p *Proxy) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
}

// pump copies one direction, recording each packet before passing it on. The
// order matters: a packet that changes the state must have changed it here
// before the other side can answer it.
func (s *session) pump(dst io.Writer, src io.Reader, flow string) {
	r := bufio.NewReaderSize(src, 1<<16)
	for {
		frame, err := readFrame(r)
		if err != nil {
			if err != io.EOF && !isClosed(err) {
				s.p.fail(fmt.Errorf("%s frame: %w", flow, err))
			}
			return
		}
		if err := s.record(flow, frame); err != nil {
			s.p.fail(err)
			return
		}
		if _, err := dst.Write(frame); err != nil {
			return
		}
	}
}

// record splits a frame into the packet it carries, notes what it changes and
// keeps it.
func (s *session) record(flow string, frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := s.body(frame)
	if err != nil {
		return fmt.Errorf("%s %s: %w", s.state, flow, err)
	}
	id, rest, err := readVarInt(body)
	if err != nil {
		return fmt.Errorf("%s %s packet id: %w", s.state, flow, err)
	}
	state := s.state
	if err := s.advance(state, flow, id, rest); err != nil {
		return err
	}
	s.keep(Packet{State: state, Flow: flow, ID: id, Data: hex.EncodeToString(rest)})
	return nil
}

// body is a frame without its length prefix and without its compression, if it
// had any.
func (s *session) body(frame []byte) ([]byte, error) {
	_, body, err := readVarInt(frame) // the length, which framed what we already have
	if err != nil {
		return nil, err
	}
	if s.threshold < 0 {
		return body, nil
	}
	size, rest, err := readVarInt(body)
	if err != nil {
		return nil, fmt.Errorf("uncompressed size: %w", err)
	}
	if size == 0 {
		return rest, nil // below the threshold: sent as it is
	}
	z, err := zlib.NewReader(bytes.NewReader(rest))
	if err != nil {
		return nil, fmt.Errorf("zlib: %w", err)
	}
	defer z.Close()
	out := make([]byte, size)
	if _, err := io.ReadFull(z, out); err != nil {
		return nil, fmt.Errorf("zlib: %d bytes: %w", size, err)
	}
	return out, nil
}

// advance applies what this packet changes: the state the next packets are in,
// or the framing they arrive in.
func (s *session) advance(state, flow string, id int32, data []byte) error {
	ids := s.p.IDs
	switch {
	case state == "handshake" && flow == "serverbound":
		// ClientIntentionPacket: protocol var int, host string, port, intent.
		// The intent is what says whether login or status follows.
		intent, err := intentOf(data)
		if err != nil {
			return fmt.Errorf("the handshake: %w", err)
		}
		switch intent {
		case 1:
			s.state = "status"
		case 2, 3: // login, transfer
			s.state = "login"
		default:
			return fmt.Errorf("the handshake asked for intent %d, which is neither status nor login", intent)
		}
	case state == "login" && flow == "clientbound" && id == ids.EncryptionRequest:
		return fmt.Errorf("the server asked for encryption; this records plain frames only")
	case state == "login" && flow == "clientbound" && id == ids.Compression:
		n, _, err := readVarInt(data)
		if err != nil {
			return fmt.Errorf("the compression threshold: %w", err)
		}
		s.threshold = int(n)
	case state == "login" && flow == "serverbound" && id == ids.LoginAck:
		s.state = "configuration"
	case state == "configuration" && flow == "serverbound" && id == ids.FinishConfig:
		s.state = "play"
	case state == "play" && flow == "serverbound" && id == ids.ConfigAck:
		s.state = "configuration"
	}
	return nil
}

// keep records a packet, at most PerID of each state/flow/id per session. What
// matters is the variety of ids, not the volume — but the newest of an id is kept
// too, replacing the last one recorded for it, because a packet that carries
// accumulated state says most when it is the latest: the inventory after every
// item was given, not after the first four.
func (s *session) keep(x Packet) {
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.PerID <= 0 {
		p.out = append(p.out, x)
		return
	}
	k := fmt.Sprintf("%s/%s/%d", x.State, x.Flow, x.ID)
	if s.seen[k] >= p.PerID {
		p.out[s.last[k]] = x
		return
	}
	s.seen[k]++
	s.last[k] = len(p.out)
	p.out = append(p.out, x)
}

// readFrame reads one length-prefixed frame and returns it whole, prefix and
// all, so it can be forwarded exactly as it arrived.
func readFrame(r *bufio.Reader) ([]byte, error) {
	var head []byte
	var n int32
	for shift := 0; ; shift += 7 {
		if shift >= 35 {
			return nil, fmt.Errorf("a length prefix longer than five bytes")
		}
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && len(head) > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		head = append(head, b)
		n |= int32(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
	}
	if n < 0 || n > 1<<24 {
		return nil, fmt.Errorf("a frame of %d bytes", n)
	}
	frame := make([]byte, len(head)+int(n))
	copy(frame, head)
	if _, err := io.ReadFull(r, frame[len(head):]); err != nil {
		return nil, err
	}
	return frame, nil
}

func readVarInt(b []byte) (int32, []byte, error) {
	var v int32
	for i := 0; i < 5; i++ {
		if i >= len(b) {
			return 0, nil, io.ErrUnexpectedEOF
		}
		v |= int32(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, b[i+1:], nil
		}
	}
	return 0, nil, fmt.Errorf("a var int longer than five bytes")
}

// intentOf reads the last field of the handshake, stepping over the three
// before it (net.minecraft.network.protocol.handshake.ClientIntentionPacket).
func intentOf(data []byte) (int32, error) {
	_, rest, err := readVarInt(data) // protocol version
	if err != nil {
		return 0, err
	}
	n, rest, err := readVarInt(rest) // host name, as a length and that many bytes
	if err != nil {
		return 0, err
	}
	if n < 0 || int(n)+2 > len(rest) {
		return 0, fmt.Errorf("a host name of %d bytes in %d", n, len(rest))
	}
	rest = rest[int(n)+2:] // the name and the two bytes of the port
	intent, _, err := readVarInt(rest)
	return intent, err
}

// isClosed reports whether the read failed because the session ended rather
// than because the frame was unreadable. A bot that disconnects mid-frame is
// not a fault of the recording.
func isClosed(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	return false
}
