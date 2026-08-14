package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func blocklistTitle(t *testing.T, st *Store, title string) db.Series {
	t.Helper()
	s, err := st.Q.CreateTitle(context.Background(), db.CreateTitleParams{
		Title: title, Format: "TV", Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	return s
}

// A repeat failure of the same release must land on the existing row so the
// escalation ladder can see how many times it has failed.
func TestUpsertBlocklistEntryBumpsFailures(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	title := blocklistTitle(t, st, "Blocklist Upsert")

	until := FormatTimestamp(time.Now().Add(24 * time.Hour))
	first, err := st.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
		SeriesID:        title.ID,
		InfoHash:        "aaaa1111",
		ReleaseTitle:    "[SynthSubs] Sample Show - 01 [1080p].mkv",
		NormalizedTitle: "[synthsubs] sample show - 01 [1080p].mkv",
		Reason:          "download failed in the client",
		BlockedUntil:    nullString(until),
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Failures != 1 {
		t.Errorf("failures after first record = %d, want 1", first.Failures)
	}

	second, err := st.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
		SeriesID:        title.ID,
		InfoHash:        "bbbb2222",
		ReleaseTitle:    "[SynthSubs] Sample Show - 01 [1080p].mkv",
		NormalizedTitle: "[synthsubs] sample show - 01 [1080p].mkv",
		Reason:          "download gone from the client",
		BlockedUntil:    nullString(FormatTimestamp(time.Now().Add(7 * 24 * time.Hour))),
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second upsert inserted a new row (%d != %d); want the same row bumped", second.ID, first.ID)
	}
	if second.Failures != 2 {
		t.Errorf("failures after second record = %d, want 2", second.Failures)
	}
	// The latest attempt's hash is what the next decide pass will see.
	if second.InfoHash != "bbbb2222" {
		t.Errorf("info_hash = %q, want the latest attempt's bbbb2222", second.InfoHash)
	}
	if second.Reason != "download gone from the client" {
		t.Errorf("reason = %q, want the latest attempt's", second.Reason)
	}

	all, err := st.Q.ListBlocklistByTitle(ctx, title.ID)
	if err != nil {
		t.Fatalf("list by series: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 blocklist row after two records, got %d", len(all))
	}
}

// Expiry is a filter, never a delete: the row carries the failure count, so an
// expired entry must survive to escalate on the next failure.
func TestListActiveBlocklistFiltersExpiredButKeepsPermanent(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	title := blocklistTitle(t, st, "Blocklist Expiry")
	other := blocklistTitle(t, st, "Other Series")
	now := time.Now()

	seed := func(titleID int64, release string, until any) {
		t.Helper()
		p := db.UpsertBlocklistEntryParams{
			SeriesID:        titleID,
			InfoHash:        "hash-" + release,
			ReleaseTitle:    release,
			NormalizedTitle: release,
			Reason:          "test",
		}
		if s, ok := until.(string); ok {
			p.BlockedUntil = nullString(s)
		}
		if _, err := st.Q.UpsertBlocklistEntry(ctx, p); err != nil {
			t.Fatalf("seed %s: %v", release, err)
		}
	}

	seed(title.ID, "expired", FormatTimestamp(now.Add(-time.Hour)))
	seed(title.ID, "live", FormatTimestamp(now.Add(time.Hour)))
	seed(title.ID, "permanent", nil)
	seed(other.ID, "other-series-live", FormatTimestamp(now.Add(time.Hour)))

	active, err := st.Q.ListActiveBlocklist(ctx, db.ListActiveBlocklistParams{
		SeriesID:     title.ID,
		BlockedUntil: nullString(FormatTimestamp(now)),
	})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	got := map[string]bool{}
	for _, e := range active {
		got[e.NormalizedTitle] = true
	}
	if !got["live"] || !got["permanent"] {
		t.Errorf("active entries = %v, want live and permanent", got)
	}
	if got["expired"] {
		t.Error("expired entry is still active")
	}
	if got["other-series-live"] {
		t.Error("another series' entry leaked into this series' active list")
	}

	// The expired row must still exist, or the ladder resets every expiry.
	all, err := st.Q.ListBlocklistByTitle(ctx, title.ID)
	if err != nil {
		t.Fatalf("list by series: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("stored entries = %d, want 3 (expiry filters, it does not delete)", len(all))
	}
}

func TestDeleteBlocklistEntryIsScopedToItsTitle(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	title := blocklistTitle(t, st, "Blocklist Delete")
	other := blocklistTitle(t, st, "Blocklist Delete Other")

	entry, err := st.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
		SeriesID: title.ID, InfoHash: "h", ReleaseTitle: "r", NormalizedTitle: "r", Reason: "test",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := st.Q.DeleteBlocklistEntry(ctx, db.DeleteBlocklistEntryParams{ID: entry.ID, SeriesID: other.ID})
	if err != nil {
		t.Fatalf("delete with the wrong series: %v", err)
	}
	if rows != 0 {
		t.Errorf("deleted %d rows via another series; want 0", rows)
	}

	rows, err = st.Q.DeleteBlocklistEntry(ctx, db.DeleteBlocklistEntryParams{ID: entry.ID, SeriesID: title.ID})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows != 1 {
		t.Errorf("deleted %d rows, want 1", rows)
	}
}
