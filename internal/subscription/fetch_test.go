package subscription

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExpiryMetadata(t *testing.T) {
	for _, value := range []string{"", "expire=0", "expire=bad", "expire=-1"} {
		if !ParseExpiry(value).IsZero() {
			t.Fatalf("invalid expiry accepted: %q", value)
		}
	}
	want := time.Unix(1900000000, 0).UTC()
	if got := ParseExpiry("upload=1; download=2; total=3; expire=1900000000"); !got.Equal(want) {
		t.Fatalf("expiry=%v", got)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "expire=1900000000")
		w.Write([]byte(`{"outbounds":[]}`))
	}))
	defer server.Close()
	f := NewHTTPFetcher(server.Client(), 1024)
	_, _, expiry, err := f.FetchWithMetadata(context.Background(), Source{ID: "1", URL: server.URL})
	if err != nil || !expiry.Equal(want) {
		t.Fatalf("expiry=%v err=%v", expiry, err)
	}
}

func TestManagerRetainsExpiryOnFetchFailureAndCopiesSnapshot(t *testing.T) {
	fail := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Subscription-Userinfo", "expire=1900000000")
		w.Write([]byte(`{"outbounds":[]}`))
	}))
	defer server.Close()
	m := NewManager([]Source{{ID: "1", URL: server.URL}}, NewHTTPFetcher(server.Client(), 1024))
	s, _ := m.Refresh(context.Background())
	if s.ExpiresAt["1"].Unix() != 1900000000 {
		t.Fatal("expiry lost with invalid node body")
	}
	s.ExpiresAt["1"] = time.Time{}
	fail = true
	s, _ = m.Refresh(context.Background())
	if s.ExpiresAt["1"].Unix() != 1900000000 {
		t.Fatal("expiry lost or snapshot aliased")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network unavailable")
}

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

func TestHTTPFetcherErrorDoesNotContainSubscriptionURL(t *testing.T) {
	client := &http.Client{Transport: failingTransport{}}
	fetcher := NewHTTPFetcher(client, 1024)
	secretURL := "https://subscription.invalid/list?token=do-not-leak"

	_, _, err := fetcher.Fetch(context.Background(), Source{ID: "7", URL: secretURL})
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if strings.Contains(err.Error(), "subscription.invalid") || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("fetch error leaked subscription URL: %q", err)
	}
}

func TestHTTPFetcherDoesNotLeakSubscriptionURLInRedirectReferer(t *testing.T) {
	referer := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(response, request, "/final", http.StatusFound)
			return
		}
		referer <- request.Header.Get("Referer")
		_, _ = response.Write([]byte(`{"outbounds":[]}`))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(server.Client(), 1024)
	_, _, err := fetcher.Fetch(context.Background(), Source{ID: "1", URL: server.URL + "/start?token=do-not-leak"})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if got := <-referer; got != "" {
		t.Fatalf("redirect leaked Referer %q", got)
	}
}

func TestHTTPFetcherRejectsCrossOriginRedirect(t *testing.T) {
	destination := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"outbounds":[]}`))
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // test servers use unrelated ephemeral roots
	fetcher := NewHTTPFetcher(client, 1024)
	_, _, err := fetcher.Fetch(context.Background(), Source{ID: "1", URL: source.URL + "/?token=do-not-leak"})
	if err == nil || !strings.Contains(err.Error(), "fetch failed") {
		t.Fatalf("expected cross-origin redirect error, got %v", err)
	}
}
