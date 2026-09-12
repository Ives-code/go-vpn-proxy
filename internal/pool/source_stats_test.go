package pool

import (
	"dual-egress-gateway/internal/subscription"
	"testing"
	"time"
)

func TestStatsBySourceCountsSharedNodesAndHealth(t *testing.T) {
	r := NewRegistry(nil, time.Second)
	r.nodes = map[string]*nodeState{
		"a": {spec: subscription.NodeSpec{SourceIDs: []string{"1", "2"}}, healthy: true},
		"b": {spec: subscription.NodeSpec{SourceIDs: []string{"2"}}, lastFailure: "timeout"},
		"c": {spec: subscription.NodeSpec{SourceIDs: []string{"1"}}},
		"d": {spec: subscription.NodeSpec{SourceIDs: []string{"1"}}, removed: true},
	}
	r.order = []string{"a", "b", "c", "d"}
	s := r.StatsBySource()
	if s["1"].Total != 2 || s["1"].Healthy != 1 || s["1"].Quarantined != 1 || s["2"].Total != 2 || s["2"].Unhealthy != 1 {
		t.Fatalf("stats=%v", s)
	}
}
