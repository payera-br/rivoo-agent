package games

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/payera-br/rivoo-agent/internal/api"
)

// scripted answers each command from a map; records what ran.
type scripted struct {
	answers map[string]string
	ran     []string
}

func (s *scripted) Exec(_ context.Context, cmd string) (string, error) {
	s.ran = append(s.ran, cmd)
	return s.answers[cmd], nil
}
func (s *scripted) Close() error { return nil }

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlayersPerGame(t *testing.T) {
	cases := []struct {
		game, cmd, file string
		want            []api.Player
	}{
		{"MINECRAFT_JAVA", "list", "minecraft", []api.Player{{Name: "Steve"}, {Name: "Alex_99"}}},
		{"RUST", "playerlist", "rust", []api.Player{{ID: "76561198000000001", Name: "Ana Souza"}}},
		{"ARK_SE", "ListPlayers", "ark_se", []api.Player{{ID: "76561198000000001", Name: "Ana Souza"}, {ID: "76561198000000002", Name: "Bia"}}},
		{"ARK_SA", "ListPlayers", "ark_sa", []api.Player{{ID: "0002a1b2c3d4e5f60718293a4b5c6d7e", Name: "Ana Souza"}}},
		{"CONAN", "listplayers", "conan", []api.Player{{ID: "76561198000000001", Name: "Ana"}, {ID: "76561198000000002", Name: "Bia"}}},
		{"PALWORLD", "ShowPlayers", "palworld", []api.Player{{ID: "76561198000000001", Name: "Ana Souza"}}},
		{"SEVEN_DAYS", "lp", "seven_days", []api.Player{{ID: "76561198000000001", Name: "Ana Souza"}}},
	}
	for _, tc := range cases {
		g, err := Get(tc.game)
		if err != nil {
			t.Fatal(err)
		}
		players, hasList, err := g.Players(context.Background(), &scripted{answers: map[string]string{tc.cmd: fixture(t, tc.file)}})
		if err != nil || !hasList {
			t.Fatalf("%s: %v %v", tc.game, hasList, err)
		}
		if len(players) != len(tc.want) {
			t.Fatalf("%s: got %+v", tc.game, players)
		}
		for i := range players {
			if players[i] != tc.want[i] {
				t.Fatalf("%s: got %+v want %+v", tc.game, players[i], tc.want[i])
			}
		}
	}
}

func TestEmptyServers(t *testing.T) {
	for game, answer := range map[string]string{
		"MINECRAFT_JAVA": "There are 0 of a max of 20 players online: ",
		"ARK_SE":         "No Players Connected",
		"RUST":           "[]",
		"PALWORLD":       "name,playeruid,steamid\n",
		"SEVEN_DAYS":     "Total of 0 in the game",
	} {
		g, _ := Get(game)
		cmd := map[string]string{"MINECRAFT_JAVA": "list", "ARK_SE": "ListPlayers", "RUST": "playerlist", "PALWORLD": "ShowPlayers", "SEVEN_DAYS": "lp"}[game]
		players, _, err := g.Players(context.Background(), &scripted{answers: map[string]string{cmd: answer}})
		if err != nil || len(players) != 0 {
			t.Fatalf("%s: %+v %v", game, players, err)
		}
	}
}

func TestNoListGamesDontCheckOnline(t *testing.T) {
	for _, game := range []string{"HYTALE", "SOURCE_GENERIC"} {
		g, _ := Get(game)
		s := &scripted{}
		if _, hasList, err := g.Players(context.Background(), s); hasList || err != nil || len(s.ran) != 0 {
			t.Fatalf("%s: %v %v %v", game, hasList, err, s.ran)
		}
	}
}

