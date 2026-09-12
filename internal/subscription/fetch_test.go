package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetcherRejectsNonHTTPSSource(t *testing.T) {
	fetcher := NewHTTPFetcher(http.DefaultClient, 1024)
	_, _, err := fetcher.Fetch(context.Background(), Source{ID: "1", URL: "http://subscription.invalid/list"})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS error, got %v", err)
	}
}

func TestHTTPFetcherLimitsResponseBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.UserAgent() != "sing-box" {
			t.Errorf("User-Agent = %q", request.UserAgent())
		}
		_, _ = response.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(server.Client(), 64)
	_, _, err := fetcher.Fetch(context.Background(), Source{ID: "1", URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestHTTPFetcherReturnsFormatHint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"outbounds":[]}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(server.Client(), 1024)
	body, hint, err := fetcher.Fetch(context.Background(), Source{ID: "1", URL: server.URL})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if string(body) != `{"outbounds":[]}` || hint != "application/json" {
		t.Fatalf("body=%q hint=%q", body, hint)
	}
}
