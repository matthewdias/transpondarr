package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func progressOf(t *testing.T, st *Store, now time.Time, title string) db.ListSeriesWithProgressRow {
	t.Helper()
	stamp := sql.NullString{String: FormatTimestamp(now), Valid: true}
	rows, err := st.Q.ListSeriesWithProgress(context.Background(),
		db.ListSeriesWithProgressParams{AirsAt: stamp, AirsAt_2: stamp})
	if err != nil {
		t.Fatalf("list series with progress: %v", err)
	}
	for _, r := range rows {
		if r.Title == title {
			return r
		}
	}
	t.Fatalf("series %q missing from the progress listing", title)
	return db.ListSeriesWithProgressRow{}
}

// The denominator is what the series is pursuing, not what it will ever have.
// The raw total rides along untouched so an API client that read it still can.
func TestListSeriesWithProgressCountsMonitoredAndAired(t *testing.T) {
	st := tempStore(t)
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	id := seedSearchSeries(t, st, "narrowed-long-runner", 1)
	// Tracked and held.
	seedSearchItem(t, st, id, 1, 1, &past)
	// Tracked, still missing.
	seedSearchItem(t, st, id, 2, 0, &past)
	// A null air date must read as aired: AniList's coverage thins out badly, so
	// the inverted form would make half the library read 100%.
	seedSearchItem(t, st, id, 3, 0, nil)
	// Not tracked: unmonitored, in either possession state.
	unmonitorItem(t, st, seedSearchItem(t, st, id, 4, 0, &past))
	unmonitorItem(t, st, seedSearchItem(t, st, id, 5, 1, &past))
	// Not tracked: not broadcast yet, in either possession state.
	seedSearchItem(t, st, id, 6, 0, &future)
	seedSearchItem(t, st, id, 7, 1, &future)

	got := progressOf(t, st, now, "narrowed-long-runner")
	if got.TotalItems != 7 {
		t.Errorf("total_items = %d, want 7 -- the raw count keeps its old meaning", got.TotalItems)
	}
	if got.TrackedItems != 3 {
		t.Errorf("tracked_items = %d, want 3 (monitored and aired)", got.TrackedItems)
	}
	// The numerator carries the identical filter, or the held unaired and held
	// unmonitored items above would push this past its own denominator.
	if got.InLibraryItems != 1 {
		t.Errorf("in_library_items = %d, want 1", got.InLibraryItems)
	}
}

// A series with no items at all must read 0 / 0 rather than tripping the
// LEFT JOIN's NULL row into a phantom count.
func TestListSeriesWithProgressHandlesAnEmptySeries(t *testing.T) {
	st := tempStore(t)
	seedSearchSeries(t, st, "empty", 1)

	got := progressOf(t, st, time.Now(), "empty")
	if got.TotalItems != 0 || got.TrackedItems != 0 || got.InLibraryItems != 0 {
		t.Errorf("progress = %d/%d (%d total), want all zero",
			got.InLibraryItems, got.TrackedItems, got.TotalItems)
	}
}
