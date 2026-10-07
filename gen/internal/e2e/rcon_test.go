package e2e

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRCON answers as vanilla's RconClient does: auth, a command's answer
// cut into frames of rconSplit characters under the request's id, and
// "Unknown request <type>" for a type it does not know.
type fakeRCON struct {
	ln     net.Listener
	answer func(cmd string) string // may take its time
	mu     sync.Mutex
	got    map[string]int // the commands run
}

func newFakeRCON(t *testing.T, answer func(cmd string) string) *fakeRCON {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRCON{ln: ln, answer: answer, got: map[string]int{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeRCON) count(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got[cmd]
}

func (f *fakeRCON) serve(conn net.Conn) {
	defer conn.Close()
	write := func(id, typ int32, payload string) error {
		b := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)+10))
		b = binary.LittleEndian.AppendUint32(b, uint32(id))
		b = binary.LittleEndian.AppendUint32(b, uint32(typ))
		b = append(append(b, payload...), 0, 0)
		_, err := conn.Write(b)
		return err
	}
	for {
		var head [12]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			return
		}
		length := binary.LittleEndian.Uint32(head[0:4])
		id := int32(binary.LittleEndian.Uint32(head[4:8]))
		typ := int32(binary.LittleEndian.Uint32(head[8:12]))
		body := make([]byte, length-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		payload := string(body[:len(body)-2])
		switch typ {
		case rconAuth:
			if write(id, rconCommand, "") != nil {
				return
			}
		case rconCommand:
			f.mu.Lock()
			f.got[payload]++
			f.mu.Unlock()
			ans := f.answer(payload)
			for { // vanilla's sendCmdResponse: at least one frame
				n := min(len(ans), rconSplit)
				if write(id, rconResponseVal, ans[:n]) != nil {
					return
				}
				if ans = ans[n:]; ans == "" {
					break
				}
			}
		default:
			if write(id, rconResponseVal, fmt.Sprintf("Unknown request %x", typ)) != nil {
				return
			}
		}
	}
}

// An answer longer than a frame is read whole, and the command after it
// gets its own answer.
func TestRCONSplitAnswer(t *testing.T) {
	answers := map[string]string{
		"long":  strings.Repeat("a", rconSplit) + strings.Repeat("b", rconSplit) + "tail",
		"exact": strings.Repeat("c", rconSplit),
		"short": "There are 0 of a max of 20 players online: ",
	}
	f := newFakeRCON(t, func(cmd string) string { return answers[cmd] })
	c, err := dialRCON(f.ln.Addr().String(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	for _, cmd := range []string{"long", "exact", "short", "long", "short"} {
		got, err := c.command(cmd)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if got != answers[cmd] {
			t.Fatalf("%s: answered %d bytes, want %d", cmd, len(got), len(answers[cmd]))
		}
	}
	if n := f.count("long"); n != 2 {
		t.Errorf("long ran %d times, want 2", n)
	}
}

// A command whose answer came late is an error, not sent again (the server
// ran it); the next command dials again and gets its own answer, not the
// late one.
func TestRCONNoResendAfterReadTimeout(t *testing.T) {
	f := newFakeRCON(t, func(cmd string) string {
		if cmd == "time add 1000" {
			time.Sleep(600 * time.Millisecond)
			return "late"
		}
		return "answer to " + cmd
	})
	c, err := dialRCON(f.ln.Addr().String(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	c.timeout = 200 * time.Millisecond
	if got, err := c.command("time add 1000"); err == nil {
		t.Fatalf("a late answer came as %q, want an error", got)
	}
	c.timeout = 0
	got, err := c.command("list")
	if err != nil {
		t.Fatal(err)
	}
	if got != "answer to list" {
		t.Fatalf("list answered %q", got)
	}
	time.Sleep(800 * time.Millisecond) // a resend would have run by now
	if n := f.count("time add 1000"); n != 1 {
		t.Fatalf("time add ran %d times, want 1", n)
	}
}

// A command that could not be written is sent once more on a fresh
// connection: it never reached the server.
func TestRCONResendUnsent(t *testing.T) {
	f := newFakeRCON(t, func(cmd string) string { return "answer to " + cmd })
	c, err := dialRCON(f.ln.Addr().String(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	c.conn.Close() // broken under the client: the write fails
	got, err := c.command("give Robot minecraft:stone 1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "answer to give Robot minecraft:stone 1" {
		t.Fatalf("answered %q", got)
	}
	if n := f.count("give Robot minecraft:stone 1"); n != 1 {
		t.Fatalf("give ran %d times, want 1", n)
	}
}
