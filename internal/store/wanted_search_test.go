package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seedSearchSeries inserts a series and returns its id.
func seedSearchSeries(t *testing.T, st *Store, title string, monitored int64) int64 {
	t.Helper()
	s, err := st.Q.CreateSeries(context.Background(), db.CreateSeriesParams{
		Title: title, Format: "TV", Monitored: monitored,
	})
	if err != nil {
		t.Fatalf("create series %q: %v", title, err)
	}
	return s.ID
}

// seedSearchItem inserts one wanted item; airsAt is optional (nil = unscheduled).
func seedSearchItem(t *testing.T, st *Store, seriesID int64, number int, have int64, airsAt *time.Time) int64 {
	t.Helper()
	var at sql.NullString
	if airsAt != nil {
		at = sql.NullString{String: FormatTimestamp(*airsAt), Valid: true}
	}
	item, err := st.Q.CreateWantedItem(context.Background(), db.CreateWantedItemParams{
		SeriesID: seriesID, Kind: "episode",
		Number: sql.NullInt64{Int64: int64(number), Valid: true},
		Have:   have,
	})
	if err != nil {
		t.Fatalf("create item %d: %v", number, err)
	}
	if at.Valid {
		if _, err := st.DB.ExecContext(context.Background(),
			`UPDATE wanted_items SET airs_at = ? WHERE id = ?`, at.String, item.ID); err != nil {
			t.Fatalf("set airs_at on item %d: %v", number, err)
		}
	}
	return item.ID
}

func seedSearchGrab(t *testing.T, st *Store, itemID int64, status string) {
	t.Helper()
	if _, err := st.Q.UpsertGrab(context.Background(), db.UpsertGrabParams{
		WantedItemID: itemID, InfoHash: "hash", ReleaseTitle: "release", Status: status,
	}); err != nil {
		t.Fatalf("upsert grab: %v", err)
	}
}

