package e2e

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// rconClient is a minimal Source RCON client (what the vanilla server speaks):
// a frame is length (int32 LE, not counting itself), request id, type, the
// payload and two zero bytes. Kept here so the generator module needs nothing
// from the library beyond nbt.
type rconClient struct {
	conn net.Conn
	id   int32
}

const (
	rconAuth        = 3
	rconCommand     = 2
	rconResponseVal = 0
)

func dialRCON(addr, password string) (*rconClient, error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	c := &rconClient{conn: conn}
	if err := c.send(rconAuth, password); err != nil {
		conn.Close()
		return nil, err
	}
	id, typ, _, err := c.recv()
	if err != nil {
		conn.Close()
		return nil, err
	}
	// The server answers auth with an empty response value frame first on
	// some versions; read until the auth reply (type 2) arrives.
	for typ != rconCommand {
		if id, typ, _, err = c.recv(); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if id == -1 {
		conn.Close()
		return nil, fmt.Errorf("rcon: wrong password")
	}
	return c, nil
}

// command sends one command and returns the server's text answer.
func (c *rconClient) command(cmd string) (string, error) {
	if err := c.send(rconCommand, cmd); err != nil {
		return "", err
	}
	_, _, payload, err := c.recv()
	return payload, err
}

func (c *rconClient) close() error { return c.conn.Close() }

func (c *rconClient) send(typ int32, payload string) error {
	c.id++
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, int32(len(payload)+10))
	binary.Write(&buf, binary.LittleEndian, c.id)
	binary.Write(&buf, binary.LittleEndian, typ)
	buf.WriteString(payload)
	buf.Write([]byte{0, 0})
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(buf.Bytes())
	return err
}

func (c *rconClient) recv() (id, typ int32, payload string, err error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
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
