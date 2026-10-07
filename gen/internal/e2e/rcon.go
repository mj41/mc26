package e2e

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// rconClient is a minimal Source RCON client (what the vanilla server speaks):
// a frame is length (int32 LE, not counting itself), request id, type, the
// payload and two zero bytes. Kept here so the generator module needs nothing
// from the library beyond nbt.
type rconClient struct {
	conn           net.Conn // nil after an answer that broke off: dialled again for the next command
	id             int32
	addr, password string        // to dial again
	timeout        time.Duration // how long an answer may take (0: rconTimeout)
	mu             sync.Mutex    // one command at a time: answers are matched by id
}

const (
	rconAuth        = 3
	rconCommand     = 2
	rconResponseVal = 0
	// rconTimeout is how long an answer may take.
	rconTimeout = 20 * time.Second
	// rconSplit is where vanilla's RconClient cuts an answer into frames of
	// the same request id: 4096 characters, each frame at least as many
	// bytes, so a shorter frame is an answer's last.
	rconSplit = 4096
)

func dialRCON(addr, password string) (*rconClient, error) {
	c := &rconClient{addr: addr, password: password}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

// dial connects and authenticates.
func (c *rconClient) dial() error {
	conn, err := net.DialTimeout("tcp", c.addr, 10*time.Second)
	if err != nil {
		return err
	}
	c.conn = conn
	fail := func(err error) error {
		conn.Close()
		c.conn = nil
		return err
	}
	if _, err := c.send(rconAuth, c.password); err != nil {
		return fail(err)
	}
	id, typ, _, err := c.recv()
	if err != nil {
		return fail(err)
	}
	// The server answers auth with an empty response value frame first on
	// some versions; read until the auth reply (type 2) arrives.
	for typ != rconCommand {
		if id, typ, _, err = c.recv(); err != nil {
			return fail(err)
		}
	}
	if id == -1 {
		return fail(fmt.Errorf("rcon: wrong password"))
	}
	return nil
}

// command sends one command and returns the server's whole text answer.
// The command is sent again, on a fresh connection, only when it provably
// did not reach the server (the connection could not be written to). An
// answer that broke off or came late is this command's error — the server
// may have run it, and a `give`, a `fill` or a `time add` must not run
// twice — and the next command dials again.
func (c *rconClient) command(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for try := 0; ; try++ {
		if c.conn == nil {
			if err := c.dial(); err != nil {
				return "", err
			}
		}
		id, err := c.send(rconCommand, cmd)
		if err != nil {
			c.drop()
			if try == 0 {
				continue // not sent: once more on a fresh connection
			}
			return "", err
		}
		ans, err := c.answer(id)
		if err != nil {
			c.drop()
			return "", err
		}
		return ans, nil
	}
}

// answer reads the answer to request id, every frame of it. A long answer
// comes in frames of rconSplit characters under the same id and nothing
// marks the last one: then a request the server answers at once (a type it
// does not know: "Unknown request 0") follows, and the frames up to its
// answer are this one's. It is sent only once the first frame is in — the
// server is past the command — since vanilla takes a request from a single
// read and refuses two that came together.
func (c *rconClient) answer(id int32) (string, error) {
	got, _, payload, err := c.recv()
	if err != nil {
		return "", err
	}
	if got != id {
		return "", fmt.Errorf("rcon: the answer to request %d came for %d", id, got)
	}
	if len(payload) < rconSplit {
		return payload, nil
	}
	end, err := c.send(rconResponseVal, "")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(payload)
	for {
		got, _, payload, err := c.recv()
		if err != nil {
			return "", err
		}
		switch got {
		case id:
			sb.WriteString(payload)
		case end:
			return sb.String(), nil
		default:
			return "", fmt.Errorf("rcon: the answer to request %d came for %d", id, got)
		}
	}
}

// drop closes a connection whose answers can no longer be trusted.
func (c *rconClient) drop() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *rconClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// RCON sends one command to a server's RCON (a test server's: 127.0.0.1,
// the server's port + 1, password mc26) and returns its answer.
func RCON(addr, password, cmd string) (string, error) {
	c, err := dialRCON(addr, password)
	if err != nil {
		return "", err
	}
	defer c.close()
	return c.command(cmd)
}

// send writes one request and returns its id.
func (c *rconClient) send(typ int32, payload string) (int32, error) {
	c.id++
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, int32(len(payload)+10))
	binary.Write(&buf, binary.LittleEndian, c.id)
	binary.Write(&buf, binary.LittleEndian, typ)
	buf.WriteString(payload)
	buf.Write([]byte{0, 0})
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(buf.Bytes())
	return c.id, err
}

func (c *rconClient) recv() (id, typ int32, payload string, err error) {
	timeout := c.timeout
	if timeout == 0 {
		timeout = rconTimeout
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	var length int32
	if err = binary.Read(c.conn, binary.LittleEndian, &length); err != nil {
		return
	}
	if length < 10 || length > 1<<20 {
		err = fmt.Errorf("rcon: bad frame length %d", length)
		return
	}
	body := make([]byte, length)
	if _, err = io.ReadFull(c.conn, body); err != nil {
		return
	}
	id = int32(binary.LittleEndian.Uint32(body[0:4]))
	typ = int32(binary.LittleEndian.Uint32(body[4:8]))
	payload = string(body[8 : len(body)-2])
	return
}
