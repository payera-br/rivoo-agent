package pair

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/payera-br/rivoo-agent/internal/api"
)

func TestPairingReturnsTheKeyAfterTheCodeIsClaimed(t *testing.T) {
	var mu sync.Mutex
	var got api.PairingReq
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/agent/pairings":
			json.NewDecoder(r.Body).Decode(&got)
			json.NewEncoder(w).Encode(map[string]any{"code": "ABCD-1234", "expiresAt": time.Now().Add(10 * time.Minute), "pollSeconds": 0})
		case "/v1/agent/pairings/current":
			polls++
			secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Pairing ")
			if sum("pair:"+secret) != got.LookupHash {
				t.Errorf("secret does not match lookupHash")
			}
			if polls < 2 {
				json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "claimed", "connection": map[string]any{"id": "conn_1", "name": "Rust"}, "project": map[string]any{"name": "Loja"}})
		}
	}))
	defer srv.Close()
	var shown string
	key, err := Pair(context.Background(), &api.Client{BaseURL: srv.URL}, "RUST", "0.1.0", func(code string) { shown = code }, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if shown != "ABCD-1234" || got.Type != "GAME_AGENT" || got.Server["game"] != "RUST" {
		t.Fatalf("shown %q, req %+v", shown, got)
	}
	if !strings.HasPrefix(key, "rck_") || sum(key) != got.KeyHash {
		t.Fatalf("key %q does not match keyHash", key)
	}
}
