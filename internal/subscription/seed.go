package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// Seed is a manually supplied last-known-good snapshot, bound to a source URL hash.
// Body contains credentials and must be stored in a private file outside the repository.
type Seed struct {
	SourceSHA256 string    `json:"source_sha256"`
	Body         string    `json:"body"`
	Hint         string    `json:"hint"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (manager *Manager) LoadSeed(path string, maxBytes int64) error {
	if maxBytes <= 0 {
		return errors.New("invalid seed size limit")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 {
		return errors.New("subscription seed must be a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return errors.New("cannot read subscription seed")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(b)) > maxBytes {
		return errors.New("subscription seed exceeds size limit or cannot be read")
	}
	var entries []Seed
	if json.Unmarshal(b, &entries) != nil || len(entries) == 0 || len(entries) > len(manager.sources) {
		return errors.New("invalid subscription seed document")
	}
	sources := make(map[string]string)
	for _, s := range manager.sources {
		h := sha256.Sum256([]byte(s.URL))
		sources[hex.EncodeToString(h[:])] = s.ID
	}
	nodes := make(map[string][]NodeSpec)
	expiries := make(map[string]time.Time)
	for _, entry := range entries {
		id, ok := sources[entry.SourceSHA256]
		if !ok {
			return errors.New("subscription seed does not match configured sources")
		}
		if _, exists := nodes[id]; exists {
			return errors.New("duplicate subscription seed")
		}
		parsed, err := Parse([]byte(entry.Body), entry.Hint)
		if err != nil || len(parsed) == 0 {
			return errors.New("invalid nodes in subscription seed")
		}
		for i := range parsed {
			parsed[i].SourceIDs = []string{id}
		}
		nodes[id] = parsed
		expiries[id] = entry.ExpiresAt
	}
	manager.refreshMu.Lock()
	defer manager.refreshMu.Unlock()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, parsed := range nodes {
		manager.bySource[id] = parsed
		manager.expiresAt[id] = expiries[id]
	}
	manager.last = Snapshot{Nodes: mergeSources(manager.bySource), ExpiresAt: manager.expiresAt, SourceErrors: make(map[string]string)}
	return nil
}
