package proxycore

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type directDialer struct{}

func (directDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func (directDialer) Close() error { return nil }

func TestProbeRequiresExpectedStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	prober, err := NewProber(server.URL, http.StatusNoContent, &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs})
	if err != nil {
		t.Fatalf("NewProber: %v", err)
	}
	_, err = prober.Probe(context.Background(), directDialer{})
	if err == nil || !strings.Contains(err.Error(), "unexpected HTTP status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestProbeHonorsCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer conn.Close()
			time.Sleep(5 * time.Second)
		}
	}()

	prober, err := NewProber("https://"+listener.Addr().String()+"/", http.StatusNoContent, &tls.Config{})
	if err != nil {
		t.Fatalf("NewProber: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = prober.Probe(ctx, directDialer{})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("probe ignored cancellation for %s", elapsed)
	}
}
