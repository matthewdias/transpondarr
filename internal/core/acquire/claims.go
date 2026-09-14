package acquire

import "sync"

// claims is the set of wanted items with a grab in flight. It is process-local
// by design and that is sufficient: Transpondarr is one binary, so the search sweep,
// the feed poll and every manual grab all run here.
//
// Claims are counted rather than flagged because the automation path nests —
// AutoGrab acquires a claim and then calls Grab, which acquires it again — and
// because two manual grabs may legitimately claim one item at once.
type claims struct {
	mu   sync.Mutex
	held map[int64]int
}

func newClaims() *claims { return &claims{held: make(map[int64]int)} }

// TryAcquire claims every id or none, reporting whether it did. Automation uses
// it, so automation skips anything already in flight.
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
// and always succeeds (PR #57), so it acquires a claim rather than trying for one.
func (c *claims) Acquire(ids []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		c.held[id]++
	}
}

// TryClaimItems exposes the registry to one caller outside this package —
// the importer, placing a payload file for an item no grab row claimed (#126).
// One registry is the point: a minutes-long copy must exclude a concurrent grab.
func (s *Service) TryClaimItems(ids []int64) bool { return s.claims.TryAcquire(ids) }

// ReleaseClaims releases what TryClaimItems claimed.
func (s *Service) ReleaseClaims(ids []int64) { s.claims.Release(ids) }

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
