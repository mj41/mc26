// Package management is a client of the dedicated server's management
// protocol: JSON-RPC 2.0 over a WebSocket, served when server.properties has
// management-server-enabled=true, authenticated by the 40-character
// management-server-secret. The methods and the types are generated from the
// OpenRPC document the server publishes (types_gen.go, methods_gen.go); this
// file is the transport — the WebSocket handshake and frames, the request ids,
// and the notifications the server sends on its own.
//
//	c, err := management.Dial(ctx, "127.0.0.1:25585", secret)
//	st, err := c.ServerStatus(ctx)
//	for n := range c.Notifications() { ... }
//
// The server aggregates frames up to 64 KiB, so a request larger than that is
// refused by it; a response it sends is at most that large as well.
package management

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Error is an error the server returned for a request (JSON-RPC 2.0 error object).
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("management: %s (code %d): %s", e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("management: %s (code %d)", e.Message, e.Code)
}

// Option configures Dial.
type Option func(*Client)

// WithTLS connects over TLS (management-server-tls-enabled=true, the default
// of a server) with the given configuration; nil uses the defaults, which
// verify the server's certificate against the system roots.
func WithTLS(cfg *tls.Config) Option {
	return func(c *Client) {
		c.tls = cfg
		if c.tls == nil {
			c.tls = &tls.Config{}
		}
		c.useTLS = true
	}
}

// WithNotificationBuffer sets how many notifications are held for the reader
// of Notifications before the connection stops reading; 64 by default.
func WithNotificationBuffer(n int) Option {
	return func(c *Client) { c.notifyBuf = n }
}

