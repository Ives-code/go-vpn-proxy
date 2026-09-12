package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/subscription"
)

type recordingFactory struct {
	mu      sync.Mutex
	dialers map[string]*recordingDialer
}

type recordingDialer struct {
	id       string
	fail     bool
	calls    *atomic.Int32
	sequence *[]string
	mu       *sync.Mutex
}

func (dialer *recordingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer.calls.Add(1)
	dialer.mu.Lock()
	*dialer.sequence = append(*dialer.sequence, dialer.id)
	dialer.mu.Unlock()
	if dialer.fail {
		return nil, errors.New("secret-endpoint.invalid:443 failed")
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func (*recordingDialer) Close() error { return nil }

func (factory *recordingFactory) Build(spec subscription.NodeSpec) (pool.Dialer, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return factory.dialers[spec.ID], nil
}

type proxyFixture struct {
	server   *Server
	listener net.Listener
	registry *pool.Registry
	calls    *atomic.Int32
	sequence *[]string
}

func newProxyFixture(t *testing.T, failing map[string]bool) *proxyFixture {
	return newProxyFixtureWithLogger(t, failing, nil)
}

func newProxyFixtureWithLogger(t *testing.T, failing map[string]bool, logger *slog.Logger) *proxyFixture {
	t.Helper()
	calls := &atomic.Int32{}
	sequence := []string{}
	sequenceMu := &sync.Mutex{}
	factory := &recordingFactory{dialers: make(map[string]*recordingDialer)}
	var specs []subscription.NodeSpec
	for _, id := range []string{"a", "b", "c"} {
		factory.dialers[id] = &recordingDialer{id: id, fail: failing[id], calls: calls, sequence: &sequence, mu: sequenceMu}
		specs = append(specs, subscription.NodeSpec{ID: id, Type: "test"})
	}
	registry := pool.NewRegistry(factory, 30*time.Second)
	if _, err := registry.Apply(subscription.Snapshot{Nodes: specs}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		registry.MarkSuccess(id, time.Millisecond)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := New("test", Config{Username: "user", Password: "password", DialTimeout: time.Second}, registry, pool.NewSelector(), logger)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return &proxyFixture{server: server, listener: listener, registry: registry, calls: calls, sequence: &sequence}
}

func TestFailureLogsUseAnonymousIDsWithoutRawErrors(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	fixture := newProxyFixtureWithLogger(t, map[string]bool{"a": true, "b": true, "c": true}, logger)
	conn, err := net.Dial("tcp", fixture.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	_, _ = fmt.Fprintf(conn, "CONNECT target.invalid:443 HTTP/1.1\r\nHost: target.invalid:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	_, _ = http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})

	logs := output.String()
	if !strings.Contains(logs, `"node_id":"a"`) || !strings.Contains(logs, `"listener":"test"`) || !strings.Contains(logs, `"failure_class":"dial"`) {
		t.Fatalf("missing anonymous diagnostics: %s", logs)
	}
	for _, forbidden := range []string{"secret-endpoint.invalid", "target.invalid"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("logs leaked %q: %s", forbidden, logs)
		}
	}
}

func TestProxyRequiresAuthenticationBeforeDial(t *testing.T) {
	fixture := newProxyFixture(t, nil)
	proxyURL, _ := url.Parse("http://" + fixture.listener.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get("http://example.invalid/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := fixture.calls.Load(); got != 0 {
		t.Fatalf("dial calls before auth = %d", got)
	}
}

func TestBasicAuthenticationSchemeIsCaseInsensitive(t *testing.T) {
	fixture := newProxyFixture(t, nil)
	request := httptest.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	request.Header.Set("Proxy-Authorization", "bAsIc "+base64.StdEncoding.EncodeToString([]byte("user:password")))
	if !fixture.server.authorized(request) {
		t.Fatal("case-insensitive Basic scheme was rejected")
	}
}

func TestConnectRetriesUntilNodeSucceeds(t *testing.T) {
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	defer echoListener.Close()
	go func() {
		conn, acceptErr := echoListener.Accept()
		if acceptErr == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	fixture := newProxyFixture(t, map[string]bool{"a": true})

	conn := authenticatedConnect(t, fixture.listener.Addr().String(), echoListener.Addr().String())
	defer conn.Close()
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write tunnel: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "hello\n" {
		t.Fatalf("tunnel echo=%q err=%v", line, err)
	}
	if got := append([]string(nil), (*fixture.sequence)...); len(got) < 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("dial sequence = %#v", got)
	}
}

func TestAllNodesFailReturnsRedacted502(t *testing.T) {
	fixture := newProxyFixture(t, map[string]bool{"a": true, "b": true, "c": true})
	conn, err := net.Dial("tcp", fixture.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	_, _ = fmt.Fprintf(conn, "CONNECT target.invalid:443 HTTP/1.1\r\nHost: target.invalid:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", response.StatusCode)
	}
	for _, forbidden := range []string{"secret-endpoint", "target.invalid", "failed"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("502 body leaked %q: %q", forbidden, body)
		}
	}
}

func TestKeepAlivePinsNode(t *testing.T) {
	origin := http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Connection", "close")
		_, _ = response.Write([]byte(request.URL.Path))
	})}
	originListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	defer origin.Close()
	go func() { _ = origin.Serve(originListener) }()

	fixture := newProxyFixture(t, nil)
	conn, err := net.Dial("tcp", fixture.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	for _, path := range []string{"/first", "/second"} {
		_, _ = fmt.Fprintf(conn, "GET http://%s%s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", originListener.Addr(), path, originListener.Addr(), auth)
		response, readErr := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
		if readErr != nil {
			t.Fatalf("read %s response: %v", path, readErr)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != path {
			t.Fatalf("body = %q, want %q", body, path)
		}
	}
	if got := append([]string(nil), (*fixture.sequence)...); len(got) != 2 || got[0] != "a" || got[1] != "a" {
		t.Fatalf("keep-alive dial sequence = %#v", got)
	}
}

func TestHTTPUpgradeStaysOnSelectedNode(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			http.Error(response, "upgrade required", http.StatusBadRequest)
			return
		}
		client, buffered, err := response.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = client.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"))
		if buffered.Reader.Buffered() > 0 {
			_, _ = io.CopyN(client, buffered, int64(buffered.Reader.Buffered()))
		}
		_, _ = io.Copy(client, client)
	}))
	defer origin.Close()

	fixture := newProxyFixture(t, nil)
	conn, err := net.Dial("tcp", fixture.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	_, _ = fmt.Fprintf(conn, "GET %s/ HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nProxy-Authorization: Basic %s\r\n\r\n", origin.URL, strings.TrimPrefix(origin.URL, "http://"), auth)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
	if _, err := conn.Write([]byte("ws-payload\n")); err != nil {
		t.Fatalf("write upgraded tunnel: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "ws-payload\n" {
		t.Fatalf("upgrade echo=%q err=%v", line, err)
	}
	if got := append([]string(nil), (*fixture.sequence)...); len(got) != 1 || got[0] != "a" {
		t.Fatalf("upgrade dial sequence = %#v", got)
	}
}

func TestConnectPreservesResponseAfterClientHalfClose(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("target listen: %v", err)
	}
	defer target.Close()
	go func() {
		conn, acceptErr := target.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadAll(conn)
		_, _ = conn.Write([]byte("response-after-eof\n"))
	}()

	fixture := newProxyFixture(t, nil)
	conn := authenticatedConnect(t, fixture.listener.Addr().String(), target.Addr().String())
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		t.Fatalf("client connection type = %T", conn)
	}
	defer tcpConn.Close()
	if _, err := tcpConn.Write([]byte("request-body")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := tcpConn.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	line, err := bufio.NewReader(tcpConn).ReadString('\n')
	if err != nil || line != "response-after-eof\n" {
		t.Fatalf("half-close response=%q err=%v", line, err)
	}
}

