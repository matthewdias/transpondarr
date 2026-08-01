package blocklist

import (
	"sync"
	"time"
)

// How many items the window's releases must credit between them to open the
// breaker, and how long a failure counts for. See the package doc for the unit.
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

// releaseRef identifies a release the way the blocklist's own unique index does,
// so the breaker and the table agree on what "the same release" means.
type releaseRef struct {
	seriesID   int64
	normalized string
}

// failure is one release's latest failure, credited to the lowest item it has
// covered — lowest, so a batch delivered item by item still collapses to one.
type failure struct {
	item int64
	at   time.Time
}

// breaker weighs whether a failure is about the release or the environment. In
// memory because a re-grab overwrites the grab row, the only durable record.
type breaker struct {
	mu     sync.Mutex
	failed map[releaseRef]failure
	since  time.Time
}

// observe records a failure and reports whether the memory of it is trustworthy.
func (b *breaker) observe(ref releaseRef, itemIDs []int64, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.prune(now)
	// No item to attribute it to is no evidence of breadth, so it neither counts
	// nor is judged by a count it could not have joined.
	if len(itemIDs) == 0 {
		return b.since.IsZero()
	}
	if b.failed == nil {
		b.failed = make(map[releaseRef]failure, breakerItems)
	}
	item := lowest(itemIDs)
	if prev, ok := b.failed[ref]; ok && prev.item < item {
		item = prev.item
	}
	b.failed[ref] = failure{item: item, at: now}

	if b.count() < breakerItems {
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
		Items:     b.count(),
		Threshold: breakerItems,
		Window:    breakerWindow,
		Since:     b.since,
	}
}

// reset forgets the window, so clearing the blocklist need not also wait one out.
func (b *breaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failed = nil
	b.since = time.Time{}
}

// count is how many distinct items the window's releases credit between them;
// the caller holds the lock.
func (b *breaker) count() int {
	items := make(map[int64]struct{}, len(b.failed))
	for _, f := range b.failed {
		items[f.item] = struct{}{}
	}
	return len(items)
}

// prune drops failures that have left the window; the caller holds the lock.
func (b *breaker) prune(now time.Time) {
	cutoff := now.Add(-breakerWindow)
	for ref, f := range b.failed {
		if !f.at.After(cutoff) {
			delete(b.failed, ref)
		}
	}
	if b.count() < breakerItems {
		b.since = time.Time{}
	}
}

func lowest(ids []int64) int64 {
	low := ids[0]
	for _, id := range ids[1:] {
		if id < low {
			low = id
		}
	}
	return low
}
