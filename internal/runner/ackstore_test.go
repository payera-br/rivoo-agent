package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/payera-br/rivoo-agent/internal/api"
)

func TestFileAckStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acks.json")
	s := NewFileAckStore(path)
	if got, err := s.Load(); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
	in := []api.AckResult{{ID: "a", Status: "ok", Output: "x"}, {ID: "b", Status: "failed", Error: "e"}}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := NewFileAckStore(path).Load()
	if err != nil || len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file left behind: %v", err)
	}
	if err := s.Save(nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); len(got) != 0 {
		t.Fatalf("after clear: %+v", got)
	}
}

func TestFileAckStoreCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acks.json")
	os.WriteFile(path, []byte("{nope"), 0o600)
	if _, err := NewFileAckStore(path).Load(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFileAckStoreSaveFailsWhenDirIsMissing(t *testing.T) {
	s := NewFileAckStore(filepath.Join(t.TempDir(), "no", "such", "acks.json"))
	if err := s.Save([]api.AckResult{{ID: "a"}}); err == nil {
		t.Fatal("expected an error")
	}
}
