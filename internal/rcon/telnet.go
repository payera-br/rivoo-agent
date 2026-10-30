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

const (
	// 7 Days to Die doesn't mark the end of an answer: stop after this much silence…
	idle = 300 * time.Millisecond
	// …or after this long in total (the console also streams the server log).
	answerMax = 3 * time.Second
	maxLine   = 4096
)

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
	c.SetDeadline(deadline(ctx))
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
	t.drain(answerMax)
	return t, nil
}

// until reads lines (each cut to maxLine) until one contains any of the words.
func (t *telnet) until(words ...string) (string, error) {
	for {
		line, err := t.r.ReadString('\n')
		if len(line) > maxLine {
			line = line[:maxLine]
		}
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

// drain reads until a quiet gap, at most `max` in total and maxAnswer bytes. A dropped
// connection is an error; the quiet gap (timeout) is the normal end.
func (t *telnet) drain(max time.Duration) (string, error) {
	var b strings.Builder
	end := time.Now().Add(max)
	for time.Now().Before(end) {
		d := time.Now().Add(idle)
		if d.After(end) {
			d = end
		}
		t.c.SetReadDeadline(d)
		line, err := t.r.ReadString('\n')
		if b.Len() < maxAnswer {
			b.WriteString(line)
		}
		if err != nil {
			if timeout(err) {
				return b.String(), nil
			}
			return b.String(), err
		}
	}
	return b.String(), nil
}

func (t *telnet) Exec(ctx context.Context, cmd string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Log lines that queued up before the command are not its answer.
	if _, err := t.drain(50 * time.Millisecond); err != nil {
		return "", fmt.Errorf("%w: %v", ErrConn, err)
	}
	t.c.SetWriteDeadline(deadline(ctx))
	if _, err := t.c.Write([]byte(cmd + "\r\n")); err != nil {
		return "", fmt.Errorf("%w: %v", ErrConn, err)
	}
	out, err := t.drain(answerMax)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoAnswer, err)
	}
	return out, nil
}

func (t *telnet) Close() error { return t.c.Close() }
