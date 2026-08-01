package blocklist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

func TestBlockDurationLadder(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, firstBlock},
		{1, firstBlock},
		{2, secondBlock},
		{3, 0},
		{9, 0},
	}
	for _, c := range cases {
		if got := blockDuration(c.failures); got != c.want {
			t.Errorf("blockDuration(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}

func newService(t *testing.T) (*Service, *store.Store, db.Series) {
	t.Helper()
	st := coretest.NewStore(t)
	series, err := st.Q.CreateSeries(context.Background(), db.CreateSeriesParams{
		Title: "Placeholder Saga", Format: "TV", Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	return New(st, nil), st, series
}

// The first failure blocks for a day, the second for a week, the third forever:
// environmental faults fail many grabs at once, so only a repeat of the same
// release proves the release itself is dead.
func TestRecordEscalatesExpiry(t *testing.T) {
	svc, _, series := newService(t)
	ctx := context.Background()
	const title = "[SynthSubs] Placeholder Saga - 03 [1080p].mkv"

	record := func() db.ReleaseBlocklist {
		t.Helper()
		if err := svc.Record(ctx, series.ID, "abcd1234", title, "download failed in the client"); err != nil {
			t.Fatalf("record: %v", err)
		}
		all, err := svc.List(ctx, series.ID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("entries = %d, want 1", len(all))
		}
		return all[0]
	}

	first := record()
	if first.Failures != 1 {
		t.Errorf("failures = %d, want 1", first.Failures)
	}
	assertBlockedFor(t, first, firstBlock)

	second := record()
	if second.Failures != 2 {
		t.Errorf("failures = %d, want 2", second.Failures)
	}
	assertBlockedFor(t, second, secondBlock)

	third := record()
	if third.Failures != 3 {
		t.Errorf("failures = %d, want 3", third.Failures)
	}
	if third.BlockedUntil.Valid {
		t.Errorf("blocked_until = %q after the third failure, want NULL (permanent)", third.BlockedUntil.String)
	}
}

func assertBlockedFor(t *testing.T, e db.ReleaseBlocklist, want time.Duration) {
	t.Helper()
	if !e.BlockedUntil.Valid {
		t.Fatalf("blocked_until is NULL, want ~%v out", want)
	}
	until, err := store.ParseTimestamp(e.BlockedUntil.String)
	if err != nil {
		t.Fatalf("parse blocked_until %q: %v", e.BlockedUntil.String, err)
	}
	got := time.Until(until)
	if got < want-time.Minute || got > want+time.Minute {
		t.Errorf("blocked for %v, want ~%v", got.Round(time.Second), want)
	}
}

// Record stores the normalized title decide matches on, so a release differing
// only in spacing or case is still recognised.
func TestRecordStoresTheNormalizedTitle(t *testing.T) {
	svc, _, series := newService(t)
	ctx := context.Background()
	if err := svc.Record(ctx, series.ID, "", "[SynthSubs]  Placeholder Saga - 03  ", "failed"); err != nil {
		t.Fatalf("record: %v", err)
	}
	all, err := svc.List(ctx, series.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if all[0].NormalizedTitle != "[synthsubs] placeholder saga - 03" {
		t.Errorf("normalized_title = %q", all[0].NormalizedTitle)
	}
	if all[0].ReleaseTitle != "[SynthSubs]  Placeholder Saga - 03  " {
		t.Errorf("release_title = %q, want the original for display", all[0].ReleaseTitle)
	}
}

func TestActiveExcludesExpiredAndClearRemoves(t *testing.T) {
	svc, st, series := newService(t)
	ctx := context.Background()

	if err := svc.Record(ctx, series.ID, "h1", "[SynthSubs] Placeholder Saga - 01", "failed"); err != nil {
		t.Fatalf("record live: %v", err)
	}
	if err := svc.Record(ctx, series.ID, "h2", "[SynthSubs] Placeholder Saga - 02", "failed"); err != nil {
		t.Fatalf("record expired: %v", err)
	}
	// Expire the second entry behind the service's back; only time can do this.
	if _, err := st.DB.ExecContext(ctx,
		"UPDATE release_blocklist SET blocked_until = ? WHERE info_hash = 'h2'",
		store.FormatTimestamp(time.Now().Add(-time.Hour)),
	); err != nil {
		t.Fatalf("expire: %v", err)
	}

	active, err := svc.Active(ctx, series.ID)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 1 || active[0].InfoHash != "h1" {
		t.Fatalf("active = %+v, want only the unexpired entry", active)
	}

	if err := svc.Clear(ctx, series.ID, active[0].ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, _ := svc.Active(ctx, series.ID); len(got) != 0 {
		t.Errorf("active after clear = %d entries, want 0", len(got))
	}
	// Clear is scoped: a cleared entry that is gone reports ErrNotFound.
	if err := svc.Clear(ctx, series.ID, active[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("clear of a missing entry = %v, want ErrNotFound", err)
	}
}

// expire backdates an entry's block, which only time can otherwise do.
func expire(t *testing.T, st *store.Store, hash string) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		"UPDATE release_blocklist SET blocked_until = ? WHERE info_hash = ?",
		store.FormatTimestamp(time.Now().Add(-time.Hour)), hash,
	); err != nil {
		t.Fatalf("expire %s: %v", hash, err)
	}
}

func hashes(entries []db.ReleaseBlocklist) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.InfoHash)
	}
	return out
}

// ClearExpired is the "forget the history, keep what still blocks" affordance;
// ClearSeries is the whole-series one. Both stop at the series boundary.
func TestClearExpiredAndClearSeries(t *testing.T) {
	svc, st, series := newService(t)
	ctx := context.Background()
	other, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{
		Title: "Another Placeholder", Format: "TV", Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create other series: %v", err)
	}

	for _, e := range []struct {
		seriesID    int64
		hash, title string
	}{
		{series.ID, "live", "[SynthSubs] Placeholder Saga - 01"},
		{series.ID, "gone", "[SynthSubs] Placeholder Saga - 02"},
		{series.ID, "forever", "[SynthSubs] Placeholder Saga - 03"},
		{other.ID, "elsewhere", "[SynthSubs] Another Placeholder - 01"},
	} {
		if err := svc.Record(ctx, e.seriesID, e.hash, e.title, "failed"); err != nil {
			t.Fatalf("record %s: %v", e.hash, err)
		}
	}
	expire(t, st, "gone")
	expire(t, st, "elsewhere")
	if _, err := st.DB.ExecContext(ctx,
		"UPDATE release_blocklist SET blocked_until = NULL WHERE info_hash = 'forever'"); err != nil {
		t.Fatalf("make permanent: %v", err)
	}

	cleared, err := svc.ClearExpired(ctx, series.ID)
	if err != nil {
		t.Fatalf("clear expired: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared %d expired entries, want 1", cleared)
	}
	left, err := svc.List(ctx, series.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := hashes(left); len(got) != 2 {
		t.Errorf("entries after clearing expired = %v, want the live and permanent ones", got)
	}

	cleared, err = svc.ClearSeries(ctx, series.ID)
	if err != nil {
		t.Fatalf("clear series: %v", err)
	}
	if cleared != 2 {
		t.Errorf("cleared %d entries, want the 2 that were left", cleared)
	}
	if left, _ := svc.List(ctx, series.ID); len(left) != 0 {
		t.Errorf("entries after clearing the series = %v, want none", hashes(left))
	}
	// Neither clear reaches past the series it was scoped to.
	if elsewhere, _ := svc.List(ctx, other.ID); len(elsewhere) != 1 {
		t.Errorf("other series has %d entries, want its own 1 untouched", len(elsewhere))
	}
}
