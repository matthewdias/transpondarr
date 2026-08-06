package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	return st
}

func TestUpsertGrabIsOnePerItemAndLeavesHaveUntouched(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	series, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "X", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID:  series.ID,
		Kind:      "episode",
		Number:    sql.NullInt64{Int64: 1, Valid: true},
		InLibrary: 0,
	})
	if err != nil {
		t.Fatalf("create wanted item: %v", err)
	}

	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: "hash1", ReleaseTitle: "rel1", Status: "grabbed",
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Re-grabbing the same item replaces its grab rather than adding a second.
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: "hash2", ReleaseTitle: "rel2", Status: "grabbed",
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if old, _ := st.Q.ListGrabsByInfoHash(ctx, "hash1"); len(old) != 0 {
		t.Errorf("old grab for hash1 still present: %d rows", len(old))
	}
	cur, err := st.Q.ListGrabsByInfoHash(ctx, "hash2")
	if err != nil {
		t.Fatalf("list by hash: %v", err)
	}
	if len(cur) != 1 {
		t.Fatalf("expected 1 grab for hash2, got %d", len(cur))
	}
	if cur[0].WantedItemID != item.ID || cur[0].ReleaseTitle != "rel2" {
		t.Errorf("grab row = %+v, want item %d / rel2", cur[0], item.ID)
	}

	bySeries, err := st.Q.ListGrabsBySeries(ctx, series.ID)
	if err != nil {
		t.Fatalf("list by series: %v", err)
	}
	if len(bySeries) != 1 {
		t.Errorf("expected 1 grab for the series, got %d", len(bySeries))
	}

	// A grab must not mark the item as had — that is import's job.
	items, err := st.Q.ListWantedItems(ctx, series.ID)
	if err != nil {
		t.Fatalf("list wanted items: %v", err)
	}
	if items[0].InLibrary != 0 {
		t.Errorf("have = %d after grab, want 0 (import sets have, not grab)", items[0].InLibrary)
	}
}

// A batch inserts one grab per covered item, all sharing the torrent's info hash.
func TestUpsertGrabBatchSharesInfoHash(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	series, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "X", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for n := 1; n <= 3; n++ {
		item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: series.ID, Kind: "episode",
			Number: sql.NullInt64{Int64: int64(n), Valid: true},
		})
		if err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
		if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: item.ID, InfoHash: "batchhash", ReleaseTitle: "pack", Status: "grabbed",
		}); err != nil {
			t.Fatalf("grab item %d: %v", n, err)
		}
	}

	grabs, err := st.Q.ListGrabsByInfoHash(ctx, "batchhash")
	if err != nil {
		t.Fatalf("list by hash: %v", err)
	}
	if len(grabs) != 3 {
		t.Errorf("expected 3 grabs sharing the batch hash, got %d", len(grabs))
	}
}
