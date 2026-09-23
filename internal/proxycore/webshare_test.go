package proxycore

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dual-egress-gateway/internal/subscription"
)

func TestWebshareNodeAuthenticatesToHTTPUpstream(t *testing.T) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Proxy-Authorization")
		if r.Header.Get("Proxy-Authorization") != want {
			http.Error(w, "auth required", http.StatusProxyAuthRequired)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer client.Close()
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	}))
	defer upstream.Close()
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	nodes, err := subscription.Parse([]byte(fmt.Sprintf("%s:%s:test-user:test-pass", host, portText)), "text/plain")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parse count=%d err=%v", len(nodes), err)
	}
	engine, err := NewEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	dialer, err := engine.Build(nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	defer dialer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", "example.invalid:443")
	if err != nil {
		t.Fatalf("upstream dial: %v", err)
	}
	conn.Close()
	select {
	case got := <-seen:
		if got != want {
			t.Fatal("wrong upstream credentials")
		}
	case <-ctx.Done():
		t.Fatal("upstream did not receive auth")
	}
}
