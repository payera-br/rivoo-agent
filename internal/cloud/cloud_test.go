package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPublicAddress(t *testing.T) {
	for ip, ok := range map[string]bool{"8.8.8.8": true, "203.0.113.10": true, "127.0.0.1": false, "10.1.2.3": false, "172.20.0.1": false, "192.168.0.2": false, "169.254.169.254": false, "100.100.0.1": false, "::1": false, "fd12::1": false, "2001:4860:4860::8888": true} {
		if got := publicIP(ip); got != ok {
			t.Fatalf("%s: %v", ip, got)
		}
	}
}

func TestWorkerStartsStopsAndRestartsAgents(t *testing.T) {
	var mu sync.Mutex
	list := []Agent{{ConnectionID: "conn_a", Game: "RUST", Protocol: "webrcon", Host: "a.example.com", Port: 28016, Password: "p"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer plat" || r.URL.Path != "/v1/agent-cloud/connections" {
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": list})
	}))
	defer srv.Close()

	started := map[string]int{}
	running := map[string]bool{}
	start := func(ctx context.Context, a Agent) {
		mu.Lock()
		started[a.ConnectionID]++
		running[a.ConnectionID] = true
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		running[a.ConnectionID] = false
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Work(ctx, Options{BaseURL: srv.URL, Key: "plat", Every: 20 * time.Millisecond, Start: start})

	wait := func(cond func() bool) {
		for i := 0; i < 100; i++ {
			mu.Lock()
			ok := cond()
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timeout: started %v running %v", started, running)
	}
	wait(func() bool { return running["conn_a"] })

	mu.Lock()
	list = []Agent{{ConnectionID: "conn_a", Game: "RUST", Protocol: "webrcon", Host: "a.example.com", Port: 28016, Password: "nova"}, {ConnectionID: "conn_b", Game: "PALWORLD", Protocol: "source", Host: "b.example.com", Port: 25575, Password: "p"}}
	mu.Unlock()
	wait(func() bool { return running["conn_b"] && started["conn_a"] == 2 && running["conn_a"] })

	mu.Lock()
	list = []Agent{{ConnectionID: "conn_b", Game: "PALWORLD", Protocol: "source", Host: "b.example.com", Port: 25575, Password: "p"}}
	mu.Unlock()
	wait(func() bool { return !running["conn_a"] && running["conn_b"] })
}

func TestRestartWaitsForTheOldLoop(t *testing.T) {
	var mu sync.Mutex
	list := []Agent{{ConnectionID: "c", Game: "RUST", Protocol: "webrcon", Host: "a.example.com", Port: 1, Password: "p1"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": list})
	}))
	defer srv.Close()
	running, overlap, started := 0, false, 0
	start := func(ctx context.Context, a Agent) {
		mu.Lock()
		running++
		started++
		if running > 1 {
			overlap = true
		}
		mu.Unlock()
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // finishing its batch and acks
		mu.Lock()
		running--
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	go Work(ctx, Options{BaseURL: srv.URL, Key: "k", Every: 10 * time.Millisecond, Start: start})
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	list = []Agent{{ConnectionID: "c", Game: "RUST", Protocol: "webrcon", Host: "a.example.com", Port: 1, Password: "p2"}}
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	cancel()
	mu.Lock()
	defer mu.Unlock()
	if overlap || started != 2 {
		t.Fatalf("overlap=%v started=%d", overlap, started)
	}
}
