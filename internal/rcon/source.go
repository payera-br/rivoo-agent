package rcon

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	typeAuth     = 3
	typeExec     = 2
	typeResponse = 0
	// A body this long may continue in the next packet.
	splitAt = 4000
	// After a long body, wait this long for the rest.
	moreWait = 300 * time.Millisecond
	// A command's answer must start within this (unless ctx is shorter).
	answerWait = 10 * time.Second
)

type source struct {
	mu   sync.Mutex
	c    net.Conn
	r    *bufio.Reader
	next int32
}

func dialSource(ctx context.Context, host string, port int, password string) (Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConn, err)
	}
	s := &source{c: c, r: bufio.NewReader(c), next: 1}
	if err := s.auth(ctx, password); err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

func deadline(ctx context.Context) time.Time {
	d := time.Now().Add(answerWait)
	if cd, ok := ctx.Deadline(); ok && cd.Before(d) {
		return cd
	}
	return d
}

func (s *source) write(id, typ int32, body string) error {
	b := make([]byte, 14+len(body))
	binary.LittleEndian.PutUint32(b[0:], uint32(10+len(body)))
	binary.LittleEndian.PutUint32(b[4:], uint32(id))
	binary.LittleEndian.PutUint32(b[8:], uint32(typ))
	copy(b[12:], body)
	_, err := s.c.Write(b)
	return err
}

// read takes one whole packet from the buffered stream.
func (s *source) read() (id, typ int32, body string, err error) {
	var size int32
	if err = binary.Read(s.r, binary.LittleEndian, &size); err != nil {
		return
	}
	if size < 10 || size > 1<<20 {
		return 0, 0, "", fmt.Errorf("pacote RCON inválido (%d bytes)", size)
	}
	buf := make([]byte, size)
	if _, err = io.ReadFull(s.r, buf); err != nil {
		return
	}
	id = int32(binary.LittleEndian.Uint32(buf[0:]))
	typ = int32(binary.LittleEndian.Uint32(buf[4:]))
	end := len(buf) - 2
	if end < 8 {
		end = 8
	}
	body = string(buf[8:end])
	return
}

func (s *source) auth(ctx context.Context, password string) error {
	s.c.SetDeadline(deadline(ctx))
	if err := s.write(0, typeAuth, password); err != nil {
		return fmt.Errorf("%w: %v", ErrConn, err)
	}
	for {
		id, typ, _, err := s.read()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrConn, err)
		}
		// Some servers send an empty RESPONSE_VALUE before the auth answer.
		if typ != typeExec {
			continue
		}
		if id == -1 {
			return ErrAuth
		}
		return nil
	}
}

func timeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded)
}

func (s *source) Exec(ctx context.Context, cmd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := s.next
	s.c.SetDeadline(deadline(ctx))
	if err := s.write(id, typeExec, cmd); err != nil {
		return "", fmt.Errorf("%w: %v", ErrConn, err)
	}
	var out []byte
	for {
		if len(out) > 0 {
			// More of a split answer? Only peek under the short wait, so a late packet is never cut.
			s.c.SetReadDeadline(time.Now().Add(moreWait))
			if _, err := s.r.Peek(4); err != nil {
				s.c.SetDeadline(deadline(ctx))
				if timeout(err) {
					return string(out), nil
				}
				return "", fmt.Errorf("%w: %v", ErrNoAnswer, err)
			}
			s.c.SetReadDeadline(deadline(ctx))
		}
		rid, typ, body, err := s.read()
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoAnswer, err)
		}
		if rid != id || typ != typeResponse {
			continue
		}
		if len(out)+len(body) > maxAnswer {
			body = body[:maxAnswer-len(out)]
		}
		out = append(out, body...)
		if len(body) < splitAt || len(out) >= maxAnswer {
			return string(out), nil
		}
	}
}

func (s *source) Close() error { return s.c.Close() }
