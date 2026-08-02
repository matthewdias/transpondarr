package acquire_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// A delete landing while the sweep is out on the network must not fail the pass:
// no tx is open during the search, and the epoch-guarded cadence write treats
// zero rows as "reset or removed mid-sweep", not an error.
func TestSweepSurvivesSeriesDeletedMidSearch(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	ctx := context.Background()
	var once sync.Once
	h.idx.SearchHook = func(indexer.Query) {
		once.Do(func() {
			if _, err := h.st.Q.DeleteSeries(ctx, id); err != nil {
				t.Errorf("delete series mid-search: %v", err)
			}
		})
	}

	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v — a mid-search delete must not fail the pass", err)
	}
}

// A delete landing between the client Add (deliberately outside the grab tx) and
// the grab write hits the wanted_items FK. The pass reports it, records nothing,
// and the next pass runs clean: no wedge, and the series is not resurrected.
func TestSweepReportsButRecoversFromSeriesDeletedMidGrab(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	ctx := context.Background()
	var once sync.Once
	h.dl.AddHook = func(download.AddOptions) {
		once.Do(func() {
			if _, err := h.st.Q.DeleteSeries(ctx, id); err != nil {
				t.Errorf("delete series mid-grab: %v", err)
			}
		})
	}

	if err := h.svc.SweepOnce(ctx); err == nil {
		t.Fatal("SweepOnce returned nil, want the failed grab write surfaced")
	}
	var count int
	if err := h.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM grabs`).Scan(&count); err != nil {
		t.Fatalf("count grabs: %v", err)
	}
	if count != 0 {
		t.Errorf("%d grab rows recorded against a deleted series, want 0", count)
	}
	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("second SweepOnce: %v — one lost race must not wedge the sweep", err)
	}
}
