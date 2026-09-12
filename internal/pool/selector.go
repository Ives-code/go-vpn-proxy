package pool

import "sync"

type Selector struct {
	mu     sync.Mutex
	cursor int
}

func NewSelector() *Selector {
	return &Selector{}
}

func (selector *Selector) Begin(candidates []Candidate) *Attempt {
	copyOfCandidates := append([]Candidate(nil), candidates...)
	selector.mu.Lock()
	start := 0
	if len(copyOfCandidates) > 0 {
		start = selector.cursor % len(copyOfCandidates)
		selector.cursor = (selector.cursor + 1) % len(copyOfCandidates)
	}
	selector.mu.Unlock()
	return &Attempt{selector: selector, candidates: copyOfCandidates, start: start}
}

type Attempt struct {
	selector   *Selector
	candidates []Candidate
	start      int
	offset     int
	returned   map[string]int
	committed  bool
}

func (attempt *Attempt) Next() (string, bool) {
	if len(attempt.candidates) == 0 {
		return "", false
	}
	if attempt.returned == nil {
		attempt.returned = make(map[string]int)
	}
	for attempt.offset < len(attempt.candidates) {
		offset := attempt.offset
		index := (attempt.start + offset) % len(attempt.candidates)
		attempt.offset++
		candidate := attempt.candidates[index]
		if !candidate.Eligible {
			continue
		}
		attempt.returned[candidate.ID] = offset
		return candidate.ID, true
	}
	return "", false
}

func (attempt *Attempt) Commit(id string) {
	if attempt.committed || len(attempt.candidates) == 0 {
		return
	}
	offset, ok := attempt.returned[id]
	if !ok {
		return
	}
	attempt.selector.mu.Lock()
	attempt.selector.cursor = (attempt.selector.cursor + offset) % len(attempt.candidates)
	attempt.selector.mu.Unlock()
	attempt.committed = true
}
