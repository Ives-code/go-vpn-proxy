package pool

import "sync/atomic"

// Selector assigns a monotonically increasing reservation ticket to each new
// connection. Tickets give concurrent connections distinct round-robin starts;
// a successful attempt can move the high-water mark forward when it skipped
// failed nodes, but can never move it backward.
type Selector struct {
	next atomic.Uint64
}

func NewSelector() *Selector {
	return &Selector{}
}

func (selector *Selector) Begin(candidates []Candidate) *Attempt {
	copyOfCandidates := append([]Candidate(nil), candidates...)
	if len(copyOfCandidates) == 0 {
		return &Attempt{selector: selector}
	}
	ticket := selector.next.Add(1) - 1
	return &Attempt{
		selector:   selector,
		candidates: copyOfCandidates,
		start:      int(ticket % uint64(len(copyOfCandidates))),
		ticket:     ticket,
		reserved:   true,
	}
}

type Attempt struct {
	selector   *Selector
	candidates []Candidate
	start      int
	offset     int
	returned   map[string]int
	ticket     uint64
	reserved   bool
	finished   bool
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
	target := attempt.ticket + uint64(offset) + 1
	for {
		current := attempt.selector.next.Load()
		if current >= target || attempt.selector.next.CompareAndSwap(current, target) {
			break
		}
	}
	attempt.finished = true
}

// Abort rolls back an uncontended reservation. If another connection has
// already reserved a later ticket, retaining this ticket avoids duplicating a
// start already assigned to that concurrent connection.
func (attempt *Attempt) Abort() {
	if attempt.finished || !attempt.reserved {
		return
	}
	attempt.selector.next.CompareAndSwap(attempt.ticket+1, attempt.ticket)
	attempt.finished = true
}
