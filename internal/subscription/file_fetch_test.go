package subscription

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSourceReadAndPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webshare-private.txt")
	if err := os.WriteFile(path, []byte("192.0.2.1:8080:user:secret\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	fetcher := NewHTTPFetcher(nil, 128)
	content, hint, err := fetcher.Fetch(context.Background(), Source{ID: "7", URL: "file://" + filepath.ToSlash(path)})
	if err != nil || hint != "text/plain" || !strings.Contains(string(content), "192.0.2.1") {
		t.Fatalf("content unavailable: hint=%q err=%v", hint, err)
	}
	link := filepath.Join(t.TempDir(), "linked-file")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{link, filepath.Join(t.TempDir(), "missing"), path + "-wrong"} {
		_, _, err := fetcher.Fetch(context.Background(), Source{ID: "7", URL: "file://" + filepath.ToSlash(invalid)})
		if err == nil || strings.Contains(err.Error(), invalid) {
			t.Fatalf("bad file accepted or leaked its path: %v", err)
		}
	}
	_, _, err = NewHTTPFetcher(nil, 4).Fetch(context.Background(), Source{ID: "7", URL: "file://" + filepath.ToSlash(path)})
	if err == nil {
		t.Fatal("oversize file accepted")
	}
}

func TestWebshareFileRefreshKeepsLastGoodNodesOnBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webshare-private.txt")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("192.0.2.1:8080:user:secret\n192.0.2.2:8081:user:secret\n")
	m := NewManager([]Source{{ID: "7", URL: "file://" + filepath.ToSlash(path)}}, NewHTTPFetcher(nil, 4096))
	first, err := m.Refresh(context.Background())
	if err != nil || len(first.Nodes) != 2 {
		t.Fatalf("initial file nodes=%d err=%v", len(first.Nodes), err)
	}
	write("192.0.2.1:8080:user:secret\n192.0.2.2:0:user:secret\n")
	bad, err := m.Refresh(context.Background())
	if err == nil || len(bad.Nodes) != 2 || bad.SourceErrors["7"] == "" {
		t.Fatal("invalid file removed last-known-good nodes")
	}
	write("192.0.2.3:8082:user:secret\n")
	updated, err := m.Refresh(context.Background())
	if err != nil || len(updated.Nodes) != 1 || len(updated.SourceErrors) != 0 {
		t.Fatal("corrected file did not replace old nodes")
	}
}
