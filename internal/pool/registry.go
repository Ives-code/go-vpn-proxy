package pool

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"dual-egress-gateway/internal/subscription"
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Close() error
}

type Factory interface {
	Build(subscription.NodeSpec) (Dialer, error)
}

type Candidate struct {
	ID       string
	Eligible bool
	HalfOpen bool
}

type Diff struct {
	Added   int
	Removed int
	Kept    int
}

type nodeState struct {
	spec        subscription.NodeSpec
	dialer      Dialer
	healthy     bool
	removed     bool
	probing     bool
	active      int
	latency     time.Duration
	lastFailure string
	nextProbe   time.Time
}

type Registry struct {
	mu            sync.Mutex
	factory       Factory
	probeInterval time.Duration
	nodes         map[string]*nodeState
	order         []string
}

func NewRegistry(factory Factory, probeInterval time.Duration) *Registry {
	return &Registry{
		factory:       factory,
		probeInterval: probeInterval,
		nodes:         make(map[string]*nodeState),
	}
}

func (registry *Registry) Apply(snapshot subscription.Snapshot) (Diff, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	wanted := make(map[string]subscription.NodeSpec, len(snapshot.Nodes))
	for _, spec := range snapshot.Nodes {
		if spec.ID == "" {
			return Diff{}, errors.New("node has empty ID")
		}
		wanted[spec.ID] = spec
	}

	built := make(map[string]Dialer)
	for id, spec := range wanted {
		if existing, ok := registry.nodes[id]; ok && !existing.removed {
			continue
		}
		if existing, ok := registry.nodes[id]; ok && existing.removed {
			continue
		}
		dialer, err := registry.factory.Build(spec)
		if err != nil {
			for _, pending := range built {
				_ = pending.Close()
			}
			return Diff{}, err
		}
		if dialer == nil {
			for _, pending := range built {
				_ = pending.Close()
			}
			return Diff{}, errors.New("factory returned a nil dialer")
		}
		built[id] = dialer
	}

	diff := Diff{}
	for id, state := range registry.nodes {
		_, keep := wanted[id]
		if keep {
			state.removed = false
			state.spec = wanted[id]
			diff.Kept++
			continue
		}
		if state.removed {
			continue
		}
		state.removed = true
		diff.Removed++
		if state.active == 0 {
			_ = state.dialer.Close()
			delete(registry.nodes, id)
		}
	}
	for id, dialer := range built {
		registry.nodes[id] = &nodeState{
			spec:      wanted[id],
			dialer:    dialer,
			nextProbe: time.Time{},
		}
		diff.Added++
	}

	registry.order = registry.order[:0]
	for id := range wanted {
		registry.order = append(registry.order, id)
	}
	sort.Strings(registry.order)
	return diff, nil
}

func (registry *Registry) MarkFailure(id string, cause error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, ok := registry.nodes[id]
	if !ok {
		return
	}
	state.healthy = false
	state.probing = false
	state.nextProbe = time.Now().Add(registry.probeInterval)
	if cause != nil {
		state.lastFailure = failureClass(cause)
	}
}

func (registry *Registry) MarkSuccess(id string, latency time.Duration) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, ok := registry.nodes[id]
	if !ok || state.removed {
		return
	}
	state.healthy = true
	state.probing = false
	state.latency = latency
	state.lastFailure = ""
	state.nextProbe = time.Time{}
}

func (registry *Registry) RoutingSnapshot() []Candidate {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	healthyCount := 0
	for _, id := range registry.order {
		if state := registry.nodes[id]; state != nil && !state.removed && state.healthy {
			healthyCount++
		}
	}

	result := make([]Candidate, 0, len(registry.order))
	for _, id := range registry.order {
		state := registry.nodes[id]
		if state == nil || state.removed {
			continue
		}
		candidate := Candidate{ID: id, Eligible: state.healthy}
		if healthyCount == 0 && !state.probing {
			candidate.Eligible = true
			candidate.HalfOpen = true
		}
		result = append(result, candidate)
	}
	return result
}

func (registry *Registry) Acquire(id string, allowUnhealthy bool) (*NodeLease, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, ok := registry.nodes[id]
	if !ok || state.removed {
		return nil, false
	}
	halfOpen := !state.healthy
	if halfOpen && !allowUnhealthy {
		return nil, false
	}
	if halfOpen {
		if state.probing {
			return nil, false
		}
		state.probing = true
	}
	state.active++
	return &NodeLease{ID: id, Dialer: state.dialer, registry: registry, halfOpen: halfOpen}, true
}

func (registry *Registry) ProbeDue(now time.Time) []*NodeLease {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var leases []*NodeLease
	for _, id := range registry.order {
		state := registry.nodes[id]
		if state == nil || state.removed || state.probing || state.healthy || state.nextProbe.After(now) {
			continue
		}
		state.probing = true
		state.active++
		leases = append(leases, &NodeLease{ID: id, Dialer: state.dialer, registry: registry, halfOpen: true})
	}
	return leases
}

type NodeLease struct {
	ID       string
	Dialer   Dialer
	registry *Registry
	halfOpen bool
	once     sync.Once
}

func (lease *NodeLease) Close() error {
	lease.once.Do(func() {
		lease.registry.release(lease.ID, lease.halfOpen)
	})
	return nil
}

func (registry *Registry) release(id string, halfOpen bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, ok := registry.nodes[id]
	if !ok {
		return
	}
	if state.active > 0 {
		state.active--
	}
	if halfOpen {
		state.probing = false
	}
	if state.removed && state.active == 0 {
		_ = state.dialer.Close()
		delete(registry.nodes, id)
	}
}

func failureClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "network"
	}
	return "dial"
}
