// Package cloud is the worker Rivoo runs for merchants who can't run the agent themselves: one
// delivery loop per cloud connection, kept in sync with the list the API gives.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/payera-br/rivoo-agent/internal/api"
	"github.com/payera-br/rivoo-agent/internal/games"
	"github.com/payera-br/rivoo-agent/internal/rcon"
	"github.com/payera-br/rivoo-agent/internal/runner"
)

// Agent is one cloud connection's console, as the API lists it.
type Agent struct {
	ConnectionID string `json:"connectionId"`
	Game         string `json:"game"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Password     string `json:"password"`
}

type Options struct {
	BaseURL, Key, Version string
	Every                 time.Duration
	// Start runs one agent until ctx ends (tests replace it).
	Start func(ctx context.Context, a Agent)
}

type running struct {
	cancel context.CancelFunc
	done   chan struct{}
	same   Agent
}

// Work keeps one loop per listed agent: starts new ones, stops removed ones, restarts changed ones.
func Work(ctx context.Context, o Options) {
	if o.Every == 0 {
		o.Every = 30 * time.Second
	}
	if o.Start == nil {
		o.Start = func(ctx context.Context, a Agent) { runAgent(ctx, o, a) }
	}
	var wg sync.WaitGroup
	live := map[string]running{}
	defer func() {
		for _, r := range live {
			r.cancel()
		}
		wg.Wait()
	}()
	for {
		if list, err := fetch(ctx, o); err == nil {
			seen := map[string]bool{}
			for _, a := range list {
				seen[a.ConnectionID] = true
				var prev chan struct{}
				if r, ok := live[a.ConnectionID]; ok && r.same == a {
					continue
				} else if ok {
					r.cancel()
					prev = r.done
				}
				c, cancel := context.WithCancel(ctx)
				done := make(chan struct{})
				live[a.ConnectionID] = running{cancel: cancel, done: done, same: a}
				wg.Add(1)
				go func(a Agent) {
					defer wg.Done()
					defer close(done)
					// Never two loops on one queue: the old one finishes its batch and acks first.
					if prev != nil {
						select {
						case <-prev:
						case <-c.Done():
							return
						}
					}
					o.Start(c, a)
				}(a)
			}
			for id, r := range live {
				if !seen[id] {
					r.cancel()
					delete(live, id)
				}
			}
		}
		t := time.NewTimer(o.Every)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func fetch(ctx context.Context, o Options) ([]Agent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/v1/agent-cloud/connections", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.Key)
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var out struct {
		Data []Agent `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func runAgent(ctx context.Context, o Options, a Agent) {
	g, err := games.Get(a.Game)
	if err != nil {
		fmt.Printf("[%s] jogo desconhecido %q: agente parado\n", a.ConnectionID, a.Game)
		<-ctx.Done()
		return
	}
	runner.Run(ctx, runner.Options{
		Client:  &api.Client{BaseURL: o.BaseURL, Key: o.Key, Prefix: "/v1/agent-cloud/connections/" + a.ConnectionID},
		Game:    g,
		Version: o.Version,
		Mode:    "cloud",
		Logf:    func(f string, args ...any) { fmt.Printf("[%s] "+f+"\n", append([]any{a.ConnectionID}, args...)...) },
		Dial: func(ctx context.Context) (rcon.Conn, error) {
			ip, err := resolvePublic(ctx, a.Host)
			if err != nil {
				return nil, err
			}
			return rcon.Dial(ctx, a.Protocol, ip, a.Port, a.Password)
		},
	})
}

var errInternal = errors.New("o endereço do servidor aponta para uma rede interna")

// resolvePublic resolves the host and refuses internal addresses (DNS can point anywhere).
func resolvePublic(ctx context.Context, host string) (string, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("%w: %v", rcon.ErrConn, err)
	}
	for _, ip := range ips {
		if !publicIP(ip.IP.String()) {
			return "", errInternal
		}
	}
	return ips[0].IP.String(), nil
}

func publicIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 { // CGNAT
		return false
	}
	return true
}
