package runner

import (
	"context"
	"encoding/json"
	"fmt"
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
	if cmd == "serverinfo" {
		return `{"Players":1,"MaxPlayers":100,"Queued":2,"Joining":1}`, nil
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
	runWithStore(t, f, dial, d, nil)
}

func runWithStore(t *testing.T, f *fakeAPI, dial func(context.Context) (rcon.Conn, error), d time.Duration, store AckStore) {
	srv := f.server(t)
	g, _ := games.Get("RUST")
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "test", Wait: 0, Pause: 20 * time.Millisecond, HeartbeatEvery: 30 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, AckStore: store})
}

// flakyStore is a store whose Save can be switched to fail.
type flakyStore struct {
	mu    sync.Mutex
	inner memAckStore
	fail  bool
	saves int
}

func (s *flakyStore) Load() ([]api.AckResult, error) { return s.inner.Load() }
func (s *flakyStore) Save(r []api.AckResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return fmt.Errorf("disk full")
	}
	s.saves++
	return s.inner.Save(r)
}
func (s *flakyStore) setFail(v bool) { s.mu.Lock(); s.fail = v; s.mu.Unlock() }

func TestRestartDoesNotRerunUnacked(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_1", "command": "say oi"}}, failAcks: 1000}
	dial, ran := console()
	store := &memAckStore{}
	runWithStore(t, f, dial, 300*time.Millisecond, store) // runs, ack never gets through, "crash"
	if len(*ran) != 1 {
		t.Fatalf("first life ran %v", *ran)
	}
	if saved, _ := store.Load(); len(saved) != 1 || saved[0].ID != "run_1" {
		t.Fatalf("store holds %+v", saved)
	}
	f.mu.Lock()
	f.failAcks = 0
	f.mu.Unlock()
	runWithStore(t, f, dial, 300*time.Millisecond, store) // restart with the same store
	if len(*ran) != 1 {
		t.Fatalf("restart ran again: %v", *ran)
	}
	acks, _ := f.snapshot()
	if len(acks) != 1 || acks[0].ID != "run_1" || acks[0].Status != "ok" || acks[0].Output != "ok: say oi" {
		t.Fatalf("acks %+v", acks)
	}
	if saved, _ := store.Load(); len(saved) != 0 {
		t.Fatalf("acked results stay stored: %+v", saved)
	}
}

func TestSaveFailureStopsExecution(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_1", "command": "say a"}, {"id": "run_2", "command": "say b"}}}
	dial, ran := console()
	store := &flakyStore{fail: true}
	runWithStore(t, f, dial, 300*time.Millisecond, store)
	if len(*ran) != 1 {
		t.Fatalf("ran %v while results cannot be saved", *ran)
	}
}

func TestExecutionResumesWhenSaveRecovers(t *testing.T) {
	f := &fakeAPI{queue: []map[string]any{{"id": "run_1", "command": "say a"}, {"id": "run_2", "command": "say b"}}}
	dial, ran := console()
	store := &flakyStore{fail: true}
	go func() { time.Sleep(150 * time.Millisecond); store.setFail(false) }()
	runWithStore(t, f, dial, 600*time.Millisecond, store)
	if len(*ran) != 2 {
		t.Fatalf("ran %v", *ran)
	}
	acks, _ := f.snapshot()
	if len(acks) != 2 {
		t.Fatalf("acks %+v", acks)
	}
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
	info    func() string // serverinfo; nil answers something unreadable
}

