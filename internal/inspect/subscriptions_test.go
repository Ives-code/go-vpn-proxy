package inspect

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubscriptionsReportsCacheExpiryAndCounts(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/status" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"nodes":{"total":8,"healthy":3},"subscriptions":[{"id":"1","address":"https://example.invalid/[hidden]","expires_at":"2020-01-01T00:00:00Z","refresh_state":"failed","parsed_nodes":8,"nodes":{"total":8,"healthy":3,"unhealthy":5}}]}`))
	}))
	defer s.Close()
	var out bytes.Buffer
	if err := Subscriptions(context.Background(), s.URL, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.invalid", "缓存", "已过期", "8", "3", "5"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestSubscriptionsRejectsOldServerAndRemoteEndpoint(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ready":true}`)) }))
	defer s.Close()
	for _, endpoint := range []string{s.URL, "http://example.com:19090"} {
		if err := Subscriptions(context.Background(), endpoint, &bytes.Buffer{}); err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestSubscriptionsRejectsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	if err := Subscriptions(context.Background(), s.URL, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
}