func TestFailedAnswers(t *testing.T) {
	cases := []struct {
		game, out string
		failed    bool
	}{
		{"MINECRAFT_JAVA", "Unknown or incomplete command, see below for error", true},
		{"MINECRAFT_JAVA", "No player was found", true},
		{"MINECRAFT_JAVA", "Gave 1 [Diamond] to Steve", false},
		{"RUST", "Command 'kit.givex' not found", true},
		{"RUST", "Unknown command: kit.givex", true},
		{"PALWORLD", "Broadcasted: oi", false},
		{"SEVEN_DAYS", "*** ERROR: unknown command 'giveselfx'", true},
		{"SOURCE_GENERIC", "Unknown command", true},
	}
	for _, tc := range cases {
		g, _ := Get(tc.game)
		if failed, _ := g.Failed(tc.out); failed != tc.failed {
			t.Fatalf("%s %q: failed=%v", tc.game, tc.out, failed)
		}
	}
}

func TestConanIndex(t *testing.T) {
	g, _ := Get("CONAN")
	s := &scripted{answers: map[string]string{"listplayers": fixture(t, "conan")}}
	id := "76561198000000002"
	run := api.Run{Command: "con {index} spawnitem 10001 5"}
	run.Recipient.UUID = &id
	cmd, err := g.Prepare(context.Background(), s, run)
	if err != nil || cmd != "con 1 spawnitem 10001 5" {
		t.Fatalf("%q %v", cmd, err)
	}
	gone := "76561198000000009"
	run.Recipient.UUID = &gone
	if _, err := g.Prepare(context.Background(), s, run); err == nil || !strings.Contains(err.Error(), "não está online") {
		t.Fatalf("player gone: %v", err)
	}
	plain := api.Run{Command: "broadcast oi"}
	if cmd, err := g.Prepare(context.Background(), s, plain); err != nil || cmd != "broadcast oi" {
		t.Fatalf("%q %v", cmd, err)
	}
}

func TestDetectByAnswer(t *testing.T) {
	s := &scripted{answers: map[string]string{"playerlist": fixture(t, "rust")}}
	if g, err := Detect(context.Background(), "webrcon", s); err != nil || g.Name() != "RUST" {
		t.Fatalf("%v %v", g, err)
	}
	s = &scripted{answers: map[string]string{"list": fixture(t, "minecraft")}}
	if g, err := Detect(context.Background(), "source", s); err != nil || g.Name() != "MINECRAFT_JAVA" {
		t.Fatalf("%v %v", g, err)
	}
	if g, err := Detect(context.Background(), "telnet", &scripted{}); err != nil || g.Name() != "SEVEN_DAYS" {
		t.Fatalf("%v %v", g, err)
	}
}

func TestConanMatchesBySteamIdOnlyWhenItHasOne(t *testing.T) {
	g, _ := Get("CONAN")
	// Bia (…02) is at index 1; someone else named their character "Bia" at index 0.
	s := &scripted{answers: map[string]string{"listplayers": "Idx | Char name | Player name | User ID | Platform ID | Platform Name\n  0 | Bia | Outro | X | 76561198000000009 | Steam\n  1 | Bia | BiaS | Y | 76561198000000002 | Steam\n"}}
	id, name := "76561198000000002", "Bia"
	run := api.Run{Command: "con {index} spawnitem 1 1"}
	run.Recipient.UUID, run.Recipient.Name = &id, &name
	if cmd, err := g.Prepare(context.Background(), s, run); err != nil || cmd != "con 1 spawnitem 1 1" {
		t.Fatalf("%q %v", cmd, err)
	}
}

func TestMinecraftErrorsAndNoBroadNotFound(t *testing.T) {
	g, _ := Get("MINECRAFT_JAVA")
	for _, out := range []string{"Unknown item 'minecraft:diamondz'", "Incorrect argument for command", "Expected whitespace to end one argument, but found trailing data"} {
		if failed, _ := g.Failed(out); !failed {
			t.Fatalf("%q not a failure", out)
		}
	}
	if failed, _ := g.Failed("Gave 1 [Map of the place not found yet] to Steve"); failed {
		t.Fatal("a success that mentions 'not found' failed")
	}
}
