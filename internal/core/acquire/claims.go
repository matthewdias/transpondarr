package acquire

import "sync"

// claims is the set of wanted items with a grab in flight. It is process-local
// by design and that is sufficient: Transpondarr is one binary, so the sweep,
// the feed poll and every manual grab all run here.
//
// Holders are counted rather than flagged because the automation path nests —
// AutoGrab takes a claim and then calls Grab, which takes it again — and because
// two manual grabs may legitimately hold one item at once.
type claims struct {
	mu   sync.Mutex
	held map[int64]int
}

func newClaims() *claims { return &claims{held: make(map[int64]int)} }

// TryAcquire claims every id or none, reporting whether it did. Automation uses
// it, so automation yields to anything already in flight.
func (c *claims) TryAcquire(ids []int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		if c.held[id] > 0 {
			return false
		}
	}
	for _, id := range ids {
		c.held[id]++
	}
	return true
}

// Acquire claims every id unconditionally. A manual grab is explicit user intent
// and is never refused (PR #57), so it takes a claim rather than asking for one.
func (c *claims) Acquire(ids []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		c.held[id]++
	}
}

func (c *claims) Release(ids []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		if c.held[id] <= 1 {
			delete(c.held, id)
			continue
		}
		c.held[id]--
	}
}
