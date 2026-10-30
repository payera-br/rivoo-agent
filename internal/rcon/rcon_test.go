package rcon

import (
	"context"
	"errors"
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