func TestShutdownClosesHijackedTunnelAfterDrainDeadline(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("target listen: %v", err)
	}
	defer target.Close()
	go func() {
		conn, acceptErr := target.Accept()
		if acceptErr == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()

	fixture := newProxyFixture(t, nil)
	conn := authenticatedConnect(t, fixture.listener.Addr().String(), target.Addr().String())
	defer conn.Close()
	deadlineContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_ = fixture.server.Shutdown(deadlineContext)
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond || elapsed > time.Second {
		t.Fatalf("Shutdown duration = %s", elapsed)
	}
	for deadline := time.Now().Add(time.Second); fixture.server.ActiveConnections() != 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if active := fixture.server.ActiveConnections(); active != 0 {
		t.Fatalf("active connections after forced drain = %d", active)
	}
}

func TestHTTPUpgradePreservesResponseAfterClientHalfClose(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		client, _, err := response.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = client.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"))
		_, _ = io.ReadAll(client)
		_, _ = client.Write([]byte("upgrade-response-after-eof\n"))
	}))
	defer origin.Close()
	fixture := newProxyFixture(t, nil)
	conn, err := net.Dial("tcp", fixture.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	tcpConn := conn.(*net.TCPConn)
	defer tcpConn.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	_, _ = fmt.Fprintf(tcpConn, "GET %s/ HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nProxy-Authorization: Basic %s\r\n\r\n", origin.URL, strings.TrimPrefix(origin.URL, "http://"), auth)
	reader := bufio.NewReader(tcpConn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade response=%v err=%v", response, err)
	}
	_, _ = tcpConn.Write([]byte("upgrade-request"))
	if err := tcpConn.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	_ = tcpConn.SetReadDeadline(time.Now().Add(time.Second))
	line, err := reader.ReadString('\n')
	if err != nil || line != "upgrade-response-after-eof\n" {
		t.Fatalf("upgrade half-close response=%q err=%v", line, err)
	}
}

func authenticatedConnect(t *testing.T, proxyAddress, targetAddress string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddress)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	auth := base64.StdEncoding.EncodeToString([]byte("user:password"))
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", targetAddress, targetAddress, auth)
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		t.Fatalf("read CONNECT response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		conn.Close()
		t.Fatalf("CONNECT status = %d", response.StatusCode)
	}
	return conn
}
