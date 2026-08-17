package catalog

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// seedItemlessTitle inserts the dead end #151 is about: a tracked title with no
// wanted items at all, because AniList published neither a count nor a schedule.
func seedItemlessTitle(t *testing.T, st *store.Store, cut int64) int64 {
	t.Helper()
	var id int64
	if err := st.DB.QueryRowContext(context.Background(),
		`INSERT INTO series (provider, provider_id, title, format, monitored, monitor_new_from)
		 VALUES ('fake', 42, 'Placeholder Saga', 'TV', 1, ?) RETURNING id`, cut).Scan(&id); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	return id
}

func itemNumbers(t *testing.T, st *store.Store, titleID int64) []int {
	t.Helper()
	rows, err := st.DB.QueryContext(context.Background(),
		`SELECT number FROM wanted_items WHERE series_id = ? ORDER BY number`, titleID)
	if err != nil {
		t.Fatalf("read items: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan number: %v", err)
		}
		out = append(out, n)
	}
	return out
}

func itemKinds(t *testing.T, st *store.Store, titleID int64) []string {
	t.Helper()
	rows, err := st.DB.QueryContext(context.Background(),
		`SELECT DISTINCT kind FROM wanted_items WHERE series_id = ? ORDER BY kind`, titleID)
	if err != nil {
		t.Fatalf("read kinds: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan kind: %v", err)
		}
		out = append(out, k)
	}
	return out
}

func searchState(t *testing.T, st *store.Store, titleID int64) (epoch, backoff int64, nextSearch, airingSynced *string) {
	t.Helper()
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_epoch, search_backoff, next_search_at, airing_synced_at FROM series WHERE id = ?`,
		titleID).Scan(&epoch, &backoff, &nextSearch, &airingSynced); err != nil {
		t.Fatalf("read search state: %v", err)
	}
	return epoch, backoff, nextSearch, airingSynced
}

// settledDeadEnd is the state a dead-end title is actually found in: its
// schedule already asked for, and a backoff accrued by passes that found nothing.
func settledDeadEnd(t *testing.T, st *store.Store, titleID int64) {
	t.Helper()
	at := store.FormatTimestamp(time.Now().Add(-time.Hour))
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET airing_synced_at = ?, search_backoff = 5, next_search_at = ? WHERE id = ?`,
		at, at, titleID); err != nil {
		t.Fatalf("settle the title: %v", err)
	}
}

func TestSetItemCountMaterializesItems(t *testing.T) {
	st := coretest.NewStore(t)
	id := seedItemlessTitle(t, st, 1)

	n, err := NewService(st, &fakeProvider{}).SetItemCount(context.Background(), id, 12)
	if err != nil {
		t.Fatalf("SetItemCount: %v", err)
	}
	if n != 12 {
		t.Errorf("reported %d items created, want 12", n)
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if got := itemNumbers(t, st, id); !slices.Equal(got, want) {
		t.Errorf("items = %v, want a contiguous %v", got, want)
	}
	if got := itemKinds(t, st, id); !slices.Equal(got, []string{"episode"}) {
		t.Errorf("kinds = %v, want every item keyed as an episode", got)
	}
}

func TestSetItemCountRefusesATitleThatAlreadyHasItems(t *testing.T) {
	st := coretest.NewStore(t)
	id := seedItemlessTitle(t, st, 1)
	if _, err := st.DB.ExecContext(context.Background(),
		`INSERT INTO wanted_items (series_id, kind, number) VALUES (?, 'episode', 1)`, id); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	n, err := NewService(st, &fakeProvider{}).SetItemCount(context.Background(), id, 12)
	if !errors.Is(err, ErrTitleHasItems) {
		t.Fatalf("SetItemCount err = %v, want ErrTitleHasItems", err)
	}
	if n != 0 {
		t.Errorf("reported %d items created on a refusal, want 0", n)
	}
	if got := itemNumbers(t, st, id); !slices.Equal(got, []int{1}) {
		t.Errorf("items = %v, want the refusal to have written nothing", got)
	}
}

func TestSetItemCountRefusesAnUnknownTitle(t *testing.T) {
	st := coretest.NewStore(t)

	if _, err := NewService(st, &fakeProvider{}).SetItemCount(context.Background(), 999, 12); err == nil {
		t.Fatal("SetItemCount on an unknown title returned no error")
	}
}

func TestSetItemCountHonoursTheMonitorCut(t *testing.T) {
	st := coretest.NewStore(t)
	// The cut is the literal 3 in both places on purpose: reading it back out of
	// the row under assertion would move with any mutation and never fail.
	id := seedItemlessTitle(t, st, 3)

	if _, err := NewService(st, &fakeProvider{}).SetItemCount(context.Background(), id, 5); err != nil {
		t.Fatalf("SetItemCount: %v", err)
	}
	if got := monitoredNumbers(t, st, id); !slices.Equal(got, []int{3, 4, 5}) {
		t.Errorf("monitored = %v, want 3,4,5 -- items below the cut are created unmonitored", got)
	}
	if got := itemNumbers(t, st, id); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("items = %v, want all five created whatever the cut", got)
	}
}

func TestSetItemCountResetsSearchCadenceAndClearsTheAiringStamp(t *testing.T) {
	st := coretest.NewStore(t)
	id := seedItemlessTitle(t, st, 1)
	settledDeadEnd(t, st, id)
	epochBefore, _, _, _ := searchState(t, st, id)

	if _, err := NewService(st, &fakeProvider{}).SetItemCount(context.Background(), id, 12); err != nil {
		t.Fatalf("SetItemCount: %v", err)
	}

	epoch, backoff, nextSearch, airingSynced := searchState(t, st, id)
	if airingSynced != nil {
		t.Errorf("airing_synced_at = %q, want cleared so the sync re-pages the title", *airingSynced)
	}
	if epoch != epochBefore+1 || backoff != 0 || nextSearch != nil {
		t.Errorf("search state = epoch %d / backoff %d / next %v, want epoch %d / 0 / nil",
			epoch, backoff, nextSearch, epochBefore+1)
	}
}

// The split counter (#188): a fill entirely below the cut still needs its air
// dates, but must not put a narrowed title back at the front of the search queue.
func TestSetItemCountBelowTheMonitorCutLeavesTheSearchQueueAlone(t *testing.T) {
	st := coretest.NewStore(t)
	id := seedItemlessTitle(t, st, 9)
	settledDeadEnd(t, st, id)
	epochBefore, _, _, _ := searchState(t, st, id)

	if _, err := NewService(st, &fakeProvider{}).SetItemCount(context.Background(), id, 5); err != nil {
		t.Fatalf("SetItemCount: %v", err)
	}

	epoch, backoff, _, airingSynced := searchState(t, st, id)
	if airingSynced != nil {
		t.Errorf("airing_synced_at = %q, want cleared -- the air-date sync ignores monitoring", *airingSynced)
	}
	if epoch != epochBefore || backoff != 5 {
		t.Errorf("search state = epoch %d / backoff %d, want %d / 5 left untouched", epoch, backoff, epochBefore)
	}
}
