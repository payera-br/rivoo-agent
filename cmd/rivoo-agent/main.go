// Command rivoo-agent delivers Rivoo purchases to a game server over RCON.
//
//	rivoo-agent          pairs on first run (shows a code for the panel), then delivers
//	rivoo-agent cloud    Rivoo's worker: runs every cloud agent (RIVOO_PLATFORM_KEY)
//	rivoo-agent version
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/payera-br/rivoo-agent/internal/api"
	"github.com/payera-br/rivoo-agent/internal/cloud"
	"github.com/payera-br/rivoo-agent/internal/config"
	"github.com/payera-br/rivoo-agent/internal/games"
	"github.com/payera-br/rivoo-agent/internal/pair"
	"github.com/payera-br/rivoo-agent/internal/rcon"
	"github.com/payera-br/rivoo-agent/internal/runner"
	"golang.org/x/term"
)

var version = "dev"

// Default RCON port per game, offered on first run.
var defaultPorts = map[string]int{
	"MINECRAFT_JAVA": 25575, "RUST": 28016, "ARK_SE": 27020, "ARK_SA": 27020, "CONAN": 25575,
	"PALWORLD": 25575, "SEVEN_DAYS": 8081, "HYTALE": 25575, "SOURCE_GENERIC": 25575,
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println(version)
	case "run":
		err = run(ctx)
	case "cloud":
		err = runCloud(ctx)
	default:
		err = fmt.Errorf("comando desconhecido %q (use: run, cloud, version)", cmd)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("erro: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	path := config.Path()
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("lendo %s: %w", path, err)
	}
	if err := ask(&cfg); err != nil {
		return err
	}
	game, err := games.Get(cfg.Game)
	if err != nil {
		return err
	}
	client := &api.Client{BaseURL: strings.TrimRight(cfg.APIURL, "/"), Key: cfg.Key}
	if cfg.Key == "" {
		key, err := pair.Pair(ctx, client, cfg.Game, version, announce, 0)
		if err != nil {
			return err
		}
		cfg.Key = key
		client.Key = key
		if err := config.Save(path, cfg); err != nil {
			return fmt.Errorf("salvando %s: %w", path, err)
		}
		log.Printf("Pareado! Configuração salva em %s.", path)
	}
	// A new version stops the loop at a safe point (what ran is reported first), then restarts.
	loopCtx, stopLoop := context.WithCancel(ctx)
	defer stopLoop()
	var updated atomic.Bool
	if cfg.Updates() {
		go autoUpdate(loopCtx, func() {
			updated.Store(true)
			stopLoop()
		})
	}
	log.Printf("Rivoo Agent %s: %s em %s:%d", version, game.Name(), cfg.RconHost, cfg.RconPort)
	runner.Run(loopCtx, runner.Options{
		Client:  client,
		Game:    game,
		Version: version,
		Dial: func(ctx context.Context) (rcon.Conn, error) {
			return rcon.Dial(ctx, game.Protocol(), cfg.RconHost, cfg.RconPort, cfg.RconPass)
		},
	})
	if updated.Load() && ctx.Err() == nil {
		log.Printf("Agente atualizado; reiniciando.")
		restart()
	}
	return ctx.Err()
}

func runCloud(ctx context.Context) error {
	key := os.Getenv("RIVOO_PLATFORM_KEY")
	if key == "" {
		return errors.New("defina RIVOO_PLATFORM_KEY")
	}
	base := os.Getenv("RIVOO_API_URL")
	if base == "" {
		base = config.DefaultAPI
	}
	log.Printf("Rivoo Agent %s: worker da nuvem em %s", version, base)
	cloud.Work(ctx, cloud.Options{BaseURL: strings.TrimRight(base, "/"), Key: key, Version: version})
	return ctx.Err()
}

func announce(code string) {
	line := strings.Repeat("=", 48)
	fmt.Printf("\n%s\n  Código de pareamento: %s\n  No painel da Rivoo: Entrega in-game → Conectar servidor\n  → Agente Rivoo → digite este código.\n%s\n\n", line, code, line)
}

// ask fills what's missing on first run, from the terminal (env vars skip it in Docker/Pterodactyl).
func ask(cfg *config.Config) error {
	in := bufio.NewReader(os.Stdin)
	interactive := isTerminal()
	prompt := func(label, def string) string {
		if !interactive {
			return def
		}
		if def != "" {
			fmt.Printf("%s [%s]: ", label, def)
		} else {
			fmt.Printf("%s: ", label)
		}
		s, _ := in.ReadString('\n')
		if s = strings.TrimSpace(s); s == "" {
			return def
		}
		return s
	}
	if cfg.Game == "" {
		if !interactive {
			return errors.New("defina RIVOO_GAME (ex.: RUST, PALWORLD, MINECRAFT_JAVA)")
		}
		names := games.Names()
		sort.Strings(names)
		fmt.Println("Jogos:", strings.Join(names, ", "))
		cfg.Game = strings.ToUpper(prompt("Jogo", "MINECRAFT_JAVA"))
	}
	if _, err := games.Get(cfg.Game); err != nil {
		return err
	}
	if cfg.Key == "" && interactive && os.Getenv("RCON_HOST") == "" {
		cfg.RconHost = prompt("IP do RCON (127.0.0.1 se o jogo roda nesta máquina)", cfg.RconHost)
	}
	if cfg.RconPort == 0 {
		p, _ := strconv.Atoi(prompt("Porta do RCON", strconv.Itoa(defaultPorts[cfg.Game])))
		cfg.RconPort = p
	}
	if cfg.RconPass == "" {
		if !interactive {
			return errors.New("defina RCON_PASSWORD")
		}
		fmt.Print("Senha do RCON: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		cfg.RconPass = strings.TrimSpace(string(b))
	}
	return nil
}

// isTerminal: a real console (not /dev/null, a pipe or a panel's stdin).
func isTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// autoUpdate checks for signed releases; onUpdated stops the loop so the new binary can start.
var autoUpdate = func(context.Context, func()) {}
