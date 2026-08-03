package store

import (
	"context"
	"testing"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func seedEventSeries(t *testing.T, st *Store, title string) int64 {
	t.Helper()
	series, err := st.Q.CreateSeries(context.Background(), db.CreateSeriesParams{Title: title, Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	return series.ID
}

func appendEvent(t *testing.T, st *Store, seriesID int64, number int64, event, createdAt string) {
	t.Helper()
	// Direct SQL so the test controls created_at, which datetime('now') would not allow.
	if _, err := st.DB.Exec(
		`INSERT INTO grab_events (series_id, wanted_item_id, item_number, item_kind, info_hash, release_title, event, created_at)
		 VALUES (?, ?, ?, 'episode', 'hash', 'rel', ?, ?)`,
		seriesID, number, number, event, createdAt,
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func TestListGrabEventsPageNewestFirstWithSeriesTitle(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	a := seedEventSeries(t, st, "Alpha")
	b := seedEventSeries(t, st, "Beta")

	appendEvent(t, st, a, 1, "grabbed", "2026-01-01 10:00:00")
	appendEvent(t, st, b, 2, "imported", "2026-01-02 10:00:00")
	appendEvent(t, st, a, 3, "failed", "2026-01-03 10:00:00")

	rows, err := st.Q.ListGrabEventsPage(ctx, 10)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 events, got %d", len(rows))
	}
	if rows[0].Event != "failed" || rows[0].SeriesTitle != "Alpha" {
		t.Errorf("rows[0] = %s/%s, want failed/Alpha", rows[0].Event, rows[0].SeriesTitle)
	}
	if rows[1].Event != "imported" || rows[1].SeriesTitle != "Beta" {
		t.Errorf("rows[1] = %s/%s, want imported/Beta", rows[1].Event, rows[1].SeriesTitle)
	}
	if rows[2].Event != "grabbed" {
		t.Errorf("rows[2].Event = %s, want grabbed", rows[2].Event)
	}
}

// Paging through events sharing one created_at must visit every event exactly
// once — the id tie-break is what the cursor's correctness rests on.
func TestListGrabEventsPageBeforeTieBreaksOnID(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	s := seedEventSeries(t, st, "Alpha")

	const stamp = "2026-01-05 12:00:00"
	for n := int64(1); n <= 4; n++ {
		appendEvent(t, st, s, n, "grabbed", stamp)
	}

	first, err := st.Q.ListGrabEventsPage(ctx, 1)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first page: expected 1 row, got %d", len(first))
	}

	seen := map[int64]bool{first[0].ID: true}
	cursorAt, cursorID := first[0].CreatedAt, first[0].ID
	for {
		page, err := st.Q.ListGrabEventsPageBefore(ctx, db.ListGrabEventsPageBeforeParams{
			CreatedAt: cursorAt, CreatedAt_2: cursorAt, ID: cursorID, Limit: 1,
		})
		if err != nil {
			t.Fatalf("page after (%s, %d): %v", cursorAt, cursorID, err)
		}
		if len(page) == 0 {
			break
		}
		row := page[0]
		if seen[row.ID] {
			t.Fatalf("event %d returned twice", row.ID)
		}
		seen[row.ID] = true
		cursorAt, cursorID = row.CreatedAt, row.ID
	}
	if len(seen) != 4 {
		t.Errorf("paged through %d events, want all 4", len(seen))
	}
}

func TestListSeriesGrabEventsScopesToSeries(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	a := seedEventSeries(t, st, "Alpha")
	b := seedEventSeries(t, st, "Beta")

	appendEvent(t, st, a, 1, "grabbed", "2026-01-01 10:00:00")
	appendEvent(t, st, a, 1, "imported", "2026-01-02 10:00:00")
	appendEvent(t, st, b, 5, "grabbed", "2026-01-03 10:00:00")

	events, err := st.Q.ListSeriesGrabEvents(ctx, a)
	if err != nil {
		t.Fatalf("list series events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events for series a, got %d", len(events))
	}
	if events[0].Event != "imported" || events[1].Event != "grabbed" {
		t.Errorf("order = %s, %s; want imported, grabbed", events[0].Event, events[1].Event)
	}
	for _, e := range events {
		if e.SeriesID != a {
			t.Errorf("event %d has series %d, want %d", e.ID, e.SeriesID, a)
		}
	}
}
