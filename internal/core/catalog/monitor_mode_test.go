package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// longRunner is the shape #160 is about: a title with a back catalogue and one
// episode still to come, which is what "future only" has to cut against.
func longRunner(next int) *fakeProvider {
	items := make([]metadata.ItemMeta, 0, 6)
	for n := 1; n <= 6; n++ {
		items = append(items, metadata.ItemMeta{Number: n})
	}
	return &fakeProvider{
		meta: metadata.TitleMeta{
			ProviderID: 42, Titles: metadata.Titles{Romaji: "Placeholder Saga"},
			Format: "TV", Status: "RELEASING", NextItem: next,
		},
		items: items,
	}
}

func monitoredNumbers(t *testing.T, st *store.Store, titleID int64) []int {
	t.Helper()
	rows, err := st.DB.QueryContext(context.Background(),
		`SELECT number FROM wanted_items WHERE series_id = ? AND monitored = 1 ORDER BY number`, titleID)
	if err != nil {
		t.Fatalf("read monitored items: %v", err)
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

func storedCut(t *testing.T, st *store.Store, titleID int64) sql.NullInt64 {
	t.Helper()
	var cut sql.NullInt64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT monitor_new_from FROM series WHERE id = ?`, titleID).Scan(&cut); err != nil {
		t.Fatalf("read monitor_new_from: %v", err)
	}
	return cut
}

// The add-time choice is what makes #160 tractable: a long-runner must be
// narrowed before the first sweep tick, not by clicking a thousand checkboxes
// against a 15-minute clock.
func TestAddTitleAppliesTheMonitorMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      MonitorMode
		next      int
		wantItems []int
		wantCut   sql.NullInt64
	}{
		{
			name: "all monitors everything, now and later",
			mode: MonitorAll, next: 7,
			wantItems: []int{1, 2, 3, 4, 5, 6},
			wantCut:   sql.NullInt64{Int64: 1, Valid: true},
		},
		{
			name: "future only monitors from the next broadcast",
			mode: MonitorFuture, next: 7,
			wantItems: nil,
			wantCut:   sql.NullInt64{Int64: 7, Valid: true},
		},
		{
			name: "future only keeps an already-created item at the cut",
			mode: MonitorFuture, next: 5,
			wantItems: []int{5, 6},
			wantCut:   sql.NullInt64{Int64: 5, Valid: true},
		},
		{
			// A FINISHED title, or a cache snapshot written before NextItem existed.
			// Falling back past the end degrades to "no back-catalogue chase", which
			// is the safe direction; deriving 1 would be the failure this exists for.
			name: "future only without a next broadcast falls past the end",
			mode: MonitorFuture, next: 0,
			wantItems: nil,
			wantCut:   sql.NullInt64{Int64: 7, Valid: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := coretest.NewStore(t)
			prov := longRunner(tc.next)
			svc := NewService(st, prov)

			title, err := svc.AddTitle(context.Background(), prov.Name(), 42, true, tc.mode, 0)
			if err != nil {
				t.Fatalf("AddSeries: %v", err)
			}
			got := monitoredNumbers(t, st, title.ID)
			if len(got) != len(tc.wantItems) {
				t.Fatalf("monitored items = %v, want %v", got, tc.wantItems)
			}
			for i := range got {
				if got[i] != tc.wantItems[i] {
					t.Fatalf("monitored items = %v, want %v", got, tc.wantItems)
				}
			}
			if cut := storedCut(t, st, title.ID); cut != tc.wantCut {
				t.Errorf("monitor_new_from = %+v, want %+v", cut, tc.wantCut)
			}
		})
	}
}

// The zero value must not read as a choice: coercing it to "all" would have a
// caller that forgot the argument silently chase a back catalogue.
func TestAddTitleRejectsAModeItDoesNotKnow(t *testing.T) {
	for _, mode := range []MonitorMode{"", "none", "nonsense"} {
		st := coretest.NewStore(t)
		prov := longRunner(7)
		svc := NewService(st, prov)

		_, err := svc.AddTitle(context.Background(), prov.Name(), 42, true, mode, 0)
		if !errors.Is(err, ErrUnknownMonitorMode) {
			t.Errorf("AddSeries(%q) = %v, want ErrUnknownMonitorMode", mode, err)
		}
	}
}

// Every mode is self-healing, which is why there is no "none": a null cut
// monitors nothing new forever, and nothing can edit the cut after the add.
func TestNoModeWritesANullCut(t *testing.T) {
	for _, mode := range []MonitorMode{MonitorAll, MonitorFuture} {
		for _, next := range []int{0, 7} {
			cut, err := monitorCut(mode, metadata.TitleMeta{NextItem: next}, 6)
			if err != nil {
				t.Fatalf("monitorCut(%q): %v", mode, err)
			}
			if !cut.Valid {
				t.Errorf("mode %q with next %d wrote a null cut, which is permanent", mode, next)
			}
		}
	}
}

// The cut is a decision taken, not a policy re-derived: it records the numeric
// boundary so an episode created six months later needs no follow-up write, and
// so a re-evaluated "future" can never unmonitor something already waited for.
func TestMonitorCutIsEvaluableByStoreMonitorNew(t *testing.T) {
	cut, err := monitorCut(MonitorFuture, metadata.TitleMeta{NextItem: 1051}, 1050)
	if err != nil {
		t.Fatalf("monitorCut: %v", err)
	}
	if got := store.MonitorNew(cut, sql.NullInt64{Int64: 1051, Valid: true}); got != 1 {
		t.Errorf("the next episode reads as %d, want monitored", got)
	}
	if got := store.MonitorNew(cut, sql.NullInt64{Int64: 1200, Valid: true}); got != 1 {
		t.Errorf("an episode created much later reads as %d, want monitored with no follow-up write", got)
	}
	if got := store.MonitorNew(cut, sql.NullInt64{Int64: 900, Valid: true}); got != 0 {
		t.Errorf("a gap-filled back-catalogue episode reads as %d, want unmonitored", got)
	}
}
