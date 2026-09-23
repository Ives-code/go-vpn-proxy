package app

import (
	"dual-egress-gateway/internal/config"
	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/subscription"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionStatusesSeparateCachedFailureFromUnloadedSource(t *testing.T) {
	r := pool.NewRegistry(appFactory{}, time.Second)
	snapshot := subscription.Snapshot{Nodes: []subscription.NodeSpec{{ID: "a", SourceIDs: []string{"1"}}}, SourceErrors: map[string]string{"1": "private raw error", "2": "failed"}}
	if _, err := r.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	r.MarkSuccess("a", time.Millisecond)
	a := &App{registry: r, config: config.Config{SubscriptionURLs: []string{"https://example.invalid/private?token=secret", "https://other.invalid/secret", "https://pending.invalid/secret"}}}
	rows := a.subscriptionStatuses(snapshot)
	if len(rows) != 3 || rows[0].RefreshState != "failed" || rows[0].Nodes.Healthy != 1 || rows[0].ParsedNodes != 1 || rows[1].Nodes.Total != 0 || rows[2].RefreshState != "pending" {
		t.Fatalf("rows=%v", rows)
	}
	b, _ := json.Marshal(rows)
	for _, secret := range []string{"private", "token=", "secret", "raw error"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("subscription report leaked secret")
		}
	}
}

func TestWebshareFileIsShownAsSeventhSourceWithoutPath(t *testing.T) {
	a := &App{registry: pool.NewRegistry(appFactory{}, time.Second), config: config.Config{SubscriptionURLs: []string{"https://example.invalid/list"}, WebshareFile: "/etc/dual-egress-gateway/webshare-proxies.txt"}}
	urls := a.sourceURLs()
	if len(urls) != 2 || urls[1] != "file:///etc/dual-egress-gateway/webshare-proxies.txt" {
		t.Fatalf("source URLs count=%d", len(urls))
	}
	rows := a.subscriptionStatuses(subscription.Snapshot{})
	if len(rows) != 2 || rows[1].Address != "Webshare 文件" {
		t.Fatalf("status=%v", rows)
	}
}
