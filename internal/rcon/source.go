package rcon

import (
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
)

type source struct {
	mu   sync.Mutex
	c    net.Conn
	next int32
}

func dialSource(ctx context.Context, host string, port int, password string) (Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConn, err)
	}
	s := &source{c: c, next: 1}
	if err := s.auth(ctx, password); err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

func (s *source) deadline(ctx context.Context) {
	if d, ok := ctx.Deadline(); ok {
		s.c.SetDeadline(d)
	} else {
		s.c.SetDeadline(time.Now().Add(10 * time.Second))
	}
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

func (s *source) read() (id, typ int32, body string, err error) {
	var size int32
	if err = binary.Read(s.c, binary.LittleEndian, &size); err != nil {
		return
	}
	if size < 10 || size > 1<<20 {
		return 0, 0, "", fmt.Errorf("pacote RCON inválido (%d bytes)", size)
	}
	buf := make([]byte, size)
	if _, err = io.ReadFull(s.c, buf); err != nil {
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
	s.deadline(ctx)
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

func (s *source) Exec(ctx context.Context, cmd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := s.next
	s.deadline(ctx)
	if err := s.write(id, typeExec, cmd); err != nil {
		return "", fmt.Errorf("%w: %v", ErrConn, err)
	}
	var out []byte
	for {
		rid, typ, body, err := s.read()
		if err != nil {
			var ne net.Error
			// The rest of a split answer never came: what we have is the answer.
			if len(out) > 0 && (errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded)) {
				s.deadline(ctx)
				return string(out), nil
			}
			return "", fmt.Errorf("%w: %v", ErrConn, err)
		}
		if rid != id || typ != typeResponse {
			continue
		}
		out = append(out, body...)
		if len(body) < splitAt {
			s.deadline(ctx)
			return string(out), nil
		}
		s.c.SetReadDeadline(time.Now().Add(moreWait))
	}
}

func (s *source) Close() error { return s.c.Close() }
