package blocklist

import (
	"sync"
	"time"
)

// The breaker's shape. Counting *distinct wanted items* rather than failures is
// what separates the two faults the ladder cannot: one item working through its
// candidate pool fails the same item over and over, however fast, and never
// advances the count, while a full disk or a client reaping torrents fails a
// different item every time. The threshold is deliberately low — a false trip
// costs one round of forgetting, since the release is retried next pass and
// remembered then, while a miss costs a whole candidate pool at 24h apiece.
const (
	breakerWindow = 15 * time.Minute
	breakerItems  = 5
)

// BreakerState is what the failure-memory panel reports.
type BreakerState struct {
	Open      bool
	Items     int
	Threshold int
	Window    time.Duration
	Since     time.Time // when it last opened; zero while closed
}

// breaker decides whether a failure is evidence about the release or about the
// environment. It is in-memory on purpose: the grabs table cannot carry the
// history (a re-grab overwrites the row), and a restart losing the window is
// harmless — a fault that is still present re-proves itself within one window.
type breaker struct {
	mu     sync.Mutex
	failed map[int64]time.Time // wanted item id -> its most recent failure
	since  time.Time
}

// observe records a failure and reports whether the memory of it is
// trustworthy. Items already inside the window only refresh their timestamp, so
// depth on one item never counts as breadth.
func (b *breaker) observe(itemIDs []int64, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.prune(now)
	// Nothing to attribute the failure to is no evidence of breadth, so it neither
	// counts nor is judged by a count it could not have contributed to.
	if len(itemIDs) == 0 {
		return b.since.IsZero()
	}
	if b.failed == nil {
		b.failed = make(map[int64]time.Time, breakerItems)
	}
	for _, id := range itemIDs {
		b.failed[id] = now
	}

	if len(b.failed) < breakerItems {
		return true
	}
	if b.since.IsZero() {
		b.since = now
	}
	return false
}

// state reports the breaker as of now, so a caller polling it sees the window
// drain without a failure having to arrive first.
func (b *breaker) state(now time.Time) BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	return BreakerState{
		Open:      !b.since.IsZero(),
		Items:     len(b.failed),
		Threshold: breakerItems,
		Window:    breakerWindow,
		Since:     b.since,
	}
}

// reset forgets the window, so clearing the blocklist after fixing the fault
// does not also mean waiting one out.
func (b *breaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failed = nil
	b.since = time.Time{}
}

// prune drops failures that have left the window; the caller holds the lock.
func (b *breaker) prune(now time.Time) {
	cutoff := now.Add(-breakerWindow)
	for id, at := range b.failed {
		if !at.After(cutoff) {
			delete(b.failed, id)
		}
	}
	if len(b.failed) < breakerItems {
		b.since = time.Time{}
	}
}
