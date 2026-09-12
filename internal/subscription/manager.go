package subscription

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

type Fetcher interface {
	Fetch(context.Context, Source) ([]byte, string, error)
}

type Manager struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	sources   []Source
	fetcher   Fetcher
	bySource  map[string][]NodeSpec
	last      Snapshot
}

func NewManager(sources []Source, fetcher Fetcher) *Manager {
	return &Manager{
		sources:  append([]Source(nil), sources...),
		fetcher:  fetcher,
		bySource: make(map[string][]NodeSpec),
		last:     Snapshot{SourceErrors: make(map[string]string)},
	}
}

func (manager *Manager) Refresh(ctx context.Context) (Snapshot, error) {
	manager.refreshMu.Lock()
	defer manager.refreshMu.Unlock()

	manager.mu.Lock()
	defer manager.mu.Unlock()

	sourceErrors := make(map[string]string)
	for _, source := range manager.sources {
		body, hint, err := manager.fetcher.Fetch(ctx, source)
		if err != nil {
			sourceErrors[source.ID] = err.Error()
			continue
		}
		nodes, err := Parse(body, hint)
		if err != nil || len(nodes) == 0 {
			if err == nil {
				err = errors.New("subscription contains no proxy nodes")
			}
			sourceErrors[source.ID] = err.Error()
			continue
		}
		for index := range nodes {
			nodes[index].SourceIDs = []string{source.ID}
		}
		manager.bySource[source.ID] = nodes
	}

	manager.last = Snapshot{
		Nodes:        mergeSources(manager.bySource),
		SourceErrors: sourceErrors,
	}
	result := cloneSnapshot(manager.last)
	if len(sourceErrors) > 0 {
		ids := make([]string, 0, len(sourceErrors))
		for id := range sourceErrors {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return result, errors.New("subscription refresh failed for source(s): " + strings.Join(ids, ","))
	}
	return result, nil
}

func (manager *Manager) Snapshot() Snapshot {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return cloneSnapshot(manager.last)
}

func mergeSources(bySource map[string][]NodeSpec) []NodeSpec {
	merged := make(map[string]NodeSpec)
	for sourceID, nodes := range bySource {
		for _, node := range nodes {
			existing, ok := merged[node.ID]
			if !ok {
				node.SourceIDs = []string{sourceID}
				merged[node.ID] = node
				continue
			}
			if !contains(existing.SourceIDs, sourceID) {
				existing.SourceIDs = append(existing.SourceIDs, sourceID)
				sort.Strings(existing.SourceIDs)
				merged[node.ID] = existing
			}
		}
	}
	result := make([]NodeSpec, 0, len(merged))
	for _, node := range merged {
		result = append(result, node)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cloneSnapshot(input Snapshot) Snapshot {
	output := Snapshot{
		Nodes:        append([]NodeSpec(nil), input.Nodes...),
		SourceErrors: make(map[string]string, len(input.SourceErrors)),
	}
	for key, value := range input.SourceErrors {
		output.SourceErrors[key] = value
	}
	for index := range output.Nodes {
		output.Nodes[index].Options = append([]byte(nil), input.Nodes[index].Options...)
		output.Nodes[index].SourceIDs = append([]string(nil), input.Nodes[index].SourceIDs...)
	}
	return output
}