func dueTitles(t *testing.T, st *Store, now time.Time, limit int64) []string {
	t.Helper()
	stamp := FormatTimestamp(now)
	rows, err := st.Q.ListSeriesDueWantedSearch(context.Background(), db.ListSeriesDueWantedSearchParams{
		NextSearchAt: sql.NullString{String: stamp, Valid: true},
		AirsAt:       sql.NullString{String: stamp, Valid: true},
		Limit:        limit,
	})
	if err != nil {
		t.Fatalf("list series due a wanted search: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// The due predicate is the whole budget control for the sweep: it must admit
// only monitored series that actually have something searchable right now.
func TestListSeriesDueWantedSearchPredicate(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	// Included: a monitored series with an aired, ungrabbed item.
	seedSearchItem(t, st, seedSearchSeries(t, st, "aired", 1), 1, 0, &past)
	// Included: no air date at all — AniList coverage gaps are normal, not a reason
	// to never search.
	seedSearchItem(t, st, seedSearchSeries(t, st, "unscheduled", 1), 1, 0, nil)
	// Included: a previous grab that failed is wanted again.
	failed := seedSearchSeries(t, st, "failed-grab", 1)
	seedSearchGrab(t, st, seedSearchItem(t, st, failed, 1, 0, &past), "failed")

	// Excluded: unmonitored.
	seedSearchItem(t, st, seedSearchSeries(t, st, "unmonitored", 0), 1, 0, &past)
	// Excluded: everything already had.
	seedSearchItem(t, st, seedSearchSeries(t, st, "all-had", 1), 1, 1, &past)
	// Excluded: the only wanted item is already in flight.
	inFlight := seedSearchSeries(t, st, "in-flight", 1)
	seedSearchGrab(t, st, seedSearchItem(t, st, inFlight, 1, 0, &past), "grabbed")
	// Excluded: a deferred grab is settled and must not be re-grabbed.
	deferred := seedSearchSeries(t, st, "deferred", 1)
	seedSearchGrab(t, st, seedSearchItem(t, st, deferred, 1, 0, &past), "import_deferred")
	// Excluded: nothing has aired yet.
	seedSearchItem(t, st, seedSearchSeries(t, st, "future-only", 1), 1, 0, &future)
	// Excluded: backed off until later, even though it has an aired item.
	backedOff := seedSearchSeries(t, st, "backed-off", 1)
	seedSearchItem(t, st, backedOff, 1, 0, &past)
	if _, err := st.DB.ExecContext(ctx, `UPDATE series SET next_search_at = ? WHERE id = ?`,
		FormatTimestamp(future), backedOff); err != nil {
		t.Fatalf("set next_search_at: %v", err)
	}

	got := dueTitles(t, st, now, 100)
	for _, want := range []string{"aired", "unscheduled", "failed-grab"} {
		if !contains(got, want) {
			t.Errorf("due set %v is missing %q", got, want)
		}
	}
	for _, unwanted := range []string{"unmonitored", "all-had", "in-flight", "deferred", "future-only", "backed-off"} {
		if contains(got, unwanted) {
			t.Errorf("due set %v wrongly includes %q", got, unwanted)
		}
	}
	if len(got) != 3 {
		t.Errorf("due set = %v, want exactly the three searchable series", got)
	}
}

// Never-searched series sort first so a freshly added title is not queued behind
// a backlog of routine re-searches, and the limit bounds one pass' budget.
func TestListSeriesDueWantedSearchOrdersNeverSearchedFirstAndLimits(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-24 * time.Hour)

	for _, name := range []string{"due-earlier", "due-later", "never-searched"} {
		seedSearchItem(t, st, seedSearchSeries(t, st, name, 1), 1, 0, &past)
	}
	for name, at := range map[string]time.Time{
		"due-earlier": now.Add(-2 * time.Hour),
		"due-later":   now.Add(-1 * time.Hour),
	} {
		if _, err := st.DB.ExecContext(ctx, `UPDATE series SET next_search_at = ? WHERE title = ?`,
			FormatTimestamp(at), name); err != nil {
			t.Fatalf("set next_search_at for %s: %v", name, err)
		}
	}

	got := dueTitles(t, st, now, 100)
	want := []string{"never-searched", "due-earlier", "due-later"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("due order = %v, want %v", got, want)
		}
	}
	if limited := dueTitles(t, st, now, 2); len(limited) != 2 {
		t.Errorf("limited due set = %v, want 2 rows", limited)
	}
}

// setNextSearchAt postpones a series, which is the state a gap recovery undoes.
func setNextSearchAt(t *testing.T, st *Store, id int64, at time.Time) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 6, next_search_at = ? WHERE id = ?`,
		FormatTimestamp(at), id); err != nil {
		t.Fatalf("set next_search_at on series %d: %v", id, err)
	}
}

func gapTitles(t *testing.T, st *Store, now, lo, hi time.Time, limit int64) []string {
	t.Helper()
	rows, err := st.Q.ListBackedOffSeriesWantedInWindow(context.Background(),
		db.ListBackedOffSeriesWantedInWindowParams{
			NextSearchAt: sql.NullString{String: FormatTimestamp(now), Valid: true},
			AirsAt:       sql.NullString{String: FormatTimestamp(lo), Valid: true},
			AirsAt_2:     sql.NullString{String: FormatTimestamp(hi), Valid: true},
			Limit:        limit,
		})
	if err != nil {
		t.Fatalf("list backed-off series wanted in window: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

// The gap-recovery set is the sweep's wanted predicate narrowed to a broadcast
// window and to series the ladder is actually postponing: a reset buys a due
// series nothing, and would spend one of the bounded slots.
func TestListBackedOffSeriesWantedInWindowPredicate(t *testing.T) {
	st := tempStore(t)
	now := time.Now()
	lo, hi := now.Add(-3*time.Hour), now
	inside := now.Add(-1 * time.Hour)
	later := now.Add(20 * time.Hour)

	// Included: postponed, with an aired-inside-the-window item still wanted.
	setNextSearchAt(t, st, mustSeed(t, st, "in-window", 1, 1, 0, &inside), later)
	// Included: a previous grab that failed leaves the item wanted again.
	failed := mustSeed(t, st, "failed-grab", 1, 1, 0, &inside)
	seedSearchGrab(t, st, itemOf(t, st, failed), "failed")
	setNextSearchAt(t, st, failed, later)

	// Excluded: unmonitored.
	setNextSearchAt(t, st, mustSeed(t, st, "unmonitored", 0, 1, 0, &inside), later)
	// Excluded: already due -- the sweep reaches it on the next tick regardless.
	setNextSearchAt(t, st, mustSeed(t, st, "already-due", 1, 1, 0, &inside), now.Add(-time.Minute))
	// Excluded: never searched, so it is at the front of the queue already.
	mustSeed(t, st, "never-searched", 1, 1, 0, &inside)
	// Excluded: aired before the window opened -- the feed never owed it.
	setNextSearchAt(t, st, mustSeed(t, st, "aired-before", 1, 1, 0, ptr(now.Add(-5*time.Hour))), later)
	// Excluded: the window is half-open, so a broadcast at hi belongs to the next one.
	setNextSearchAt(t, st, mustSeed(t, st, "aired-at-hi", 1, 1, 0, &hi), later)
	// Excluded: not broadcast yet.
	setNextSearchAt(t, st, mustSeed(t, st, "aired-after", 1, 1, 0, ptr(now.Add(time.Hour))), later)
	// Excluded: no air date at all -- nothing places it inside the gap.
	setNextSearchAt(t, st, mustSeed(t, st, "unscheduled", 1, 1, 0, nil), later)
	// Excluded: already in the library.
	setNextSearchAt(t, st, mustSeed(t, st, "all-had", 1, 1, 1, &inside), later)
	// Excluded: a settled grab holds the item.
	settled := mustSeed(t, st, "in-flight", 1, 1, 0, &inside)
	seedSearchGrab(t, st, itemOf(t, st, settled), "grabbed")
	setNextSearchAt(t, st, settled, later)

	got := gapTitles(t, st, now, lo, hi, 100)
	for _, want := range []string{"in-window", "failed-grab"} {
		if !contains(got, want) {
			t.Errorf("gap set %v is missing %q", got, want)
		}
	}
	if len(got) != 2 {
		t.Errorf("gap set = %v, want exactly the two series a reset helps", got)
	}
}

// Furthest-postponed first, because the ladder would keep those waiting longest
// -- and the limit is what keeps a routine gap from resetting more series than
// the sweep can search.
func TestListBackedOffSeriesWantedInWindowOrdersFurthestFirstAndLimits(t *testing.T) {
	st := tempStore(t)
	now := time.Now()
	inside := now.Add(-1 * time.Hour)

	for i, name := range []string{"soonest", "middle", "furthest"} {
		setNextSearchAt(t, st, mustSeed(t, st, name, 1, 1, 0, &inside),
			now.Add(time.Duration(i+1)*4*time.Hour))
	}

	got := gapTitles(t, st, now, now.Add(-3*time.Hour), now, 100)
	want := []string{"furthest", "middle", "soonest"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("gap order = %v, want %v", got, want)
		}
	}
	limited := gapTitles(t, st, now, now.Add(-3*time.Hour), now, 2)
	if len(limited) != 2 || limited[0] != "furthest" || limited[1] != "middle" {
		t.Errorf("limited gap set = %v, want the two furthest-postponed series", limited)
	}
}

// mustSeed inserts a series with one wanted item and returns the series id.
func mustSeed(t *testing.T, st *Store, title string, monitored int64, number int, have int64, airsAt *time.Time) int64 {
	t.Helper()
	id := seedSearchSeries(t, st, title, monitored)
	seedSearchItem(t, st, id, number, have, airsAt)
	return id
}

// itemOf returns the only wanted item of a series seeded by mustSeed.
func itemOf(t *testing.T, st *Store, seriesID int64) int64 {
	t.Helper()
	var id int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT id FROM wanted_items WHERE series_id = ?`, seriesID).Scan(&id); err != nil {
		t.Fatalf("read item of series %d: %v", seriesID, err)
	}
	return id
}

