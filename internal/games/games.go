// Package games knows each game's console: how to list who is online, which answers mean the
// command failed, and what a command needs before it runs (Conan's player index).
package games

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/payera-br/rivoo-agent/internal/api"
	"github.com/payera-br/rivoo-agent/internal/rcon"
)

// Game is one game's console rules.
type Game interface {
	Name() string
	Protocol() string
	// Players lists who is online; hasList is false for games whose console can't tell.
	Players(ctx context.Context, c rcon.Conn) (players []api.Player, hasList bool, err error)
	// Prepare turns a queued command into what the console runs.
	Prepare(ctx context.Context, c rcon.Conn, run api.Run) (string, error)
	// Failed reports whether the answer means the command didn't work, and why.
	Failed(output string) (bool, string)
}

// Status is how full the server is. Queue counts who waits to get in.
type Status struct{ Players, MaxPlayers, Queue int }

// Statuser is implemented by games whose console reports server load and queue.
type Statuser interface {
	Status(ctx context.Context, c rcon.Conn) (Status, error)
}

type game struct {
	name, protocol, listCmd string
	statusCmd               string
	parseStatus             func(string) (Status, error)
	parse                   func(string) []api.Player
	errors                  []string
	prepare                 func(ctx context.Context, c rcon.Conn, run api.Run) (string, error)
}

func (g *game) Name() string     { return g.name }
func (g *game) Protocol() string { return g.protocol }

func (g *game) Players(ctx context.Context, c rcon.Conn) ([]api.Player, bool, error) {
	if g.listCmd == "" {
		return nil, false, nil
	}
	out, err := c.Exec(ctx, g.listCmd)
	if err != nil {
		return nil, true, err
	}
	return g.parse(out), true, nil
}

// Status reads the server load; it errors when the game has no status command or the answer
// can't be read.
func (g *game) Status(ctx context.Context, c rcon.Conn) (Status, error) {
	if g.statusCmd == "" {
		return Status{}, fmt.Errorf("%s não informa o estado do servidor", g.name)
	}
	out, err := c.Exec(ctx, g.statusCmd)
	if err != nil {
		return Status{}, err
	}
	return g.parseStatus(out)
}

// HasStatus reports whether the game can tell its load and queue.
func HasStatus(g Game) bool {
	gg, ok := g.(*game)
	return ok && gg.statusCmd != ""
}

func (g *game) Prepare(ctx context.Context, c rcon.Conn, run api.Run) (string, error) {
	if g.prepare != nil {
		return g.prepare(ctx, c, run)
	}
	return run.Command, nil
}

// Generic answers that mean "didn't run" in most consoles.
var common = []string{"unknown command", "unknown or incomplete command", "no player was found", "player not found"}

func (g *game) Failed(output string) (bool, string) {
	low := strings.ToLower(output)
	for _, e := range append(g.errors, common...) {
		if strings.Contains(low, e) {
			return true, firstLine(output)
		}
	}
	return false, ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

var registry = map[string]*game{}

func register(g *game) { registry[g.name] = g }

// Get returns a game by its Rivoo name (RUST, PALWORLD…).
func Get(name string) (Game, error) {
	if g, ok := registry[name]; ok {
		return g, nil
	}
	return nil, fmt.Errorf("jogo desconhecido: %s", name)
}

// Names lists the supported games.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// Detect guesses the game from what its console answers.
func Detect(ctx context.Context, protocol string, c rcon.Conn) (Game, error) {
	switch protocol {
	case "webrcon":
		return Get("RUST")
	case "telnet":
		return Get("SEVEN_DAYS")
	}
	probes := []struct {
		cmd, game string
		re        *regexp.Regexp
	}{
		{"list", "MINECRAFT_JAVA", mcList},
		{"ShowPlayers", "PALWORLD", regexp.MustCompile(`(?i)^name,playeruid,steamid`)},
		{"listplayers", "CONAN", regexp.MustCompile(`(?i)Idx\s*\|\s*Char name`)},
	}
	for _, p := range probes {
		if out, err := c.Exec(ctx, p.cmd); err == nil && p.re.MatchString(strings.TrimSpace(out)) {
			return Get(p.game)
		}
	}
	return Get("SOURCE_GENERIC")
}

var (
	mcList   = regexp.MustCompile(`(?i)There are \d+ of a max(?: of)? \d+ players online:?\s*(.*)`)
	arkLine  = regexp.MustCompile(`^\s*\d+\.\s*(.+?),\s*([0-9A-Za-z]+)\s*$`)
	sdtdLine = regexp.MustCompile(`^\s*\d+\.\s*id=\d+,\s*([^,]+),.*?pltfmid=(?:Steam_)?([^,\s]+)`)
)

func lines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
}

