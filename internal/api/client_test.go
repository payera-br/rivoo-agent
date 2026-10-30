package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPollSendsKeyAndPlayers(t *testing.T) {
	var got struct {
		Wait    int      `json:"wait"`
		Limit   int      `json:"limit"`
		Players []Player `json:"players"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/commands" || r.Method != http.MethodPost {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer rck_abc" {
			t.Fatalf("auth header %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"data":[{"id":"run_1","command":"say oi","recipient":{"uuid":"76561198000000001","name":null},"requireOnline":true}],"hasMore":false}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Key: "rck_abc"}
	runs, err := c.Poll(context.Background(), []Player{{ID: "76561198000000001", Name: "Ana"}}, 25, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Wait != 25 || got.Limit != 50 || len(got.Players) != 1 || got.Players[0].ID != "76561198000000001" {
		t.Fatalf("body %+v", got)
	}
	if len(runs) != 1 || runs[0].ID != "run_1" || runs[0].Recipient.UUID == nil || *runs[0].Recipient.UUID != "76561198000000001" || !runs[0].RequireOnline {
		t.Fatalf("runs %+v", runs)
	}
}

func TestStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{{401, ErrUnauthorized}, {503, ErrTemporary}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		c := &Client{BaseURL: srv.URL, Key: "k"}
		_, err := c.Poll(context.Background(), nil, 0, 1)
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: got %v", tc.status, err)
		}
		srv.Close()
	}
}

func TestAckSendsResults(t *testing.T) {
	var got struct{ Results []AckResult `json:"results"` }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/commands/ack" {
			t.Fatalf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"acked":["run_1"],"ignored":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k"}
	if err := c.Ack(context.Background(), []AckResult{{ID: "run_1", Status: "failed", Error: "Unknown command", Output: "Unknown command: x"}}); err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 1 || got.Results[0].Status != "failed" || got.Results[0].Output != "Unknown command: x" {
		t.Fatalf("got %+v", got)
	}
}

func TestPathPrefixForCloudWorker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent-cloud/connections/conn_1/heartbeat" || r.Header.Get("Authorization") != "Bearer platform-key" {
			t.Fatalf("got %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "platform-key", Prefix: "/v1/agent-cloud/connections/conn_1"}
	if err := c.Heartbeat(context.Background(), "0.1.0", map[string]any{"game": "RUST"}); err != nil {
		t.Fatal(err)
	}
}
