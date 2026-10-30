// Package rcon speaks the three remote consoles game servers use: Source RCON (Minecraft, ARK,
// Conan, Palworld, Hytale via HyRCON), WebRCON (Rust) and Telnet (7 Days to Die).
package rcon

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrAuth: the server refused the password.
	ErrAuth = errors.New("senha do RCON recusada")
	// ErrConn: couldn't reach the console, or the connection dropped.
	ErrConn = errors.New("não foi possível falar com o RCON")
)

// Conn runs one console command and returns what the server answered.
type Conn interface {
	Exec(ctx context.Context, cmd string) (string, error)
	Close() error
}

// Dial opens and authenticates a console: protocol is "source", "webrcon" or "telnet".
func Dial(ctx context.Context, protocol, host string, port int, password string) (Conn, error) {
	switch protocol {
	case "source":
		return dialSource(ctx, host, port, password)
	case "webrcon":
		return dialWebRcon(ctx, host, port, password)
	case "telnet":
		return dialTelnet(ctx, host, port, password)
	}
	return nil, fmt.Errorf("protocolo desconhecido: %s", protocol)
}
