package app

import (
	"context"
	"dual-egress-gateway/internal/alert"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/subscription"
)

type alertSender struct{ sent chan string }

func (s alertSender) Send(ctx context.Context, title, body string) error {
	select {
	case s.sent <- title:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func TestAlertLoopWaitsForInitialRefreshAndDeliversLowCount(t *testing.T) {
	s := alertSender{sent: make(chan string, 4)}
	a := &App{registry: pool.NewRegistry(appFactory{}, time.Second), subscriptions: subscription.NewManager(nil, nil), alerts: alert.NewMonitor(s, 30, nil), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go a.alertLoop(ctx, done)
	defer func() { cancel(); <-done }()
	select {
	case <-s.sent:
		t.Fatal("alert before initial refresh")
	case <-time.After(5200 * time.Millisecond):
	}
	a.statusMu.Lock()
	a.lastRefresh = time.Now()
	a.statusMu.Unlock()
	select {
	case title := <-s.sent:
		if title != "代理可用节点不足" {
			t.Fatalf("title=%s", title)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("no alert after refresh")
	}
}

type trackingListener struct {
	closed atomic.Bool
}

func (*trackingListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (listener *trackingListener) Close() error {
	listener.closed.Store(true)
	return nil
}
func (*trackingListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestListenAllIsAtomic(t *testing.T) {
	first := &trackingListener{}
	calls := 0
	listen := func(_, _ string) (net.Listener, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return nil, errors.New("address in use")
	}

	_, err := listenAll([]string{"127.0.0.1:18080", "127.0.0.1:18081"}, listen)
	if err == nil {
		t.Fatal("expected bind failure")
	}
	if !first.closed.Load() {
		t.Fatal("first listener was not closed after second bind failed")
	}
}

type appDialer struct{}

func (appDialer) DialContext(context.Context, string, string) (net.Conn, error) { return nil, nil }
func (appDialer) Close() error                                                  { return nil }

type appFactory struct{}

func (appFactory) Build(subscription.NodeSpec) (pool.Dialer, error) { return appDialer{}, nil }

type successfulProber struct{}

func (successfulProber) Probe(context.Context, pool.Dialer) (time.Duration, error) {
	return 12 * time.Millisecond, nil
}

type blockingProber struct{}

func (blockingProber) Probe(ctx context.Context, _ pool.Dialer) (time.Duration, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestProbeDueNodesMakesRegistryReady(t *testing.T) {
	registry := pool.NewRegistry(appFactory{}, time.Second)
	if _, err := registry.Apply(subscription.Snapshot{Nodes: []subscription.NodeSpec{{ID: "a", Type: "test"}}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if registry.Stats().Healthy != 0 {
		t.Fatal("new node became healthy before probe")
	}

	probeDue(context.Background(), registry, successfulProber{}, 4, time.Second)
	stats := registry.Stats()
	if stats.Healthy != 1 || stats.Quarantined != 0 {
		t.Fatalf("stats after probe = %#v", stats)
	}
}

func TestProbeDueHonorsPerNodeTimeout(t *testing.T) {
	registry := pool.NewRegistry(appFactory{}, time.Second)
	if _, err := registry.Apply(subscription.Snapshot{Nodes: []subscription.NodeSpec{{ID: "a", Type: "test"}}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	started := time.Now()
	probeDue(context.Background(), registry, blockingProber{}, 1, 25*time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("probe exceeded timeout: %s", elapsed)
	}
	if stats := registry.Stats(); stats.Unhealthy != 1 {
		t.Fatalf("timed-out node stats = %#v", stats)
	}
}