func ptr(t time.Time) *time.Time { return &t }

// The write is guarded on the value read at selection so a concurrent reset — a
// series that just grew, or was re-monitored — wins over a stale backoff.
// The guard has to survive the case that motivated it: a due series carries
// next_search_at NULL, and a reset writes NULL too, so the column alone cannot
// tell "nobody touched this" from "a reset landed while I searched".
func TestSetSeriesSearchStateGuardsOnReadEpoch(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	id := seedSearchSeries(t, st, "guarded", 1)
	now := time.Now()

	epoch := readEpoch(t, st, id) // read at selection: never searched, NULL, epoch 0

	// A reset lands mid-sweep. next_search_at is NULL before and after.
	if err := st.Q.ResetSeriesSearchState(ctx, id); err != nil {
		t.Fatalf("interleave a reset: %v", err)
	}

	// Zero rows is how the caller learns its write lost, rather than assuming it
	// landed.
	rows, err := st.Q.SetSeriesSearchState(ctx, db.SetSeriesSearchStateParams{
		ID:             id,
		LastSearchedAt: sql.NullString{String: FormatTimestamp(now), Valid: true},
		SearchBackoff:  3,
		NextSearchAt:   sql.NullString{String: FormatTimestamp(now.Add(4 * time.Hour)), Valid: true},
		SearchEpoch:    epoch,
	})
	if err != nil {
		t.Fatalf("set search state: %v", err)
	}
	if rows != 0 {
		t.Errorf("rows affected = %d, want 0 — the stale write must report that it lost", rows)
	}

	backoff, next := readCadence(t, st, id)
	if backoff != 0 || next.Valid {
		t.Errorf("backoff = %d, next_search_at = %+v; want 0 and NULL — the stale write clobbered a concurrent reset",
			backoff, next)
	}

	// With the current epoch, the same write lands.
	rows, err = st.Q.SetSeriesSearchState(ctx, db.SetSeriesSearchStateParams{
		ID:             id,
		LastSearchedAt: sql.NullString{String: FormatTimestamp(now), Valid: true},
		SearchBackoff:  3,
		NextSearchAt:   sql.NullString{String: FormatTimestamp(now.Add(4 * time.Hour)), Valid: true},
		SearchEpoch:    readEpoch(t, st, id),
	})
	if err != nil {
		t.Fatalf("set search state (matching guard): %v", err)
	}
	if rows != 1 {
		t.Errorf("rows affected = %d, want 1 once the guard matches", rows)
	}
	if backoff, _ := readCadence(t, st, id); backoff != 3 {
		t.Errorf("search_backoff = %d, want 3 once the guard matches", backoff)
	}
}

