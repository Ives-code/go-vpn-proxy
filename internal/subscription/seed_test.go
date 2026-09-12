package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSeedSurvivesFailedRefreshAndIsReplacedOnRecovery(t *testing.T) {
	source := Source{ID: "4", URL: "https://example.invalid/private"}
	hash := sha256.Sum256([]byte(source.URL))
	expiry := time.Unix(1900000000, 0).UTC()
	entries := []Seed{{SourceSHA256: hex.EncodeToString(hash[:]), Body: "http://user:password@cached.invalid:8080", ExpiresAt: expiry}}
	b, _ := json.Marshal(entries)
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	f := &scriptedFetcher{responses: map[string][]fetchResult{"4": {{}, {body: []byte("http://user:password@fresh.invalid:8080")}}}}
	m := NewManager([]Source{source}, f)
	if err := m.LoadSeed(path, 4096); err != nil {
		t.Fatal(err)
	}
	s, err := m.Refresh(context.Background())
	if err == nil || len(s.Nodes) != 1 || s.Nodes[0].SourceIDs[0] != "4" {
		t.Fatalf("snapshot=%v err=%v", s, err)
	}
	old := s.Nodes[0].ID
	s, err = m.Refresh(context.Background())
	if err != nil || len(s.Nodes) != 1 || s.Nodes[0].ID == old {
		t.Fatal("fresh subscription did not replace seed")
	}
}

func TestSeedRejectsMismatchInvalidAndOversizeWithoutPartialLoad(t *testing.T) {
	source := Source{ID: "4", URL: "https://example.invalid/private"}
	h := sha256.Sum256([]byte(source.URL))
	for _, entries := range [][]Seed{
		{{SourceSHA256: "wrong", Body: "http://example.invalid:8080"}},
		{{SourceSHA256: hex.EncodeToString(h[:]), Body: "not a subscription"}},
	} {
		b, _ := json.Marshal(entries)
		p := filepath.Join(t.TempDir(), "seed.json")
		os.WriteFile(p, b, 0600)
		m := NewManager([]Source{source}, nil)
		if err := m.LoadSeed(p, 4096); err == nil {
			t.Fatal("invalid seed accepted")
		}
		if len(m.Snapshot().Nodes) != 0 {
			t.Fatal("partial load")
		}
	}
	p := filepath.Join(t.TempDir(), "seed.json")
	os.WriteFile(p, []byte("[]"), 0600)
	if err := NewManager([]Source{source}, nil).LoadSeed(p, 1); err == nil {
		t.Fatal("size limit ignored")
	}
}
