package proxycore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dual-egress-gateway/internal/subscription"
)

func TestFactoryRejectsNonProxyOutbound(t *testing.T) {
	engine, err := NewEngine(context.Background())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	_, err = engine.Build(subscription.NodeSpec{
		ID:      "direct-id",
		Type:    "direct",
		Format:  subscription.FormatSingBox,
		Options: json.RawMessage(`{"type":"direct","tag":"direct"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "not a proxy node") {
		t.Fatalf("expected non-proxy error, got %v", err)
	}
}

func TestMinimalRegistryRecognizesSupportedProxyTypes(t *testing.T) {
	registry := minimalOutboundRegistry()
	for _, typeName := range []string{"http", "vless"} {
		if _, ok := registry.CreateOptions(typeName); !ok {
			t.Errorf("outbound type %q is not registered", typeName)
		}
	}
}

func TestEngineBuildsHTTPOutboundDialer(t *testing.T) {
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	defer echoListener.Close()
	go serveOneEcho(echoListener)

	proxyServer := newConnectProxy(t)
	defer proxyServer.Close()
	proxyAddress := strings.TrimPrefix(proxyServer.URL, "http://")
	host, port, err := net.SplitHostPort(proxyAddress)
	if err != nil {
		t.Fatalf("split proxy address: %v", err)
	}

	options := fmt.Sprintf(`{"type":"http","server":%q,"server_port":%s}`, host, port)
	engine, err := NewEngine(context.Background())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()
	dialer, err := engine.Build(subscription.NodeSpec{
		ID:      "http-node",
		Type:    "http",
		Format:  subscription.FormatSingBox,
		Options: json.RawMessage(options),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer dialer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", echoListener.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != "ping\n" {
		t.Fatalf("echo = %q", line)
	}
}

func serveOneEcho(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = io.Copy(conn, conn)
}

func newConnectProxy(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			http.Error(response, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.DialTimeout("tcp", request.Host, 2*time.Second)
		if err != nil {
			http.Error(response, "dial failed", http.StatusBadGateway)
			return
		}
		hijacker, ok := response.(http.Hijacker)
		if !ok {
			upstream.Close()
			http.Error(response, "hijacking unsupported", http.StatusInternalServerError)
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		if buffered.Reader.Buffered() > 0 {
			_, _ = io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered()))
		}
		var wait sync.WaitGroup
		wait.Add(2)
		go func() { defer wait.Done(); _, _ = io.Copy(upstream, client); _ = upstream.(*net.TCPConn).CloseWrite() }()
		go func() { defer wait.Done(); _, _ = io.Copy(client, upstream); _ = client.Close() }()
		wait.Wait()
	}))
}
