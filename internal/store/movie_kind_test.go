package store

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// idx_wanted_items_identity is (series_id, kind, number), so a re-keyed
// ('movie', 1) does not conflict with a legacy ('episode', 1): without the
// backfill the first refresh after deploy silently doubles every pre-existing
// movie, and the title reads 1/2 forever.
func TestMigrationBackfillsMovieItemKind(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	if err := goose.DownTo(st.DB, "migrations", 21); err != nil {
		t.Fatalf("roll back to the pre-year schema: %v", err)
	}

	var seriesID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (provider, provider_id, title, format, monitored)
		 VALUES ('anilist', 9001, 'Example Film', 'MOVIE', 1) RETURNING id`).Scan(&seriesID); err != nil {
		t.Fatalf("seed movie series: %v", err)
	}
	var itemID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number, in_library) VALUES (?, 'episode', 1, 1) RETURNING id`,
		seriesID).Scan(&itemID); err != nil {
		t.Fatalf("seed wanted item: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO grabs (wanted_item_id, info_hash, release_title, status)
		 VALUES (?, 'aabbcc', '[ExampleSubs] Example Film 1080p', 'imported')`, itemID); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	if err := goose.Up(st.DB, "migrations"); err != nil {
		t.Fatalf("apply the title-year migration: %v", err)
	}

	var (
		gotID     int64
		kind      string
		number    int64
		inLibrary int64
		items     int
	)
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wanted_items WHERE series_id = ?`, seriesID).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items != 1 {
		t.Fatalf("items after migration = %d, want 1", items)
	}
	if err := st.DB.QueryRowContext(ctx,
		`SELECT id, kind, number, in_library FROM wanted_items WHERE series_id = ?`,
		seriesID).Scan(&gotID, &kind, &number, &inLibrary); err != nil {
		t.Fatalf("read migrated item: %v", err)
	}
	if kind != "movie" {
		t.Errorf("kind = %q, want movie", kind)
	}
	if gotID != itemID || number != 1 || inLibrary != 1 {
		t.Errorf("item = (id %d, number %d, in_library %d), want (%d, 1, 1)", gotID, number, inLibrary, itemID)
	}

	var grabs int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM grabs g JOIN wanted_items w ON w.id = g.wanted_item_id WHERE w.series_id = ?`,
		seriesID).Scan(&grabs); err != nil {
		t.Fatalf("count joined grabs: %v", err)
	}
	if grabs != 1 {
		t.Errorf("grabs still joined = %d, want 1", grabs)
	}
}

func TestMigrationLeavesNonMovieItemKind(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	if err := goose.DownTo(st.DB, "migrations", 21); err != nil {
		t.Fatalf("roll back to the pre-year schema: %v", err)
	}
	var seriesID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (provider, provider_id, title, format, monitored)
		 VALUES ('anilist', 9002, 'Example Show', 'TV', 1) RETURNING id`).Scan(&seriesID); err != nil {
		t.Fatalf("seed series: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number) VALUES (?, 'episode', 1)`, seriesID); err != nil {
		t.Fatalf("seed wanted item: %v", err)
	}

	if err := goose.Up(st.DB, "migrations"); err != nil {
		t.Fatalf("apply the title-year migration: %v", err)
	}

	var kind string
	if err := st.DB.QueryRowContext(ctx,
		`SELECT kind FROM wanted_items WHERE series_id = ?`, seriesID).Scan(&kind); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if kind != "episode" {
		t.Errorf("kind = %q, want episode", kind)
	}
}

func TestCreateSeriesDefaultsYearToUnknown(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	var seriesID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (provider, provider_id, title, format, monitored)
		 VALUES ('anilist', 9003, 'Example Show', 'TV', 1) RETURNING id`).Scan(&seriesID); err != nil {
		t.Fatalf("seed series: %v", err)
	}
	var year int64
	if err := st.DB.QueryRowContext(ctx, `SELECT year FROM series WHERE id = ?`, seriesID).Scan(&year); err != nil {
		t.Fatalf("read year: %v", err)
	}
	if year != 0 {
		t.Errorf("year = %d, want 0 (no year on record)", year)
	}
}
