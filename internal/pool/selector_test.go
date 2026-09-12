package pool

import "testing"

func nextID(t *testing.T, selector *Selector, candidates []Candidate) string {
	t.Helper()
	attempt := selector.Begin(candidates)
	id, ok := attempt.Next()
	if !ok {
		t.Fatal("attempt returned no candidate")
	}
	attempt.Commit(id)
	return id
}

func TestSelectorsHaveIndependentCursors(t *testing.T) {
	candidates := []Candidate{{ID: "a", Eligible: true}, {ID: "b", Eligible: true}, {ID: "c", Eligible: true}}
	httpSelector := NewSelector()
	wsSelector := NewSelector()

	if got := nextID(t, httpSelector, candidates); got != "a" {
		t.Fatalf("HTTP first = %q", got)
	}
	if got := nextID(t, httpSelector, candidates); got != "b" {
		t.Fatalf("HTTP second = %q", got)
	}
	if got := nextID(t, wsSelector, candidates); got != "a" {
		t.Fatalf("WS first = %q", got)
	}
}

func TestAttemptSkipsFailureAndCommitsAfterSuccess(t *testing.T) {
	selector := NewSelector()
	candidates := []Candidate{{ID: "a", Eligible: true}, {ID: "b", Eligible: true}, {ID: "c", Eligible: true}}
	attempt := selector.Begin(candidates)

	if id, _ := attempt.Next(); id != "a" {
		t.Fatalf("first candidate = %q", id)
	}
	if id, _ := attempt.Next(); id != "b" {
		t.Fatalf("second candidate = %q", id)
	} else {
		attempt.Commit(id)
	}

	if got := nextID(t, selector, candidates); got != "c" {
		t.Fatalf("next connection began at %q", got)
	}
}

func TestAttemptReturnsEachEligibleNodeOnce(t *testing.T) {
	selector := NewSelector()
	attempt := selector.Begin([]Candidate{{ID: "a", Eligible: true}, {ID: "b"}, {ID: "c", Eligible: true}})
	var got []string
	for {
		id, ok := attempt.Next()
		if !ok {
			break
		}
		got = append(got, id)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("attempt order = %#v", got)
	}
}

func TestConcurrentAttemptsDoNotRegressCursor(t *testing.T) {
	selector := NewSelector()
	candidates := []Candidate{{ID: "a", Eligible: true}, {ID: "b", Eligible: true}, {ID: "c", Eligible: true}}
	first := selector.Begin(candidates)
	second := selector.Begin(candidates)
	firstID, _ := first.Next()
	secondID, _ := second.Next()
	if firstID != "a" || secondID != "b" {
		t.Fatalf("reserved starts = %q, %q", firstID, secondID)
	}
	first.Commit(firstID)
	second.Commit(secondID)
	if got := nextID(t, selector, candidates); got != "c" {
		t.Fatalf("next candidate after concurrent commits = %q", got)
	}
}

func TestUncontendedAllFailedAttemptRollsBackReservation(t *testing.T) {
	selector := NewSelector()
	candidates := []Candidate{{ID: "a", Eligible: true}, {ID: "b", Eligible: true}}
	attempt := selector.Begin(candidates)
	for {
		if _, ok := attempt.Next(); !ok {
			break
		}
	}
	attempt.Abort()
	if got := nextID(t, selector, candidates); got != "a" {
		t.Fatalf("next candidate after all-failed rollback = %q", got)
	}
}
