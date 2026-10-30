// Package pair links a new agent to a Rivoo project: it shows a code the merchant types in the panel
// and gets the connection key once the code is claimed. Same scheme as the Minecraft plugin.
package pair

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"time"

	"github.com/payera-br/rivoo-agent/internal/api"
)

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func secret() (string, error) {
	b := make([]byte, 32)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Pair shows a code (announce) and waits until it's claimed; returns the connection key. An expired
// code gets a new one. poll overrides the server's poll interval (tests).
func Pair(ctx context.Context, c *api.Client, game, version string, announce func(code string), poll time.Duration) (string, error) {
	for {
		s, err := secret()
		if err != nil {
			return "", err
		}
		key := "rck_" + s
		res, err := c.CreatePairing(ctx, api.PairingReq{
			Type: "GAME_AGENT", AgentVersion: version, LookupHash: sum("pair:" + s), KeyHash: sum(key),
			Server: map[string]any{"game": game},
		})
		if err != nil {
			if wait(ctx, 10*time.Second) != nil {
				return "", ctx.Err()
			}
			continue
		}
		announce(res.Code)
		every := poll
		if every == 0 {
			every = time.Duration(max(res.PollSeconds, 2)) * time.Second
		}
		for {
			if err := wait(ctx, every); err != nil {
				return "", err
			}
			st, err := c.PairingStatus(ctx, s)
			if errors.Is(err, api.ErrNotFound) {
				break // expired: new code
			}
			if err == nil && st.Status == "claimed" {
				return key, nil
			}
		}
	}
}

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
