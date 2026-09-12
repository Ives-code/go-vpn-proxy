package integration

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"dual-egress-gateway/internal/httpproxy"
	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/subscription"
)

type factory struct {
	mu       sync.Mutex
	sequence map[string][]string
	failing  map[string]bool
}

type dialer struct {
	id      string
	factory *factory
}

func (dialer *dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer.factory.mu.Lock()
	dialer.factory.sequence[address] = append(dialer.factory.sequence[address], dialer.id)
	dialer.factory.mu.Unlock()
	if dialer.factory.failing[dialer.id] {
		return nil, fmt.Errorf("simulated failure")
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestFailureOnHTTPListenerIsSharedWithWSListener(t *testing.T) {
	factory := &factory{sequence: make(map[string][]string), failing: map[string]bool{"a": true}}
	registry := pool.NewRegistry(factory, time.Minute)
	if _, err := registry.Apply(subscription.Snapshot{Nodes: []subscription.NodeSpec{{ID: "a"}, {ID: "b"}}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		registry.MarkSuccess(id, time.Millisecond)
	}
	httpTarget := startEcho(t)
	wsTarget := startEcho(t)
	httpAddress, stopHTTP := startGateway(t, "http", registry, pool.NewSelector())
	defer stopHTTP()
	wsAddress, stopWS := startGateway(t, "ws", registry, pool.NewSelector())
	defer stopWS()

	connectAndEcho(t, httpAddress, httpTarget)
	connectAndEcho(t, wsAddress, wsTarget)

	factory.mu.Lock()
	defer factory.mu.Unlock()
	if got := factory.sequence[httpTarget]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("HTTP failover sequence = %#v", got)
	}
	if got := factory.sequence[wsTarget]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("WS did not share HTTP failure state: %#v", got)
	}
}

func (*dialer) Close() error { return nil }

func (factory *factory) Build(spec subscription.NodeSpec) (pool.Dialer, error) {
	return &dialer{id: spec.ID, factory: factory}, nil
}

func TestDualListenersRotateIndependently(t *testing.T) {
	factory := &factory{sequence: make(map[string][]string)}
	registry := pool.NewRegistry(factory, time.Minute)
	if _, err := registry.Apply(subscription.Snapshot{Nodes: []subscription.NodeSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		registry.MarkSuccess(id, time.Millisecond)
	}

	httpTarget := startEcho(t)
	wsTarget := startEcho(t)
	httpAddress, stopHTTP := startGateway(t, "http", registry, pool.NewSelector())
	defer stopHTTP()
	wsAddress, stopWS := startGateway(t, "ws", registry, pool.NewSelector())
	defer stopWS()

	connectAndEcho(t, httpAddress, httpTarget)
	connectAndEcho(t, httpAddress, httpTarget)
	connectAndEcho(t, wsAddress, wsTarget)
	connectAndEcho(t, wsAddress, wsTarget)

	factory.mu.Lock()
	defer factory.mu.Unlock()
	if got := factory.sequence[httpTarget]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("HTTP sequence = %#v", got)
	}
	if got := factory.sequence[wsTarget]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("WS sequence = %#v", got)
	}
}

func startGateway(t *testing.T, name string, registry *pool.Registry, selector *pool.Selector) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen gateway: %v", err)
	}
	server := httpproxy.New(name, httpproxy.Config{Username: "user", Password: "password", DialTimeout: time.Second}, registry, selector, nil)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
}

func startEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	return listener.Addr().String()
}

func connectAndEcho(t *testing.T, proxyAddress, targetAddress string) {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddress)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", targetAddress, targetAddress, auth)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT response=%v err=%v", response, err)
	}
	_, _ = conn.Write([]byte("ping\n"))
	line, err := reader.ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("echo=%q err=%v", line, err)
	}
}
