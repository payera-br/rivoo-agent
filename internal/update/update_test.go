package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.2.0", "0.1.9", true}, {"0.1.10", "0.1.9", true}, {"0.1.0", "0.1.0", false}, {"0.1.0", "dev", false}, {"1.0.0", "0.9.9", true}} {
		if got := newer(tc.a, tc.b); got != tc.want {
			t.Fatalf("newer(%s,%s)=%v", tc.a, tc.b, got)
		}
	}
}

// release serves a manifest for this platform, signed with priv (or not).
func release(t *testing.T, priv ed25519.PrivateKey, binary []byte, sha string, tamper bool) string {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, _ := json.Marshal(Manifest{Version: "0.2.0", Files: map[string]File{runtime.GOOS + "-" + runtime.GOARCH: {URL: srv.URL + "/bin", SHA256: sha}}})
		switch r.URL.Path {
		case "/latest.json":
			w.Write(m)
		case "/latest.json.sig":
			sig := ed25519.Sign(priv, m)
			if tamper {
				sig[0] ^= 1
			}
			w.Write([]byte(base64.StdEncoding.EncodeToString(sig)))
		case "/bin":
			w.Write(binary)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/latest.json"
}

func TestAppliesOnlySignedReleases(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	bin := []byte("novo binario")
	h := sha256.Sum256(bin)
	sha := hex.EncodeToString(h[:])

	exe := filepath.Join(t.TempDir(), "rivoo-agent")
	os.WriteFile(exe, []byte("velho"), 0o755)

	if _, err := Apply(context.Background(), Options{ManifestURL: release(t, priv, bin, sha, true), PublicKey: pub, Current: "0.1.0", Executable: exe}); err == nil {
		t.Fatal("accepted a bad signature")
	}
	if _, err := Apply(context.Background(), Options{ManifestURL: release(t, priv, bin, "00"+sha[2:], false), PublicKey: pub, Current: "0.1.0", Executable: exe}); err == nil {
		t.Fatal("accepted a checksum mismatch")
	}
	if b, _ := os.ReadFile(exe); string(b) != "velho" {
		t.Fatalf("binary changed after a refused update: %q", b)
	}
	updated, err := Apply(context.Background(), Options{ManifestURL: release(t, priv, bin, sha, false), PublicKey: pub, Current: "0.1.0", Executable: exe})
	if err != nil || !updated {
		t.Fatalf("%v %v", updated, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "novo binario" {
		t.Fatalf("binary %q", b)
	}
	if updated, _ := Apply(context.Background(), Options{ManifestURL: release(t, priv, bin, sha, false), PublicKey: pub, Current: "0.2.0", Executable: exe}); updated {
		t.Fatal("updated to the same version")
	}
}

func TestReplaceRestoresTheOldBinaryWhenItFails(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "rivoo-agent.exe")
	os.WriteFile(exe, []byte("velho"), 0o755)
	if err := replace(exe, filepath.Join(dir, "missing.new"), true); err == nil {
		t.Fatal("expected an error")
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "velho" {
		t.Fatalf("binary lost: %q %v", b, err)
	}
}
