package rcon

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestSource(t *testing.T) {
	host, port := fakeSource(t)
	if _, err := Dial(ctx(t), "source", host, port, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong password: %v", err)
	}
	c, err := Dial(ctx(t), "source", host, port, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.Exec(ctx(t), "say oi")
	if err != nil || out != "ran: say oi" {
		t.Fatalf("%q %v", out, err)
	}
	big, err := c.Exec(ctx(t), "big")
	if err != nil || big != strings.Repeat("x", 10000) {
		t.Fatalf("big: len %d %v", len(big), err)
	}
	out, err = c.Exec(ctx(t), "after")
	if err != nil || out != "ran: after" {
		t.Fatalf("after big: %q %v", out, err)
	}
}

func TestWebRcon(t *testing.T) {
	host, port := fakeWebRcon(t)
	if _, err := Dial(ctx(t), "webrcon", host, port, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong password: %v", err)
	}
	c, err := Dial(ctx(t), "webrcon", host, port, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, cmd := range []string{"playerlist", "say oi"} {
		out, err := c.Exec(ctx(t), cmd)
		if err != nil || out != "ran: "+cmd {
			t.Fatalf("%s: %q %v", cmd, out, err)
		}
	}
}

func TestTelnet(t *testing.T) {
	host, port := fakeTelnet(t)
	if _, err := Dial(ctx(t), "telnet", host, port, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong password: %v", err)
	}
	c, err := Dial(ctx(t), "telnet", host, port, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.Exec(ctx(t), "lp")
	if err != nil || strings.TrimSpace(out) != "ran: lp" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestConnRefused(t *testing.T) {
	if _, err := Dial(ctx(t), "source", "127.0.0.1", 1, "pw"); !errors.Is(err, ErrConn) {
		t.Fatalf("%v", err)
	}
}

// silent accepts the auth and then never answers commands (a frozen server).
func silentSource(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				var size int32
				binary.Read(c, binary.LittleEndian, &size)
				buf := make([]byte, size)
				io.ReadFull(c, buf)
				b := make([]byte, 14)
				binary.LittleEndian.PutUint32(b[0:], 10)
				binary.LittleEndian.PutUint32(b[4:], binary.LittleEndian.Uint32(buf[0:]))
				binary.LittleEndian.PutUint32(b[8:], 2)
				c.Write(b)
				io.Copy(io.Discard, c) // reads commands, never answers
			}(c)
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func TestSentWithoutAnswerIsNotAConnError(t *testing.T) {
	host, port := silentSource(t)
	c, err := Dial(ctx(t), "source", host, port, "pw")
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = c.Exec(short, "give x")
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("got %v, want ErrNoAnswer (the command may have run)", err)
	}
}

func TestTelnetDeadConnectionIsAnError(t *testing.T) {
	host, port := fakeTelnet(t)
	c, err := Dial(ctx(t), "telnet", host, port, "pw")
	if err != nil {
		t.Fatal(err)
	}
	c.(*telnet).c.(*net.TCPConn).CloseRead()
	if out, err := c.Exec(ctx(t), "lp"); err == nil {
		t.Fatalf("dead console answered %q with no error", out)
	}
}

func TestTelnetDrainIsBounded(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		c.Write([]byte("Please enter password:\r\n"))
		bufio.NewReader(c).ReadString('\n')
		c.Write([]byte("Logon successful.\r\n"))
		for { // a busy server: log lines every 50 ms, forever
			if _, err := c.Write([]byte("INF log line\r\n")); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	c, err := Dial(ctx(t), "telnet", a.IP.String(), a.Port, "pw")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c.Exec(ctx(t), "lp")
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("Exec took %v on a chatty server", d)
	}
}
