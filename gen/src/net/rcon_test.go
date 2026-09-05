package net

import (
	"fmt"
	"testing"
)

// TestRCON runs a server and a client on a random local port: login, one
// command, one response.
func TestRCON(t *testing.T) {
	l, err := ListenRCON("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	serverErr := make(chan error, 1)
	go func() { serverErr <- serveOne(l) }()

	conn, err := DialRCON(l.Addr().String(), "RightPassword")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Cmd("TEST COMMAND"); err != nil {
		t.Fatal(err)
	}
	resp, err := conn.Resp()
	if err != nil {
		t.Fatal(err)
	}
	if want := handleCommand("TEST COMMAND"); resp != want {
		t.Fatalf("response %q, want %q", resp, want)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

// serveOne accepts one connection, checks the password, answers one command.
func serveOne(l *RCONListener) error {
	conn, err := l.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.AcceptLogin("RightPassword"); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	cmd, err := conn.AcceptCmd()
	if err != nil {
		return fmt.Errorf("command: %w", err)
	}
	return conn.RespCmd(handleCommand(cmd))
}

func handleCommand(cmd string) (resp string) {
	return fmt.Sprintf("your command is %q", cmd)
}

func ExampleListenRCON() {
	l, err := ListenRCON("localhost:25575")
	if err != nil {
		panic(err)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Accept connection error: %v", err)
		}

		go func(conn RCONServerConn) {
			err = conn.AcceptLogin("CORRECT_PASSWORD")
			if err != nil {
				fmt.Printf("Login fail: %v", err)
			}
			defer conn.Close()

			// The client is login, we are accepting its command
			for {
				cmd, err := conn.AcceptCmd()
				if err != nil {
					fmt.Printf("Read command fail: %v", err)
					break
				}

				resp := handleCommand(cmd)

				// Return the result of command.
				// It's allowed to call RespCmd multiple times for one command.
				err = conn.RespCmd(resp)
				if err != nil {
					fmt.Printf("Response command fail: %v", err)
					break
				}
			}
		}(conn)
	}
}

func ExampleDialRCON() {
	conn, err := DialRCON("localhost:25575", "CORRECT_PASSWORD")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	err = conn.Cmd("TEST COMMAND")
	if err != nil {
		panic(err)
	}

	for {
		// Server may send the result in more(or less) than one packet.
		// See: https://wiki.vg/RCON#Fragmentation
		resp, err := conn.Resp()
		if err != nil {
			fmt.Print(err)
		}
		fmt.Printf("Server response: %q", resp)
		break
	}
}