func init() {
	register(&game{name: "MINECRAFT_JAVA", protocol: "source", listCmd: "list", errors: []string{"unknown item", "incorrect argument", "expected whitespace", "invalid", "no entity was found", "that player cannot be found"}, parse: func(out string) []api.Player {
		m := mcList.FindStringSubmatch(out)
		if m == nil {
			return nil
		}
		var ps []api.Player
		for _, n := range strings.Split(m[1], ",") {
			if n = strings.TrimSpace(n); n != "" {
				ps = append(ps, api.Player{Name: n})
			}
		}
		return ps
	}})

	register(&game{name: "RUST", protocol: "webrcon", listCmd: "playerlist", statusCmd: "serverinfo", parseStatus: rustStatus, errors: []string{"' not found", "couldn't find player"}, parse: func(out string) []api.Player {
		var rows []struct{ SteamID, DisplayName string }
		if json.Unmarshal([]byte(out), &rows) != nil {
			return nil
		}
		ps := make([]api.Player, 0, len(rows))
		for _, r := range rows {
			ps = append(ps, api.Player{ID: r.SteamID, Name: r.DisplayName})
		}
		return ps
	}})

	ark := func(out string) []api.Player {
		var ps []api.Player
		for _, l := range lines(out) {
			if m := arkLine.FindStringSubmatch(l); m != nil {
				ps = append(ps, api.Player{ID: m[2], Name: strings.TrimSpace(m[1])})
			}
		}
		return ps
	}
	register(&game{name: "ARK_SE", protocol: "source", listCmd: "ListPlayers", parse: ark})
	register(&game{name: "ARK_SA", protocol: "source", listCmd: "ListPlayers", parse: ark})

	conanPlayers := func(out string) []api.Player {
		var ps []api.Player
		for _, l := range lines(out) {
			cols := strings.Split(l, "|")
			if len(cols) < 5 || strings.Contains(strings.ToLower(cols[0]), "idx") {
				continue
			}
			ps = append(ps, api.Player{ID: strings.TrimSpace(cols[4]), Name: strings.TrimSpace(cols[1])})
		}
		return ps
	}
	register(&game{name: "CONAN", protocol: "source", listCmd: "listplayers", parse: conanPlayers, prepare: conanIndex})

	register(&game{name: "PALWORLD", protocol: "source", listCmd: "ShowPlayers", parse: func(out string) []api.Player {
		var ps []api.Player
		for i, l := range lines(out) {
			cols := strings.Split(l, ",")
			if i == 0 && strings.EqualFold(strings.TrimSpace(cols[0]), "name") || len(cols) < 3 {
				continue
			}
			ps = append(ps, api.Player{ID: strings.TrimSpace(cols[2]), Name: strings.TrimSpace(cols[0])})
		}
		return ps
	}})

	register(&game{name: "SEVEN_DAYS", protocol: "telnet", listCmd: "lp", errors: []string{"*** error"}, parse: func(out string) []api.Player {
		var ps []api.Player
		for _, l := range lines(out) {
			if m := sdtdLine.FindStringSubmatch(l); m != nil {
				ps = append(ps, api.Player{ID: m[2], Name: strings.TrimSpace(m[1])})
			}
		}
		return ps
	}})

	// No reliable player list yet: deliver without the online check.
	register(&game{name: "HYTALE", protocol: "source"})
	register(&game{name: "SOURCE_GENERIC", protocol: "source"})
}

// conanIndex swaps {index} for the player's session index (from listplayers), by id or name.
func conanIndex(ctx context.Context, c rcon.Conn, run api.Run) (string, error) {
	if !strings.Contains(run.Command, "{index}") {
		return run.Command, nil
	}
	out, err := c.Exec(ctx, "listplayers")
	if err != nil {
		return "", err
	}
	// With a Steam id, only the id: anyone can name a character after the buyer.
	want := func(id, name string) bool {
		if run.Recipient.UUID != nil && *run.Recipient.UUID != "" {
			return *run.Recipient.UUID == id
		}
		return run.Recipient.Name != nil && strings.EqualFold(*run.Recipient.Name, name)
	}
	for _, l := range lines(out) {
		cols := strings.Split(l, "|")
		if len(cols) < 5 || strings.Contains(strings.ToLower(cols[0]), "idx") {
			continue
		}
		if want(strings.TrimSpace(cols[4]), strings.TrimSpace(cols[1])) {
			return strings.ReplaceAll(run.Command, "{index}", strings.TrimSpace(cols[0])), nil
		}
	}
	return "", fmt.Errorf("jogador não está online no Conan")
}

// rustStatus reads `serverinfo`: players, capacity and the queue (waiting + joining).
func rustStatus(out string) (Status, error) {
	var info struct {
		Players, MaxPlayers, Queued, Joining *int
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &info); err != nil {
		return Status{}, fmt.Errorf("serverinfo inválido: %w", err)
	}
	if info.Players == nil || info.MaxPlayers == nil {
		return Status{}, fmt.Errorf("serverinfo sem Players/MaxPlayers")
	}
	st := Status{Players: *info.Players, MaxPlayers: *info.MaxPlayers}
	if info.Queued != nil {
		st.Queue += *info.Queued
	}
	if info.Joining != nil {
		st.Queue += *info.Joining
	}
	if st.Players < 0 || st.MaxPlayers < 0 || st.Queue < 0 {
		return Status{}, fmt.Errorf("serverinfo com números negativos")
	}
	return st, nil
}
