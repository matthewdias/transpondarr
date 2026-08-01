package acquire

import (
	"sync"
	"testing"
)

// TryAcquire is all-or-nothing: a partial overlap takes nothing, so a caller
// that loses cannot leave half a claim behind for the winner to trip over.
func TestTryAcquireIsAllOrNothing(t *testing.T) {
	c := newClaims()
	if !c.TryAcquire([]int64{1, 2}) {
		t.Fatal("first TryAcquire failed on an empty registry")
	}
	if c.TryAcquire([]int64{2, 3}) {
		t.Fatal("overlapping TryAcquire succeeded")
	}
	// Item 3 did not overlap, so the failed attempt must not have claimed it.
	c.Release([]int64{1, 2})
	if !c.TryAcquire([]int64{3}) {
		t.Error("item 3 was left claimed by the failed all-or-nothing attempt")
	}
}

// Counting is what lets automation's TryAcquire nest inside Grab's Acquire: both
// defers must unwind before the item is free again.
func TestClaimsCountHolders(t *testing.T) {
	c := newClaims()
	if !c.TryAcquire([]int64{7}) {
		t.Fatal("TryAcquire failed on an empty registry")
	}
	c.Acquire([]int64{7}) // the nested manual path inside AutoGrab
	c.Release([]int64{7})
	if c.TryAcquire([]int64{7}) {
		t.Error("item 7 freed while one holder remained")
	}
	c.Release([]int64{7})
	if !c.TryAcquire([]int64{7}) {
		t.Error("item 7 still held after every holder released")
	}
}

// Acquire never refuses — that is the manual path's never-gated rule (PR #57)
// expressed in the registry itself.
func TestAcquireNeverBlocks(t *testing.T) {
	c := newClaims()
	c.Acquire([]int64{4})
	c.Acquire([]int64{4})
	c.Release([]int64{4})
	c.Release([]int64{4})
	if !c.TryAcquire([]int64{4}) {
		t.Error("unbalanced Acquire/Release left item 4 claimed")
	}
}

// Exactly one of N racing automation callers may hold an item at a time. Run
// under -race: this is the shared mutable state the registry exists to guard.
func TestTryAcquireIsExclusiveUnderConcurrency(t *testing.T) {
	c := newClaims()
	const goroutines = 50

	var mu sync.Mutex
	var concurrent, maxConcurrent, won int
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range goroutines {
		wg.Go(func() {
			<-start
			if !c.TryAcquire([]int64{1}) {
				return
			}
			mu.Lock()
			won++
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()

			mu.Lock()
			concurrent--
			mu.Unlock()
			c.Release([]int64{1})
		})
	}
	close(start)
	wg.Wait()

	if maxConcurrent > 1 {
		t.Errorf("%d holders at once, want at most 1", maxConcurrent)
	}
	if won == 0 {
		t.Error("no goroutine ever acquired the item")
	}
	if !c.TryAcquire([]int64{1}) {
		t.Error("item still claimed after every holder released")
	}
}
