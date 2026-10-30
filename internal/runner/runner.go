// Package runner is the delivery loop: who is online → queue → run on the console → report.
package runner

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strings"
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
	// StaleAfter: a player list older than this is taken again before running online-only runs.
	StaleAfter time.Duration
}

const (
	defaultWait      = 25
	defaultPause     = 2 * time.Second
	defaultHeartbeat = 60 * time.Second
	defaultBackoff   = 60 * time.Second
	defaultStale     = 5 * time.Second
	limit            = 50
	maxOutput        = 2000
	// On the way out, what ran still gets reported for this long.
	finalFlush = 10 * time.Second
	noAnswer   = "O servidor não respondeu: o comando pode ter rodado. Confira no jogo antes de reenviar."
)

type loop struct {
	Options
	conn      rcon.Conn
	lastErr   string
	beatErr   string // lastErr as last reported
	online    int
	players   []api.Player
	listedAt  time.Time
	lastBeat  time.Time
	backoff   time.Duration
	unacked   map[string]api.AckResult // ran, not yet confirmed to the API: never run again
	unackedAt []string
}

// Run delivers until ctx ends. A down console or API is retried; what already ran is reported
// before it returns.
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
	if o.StaleAfter == 0 {
		o.StaleAfter = defaultStale
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	l := &loop{Options: o, unacked: map[string]api.AckResult{}}
	defer func() {
		if len(l.unackedAt) > 0 {
			fc, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalFlush)
			if !l.flushAcks(fc) {
				l.Logf("não deu para avisar a Rivoo de %d comandos que já rodaram", len(l.unackedAt))
			}
			cancel()
		}
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
		if !l.list(ctx) {
			continue
		}
		runs, err := l.Client.Poll(ctx, l.players, l.Wait, limit)
		if err != nil {
			if errors.Is(err, api.ErrUnauthorized) {
				l.Logf("a Rivoo recusou a chave desta conexão: apague rivoo-agent.json e pareie de novo")
			} else if !errors.Is(err, api.ErrTemporary) && ctx.Err() == nil {
				l.Logf("Rivoo: %v", err)
			}
			l.sleep(ctx)
			continue
		}
		for _, r := range runs {
			if ctx.Err() != nil {
				break // stop between runs; what ran is reported on the way out
			}
			if _, done := l.unacked[r.ID]; done {
				continue
			}
			if r.RequireOnline && !l.stillOnline(ctx, r) {
				continue // left while we waited: stays queued
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

// list takes who is online; false when the console dropped.
func (l *loop) list(ctx context.Context) bool {
	players, hasList, err := l.Game.Players(ctx, l.conn)
	if err != nil {
		l.drop(err)
		return false
	}
	l.listedAt = time.Now()
	if !hasList {
		l.players = nil // the API then gives every due run
	} else {
		l.players = clean(players)
	}
	if len(l.players) != l.online || l.lastErr != l.beatErr {
		l.online = len(l.players)
		l.lastBeat = time.Time{} // the panel shows who is online and the console state: tell it now
		l.beat(ctx)
	}
	return true
}

// stillOnline re-checks a stale list before an online-only run.
func (l *loop) stillOnline(ctx context.Context, r api.Run) bool {
	if l.players == nil {
		return true // no list in this game
	}
	if time.Since(l.listedAt) > l.StaleAfter && !l.list(ctx) {
		return false
	}
	for _, p := range l.players {
		if r.Recipient.UUID != nil && *r.Recipient.UUID == p.ID {
			return true
		}
		if r.Recipient.Name != nil && (strings.EqualFold(*r.Recipient.Name, p.Name) || *r.Recipient.Name == p.ID) {
			return true
		}
	}
	return false
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// clean keeps rows the API accepts: a name (cut to 64), and an id only when well formed.
func clean(in []api.Player) []api.Player {
	out := make([]api.Player, 0, len(in))
	for _, p := range in {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		if len(p.Name) > 64 {
			p.Name = p.Name[:64]
		}
		if !validID.MatchString(p.ID) {
			p.ID = ""
		}
		out = append(out, p)
	}
	return out
}

// exec runs one command; false when the console dropped.
func (l *loop) exec(ctx context.Context, r api.Run) bool {
	cmd, err := l.Game.Prepare(ctx, l.conn, r)
	if err != nil {
		if errors.Is(err, rcon.ErrConn) || errors.Is(err, rcon.ErrNoAnswer) {
			l.drop(err) // the command itself never went out
			return false
		}
		l.record(api.AckResult{ID: r.ID, Status: "failed", Error: err.Error()})
		return true
	}
	out, err := l.conn.Exec(ctx, cmd)
	if errors.Is(err, rcon.ErrNoAnswer) {
		// It went out and may have run: report it, never run it again by itself.
		l.record(api.AckResult{ID: r.ID, Status: "failed", Error: noAnswer})
		l.drop(err)
		return false
	}
	if err != nil {
		l.drop(err)
		return false
	}
	failed, why := l.Game.Failed(out)
	if len(out) > maxOutput {
		out = out[:maxOutput]
	}
	if failed {
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
		if l.lastErr != l.beatErr {
			l.lastBeat = time.Time{}
			l.beat(ctx)
		}
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
		l.beatErr = l.lastErr
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
	case errors.Is(err, rcon.ErrNoAnswer):
		return "o servidor parou de responder no meio de um comando"
	case errors.Is(err, rcon.ErrConn):
		return "não foi possível falar com o RCON (servidor desligado, porta ou IP errados)"
	}
	return err.Error()
}
