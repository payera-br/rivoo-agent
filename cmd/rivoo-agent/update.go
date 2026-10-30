package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"log"
	"os"
	"time"

	"github.com/payera-br/rivoo-agent/internal/update"
)

// releasePublicKey verifies release manifests (the private half is the CI secret AGENT_SIGNING_KEY).
const releasePublicKey = "zjHWFkyjJMmyhRSihcsxJb5NT3UewxNiuvn7xL//Znc="

// manifestURL is where releases are announced (the release workflow uploads it with the
// binaries); RIVOO_UPDATE_URL overrides it.
const manifestURL = "https://github.com/payera-br/rivoo-agent/releases/latest/download/latest.json"

func init() {
	autoUpdate = func(ctx context.Context, onUpdated func()) {
		pub, _ := base64.StdEncoding.DecodeString(releasePublicKey)
		url := manifestURL
		if v := os.Getenv("RIVOO_UPDATE_URL"); v != "" {
			url = v
		}
		for {
			updated, err := update.Apply(ctx, update.Options{ManifestURL: url, PublicKey: ed25519.PublicKey(pub), Current: version})
			if err != nil {
				log.Printf("atualização: %v", err)
			} else if updated {
				onUpdated()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(6 * time.Hour):
			}
		}
	}
}
