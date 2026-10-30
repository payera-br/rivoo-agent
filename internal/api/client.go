// Package api talks to the Rivoo agent protocol (/v1/agent), the same one the Minecraft plugin uses.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	// ErrUnauthorized means the key was revoked or the connection removed: stop and pair again.
	ErrUnauthorized = errors.New("chave recusada pela Rivoo")
	// ErrTemporary is a network or 5xx failure: try again later.
	ErrTemporary = errors.New("Rivoo indisponível no momento")
	// ErrNotFound is a 404 (pairing expired, connection gone).
	ErrNotFound = errors.New("não encontrado")
)

// Client calls the agent routes. Prefix defaults to /v1/agent; the cloud worker uses
// /v1/agent-cloud/connections/<id> with the platform key.
type Client struct {
	BaseURL string
	Key     string
	Prefix  string
	HTTP    *http.Client
}

// Player is someone on the server right now: the game's id (SteamID64, Epic id…) and name.
type Player struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

// Run is a queued command. Recipient.UUID carries the stable id (UUID or SteamID64).
type Run struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	Recipient struct {
		UUID *string `json:"uuid"`
		Name *string `json:"name"`
	} `json:"recipient"`
	RequireOnline bool `json:"requireOnline"`
}

// AckResult reports one run: "ok" or "failed", with the server's answer.
type AckResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
}

// ConnInfo is what GET /connection answers.
type ConnInfo struct {
	Connection struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
		Game string `json:"game"`
	} `json:"connection"`
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
}

// PairingReq starts a pairing; the code comes back for the merchant to type in the panel.
type PairingReq struct {
	Type         string         `json:"type"`
	AgentVersion string         `json:"agentVersion"`
	LookupHash   string         `json:"lookupHash"`
	KeyHash      string         `json:"keyHash"`
	Server       map[string]any `json:"server,omitempty"`
}

type PairingResp struct {
	Code        string    `json:"code"`
	ExpiresAt   time.Time `json:"expiresAt"`
	PollSeconds int       `json:"pollSeconds"`
}

type PairingStatus struct {
	Status     string `json:"status"`
	Connection struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"connection"`
	Project struct {
		Name string `json:"name"`
	} `json:"project"`
}

func (c *Client) prefix() string {
	if c.Prefix != "" {
		return c.Prefix
	}
	return "/v1/agent"
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// Long-poll waits up to 30 s on the server.
	return &http.Client{Timeout: 45 * time.Second}
}

func (c *Client) do(ctx context.Context, method, path, auth string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("User-Agent", "rivoo-agent")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTemporary, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d", ErrTemporary, res.StatusCode)
	case res.StatusCode >= 400:
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 500))
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, msg)
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *Client) bearer() string { return "Bearer " + c.Key }

// CreatePairing asks for a pairing code (no key yet).
func (c *Client) CreatePairing(ctx context.Context, req PairingReq) (PairingResp, error) {
	var out PairingResp
	err := c.do(ctx, http.MethodPost, "/v1/agent/pairings", "", req, &out)
	return out, err
}

// PairingStatus tells whether the code was claimed in the panel.
func (c *Client) PairingStatus(ctx context.Context, secret string) (PairingStatus, error) {
	var out PairingStatus
	err := c.do(ctx, http.MethodGet, "/v1/agent/pairings/current", "Pairing "+secret, nil, &out)
	return out, err
}

func (c *Client) Connection(ctx context.Context) (ConnInfo, error) {
	var out ConnInfo
	err := c.do(ctx, http.MethodGet, c.prefix()+"/connection", c.bearer(), nil, &out)
	return out, err
}

// Heartbeat reports the agent version and its state (game, players online, last RCON error).
func (c *Client) Heartbeat(ctx context.Context, version string, details map[string]any) error {
	return c.do(ctx, http.MethodPost, c.prefix()+"/heartbeat", c.bearer(), map[string]any{"agentVersion": version, "details": details}, nil)
}

// Poll long-polls the queue. With players, only runs that can run now come back.
func (c *Client) Poll(ctx context.Context, players []Player, wait, limit int) ([]Run, error) {
	if players == nil {
		players = []Player{}
	}
	var out struct {
		Data []Run `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, c.prefix()+"/commands", c.bearer(), map[string]any{"wait": wait, "limit": limit, "players": players}, &out)
	return out.Data, err
}

func (c *Client) Ack(ctx context.Context, results []AckResult) error {
	return c.do(ctx, http.MethodPost, c.prefix()+"/commands/ack", c.bearer(), map[string]any{"results": results}, nil)
}
