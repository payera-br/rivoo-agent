package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/payera-br/rivoo-agent/internal/api"
)

// AckStore keeps the results the API has not confirmed yet, so a restart never runs a command
// twice: what ran is on disk before the loop goes on.
type AckStore interface {
	// Load returns what was saved (nothing when there is no file yet).
	Load() ([]api.AckResult, error)
	// Save replaces the saved list; it must be durable when it returns nil.
	Save([]api.AckResult) error
}

type fileAckStore struct{ path string }

// NewFileAckStore saves to path: written to path+".tmp", fsynced, then renamed over it, so the
// file is always either the old list or the new one.
func NewFileAckStore(path string) AckStore { return &fileAckStore{path: path} }

func (s *fileAckStore) Load() ([]api.AckResult, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []api.AckResult
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *fileAckStore) Save(results []api.AckResult) error {
	if results == nil {
		results = []api.AckResult{}
	}
	b, err := json.Marshal(results)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	// Make the rename itself durable; not every platform can sync a directory, so best effort.
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// memAckStore keeps results in memory only: tests, and a cloud worker with no state directory.
type memAckStore struct {
	mu   sync.Mutex
	list []api.AckResult
}

func (m *memAckStore) Load() ([]api.AckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]api.AckResult(nil), m.list...), nil
}

func (m *memAckStore) Save(r []api.AckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list = append([]api.AckResult(nil), r...)
	return nil
}

// NewMemAckStore is the no-disk store (state lost on restart).
func NewMemAckStore() AckStore { return &memAckStore{} }
