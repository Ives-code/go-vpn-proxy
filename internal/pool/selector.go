package pool

import "sync"

// Selector allows concurrent connection attempts to reserve distinct starts,
// while retiring their success/abort results in ticket order. Only successful
// attempts advance the committed cursor.
type Selector struct {
	mu              sync.Mutex
	cursor          uint64
	reservationNext uint64
	nextTicket      uint64
	retireTicket    uint64
	results         map[uint64]reservationResult
}

type reservationResult struct {
	committed bool
	target    uint64
}

func NewSelector() *Selector {
	return &Selector{results: make(map[uint64]reservationResult)}
}

func (selector *Selector) Begin(candidates []Candidate) *Attempt {
	copyOfCandidates := append([]Candidate(nil), candidates...)
	if len(copyOfCandidates) == 0 {
		return &Attempt{selector: selector}
	}
	selector.mu.Lock()
	ticket := selector.nextTicket
	selector.nextTicket++
	startAbsolute := selector.reservationNext
	selector.reservationNext++
	selector.mu.Unlock()
	return &Attempt{
		selector:      selector,
		candidates:    copyOfCandidates,
		start:         int(startAbsolute % uint64(len(copyOfCandidates))),
		startAbsolute: startAbsolute,
		ticket:        ticket,
		reserved:      true,
	}
}

type Attempt struct {
	selector      *Selector
	candidates    []Candidate
	start         int
	offset        int
	returned      map[string]int
	startAbsolute uint64
	ticket        uint64
	reserved      bool
	finished      bool
}

func (attempt *Attempt) Next() (string, bool) {
	if len(attempt.candidates) == 0 || attempt.finished {
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
	if attempt.finished || !attempt.reserved {
		return
	}
	offset, ok := attempt.returned[id]
	if !ok {
		return
	}
	target := attempt.startAbsolute + uint64(offset) + 1
	attempt.selector.finish(attempt.ticket, reservationResult{committed: true, target: target})
	attempt.finished = true
}

func (attempt *Attempt) Abort() {
	if attempt.finished || !attempt.reserved {
		return
	}
	attempt.selector.finish(attempt.ticket, reservationResult{})
	attempt.finished = true
}

func (selector *Selector) finish(ticket uint64, result reservationResult) {
	selector.mu.Lock()
	defer selector.mu.Unlock()
	selector.results[ticket] = result
	for {
		next, ok := selector.results[selector.retireTicket]
		if !ok {
			break
		}
		if next.committed && next.target > selector.cursor {
			selector.cursor = next.target
		}
		delete(selector.results, selector.retireTicket)
		selector.retireTicket++
	}
	if selector.retireTicket == selector.nextTicket {
		selector.reservationNext = selector.cursor
	}
}
