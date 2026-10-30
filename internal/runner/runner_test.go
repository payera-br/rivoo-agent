package runner

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
	"github.com/payera-br/rivoo-agent/internal/games"
	"github.com/payera-br/rivoo-agent/internal/rcon"
)

// fakeAPI serves one queue; runs leave the queue once acked.
type fakeAPI struct {
	mu         sync.Mutex
	queue      []map[string]any
	acks       []api.AckResult
	heartbeats []map[string]any
	failAcks   int
	polls      [][]api.Player
}

func (f *fakeAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/agent/commands":
			var body struct{ Players []api.Player }
			json.NewDecoder(r.Body).Decode(&body)
			f.polls = append(f.polls, body.Players)
			json.NewEncoder(w).Encode(map[string]any{"data": f.queue})
		case "/v1/agent/commands/ack":
			if f.failAcks > 0 {
				f.failAcks--
				w.WriteHeader(503)
				return
			}
			var body struct{ Results []api.AckResult }
			json.NewDecoder(r.Body).Decode(&body)
			f.acks = append(f.acks, body.Results...)
			done := map[string]bool{}
			for _, a := range body.Results {
				done[a.ID] = true
			}
			var left []map[string]any
			for _, q := range f.queue {
				if !done[q["id"].(string)] {
					left = append(left, q)
				}
			}
			f.queue = left
			w.Write([]byte(`{}`))
		case "/v1/agent/heartbeat":
			var body struct{ Details map[string]any }
			json.NewDecoder(r.Body).Decode(&body)
			f.heartbeats = append(f.heartbeats, body.Details)
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAPI) snapshot() ([]api.AckResult, []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.AckResult(nil), f.acks...), append([]map[string]any(nil), f.heartbeats...)
}

// fakeConsole is a Rust console: one player online; "boom" drops the connection.
type fakeConsole struct {
	mu   *sync.Mutex
	ran  *[]string
	dead bool
}

func (c *fakeConsole) Exec(_ context.Context, cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return "", rcon.ErrConn
	}
	if cmd == "playerlist" {
		return `[{"SteamID":"76561198000000001","DisplayName":"Ana"}]`, nil
	}
	if cmd == "boom" {
		c.dead = true
		return "", rcon.ErrConn
	}
	*c.ran = append(*c.ran, cmd)
	if strings.HasPrefix(cmd, "bad") {
		return "Unknown command: " + cmd, nil
	}
	return "ok: " + cmd, nil
}
func (c *fakeConsole) Close() error { return nil }

func runFor(t *testing.T, f *fakeAPI, dial func(context.Context) (rcon.Conn, error), d time.Duration) {
	srv := f.server(t)
	g, _ := games.Get("RUST")
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "test", Wait: 0, Pause: 20 * time.Millisecond, HeartbeatEvery: 30 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
}

func console() (func(context.Context) (rcon.Conn, error), *[]string) {
	var mu sync.Mutex
	var ran []string
	return func(context.Context) (rcon.Conn, error) { return &fakeConsole{mu: &mu, ran: &ran}, nil }, &ran
}

func TestDeliversAndReportsTheAnswer(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{
		{"id": "run_ok", "command": "inventory.giveto 76561198000000001 rifle.ak 1", "requireOnline": true, "recipient": map[string]any{"uuid": "76561198000000001", "name": nil}},
		{"id": "run_bad", "command": "bad.cmd", "requireOnline": false},
	}}
	dial, ran := console()
	runFor(t, f, dial, 300*time.Millisecond)
	acks, _ := f.snapshot()
	byID := map[string]api.AckResult{}
	for _, a := range acks {
		byID[a.ID] = a
	}
	if byID["run_ok"].Status != "ok" || byID["run_ok"].Output != "ok: inventory.giveto 76561198000000001 rifle.ak 1" {
		t.Fatalf("ok run: %+v", byID["run_ok"])
	}
	if byID["run_bad"].Status != "failed" || !strings.Contains(byID["run_bad"].Error, "Unknown command") {
		t.Fatalf("bad run: %+v", byID["run_bad"])
	}
	if len(*ran) != 2 {
		t.Fatalf("ran %v", *ran)
	}
	if len(f.polls) == 0 || len(f.polls[0]) != 1 || f.polls[0][0].ID != "76561198000000001" {
		t.Fatalf("players sent: %+v", f.polls)
	}
}

func TestConsoleDownLeavesTheQueueAndSaysWhy(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_1", "command": "say oi"}}}
	runFor(t, f, func(context.Context) (rcon.Conn, error) { return nil, rcon.ErrAuth }, 200*time.Millisecond)
	acks, hbs := f.snapshot()
	if len(acks) != 0 {
		t.Fatalf("acked while console down: %+v", acks)
	}
	if len(hbs) == 0 || !strings.Contains(hbs[len(hbs)-1]["lastRconError"].(string), "senha") {
		t.Fatalf("heartbeats %+v", hbs)
	}
	for _, h := range hbs {
		b, _ := json.Marshal(h)
		if strings.Contains(string(b), "pw") {
			t.Fatalf("secret in heartbeat: %s", b)
		}
	}
}