func (c *scriptedConsole) Exec(_ context.Context, cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd == "playerlist" {
		return c.players(), nil
	}
	if cmd == "serverinfo" {
		if c.info == nil {
			return "not json", nil
		}
		return c.info(), nil
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

func lastStatus(hbs []map[string]any) (map[string]any, bool) {
	if len(hbs) == 0 {
		return nil, false
	}
	st, ok := hbs[len(hbs)-1]["status"].(map[string]any)
	return st, ok
}

func TestHeartbeatCarriesPlayersAndQueue(t *testing.T) {
	f := &fakeAPI{}
	dial, _ := console()
	runFor(t, f, dial, 200*time.Millisecond)
	_, hbs := f.snapshot()
	st, ok := lastStatus(hbs)
	if !ok || st["players"] != float64(1) || st["maxPlayers"] != float64(100) || st["queue"] != float64(3) {
		t.Fatalf("heartbeats %+v", hbs)
	}
}

func TestHeartbeatGoesOutWhenQueueChanges(t *testing.T) {
	f := &fakeAPI{}
	srv := f.server(t)
	g, _ := games.Get("RUST")
	var mu, qmu sync.Mutex
	var ran []string
	queue := 0
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string { return "[]" }, info: func() string {
			qmu.Lock()
			defer qmu.Unlock()
			return fmt.Sprintf(`{"Players":0,"MaxPlayers":50,"Queued":%d,"Joining":0}`, queue)
		}}, nil
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		qmu.Lock()
		queue = 7
		qmu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	// HeartbeatEvery is an hour: only a change can send a second heartbeat.
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "t", Pause: 20 * time.Millisecond, HeartbeatEvery: time.Hour, StatusEvery: 20 * time.Millisecond, ForceEvery: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	_, hbs := f.snapshot()
	// the very first heartbeat (connect) has no status yet
	var first map[string]any
	for _, h := range hbs {
		if s, ok := h["status"].(map[string]any); ok {
			first = s
			break
		}
	}
	st, ok := lastStatus(hbs)
	if len(hbs) < 3 || first["queue"] != float64(0) || !ok || st["queue"] != float64(7) {
		t.Fatalf("heartbeats %+v", hbs)
	}
}

func TestUnreadableServerinfoLeavesStatusOut(t *testing.T) {
	f := &fakeAPI{}
	var mu sync.Mutex
	var ran []string
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string { return "[]" }}, nil
	}
	runFor(t, f, dial, 200*time.Millisecond)
	_, hbs := f.snapshot()
	if len(hbs) == 0 {
		t.Fatal("no heartbeat")
	}
	for _, h := range hbs {
		if _, has := h["status"]; has {
			t.Fatalf("status should be omitted: %+v", h)
		}
	}
}

func TestStatusChurnDoesNotStormHeartbeats(t *testing.T) {
	f := &fakeAPI{}
	srv := f.server(t)
	g, _ := games.Get("RUST")
	var mu, nmu sync.Mutex
	var ran []string
	n := 0
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string { return "[]" }, info: func() string {
			nmu.Lock()
			defer nmu.Unlock()
			n++ // players and queue move on every read
			return fmt.Sprintf(`{"Players":%d,"MaxPlayers":100,"Queued":%d}`, n*20%100, n%2)
		}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "t", Pause: 10 * time.Millisecond, HeartbeatEvery: time.Hour, StatusEvery: 10 * time.Millisecond, ForceEvery: 200 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	_, hbs := f.snapshot()
	if len(hbs) < 2 || len(hbs) > 5 { // connect + at most one per 200 ms
		t.Fatalf("%d heartbeats: %+v", len(hbs), hbs)
	}
}

func TestFailedStatusReadKeepsLastGoodOne(t *testing.T) {
	f := &fakeAPI{}
	srv := f.server(t)
	g, _ := games.Get("RUST")
	var mu, nmu sync.Mutex
	var ran []string
	reads := 0
	dial := func(context.Context) (rcon.Conn, error) {
		return &scriptedConsole{mu: &mu, ran: &ran, players: func() string { return "[]" }, info: func() string {
			nmu.Lock()
			defer nmu.Unlock()
			reads++
			if reads > 1 {
				return "garbage"
			}
			return `{"Players":5,"MaxPlayers":100,"Queued":4}`
		}}, nil
	}
	// The good read comes first and goes stale at 2 x StatusEvery = 100 ms: the run ends before that,
	// after the next read (~50 ms) failed, with regular heartbeats every 20 ms in between.
	ctx, cancel := context.WithTimeout(context.Background(), 95*time.Millisecond)
	defer cancel()
	Run(ctx, Options{Client: &api.Client{BaseURL: srv.URL, Key: "k"}, Game: g, Dial: dial, Version: "t", Pause: 5 * time.Millisecond, HeartbeatEvery: 20 * time.Millisecond, StatusEvery: 50 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	nmu.Lock()
	n := reads
	nmu.Unlock()
	if n < 2 {
		t.Fatalf("the failed read was never reached (%d reads)", n)
	}
	_, hbs := f.snapshot()
	st, ok := lastStatus(hbs)
	if !ok || st["queue"] != float64(4) {
		t.Fatalf("heartbeats %+v", hbs)
	}
}
