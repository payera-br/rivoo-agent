// Package config reads the agent's settings: a JSON file next to the binary, overridden by env vars
// (Docker, Pterodactyl). The RCON password and the key live only here.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
)

// DefaultAPI is production; RIVOO_API_URL overrides it.
const DefaultAPI = "https://api.rivoo.gg"

type Config struct {
	APIURL     string `json:"apiUrl,omitempty"`
	Key        string `json:"key,omitempty"`
	Game       string `json:"game"`
	RconHost   string `json:"rconHost"`
	RconPort   int    `json:"rconPort"`
	RconPass   string `json:"rconPassword"`
	AutoUpdate *bool  `json:"autoUpdate,omitempty"`
}

// Path is the config file: RIVOO_CONFIG, else rivoo-agent.json in the working directory.
func Path() string {
	if p := os.Getenv("RIVOO_CONFIG"); p != "" {
		return p
	}
	return "rivoo-agent.json"
}

// Load reads the file (missing is fine) and applies env overrides and defaults.
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	}
	if v := os.Getenv("RIVOO_API_URL"); v != "" {
		c.APIURL = v
	}
	if v := os.Getenv("RIVOO_KEY"); v != "" {
		c.Key = v
	}
	if v := os.Getenv("RIVOO_GAME"); v != "" {
		c.Game = v
	}
	if v := os.Getenv("RCON_HOST"); v != "" {
		c.RconHost = v
	}
	if v := os.Getenv("RCON_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RconPort = n
		}
	}
	if v := os.Getenv("RCON_PASSWORD"); v != "" {
		c.RconPass = v
	}
	if v := os.Getenv("RIVOO_AUTO_UPDATE"); v != "" {
		on := v != "false" && v != "0"
		c.AutoUpdate = &on
	}
	if c.APIURL == "" {
		c.APIURL = DefaultAPI
	}
	if c.RconHost == "" {
		c.RconHost = "127.0.0.1"
	}
	return c, nil
}

// Save writes the file readable only by the owner (it holds secrets).
func Save(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Updates reports whether auto-update is on (default on).
func (c Config) Updates() bool { return c.AutoUpdate == nil || *c.AutoUpdate }
