package rcon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// idle: 7 Days to Die doesn't mark the end of an answer; stop reading after this much silence.
const idle = 300 * time.Millisecond

type telnet struct {
	mu sync.Mutex
	c  net.Conn
	r  *bufio.Reader
}

func dialTelnet(ctx context.Context, host string, port int, password string) (Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConn, err)
	}
	t := &telnet{c: c, r: bufio.NewReader(c)}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := t.until("password"); err != nil {
		c.Close()
		return nil, fmt.Errorf("%w: %v", ErrConn, err)
	}
	if _, err := c.Write([]byte(password + "\r\n")); err != nil {
		c.Close()
		return nil, fmt.Errorf("%w: %v", ErrConn, err)
	}
	line, err := t.until("logon successful", "incorrect")
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("%w: %v", ErrConn, err)
	}
	if strings.Contains(strings.ToLower(line), "incorrect") {
		c.Close()
		return nil, ErrAuth
	}
	t.drain()
	return t, nil
}

// until reads lines until one contains any of the words (case-insensitive).
func (t *telnet) until(words ...string) (string, error) {
	for {
		line, err := t.r.ReadString('\n')
		low := strings.ToLower(line)
		for _, w := range words {
			if strings.Contains(low, w) {
				return line, nil
			}
		}
		if err != nil {
			return "", err
		}
	}
}

// drain reads whatever arrives until a quiet gap.
func (t *telnet) drain() string {
	var b strings.Builder
	for {
		t.c.SetReadDeadline(time.Now().Add(idle))
		line, err := t.r.ReadString('\n')
		b.WriteString(line)
		if err != nil { // the quiet gap (timeout) ends the answer; a drop shows on the next write
			return b.String()
		}
	}
}

func (t *telnet) Exec(ctx context.Context, cmd string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := t.c.Write([]byte(cmd + "\r\n")); err != nil {
		return "", fmt.Errorf("%w: %v", ErrConn, err)
	}
	return t.drain(), nil
}

func (t *telnet) Close() error { return t.c.Close() }