func TestDropMidQueueRunsEachCommandOnce(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{
		{"id": "run_a", "command": "say a"},
		{"id": "run_boom", "command": "boom"},
		{"id": "run_c", "command": "say c"},
	}}
	var mu sync.Mutex
	var ran []string
	dials := 0
	dial := func(context.Context) (rcon.Conn, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return &fakeConsole{mu: &mu, ran: &ran}, nil
	}
	runFor(t, f, dial, 400*time.Millisecond)
	acks, _ := f.snapshot()
	count := map[string]int{}
	for _, a := range acks {
		count[a.ID]++
	}
	if count["run_a"] != 1 {
		t.Fatalf("run_a acked %d times; acks %+v", count["run_a"], acks)
	}
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, c := range ran {
		if c == "say a" {
			n++
		}
	}
	if n != 1 || dials < 2 {
		t.Fatalf("say a ran %d times, dials %d", n, dials)
	}
}

func TestAckRetriedWithoutRunningAgain(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_1", "command": "say oi"}}, failAcks: 2}
	dial, ran := console()
	runFor(t, f, dial, 400*time.Millisecond)
	acks, _ := f.snapshot()
	if len(acks) != 1 || acks[0].ID != "run_1" {
		t.Fatalf("acks %+v", acks)
	}
	if len(*ran) != 1 {
		t.Fatalf("ran %v", *ran)
	}
}

func TestHeartbeatGoesOutWhenWhoIsOnlineChanges(t *testing.T) {
	f := &fakeAPI{}
	srv := f.server(t)
	g, _ := games.Get("RUST")
	dial, _ := console()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "t", Pause: 20 * time.Millisecond, HeartbeatEvery: time.Hour, MaxBackoff: 50 * time.Millisecond})
	_, hbs := f.snapshot()
	if len(hbs) < 2 || hbs[len(hbs)-1]["playersOnline"] != float64(1) {
		t.Fatalf("heartbeats %+v", hbs)
	}
}

// scriptedConsole answers playerlist from a function and records commands; "freeze" goes out but
// gets no answer.
type scriptedConsole struct {
	mu      *sync.Mutex
	ran     *[]string
	players func() string
}

func (c *scriptedConsole) Exec(_ context.Context, cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd == "playerlist" {
		return c.players(), nil
	}
	*c.ran = append(*c.ran, cmd)
	if cmd == "freeze" {
		return "", rcon.ErrNoAnswer
	}
	return "ok", nil
}
func (c *scriptedConsole) Close() error { return nil }

func TestNoAnswerIsReportedAndNeverRunAgain(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_f", "command": "freeze"}}}
	var mu sync.Mutex
	var ran []string
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string { return "[]" }}, nil
	}
	runFor(t, f, dial, 300*time.Millisecond)
	acks, _ := f.snapshot()
	if len(acks) != 1 || acks[0].Status != "failed" || !strings.Contains(acks[0].Error, "pode ter rodado") {
		t.Fatalf("acks %+v", acks)
	}
	if len(ran) != 1 {
		t.Fatalf("freeze ran %d times", len(ran))
	}
}

func TestShutdownStillReportsWhatRan(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_1", "command": "say oi"}}, failAcks: 1}
	srv := f.server(t)
	g, _ := games.Get("RUST")
	dial, ran := console()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			f.mu.Lock()
			n := len(*ran)
			f.mu.Unlock()
			if n > 0 {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "t", Pause: 20 * time.Millisecond, HeartbeatEvery: time.Hour, MaxBackoff: 50 * time.Millisecond})
	acks, _ := f.snapshot()
	if len(acks) != 1 || acks[0].ID != "run_1" {
		t.Fatalf("what ran wasn't reported before exit: %+v", acks)
	}
}

func TestBadPlayerRowsAreDropped(t *testing.T) {
	f := &fakeAPI{}
	var mu sync.Mutex
	var ran []string
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string {
			return `[{"SteamID":"76561198000000001","DisplayName":""},{"SteamID":"bad id","DisplayName":"Bia"},{"SteamID":"76561198000000003","DisplayName":"Caio"}]`
		}}, nil
	}
	runFor(t, f, dial, 150*time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	got := f.polls[0]
	if len(got) != 2 || got[0] != (api.Player{Name: "Bia"}) || got[1] != (api.Player{ID: "76561198000000003", Name: "Caio"}) {
		t.Fatalf("players sent %+v", got)
	}
}

func TestPlayerWhoLeftDuringTheWaitKeepsTheRunQueued(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_ana", "command": "give ana", "requireOnline": true, "recipient": map[string]any{"uuid": "76561198000000001", "name": nil}}}}
	var mu sync.Mutex
	var ran []string
	lists := 0
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string {
			lists++
			if lists == 1 {
				return `[{"SteamID":"76561198000000001","DisplayName":"Ana"}]`
			}
			return "[]" // left while the poll waited
		}}, nil
	}
	srv := f.server(t)
	g, _ := games.Get("RUST")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "t", Pause: 20 * time.Millisecond, HeartbeatEvery: time.Hour, MaxBackoff: 50 * time.Millisecond, StaleAfter: time.Nanosecond})
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 0 {
		t.Fatalf("ran for a player who left: %v", ran)
	}
}
