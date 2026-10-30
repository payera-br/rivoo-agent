// Package update installs new agent releases, only when the manifest is signed by Rivoo's key and
// the binary matches the manifest's checksum.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Manifest lists the latest release per platform ("linux-amd64"…). Its signature is
// <manifest URL>.sig: base64 ed25519 over the manifest bytes.
type Manifest struct {
	Version string          `json:"version"`
	Files   map[string]File `json:"files"`
}

type File struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type Options struct {
	ManifestURL string
	PublicKey   ed25519.PublicKey
	Current     string
	// Executable to replace (default: this process's binary).
	Executable string
}

var client = &http.Client{Timeout: 2 * time.Minute}

func get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, max))
}

// Apply installs a newer signed release; true when the binary was replaced (restart to use it).
func Apply(ctx context.Context, o Options) (bool, error) {
	raw, err := get(ctx, o.ManifestURL, 1<<20)
	if err != nil {
		return false, err
	}
	sigB64, err := get(ctx, o.ManifestURL+".sig", 4096)
	if err != nil {
		return false, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || !ed25519.Verify(o.PublicKey, raw, sig) {
		return false, errors.New("assinatura do manifesto inválida")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, err
	}
	if !newer(m.Version, o.Current) {
		return false, nil
	}
	f, ok := m.Files[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		return false, nil
	}
	bin, err := get(ctx, f.URL, 200<<20)
	if err != nil {
		return false, err
	}
	h := sha256.Sum256(bin)
	if hex.EncodeToString(h[:]) != strings.ToLower(f.SHA256) {
		return false, errors.New("checksum do binário não confere com o manifesto")
	}
	exe := o.Executable
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return false, err
		}
	}
	tmp := filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".new")
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return false, err
	}
	// Windows can't overwrite a running binary, but can rename it away first.
	old := exe + ".old"
	_ = os.Remove(old)
	if runtime.GOOS == "windows" {
		if err := os.Rename(exe, old); err != nil {
			os.Remove(tmp)
			return false, err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// newer compares dotted numeric versions; "dev" and anything unparsable never update.
func newer(a, b string) bool {
	pa, okA := parse(a)
	pb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		p, _, _ = strings.Cut(p, "-")
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
