package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvOverridesFileAndDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	if err := Save(p, Config{Game: "RUST", RconPort: 28016, RconPass: "file"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	t.Setenv("RCON_PASSWORD", "env")
	t.Setenv("RIVOO_AUTO_UPDATE", "false")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.RconPass != "env" || c.Game != "RUST" || c.RconHost != "127.0.0.1" || c.APIURL != DefaultAPI || c.Updates() {
		t.Fatalf("%+v", c)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || c.Key != "" || !c.Updates() {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestDefaultPathIsNextToTheBinary(t *testing.T) {
	t.Setenv("RIVOO_CONFIG", "")
	exe, _ := os.Executable()
	if got, want := Path(), filepath.Join(filepath.Dir(exe), "rivoo-agent.json"); got != want {
		t.Fatalf("%s != %s", got, want)
	}
}
