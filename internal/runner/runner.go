// Package runner is the delivery loop: who is online → queue → run on the console → report.
package runner

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/payera-br/rivoo-agent/internal/api"
	"github.com/payera-br/rivoo-agent/internal/games"
	"github.com/payera-br/rivoo-agent/internal/rcon"
)

// Options configures one connection's loop. Zero durations take production values.
type Options struct {
	Client  *api.Client
	Game    games.Game
	Dial    func(ctx context.Context) (rcon.Conn, error)
	Version string
	// Mode is "cloud" for the cloud worker (reported in the heartbeat).
	Mode           string
	Logf           func(format string, args ...any)
	Wait           int // long-poll seconds
	Pause          time.Duration
	HeartbeatEvery time.Duration
	MaxBackoff     time.Duration
}

const (
	defaultWait      = 25
	defaultPause     = 2 * time.Second
	defaultHeartbeat = 60 * time.Second
	defaultBackoff   = 60 * time.Second
	limit            = 50
	maxOutput        = 2000
)

type loop struct {
	Options
	conn      rcon.Conn
	lastErr   string
	online    int
	lastBeat  time.Time
	backoff   time.Duration
	unacked   map[string]api.AckResult // ran, not yet confirmed to the API: never run again
	unackedAt []string
}

// Run delivers until ctx ends. It never returns early: a down console or API is retried.
func Run(ctx context.Context, o Options) {
	if o.Wait == 0 && o.Pause == 0 {
		o.Wait = defaultWait
	}
	if o.Pause == 0 {
		o.Pause = defaultPause
	}
	if o.HeartbeatEvery == 0 {
		o.HeartbeatEvery = defaultHeartbeat
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = defaultBackoff
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	l := &loop{Options: o, unacked: map[string]api.AckResult{}}
	defer func() {
		if l.conn != nil {
			l.conn.Close()
		}
	}()
	for ctx.Err() == nil {
		l.beat(ctx)
		if !l.flushAcks(ctx) || !l.ensureConn(ctx) {
			l.sleep(ctx)
			continue
		}
		l.backoff = 0
		players, hasList, err := l.Game.Players(ctx, l.conn)
		if err != nil {
			l.drop(err)
			continue
		}
		if !hasList {
			players = nil
		} else if players == nil {
			players = []api.Player{}
		}
		l.online = len(players)
		runs, err := l.Client.Poll(ctx, players, l.Wait, limit)
		if err != nil {
			if errors.Is(err, api.ErrUnauthorized) {
				l.Logf("a Rivoo recusou a chave desta conexão; pareie de novo")
			}
			l.sleep(ctx)
			continue
		}
		for _, r := range runs {
			if _, done := l.unacked[r.ID]; done {
				continue
			}
			if !l.exec(ctx, r) {
				break // console dropped: reconnect, the rest stays queued
			}
		}
		l.flushAcks(ctx)
		if len(runs) == 0 {
			l.pause(ctx, l.Pause)
		}
	}
}

// exec runs one command; false when the console dropped (nothing reported for this run).
func (l *loop) exec(ctx context.Context, r api.Run) bool {
	cmd, err := l.Game.Prepare(ctx, l.conn, r)
	if err != nil {
		if errors.Is(err, rcon.ErrConn) {
			l.drop(err)
			return false
		}
		l.record(api.AckResult{ID: r.ID, Status: "failed", Error: err.Error()})
		return true
	}
	out, err := l.conn.Exec(ctx, cmd)
	if err != nil {
		l.drop(err)
		return false
	}
	if len(out) > maxOutput {
		out = out[:maxOutput]
	}
	if failed, why := l.Game.Failed(out); failed {
		l.record(api.AckResult{ID: r.ID, Status: "failed", Error: why, Output: out})
	} else {
		l.record(api.AckResult{ID: r.ID, Status: "ok", Output: out})
	}
	return true
}

func (l *loop) record(a api.AckResult) {
	l.unacked[a.ID] = a
	l.unackedAt = append(l.unackedAt, a.ID)
}

// flushAcks reports what ran; false while the API refuses (then nothing new runs).
func (l *loop) flushAcks(ctx context.Context) bool {
	if len(l.unackedAt) == 0 {
		return true
	}
	batch := make([]api.AckResult, 0, len(l.unackedAt))
	for _, id := range l.unackedAt {
		batch = append(batch, l.unacked[id])
	}
	if err := l.Client.Ack(ctx, batch); err != nil {
		return false
	}
	l.unacked = map[string]api.AckResult{}
	l.unackedAt = nil
	return true
}

func (l *loop) ensureConn(ctx context.Context) bool {
	if l.conn != nil {
		return true
	}
	c, err := l.Dial(ctx)
	if err != nil {
		l.lastErr = describe(err)
		l.Logf("RCON: %s", l.lastErr)
		return false
	}
	l.conn = c
	l.lastErr = ""
	return true
}

func (l *loop) drop(err error) {
	l.lastErr = describe(err)
	l.Logf("RCON caiu: %s", l.lastErr)
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
}

func (l *loop) beat(ctx context.Context) {
	if time.Since(l.lastBeat) < l.HeartbeatEvery {
		return
	}
	details := map[string]any{"agent": "rcon", "game": l.Game.Name(), "protocol": l.Game.Protocol(), "playersOnline": l.online}
	if l.lastErr != "" {
		details["lastRconError"] = l.lastErr
	}
	if l.Mode != "" {
		details["mode"] = l.Mode
	}
	if err := l.Client.Heartbeat(ctx, l.Version, details); err == nil {
		l.lastBeat = time.Now()
	}
}

func (l *loop) sleep(ctx context.Context) {
	if l.backoff == 0 {
		l.backoff = l.Pause
	} else if l.backoff *= 2; l.backoff > l.MaxBackoff {
		l.backoff = l.MaxBackoff
	}
	l.pause(ctx, l.backoff)
}

func (l *loop) pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// describe turns a console error into a line for the panel (never the password).
func describe(err error) string {
	switch {
	case errors.Is(err, rcon.ErrAuth):
		return "senha do RCON recusada"
	case errors.Is(err, rcon.ErrConn):
		return "não foi possível falar com o RCON (servidor desligado, porta ou IP errados)"
	}
	return err.Error()
}
