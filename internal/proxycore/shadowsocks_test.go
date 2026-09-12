package proxycore

import (
	"context"
	"dual-egress-gateway/internal/subscription"
	"testing"
)

func TestShadowsocksSubscriptionBuild(t *testing.T) {
	nodes, err := subscription.Parse([]byte(`{"outbounds":[{"type":"shadowsocks","tag":"test","server":"127.0.0.1","server_port":12345,"method":"aes-128-gcm","password":"test-only"}]}`), "application/json")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("count=%d err=%v", len(nodes), err)
	}
	e, err := NewEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	d, err := e.Build(nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}
