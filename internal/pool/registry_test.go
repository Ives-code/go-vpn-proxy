package pool

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"dual-egress-gateway/internal/subscription"
)

type fakeDialer struct {
	closed atomic.Int32
}

func (*fakeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, nil
}

func (dialer *fakeDialer) Close() error {
	dialer.closed.Add(1)
	return nil
}

type fakeFactory struct {
	dialers map[string]*fakeDialer
}

func (factory *fakeFactory) Build(spec subscription.NodeSpec) (Dialer, error) {
	dialer := &fakeDialer{}
	factory.dialers[spec.ID] = dialer
	return dialer, nil
}

func snapshot(ids ...string) subscription.Snapshot {
	nodes := make([]subscription.NodeSpec, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, subscription.NodeSpec{ID: id, Type: "test"})
	}
	return subscription.Snapshot{Nodes: nodes}
}

func healthyRegistry(t *testing.T, ids ...string) (*Registry, *fakeFactory) {
	t.Helper()
	factory := &fakeFactory{dialers: make(map[string]*fakeDialer)}
	registry := NewRegistry(factory, 30*time.Second)
	if _, err := registry.Apply(snapshot(ids...)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, id := range ids {
		registry.MarkSuccess(id, time.Millisecond)
	}
	return registry, factory
}

func TestFailureIsSharedAcrossSelectors(t *testing.T) {
	registry, _ := healthyRegistry(t, "a", "b")
	registry.MarkFailure("a", context.DeadlineExceeded)

	candidates := registry.RoutingSnapshot()
	if candidates[0].ID != "a" || candidates[0].Eligible {
		t.Fatalf("failed node remains eligible: %#v", candidates[0])
	}
	if !candidates[1].Eligible {
		t.Fatalf("healthy node became ineligible: %#v", candidates[1])
	}
}

func TestRemovedNodeDrainsUntilLeaseClose(t *testing.T) {
	registry, factory := healthyRegistry(t, "a", "b")
	lease, ok := registry.Acquire("a", false)
	if !ok {
		t.Fatal("failed to acquire healthy node")
	}
	if _, err := registry.Apply(snapshot("b")); err != nil {
		t.Fatalf("Apply removal: %v", err)
	}
	if got := factory.dialers["a"].closed.Load(); got != 0 {
		t.Fatalf("active runtime closed early: %d", got)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("lease close: %v", err)
	}
	if got := factory.dialers["a"].closed.Load(); got != 1 {
		t.Fatalf("drained runtime close count = %d", got)
	}
}

func TestAllDownHalfOpenIsBounded(t *testing.T) {
	registry, _ := healthyRegistry(t, "a")
	registry.MarkFailure("a", context.DeadlineExceeded)

	first, ok := registry.Acquire("a", true)
	if !ok {
		t.Fatal("first half-open acquire failed")
	}
	if _, ok := registry.Acquire("a", true); ok {
		t.Fatal("second concurrent half-open acquire succeeded")
	}
	registry.MarkFailure("a", context.DeadlineExceeded)
	if _, ok := registry.Acquire("a", true); ok {
		t.Fatal("MarkFailure reopened half-open gate before lease close")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close half-open lease: %v", err)
	}
	if second, ok := registry.Acquire("a", true); !ok {
		t.Fatal("half-open gate did not reopen after release")
	} else {
		_ = second.Close()
	}
}

func TestStatsReflectHealthAndActiveLeases(t *testing.T) {
	registry, _ := healthyRegistry(t, "a", "b")
	registry.MarkFailure("b", context.DeadlineExceeded)
	lease, ok := registry.Acquire("a", false)
	if !ok {
		t.Fatal("acquire a")
	}
	defer lease.Close()

	stats := registry.Stats()
	if stats.Total != 2 || stats.Healthy != 1 || stats.Unhealthy != 1 || stats.ActiveLeases != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestHealthyNodeBecomesDueForPeriodicProbe(t *testing.T) {
	registry, _ := healthyRegistry(t, "a")
	leases := registry.ProbeDue(time.Now().Add(31 * time.Second))
	if len(leases) != 1 || leases[0].ID != "a" {
		t.Fatalf("periodic probe leases = %#v", leases)
	}
	registry.MarkSuccess("a", time.Millisecond)
	_ = leases[0].Close()
}