func readEpoch(t *testing.T, st *Store, id int64) int64 {
	t.Helper()
	var epoch int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_epoch FROM series WHERE id = ?`, id).Scan(&epoch); err != nil {
		t.Fatalf("read search_epoch: %v", err)
	}
	return epoch
}

func readCadence(t *testing.T, st *Store, id int64) (int64, sql.NullString) {
	t.Helper()
	var backoff int64
	var next sql.NullString
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, id).Scan(&backoff, &next); err != nil {
		t.Fatalf("read cadence: %v", err)
	}
	return backoff, next
}

// A reset clears the cadence outright, which is what refresh growth and a
// re-monitored series need.
func TestResetSeriesSearchState(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	id := seedSearchSeries(t, st, "reset", 1)

	if _, err := st.DB.ExecContext(ctx,
		`UPDATE series SET search_backoff = 5, next_search_at = ? WHERE id = ?`,
		FormatTimestamp(time.Now().Add(24*time.Hour)), id); err != nil {
		t.Fatalf("seed backoff: %v", err)
	}
	before := readEpoch(t, st, id)
	if err := st.Q.ResetSeriesSearchState(ctx, id); err != nil {
		t.Fatalf("reset: %v", err)
	}

	backoff, next := readCadence(t, st, id)
	if backoff != 0 || next.Valid {
		t.Errorf("after reset backoff = %d, next_search_at = %+v, want 0 and NULL", backoff, next)
	}
	// The bump is what an in-flight sweep's guard trips over.
	if got := readEpoch(t, st, id); got != before+1 {
		t.Errorf("search_epoch = %d, want %d", got, before+1)
	}
}

// The sweep needs each item's grab status alongside the item, so it can tell an
// in-flight episode from a wanted one without a second query per item.
func TestListWantedItemsWithGrabState(t *testing.T) {
	st := tempStore(t)
	id := seedSearchSeries(t, st, "with-state", 1)
	seedSearchItem(t, st, id, 1, 0, nil)
	seedSearchGrab(t, st, seedSearchItem(t, st, id, 2, 0, nil), "grabbed")

	rows, err := st.Q.ListWantedItemsWithGrabState(context.Background(), id)
	if err != nil {
		t.Fatalf("list items with grab state: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].GrabStatus.Valid {
		t.Errorf("item 1 grab status = %+v, want none", rows[0].GrabStatus)
	}
	if rows[1].GrabStatus.String != "grabbed" {
		t.Errorf("item 2 grab status = %q, want grabbed", rows[1].GrabStatus.String)
	}
}
