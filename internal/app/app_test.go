package app

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/subscription"
)

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

func TestProbeDueNodesMakesRegistryReady(t *testing.T) {
	registry := pool.NewRegistry(appFactory{}, time.Second)
	if _, err := registry.Apply(subscription.Snapshot{Nodes: []subscription.NodeSpec{{ID: "a", Type: "test"}}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if registry.Stats().Healthy != 0 {
		t.Fatal("new node became healthy before probe")
	}

	probeDue(context.Background(), registry, successfulProber{}, 4)
	stats := registry.Stats()
	if stats.Healthy != 1 || stats.Quarantined != 0 {
		t.Fatalf("stats after probe = %#v", stats)
	}
}
