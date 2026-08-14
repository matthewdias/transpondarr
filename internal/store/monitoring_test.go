package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

// The cut is read the same way at every create site, so its edges are settled
// once here rather than re-argued in catalog, refresh and airing.
func TestMonitorNew(t *testing.T) {
	from := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
	num := from

	for _, tc := range []struct {
		name   string
		from   sql.NullInt64
		number sql.NullInt64
		want   int64
	}{
		{"a cut of 1 monitors everything", from(1), num(1), 1},
		{"an item at the cut is monitored", from(50), num(50), 1},
		{"an item above the cut is monitored", from(50), num(1051), 1},
		{"an item below the cut is not", from(50), num(49), 0},
		{"a null cut monitors nothing new", sql.NullInt64{}, num(1), 0},
		{"a numberless item follows the series", from(50), sql.NullInt64{}, 1},
		{"a numberless item under a null cut is not monitored", sql.NullInt64{}, sql.NullInt64{}, 0},
	} {
		if got := MonitorNew(tc.from, tc.number); got != tc.want {
			t.Errorf("%s: MonitorNew(%v, %v) = %d, want %d", tc.name, tc.from, tc.number, got, tc.want)
		}
	}
}

// Every row that existed before per-item monitoring must read as monitored, or
// the migration silently stops the sweep for the whole library.
func TestMonitoredColumnDefaultsToOn(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	titleID := seedSearchTitle(t, st, "legacy", 1)

	// The schema default is what backfills a pre-migration row, so it is asserted
	// through an insert that names no monitored column at all.
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number) VALUES (?, 'episode', 1)`, titleID); err != nil {
		t.Fatalf("insert legacy-shaped item: %v", err)
	}
	if got := itemMonitored(t, st, titleID, 1); got != 1 {
		t.Errorf("monitored = %d on a row that named no value, want 1", got)
	}

	var cut sql.NullInt64
	if err := st.DB.QueryRowContext(ctx,
		`SELECT monitor_new_from FROM series WHERE id = ?`, titleID).Scan(&cut); err != nil {
		t.Fatalf("read monitor_new_from: %v", err)
	}
	if !cut.Valid || cut.Int64 != 1 {
		t.Errorf("monitor_new_from = %+v, want a cut of 1 so an existing series keeps monitoring everything", cut)
	}
}

// The two upserts are the reason a narrowed title stays narrowed: refresh and
// the airing sync run every few hours over exactly the items a user unmonitored.
func TestUpsertsNeverClobberAStoredMonitoredFlag(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	titleID := seedSearchTitle(t, st, "narrowed", 1)

	for _, n := range []int{1, 2} {
		if _, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: titleID, Kind: "episode",
			Number:    sql.NullInt64{Int64: int64(n), Valid: true},
			Monitored: 0,
		}); err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
	}

	// Refresh's path: DO NOTHING, so the row is untouched and reports no growth.
	rows, err := st.Q.UpsertWantedItem(ctx, db.UpsertWantedItemParams{
		SeriesID: titleID, Kind: "episode",
		Number:    sql.NullInt64{Int64: 1, Valid: true},
		Monitored: 1,
	})
	if err != nil {
		t.Fatalf("upsert existing item: %v", err)
	}
	if rows != 0 {
		t.Errorf("upsert of an existing item reported %d new rows, want 0", rows)
	}
	if got := itemMonitored(t, st, titleID, 1); got != 0 {
		t.Errorf("monitored = %d after a refresh pass, want the stored 0", got)
	}

	// The airing sync's path: airs_at moves, monitored does not.
	airs := time.Now().Add(-time.Hour)
	if err := st.Q.UpsertWantedItemAiring(ctx, db.UpsertWantedItemAiringParams{
		SeriesID: titleID, Kind: "episode",
		Number:    sql.NullInt64{Int64: 2, Valid: true},
		AirsAt:    sql.NullString{String: FormatTimestamp(airs), Valid: true},
		Monitored: 1,
	}); err != nil {
		t.Fatalf("upsert airing: %v", err)
	}
	if got := itemMonitored(t, st, titleID, 2); got != 0 {
		t.Errorf("monitored = %d after an airing sync, want the stored 0", got)
	}
	var at sql.NullString
	if err := st.DB.QueryRowContext(ctx,
		`SELECT airs_at FROM wanted_items WHERE series_id = ? AND number = 2`, titleID).Scan(&at); err != nil {
		t.Fatalf("read airs_at: %v", err)
	}
	if !at.Valid {
		t.Error("airs_at is NULL; the airing sync must still update the column it owns")
	}
}

// A concurrent title delete must cost only the ids it removed.
func TestSetWantedItemsMonitoredSkipsUnknownIDs(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	titleID := seedSearchTitle(t, st, "bulk", 1)
	first := seedSearchItem(t, st, titleID, 1, 0, nil)
	second := seedSearchItem(t, st, titleID, 2, 0, nil)

	rows, err := st.Q.SetWantedItemsMonitored(ctx, db.SetWantedItemsMonitoredParams{
		Monitored: 0, Ids: []int64{first, second, 9999},
	})
	if err != nil {
		t.Fatalf("set monitored: %v", err)
	}
	if rows != 2 {
		t.Errorf("rows affected = %d, want 2 -- the unknown id is skipped, not fatal", rows)
	}
	for _, n := range []int{1, 2} {
		if got := itemMonitored(t, st, titleID, n); got != 0 {
			t.Errorf("item %d monitored = %d, want 0", n, got)
		}
	}
}

func TestListTitleIDsForUnmonitoredItems(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	titleID := seedSearchTitle(t, st, "narrowing", 1)
	off := seedSearchItem(t, st, titleID, 1, 0, nil)
	alsoOff := seedSearchItem(t, st, titleID, 2, 0, nil)
	on := seedSearchItem(t, st, titleID, 3, 0, nil)
	if _, err := st.Q.SetWantedItemsMonitored(ctx, db.SetWantedItemsMonitoredParams{
		Monitored: 0, Ids: []int64{off, alsoOff},
	}); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	// Two unmonitored items on one title collapse to a single reset.
	ids, err := st.Q.ListTitleIDsForUnmonitoredItems(ctx, []int64{off, alsoOff, on, 9999})
	if err != nil {
		t.Fatalf("list series ids: %v", err)
	}
	if len(ids) != 1 || ids[0] != titleID {
		t.Errorf("series ids = %v, want exactly [%d] -- one reset per distinct series", ids, titleID)
	}

	// Nothing to change means nothing to reset.
	ids, err = st.Q.ListTitleIDsForUnmonitoredItems(ctx, []int64{on})
	if err != nil {
		t.Fatalf("list series ids: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("series ids = %v, want none for an already-monitored selection", ids)
	}
}

func itemMonitored(t *testing.T, st *Store, titleID int64, number int) int64 {
	t.Helper()
	var got int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT monitored FROM wanted_items WHERE series_id = ? AND number = ?`, titleID, number).Scan(&got); err != nil {
		t.Fatalf("read monitored for item %d: %v", number, err)
	}
	return got
}
