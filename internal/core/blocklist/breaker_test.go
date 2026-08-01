package blocklist

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

// at pins the service's clock, so a window test needs no sleep.
func at(svc *Service, t time.Time) {
	svc.now = func() time.Time { return t }
}

// recordItems is one release failing, covering the given items.
func recordItems(t *testing.T, svc *Service, seriesID int64, title string, itemIDs ...int64) bool {
	t.Helper()
	ok, err := svc.Record(context.Background(), seriesID, itemIDs, "", title, "failed")
	if err != nil {
		t.Fatalf("record %q: %v", title, err)
	}
	return ok
}

// record is the shorthand the breaker tests use: one release, one item.
func record(t *testing.T, svc *Service, seriesID, itemID int64, title string) bool {
	t.Helper()
	return recordItems(t, svc, seriesID, title, itemID)
}

// The fan-out #120 is about: an environmental fault fails a different release
// every time, so the escalation ladder never fires and the whole candidate pool
// is blocked at 24h apiece. Breadth across items is the signal the ladder lacks.
func TestBreakerTripsWhenManyDistinctItemsFail(t *testing.T) {
	svc, _, series := newService(t)
	start := time.Now()
	at(svc, start)

	for item := int64(1); item < int64(breakerItems); item++ {
		if !record(t, svc, series.ID, item, fmt.Sprintf("[SynthSubs] Placeholder Saga - %02d", item)) {
			t.Fatalf("item %d was not remembered; the breaker tripped early", item)
		}
	}
	if st := svc.BreakerState(); st.Open {
		t.Fatalf("breaker open at %d items, want it to hold to %d", st.Items, breakerItems)
	}

	if record(t, svc, series.ID, breakerItems, "[SynthSubs] Placeholder Saga - 05") {
		t.Fatal("the failure that trips the breaker was still remembered")
	}
	st := svc.BreakerState()
	if !st.Open || st.Items != breakerItems {
		t.Errorf("breaker state = %+v, want open with %d items", st, breakerItems)
	}
	if st.Since.IsZero() {
		t.Error("breaker reports no opening time; the UI shows it")
	}

	all, err := svc.List(context.Background(), series.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != breakerItems-1 {
		t.Errorf("entries = %d, want the %d written before the breaker opened", len(all), breakerItems-1)
	}
}

// The other direction, and the one that matters more: one item working through
// its candidate pool is exactly what the ladder exists for. However fast it
// churns, it is one item, so it must never trip the breaker.
func TestBreakerIgnoresOneItemExhaustingItsCandidates(t *testing.T) {
	svc, _, series := newService(t)
	at(svc, time.Now())

	for n := range breakerItems * 2 {
		title := fmt.Sprintf("[Group%02d] Placeholder Saga - 03", n)
		if !record(t, svc, series.ID, 1, title) {
			t.Fatalf("candidate %d for the same item was suppressed; the ladder needs every one", n)
		}
	}
	if st := svc.BreakerState(); st.Open || st.Items != 1 {
		t.Errorf("breaker state = %+v, want closed with 1 item", st)
	}
}

// The mirror of the depth rule, and the one that is easy to miss: one release's
// breadth is not evidence about that release either. A season batch is the
// standard anime backfill shape, so counting its own items against it would
// refuse to remember every dead batch URL -- #118 all over again, for the case
// that covers the most episodes.
func TestBreakerRemembersABatchCoveringEnoughItemsToTripIt(t *testing.T) {
	svc, _, series := newService(t)
	at(svc, time.Now())

	items := make([]int64, 0, breakerItems+7)
	for n := int64(1); n <= int64(breakerItems)+7; n++ {
		items = append(items, n)
	}
	if !recordItems(t, svc, series.ID, "[SynthSubs] Placeholder Saga - 01-12 [Batch]", items...) {
		t.Fatal("a batch was refused on its own first failure; it can never be remembered")
	}
	if st := svc.BreakerState(); st.Open {
		t.Errorf("breaker state = %+v, want closed: one release is one piece of evidence", st)
	}
}

// The same batch reaching the breaker the way the importer delivers it: a grab
// row per covered item, so one dead release arrives as many single-item calls
// that only its identity ties together.
func TestBreakerRemembersABatchFailingItemByItem(t *testing.T) {
	svc, _, series := newService(t)
	at(svc, time.Now())
	const batch = "[SynthSubs] Placeholder Saga - 01-12 [Batch]"

	for item := int64(1); item <= int64(breakerItems)+7; item++ {
		if !record(t, svc, series.ID, item, batch) {
			t.Fatalf("item %d of one dead batch was suppressed", item)
		}
	}
	if st := svc.BreakerState(); st.Open {
		t.Errorf("breaker state = %+v, want closed: it is still one release", st)
	}
}

// Depth again, one level up: a batch's candidate pool churning is the ladder's
// job, exactly as a single episode's is.
func TestBreakerIgnoresOneBatchExhaustingItsCandidates(t *testing.T) {
	svc, _, series := newService(t)
	at(svc, time.Now())

	for n := range breakerItems * 2 {
		title := fmt.Sprintf("[Group%02d] Placeholder Saga - 01-06 [Batch]", n)
		if !recordItems(t, svc, series.ID, title, 1, 2, 3, 4, 5, 6) {
			t.Fatalf("candidate %d for the same batch was suppressed", n)
		}
	}
	if st := svc.BreakerState(); st.Open {
		t.Errorf("breaker state = %+v, want closed", st)
	}
}

// Failures spread thinly are a working library, not a fault: the window drops
// them so an old failure cannot combine with a new one to trip the breaker.
func TestBreakerForgetsFailuresOlderThanTheWindow(t *testing.T) {
	svc, _, series := newService(t)
	start := time.Now()

	// Items 1..4, one a minute. Four is one short of tripping.
	for item := int64(1); item <= 4; item++ {
		at(svc, start.Add(time.Duration(item)*time.Minute))
		record(t, svc, series.ID, item, fmt.Sprintf("[SynthSubs] Placeholder Saga - %02d", item))
	}

	// Far enough on that items 1 and 2 have left the window, leaving 3 and 4.
	at(svc, start.Add(breakerWindow+2*time.Minute))
	if st := svc.BreakerState(); st.Items != 2 {
		t.Fatalf("items in window = %d, want the 2 still inside it", st.Items)
	}
	// Items 5 and 6 make four in the window. Had the lapsed pair still counted,
	// item 5 would have been the fifth and been suppressed.
	for item := int64(5); item <= 6; item++ {
		if !record(t, svc, series.ID, item, fmt.Sprintf("[SynthSubs] Placeholder Saga - %02d", item)) {
			t.Errorf("item %d was suppressed by failures that had already left the window", item)
		}
	}
}

// Recovery is one action: the operator fixes the disk, clears the memory, and
// the next tick starts clean rather than waiting out the window.
func TestClearAllForgetsEverythingAndClosesTheBreaker(t *testing.T) {
	svc, st, series := newService(t)
	ctx := context.Background()
	other, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{
		Title: "Another Placeholder", Format: "TV", Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create other series: %v", err)
	}
	at(svc, time.Now())

	for item := int64(1); item <= int64(breakerItems); item++ {
		seriesID := series.ID
		if item%2 == 0 {
			seriesID = other.ID
		}
		record(t, svc, seriesID, item, fmt.Sprintf("[SynthSubs] Placeholder - %02d", item))
	}
	if !svc.BreakerState().Open {
		t.Fatal("breaker closed after failures across two series; its scope is the library")
	}

	cleared, err := svc.ClearAll(ctx)
	if err != nil {
		t.Fatalf("clear all: %v", err)
	}
	if cleared != int64(breakerItems)-1 {
		t.Errorf("cleared = %d, want the %d entries written", cleared, breakerItems-1)
	}
	if state := svc.BreakerState(); state.Open || state.Items != 0 {
		t.Errorf("breaker state = %+v after a clear, want closed and empty", state)
	}
	for _, id := range []int64{series.ID, other.ID} {
		if left, _ := svc.List(ctx, id); len(left) != 0 {
			t.Errorf("series %d still has %d entries after a library-wide clear", id, len(left))
		}
	}
}

// A failure with no item to attribute it to still blocks, but cannot be
// evidence of breadth: counting it would let one caller trip the breaker alone.
func TestBreakerIgnoresFailuresWithNoItems(t *testing.T) {
	svc, _, series := newService(t)
	at(svc, time.Now())

	for n := range breakerItems * 2 {
		ok, err := svc.Record(context.Background(), series.ID, nil, "",
			fmt.Sprintf("[Group%02d] Placeholder Saga - 03", n), "failed")
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if !ok {
			t.Fatalf("record %d was suppressed by item-less failures", n)
		}
	}
	if st := svc.BreakerState(); st.Open {
		t.Errorf("breaker state = %+v, want closed", st)
	}
}
