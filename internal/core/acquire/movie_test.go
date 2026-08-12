package acquire_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seedMovie inserts a monitored movie with its single wanted item.
func seedMovie(t *testing.T, st *store.Store, title string) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: title, Format: "MOVIE", Monitored: 1})
	if err != nil {
		t.Fatalf("create movie series: %v", err)
	}
	if _, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: "movie",
		Number:    sql.NullInt64{Int64: 1, Valid: true},
		Monitored: 1,
	}); err != nil {
		t.Fatalf("create wanted item: %v", err)
	}
	return s.ID
}

// The sweep's scarce resource is the indexer. A movie decide cannot match yet
// would never settle, so it would hold a slot at the head of the due queue and
// burn one search per pass forever. Revert with #209.
func TestSweepSkipsAMovie(t *testing.T) {
	h := newSweep(t, []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (Complete) [1080p]", Seeders: 40, DownloadURL: "http://example.invalid/1"},
	}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film")

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if len(h.idx.Queries) != 0 {
		t.Errorf("a movie cost %d indexer searches, want 0", len(h.idx.Queries))
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed %v for a movie, want nothing", got)
	}
	if state := readSearchState(t, h.st, id); state.lastSearched.Valid {
		t.Error("a movie entered the due queue and had its search state written")
	}
}