// Client is one management connection.
type Client struct {
	conn      net.Conn
	tls       *tls.Config
	useTLS    bool
	notifyBuf int

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[int]chan response
	closed  chan struct{}
	err     error

	notifications chan Notification
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// Dial connects to the management server at addr (host:port) with its secret.
func Dial(ctx context.Context, addr, secret string, opts ...Option) (*Client, error) {
	c := &Client{pending: map[int]chan response{}, closed: make(chan struct{}), notifyBuf: 64}
	for _, o := range opts {
		o(c)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if c.useTLS {
		cfg := c.tls.Clone()
		if cfg.ServerName == "" {
			host, _, _ := net.SplitHostPort(addr)
			cfg.ServerName = host
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tc
	}
	c.conn = conn
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := c.handshake(addr, secret); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	c.notifications = make(chan Notification, c.notifyBuf)
	go c.readLoop()
	return c, nil
}

// handshake is the WebSocket opening handshake (RFC 6455 §4) on the path the
// server serves, with the secret as a bearer token: the server takes it from
// the Authorization header, or from Sec-WebSocket-Protocol as
// "minecraft-v1,<secret>" for a browser, and only the latter is subject to its
// allowed-origins check.
func (c *Client) handshake(addr, secret string) error {
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	nonce := base64.StdEncoding.EncodeToString(key[:])
	req := "GET / HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + nonce + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Authorization: Bearer " + secret + "\r\n\r\n"
	if _, err := io.WriteString(c.conn, req); err != nil {
		return err
	}
	br := bufio.NewReader(c.conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("management: websocket handshake: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("management: server refused the connection: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	h := sha1.Sum([]byte(nonce + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != base64.StdEncoding.EncodeToString(h[:]) {
		return fmt.Errorf("management: websocket handshake: bad Sec-WebSocket-Accept %q", got)
	}
	// Whatever the reader buffered past the headers is the start of the frames.
	if br.Buffered() > 0 {
		rest, _ := br.Peek(br.Buffered())
		c.conn = &prefixedConn{Conn: c.conn, head: append([]byte(nil), rest...)}
	}
	return nil
}

// prefixedConn replays bytes a buffered reader took past the HTTP response.
type prefixedConn struct {
	net.Conn
	head []byte
}

func (p *prefixedConn) Read(b []byte) (int, error) {
	if len(p.head) > 0 {
		n := copy(b, p.head)
		p.head = p.head[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// Notifications delivers what the server sends unasked, in order. The channel
// is closed when the connection ends; Err says why.
func (c *Client) Notifications() <-chan Notification { return c.notifications }

// Err is the error that ended the connection, nil while it is open or after a
// clean Close.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection.
func (c *Client) Close() error {
	c.writeMu.Lock()
	_ = c.writeFrame(opClose, []byte{0x03, 0xe8}) // 1000, normal closure
	c.writeMu.Unlock()
	return c.conn.Close()
}

// Call invokes a method: params is nil, or the one positional parameter the
// method takes wrapped as []any{value}, or a map[string]any of named
// parameters; result receives the "result" member. The generated methods call
// this, and a method absent from this version's document can be called by
// name.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params}
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	c.mu.Unlock()
	body, err := json.Marshal(req)
	if err != nil {
		c.forget(req.ID)
		return err
	}
	c.writeMu.Lock()
	err = c.writeFrame(opText, body)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(req.ID)
		return err
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return r.Error
		}
		if result == nil || len(r.Result) == 0 || string(r.Result) == "null" {
			return nil
		}
		return json.Unmarshal(r.Result, result)
	case <-ctx.Done():
		c.forget(req.ID)
		return ctx.Err()
	case <-c.closed:
		return c.Err()
	}
}

func (c *Client) forget(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// message is anything the server sends: a response (id + result/error) or a
// notification (method + params, no id).
type message struct {
	ID     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Params json.RawMessage  `json:"params"`
	Result json.RawMessage  `json:"result"`
	Error  *Error           `json:"error"`
}

func (c *Client) readLoop() {
	err := c.read()
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.closed)
	close(c.notifications)
	_ = c.conn.Close()
}

func (c *Client) read() error {
	var text []byte
	for {
		op, fin, payload, err := c.readFrame()
		if err != nil {
			return err
		}
		switch op {
		case opPing:
			c.writeMu.Lock()
			err = c.writeFrame(opPong, payload)
			c.writeMu.Unlock()
			if err != nil {
				return err
			}
			continue
		case opPong:
			continue
		case opClose:
			if len(payload) >= 2 {
				return fmt.Errorf("management: server closed the connection (%d)", binary.BigEndian.Uint16(payload))
			}
			return errors.New("management: server closed the connection")
		case opText, opBinary:
			text = append(text[:0], payload...)
		case opContinuation:
			text = append(text, payload...)
		default:
			return fmt.Errorf("management: websocket opcode %d", op)
		}
		if !fin {
			continue
		}
		if err := c.dispatch(text); err != nil {
			return err
		}
	}
}

func (c *Client) dispatch(text []byte) error {
	trimmed := strings.TrimSpace(string(text))
	if strings.HasPrefix(trimmed, "[") {
		var batch []json.RawMessage
		if err := json.Unmarshal(text, &batch); err != nil {
			return fmt.Errorf("management: bad batch: %w", err)
		}
		for _, m := range batch {
			if err := c.dispatch(m); err != nil {
				return err
			}
		}
		return nil
	}
	var m message
	if err := json.Unmarshal(text, &m); err != nil {
		return fmt.Errorf("management: bad message: %w", err)
	}
	if m.ID == nil || string(*m.ID) == "null" {
		if m.Method == "" {
			return nil // an error with no id: nothing to match it to
		}
		n, err := decodeNotification(m.Method, m.Params)
		if err != nil {
			return fmt.Errorf("management: notification %s: %w", m.Method, err)
		}
		c.notifications <- n
		return nil
	}
	var id int
	if err := json.Unmarshal(*m.ID, &id); err != nil {
		return nil // not one of ours: we only send integer ids
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- response{Result: m.Result, Error: m.Error}
	}
	return nil
}

// decodeParam takes the one parameter of a notification, which the server may
// send positionally ([value]) or by name ({"name": value}).
func decodeParam(params json.RawMessage, name string, into any) error {
	t := strings.TrimSpace(string(params))
	switch {
	case strings.HasPrefix(t, "["):
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return err
		}
		if len(arr) == 0 {
			return errors.New("empty params array")
		}
		return json.Unmarshal(arr[0], into)
	case strings.HasPrefix(t, "{"):
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(params, &obj); err != nil {
			return err
		}
		v, ok := obj[name]
		if !ok {
			return fmt.Errorf("params carry no %q", name)
		}
		return json.Unmarshal(v, into)
	}
	return fmt.Errorf("params are neither an array nor an object: %s", t)
}

// ---- WebSocket frames (RFC 6455 §5) ------------------------------------------

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xa
)

// writeFrame sends one masked frame, as a client must.
func (c *Client) writeFrame(op byte, payload []byte) error {
	var hdr [14]byte
	hdr[0] = 0x80 | op
	n := 2
	switch l := len(payload); {
	case l < 126:
		hdr[1] = 0x80 | byte(l)
	case l <= 0xffff:
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(l))
		n = 4
	default:
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(l))
		n = 10
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(hdr[n:], mask[:])
	n += 4
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := c.conn.Write(hdr[:n]); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

func (c *Client) readFrame() (op byte, fin bool, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.conn, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = h[0] & 0x0f
	masked := h[1]&0x80 != 0
	length := uint64(h[1] & 0x7f)
	switch length {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.conn, b[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.conn, b[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > 1<<24 {
		err = fmt.Errorf("management: frame of %d bytes", length)
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.conn, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.conn, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}
