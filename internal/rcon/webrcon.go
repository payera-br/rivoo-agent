package rcon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// webRcon is Rust's console: ws://host:port/<password>, JSON messages matched by Identifier.
type webRcon struct {
	mu   sync.Mutex
	c    *websocket.Conn
	next int
}

func dialWebRcon(ctx context.Context, host string, port int, password string) (Conn, error) {
	u := "ws://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/" + url.PathEscape(password)
	c, res, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		if res != nil && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) {
			return nil, ErrAuth
		}
		return nil, fmt.Errorf("%w: %v", ErrConn, sanitize(err, password))
	}
	c.SetReadLimit(4 << 20)
	return &webRcon{c: c, next: 1000}, nil
}

func (w *webRcon) Exec(ctx context.Context, cmd string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	id := w.next
	msg, _ := json.Marshal(map[string]any{"Identifier": id, "Message": cmd, "Name": "WebRcon"})
	if err := w.c.Write(ctx, websocket.MessageText, msg); err != nil {
		return "", fmt.Errorf("%w: %v", ErrConn, err)
	}
	for {
		_, b, err := w.c.Read(ctx)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrConn, err)
		}
		var m struct {
			Identifier int
			Message    string
		}
		if json.Unmarshal(b, &m) != nil || m.Identifier != id {
			continue // log lines and other answers
		}
		return m.Message, nil
	}
}

func (w *webRcon) Close() error { return w.c.Close(websocket.StatusNormalClosure, "") }

// sanitize keeps the password out of errors (the dial URL carries it).
func sanitize(err error, password string) string {
	s := err.Error()
	if password == "" {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, url.PathEscape(password), "***"), password, "***")
}
