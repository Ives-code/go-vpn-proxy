package subscription

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type scriptedFetcher struct {
	mu        sync.Mutex
	responses map[string][]fetchResult
}

type fetchResult struct {
	body []byte
	err  error
}

func (fetcher *scriptedFetcher) Fetch(_ context.Context, source Source) ([]byte, string, error) {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	queue := fetcher.responses[source.ID]
	if len(queue) == 0 {
		return nil, "", fmt.Errorf("no scripted response for %s", source.ID)
	}
	result := queue[0]
	fetcher.responses[source.ID] = queue[1:]
	return result.body, "", result.err
}

func TestRefreshRetainsFailedSourceSnapshot(t *testing.T) {
	fetcher := &scriptedFetcher{responses: map[string][]fetchResult{
		"1": {
			{body: []byte("vless://one@one.invalid:443\n")},
			{body: []byte("vless://one-new@one.invalid:443\n")},
		},
		"2": {
			{body: []byte("trojan://two@two.invalid:443\n")},
			{err: errors.New("HTTP 503")},
		},
	}}
	manager := NewManager([]Source{{ID: "1", URL: "https://one.invalid"}, {ID: "2", URL: "https://two.invalid"}}, fetcher)

	if _, err := manager.Refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	snapshot, err := manager.Refresh(context.Background())
	if err == nil {
		t.Fatal("second refresh should report a source error")
	}
	if len(snapshot.Nodes) != 2 {
		t.Fatalf("node count after partial failure = %d", len(snapshot.Nodes))
	}
	if snapshot.SourceErrors["2"] == "" {
		t.Fatal("source 2 error was not recorded")
	}
}

func TestRefreshRejectsSuccessfulEmptyPool(t *testing.T) {
	fetcher := &scriptedFetcher{responses: map[string][]fetchResult{
		"1": {{body: []byte("vless://one@one.invalid:443\n")}, {body: []byte("\n")}},
	}}
	manager := NewManager([]Source{{ID: "1", URL: "https://one.invalid"}}, fetcher)
	first, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	second, err := manager.Refresh(context.Background())
	if err == nil {
		t.Fatal("empty refresh should fail")
	}
	if len(second.Nodes) != len(first.Nodes) {
		t.Fatalf("empty refresh replaced last-known-good nodes: %d -> %d", len(first.Nodes), len(second.Nodes))
	}
}

func TestRefreshDeduplicatesNodesAcrossSources(t *testing.T) {
	fetcher := &scriptedFetcher{responses: map[string][]fetchResult{
		"1": {{body: []byte("vless://same@edge.invalid:443\n")}},
		"2": {{body: []byte("vless://same@edge.invalid:443#different-display-name\n")}},
	}}
	manager := NewManager([]Source{{ID: "1", URL: "https://one.invalid"}, {ID: "2", URL: "https://two.invalid"}}, fetcher)

	snapshot, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if len(snapshot.Nodes) != 1 {
		t.Fatalf("node count = %d", len(snapshot.Nodes))
	}
	if got := snapshot.Nodes[0].SourceIDs; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("SourceIDs = %#v", got)
	}
}
