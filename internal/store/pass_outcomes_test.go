package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func outcomeItem(t *testing.T, st *Store, seriesID int64, number int64) int64 {
	t.Helper()
	row, err := st.Q.CreateWantedItem(context.Background(), db.CreateWantedItemParams{
		SeriesID: seriesID, Kind: "episode",
		Number:    sql.NullInt64{Int64: number, Valid: true},
		Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create wanted item %d: %v", number, err)
	}
	return row.ID
}

// The table is bounded by wanted_items, not by pass count: a second pass
// overwrites the first's row rather than appending. Every column takes the new
// pass's value, so an outcome carrying no hold clears a stale held_until --
// otherwise a pin window that has since closed outlives the decision it came
// from.
func TestUpsertPassOutcomeReplacesInPlace(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	series := blocklistSeries(t, st, "Pass Outcome Upsert")
	item := outcomeItem(t, st, series.ID, 1)

	if err := st.Q.UpsertPassOutcome(ctx, db.UpsertPassOutcomeParams{
		WantedItemID: item,
		Outcome:      "pin_held",
		Source:       "sweep",
		ReleaseTitle: "[OtherSubs] Sample Show - 01 [1080p]",
		Detail:       "waiting for the pinned group",
		HeldUntil:    nullString("2026-08-05T12:00:00Z"),
		RecordedAt:   "2026-08-05T06:00:00Z",
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := st.Q.UpsertPassOutcome(ctx, db.UpsertPassOutcomeParams{
		WantedItemID: item,
		Outcome:      "declined",
		Source:       "feed",
		ReleaseTitle: "[SynthSubs] Sample Show - 01 [720p]",
		Detail:       "below the profile floor",
		RecordedAt:   "2026-08-05T07:00:00Z",
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := st.Q.GetPassOutcome(ctx, item)
	if err != nil {
		t.Fatalf("get pass outcome: %v", err)
	}
	if got.Outcome != "declined" || got.Source != "feed" {
		t.Errorf("outcome/source = %q/%q, want the second pass's declined/feed", got.Outcome, got.Source)
	}
	if got.ReleaseTitle != "[SynthSubs] Sample Show - 01 [720p]" || got.Detail != "below the profile floor" {
		t.Errorf("release/detail = %q/%q, want the second pass's", got.ReleaseTitle, got.Detail)
	}
	if got.RecordedAt != "2026-08-05T07:00:00Z" {
		t.Errorf("recorded_at = %q, want the second pass's", got.RecordedAt)
	}
	if got.HeldUntil.Valid {
		t.Errorf("held_until = %q, want it cleared by an outcome that carries no hold", got.HeldUntil.String)
	}

	var rows int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pass_outcomes`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("stored rows = %d, want 1: a pass overwrites in place", rows)
	}
}

// Nothing else deletes these rows, so the series cascade is the only thing that
// bounds the table. It has to reach them, or a removed series leaves orphans no
// query can ever read.
func TestPassOutcomeCascadesWithItsSeries(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	series := blocklistSeries(t, st, "Pass Outcome Cascade")
	item := outcomeItem(t, st, series.ID, 1)

	if err := st.Q.UpsertPassOutcome(ctx, db.UpsertPassOutcomeParams{
		WantedItemID: item, Outcome: "no_match", Source: "sweep",
		RecordedAt: "2026-08-05T07:00:00Z",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := st.Q.DeleteSeries(ctx, series.ID); err != nil {
		t.Fatalf("delete series: %v", err)
	}

	var rows int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pass_outcomes`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("stored rows after the series was deleted = %d, want 0", rows)
	}
}
