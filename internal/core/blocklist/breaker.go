package blocklist

import (
	"sync"
	"time"
)

// The breaker's shape. The unit is a *release*, credited with one wanted item
// each, and the count is how many distinct items those credits land on. Both
// halves are load-bearing, because both faults it must ignore are one release
// or one item repeating:
//
//   - one item working through its candidate pool is many releases crediting the
//     same item, so the count stays at one however fast it churns;
//   - one dead release is one credit however many episodes it covers, whether it
//     arrives as a batch grab or as the grab row per item the importer fails
//     separately.
//
// Only a fault failing different releases across different items — a full disk,
// a client reaping torrents — moves the count. The threshold is deliberately
// low: a false trip costs one round of forgetting, since the release is retried
// next pass and remembered then, while a miss costs a whole candidate pool at
// 24h apiece.
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

// failure is one release's most recent failure, credited to the lowest item it
// has been seen covering. Lowest rather than latest so a batch delivered item by
// item, in whatever order, still collapses to a single credit.
type failure struct {
	item int64
	at   time.Time
}

// breaker decides whether a failure is evidence about the release or about the
// environment. It is in-memory on purpose: the grabs table cannot carry the
// history (a re-grab overwrites the row), and a restart losing the window is
// harmless — a fault that is still present re-proves itself within one window.
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
	// Nothing to attribute the failure to is no evidence of breadth, so it neither
	// counts nor is judged by a count it could not have contributed to.
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

// reset forgets the window, so clearing the blocklist after fixing the fault
// does not also mean waiting one out.
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
