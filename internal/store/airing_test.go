package store

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// The unique identity index is what lets the refresh upsert say "this episode
// already exists" instead of inserting a second row for it.
func TestWantedItemIdentityIsUnique(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	seriesID := insertSeries(t, st, 1)
	const insert = `INSERT INTO wanted_items (series_id, kind, number) VALUES (?, 'episode', 1)`
	if _, err := st.DB.ExecContext(ctx, insert, seriesID); err != nil {
		t.Fatalf("insert first item: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, insert, seriesID); err == nil {
		t.Fatal("a duplicate (series_id, kind, number) was accepted; the identity index is missing")
	}
}

// A live database that already holds duplicates must survive the upgrade rather
// than fail the index creation, keeping the row that carries state. Driven by
// rolling the airing migration back, dirtying the data, and re-applying it.
func TestAiringMigrationDedupesWantedItems(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	seriesID := insertSeries(t, st, 2)
	// DownTo pins the rollback at the pre-airing schema; a one-step Down would
	// start unwinding whichever migration is newest once 00011 lands.
	if err := goose.DownTo(st.DB, "migrations", 9); err != nil {
		t.Fatalf("roll back to the pre-airing schema: %v", err)
	}

	// The held row is inserted last, so "keep the survivor with state" and "keep
	// the lowest id" disagree — only the former passes. The column is still named
	// have at this schema version; 00019 renames it.
	for _, have := range []int{0, 1} {
		if _, err := st.DB.ExecContext(ctx,
			`INSERT INTO wanted_items (series_id, kind, number, have) VALUES (?, 'episode', 1, ?)`,
			seriesID, have); err != nil {
			t.Fatalf("insert duplicate item (have=%d): %v", have, err)
		}
	}

	if err := goose.Up(st.DB, "migrations"); err != nil {
		t.Fatalf("re-apply airing migration over duplicates: %v", err)
	}

	var count, inLibrary int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(in_library), 0) FROM wanted_items WHERE series_id = ?`,
		seriesID).Scan(&count, &inLibrary); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d items after the upgrade, want 1", count)
	}
	if inLibrary != 1 {
		t.Error("dedupe kept the empty row and dropped the one already in the library")
	}
}

func insertSeries(t *testing.T, st *Store, anilistID int64) int64 {
	t.Helper()
	var id int64
	if err := st.DB.QueryRowContext(context.Background(),
		`INSERT INTO series (provider, provider_id, title) VALUES ('anilist', ?, 'Placeholder') RETURNING id`,
		anilistID).Scan(&id); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	return id
}
