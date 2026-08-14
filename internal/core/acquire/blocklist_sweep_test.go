package acquire_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// blockRelease seeds an active blocklist entry for a release title.
func blockRelease(t *testing.T, st *store.Store, titleID int64, hash, title string, until time.Time) {
	t.Helper()
	p := db.UpsertBlocklistEntryParams{
		SeriesID:        titleID,
		InfoHash:        hash,
		ReleaseTitle:    title,
		NormalizedTitle: decide.NormalizeReleaseTitle(title),
		Reason:          "download failed in the client",
	}
	if !until.IsZero() {
		p.BlockedUntil = sql.NullString{String: store.FormatTimestamp(until), Valid: true}
	}
	if _, err := st.Q.UpsertBlocklistEntry(context.Background(), p); err != nil {
		t.Fatalf("seed blocklist entry: %v", err)
	}
}

func grabbedReleaseTitles(t *testing.T, st *store.Store, titleID int64) []string {
	t.Helper()
	grabs, err := st.Q.ListGrabsByTitle(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	out := make([]string, 0, len(grabs))
	for _, g := range grabs {
		out = append(out, g.ReleaseTitle)
	}
	return out
}

// The point of #118: a blocklisted release degrades the sweep to the next-best
// release, not to nothing.
func TestSweepSkipsBlocklistedReleaseAndTakesTheNextBest(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	top := indexer.Release{
		Title:       "[TopSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:top03",
		Seeders:     500,
	}
	next := indexer.Release{
		Title:       "[NextSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:next03",
		Seeders:     10,
	}
	h := newSweep(t, []indexer.Release{top, next}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	blockRelease(t, h.st, id, "tophash", top.Title, time.Now().Add(24*time.Hour))

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	got := grabbedReleaseTitles(t, h.st, id)
	if len(got) != 1 || got[0] != next.Title {
		t.Fatalf("grabbed %v, want only the next-best release %q", got, next.Title)
	}
	if len(h.dl.Adds) != 1 || h.dl.Adds[0].URL != next.DownloadURL {
		t.Errorf("download adds = %+v, want only the next-best release's URL", h.dl.Adds)
	}
}

// An expired entry stops blocking: the ladder needs the release retried so a
// repeat failure can escalate it.
func TestSweepTakesAReleaseWhoseBlockExpired(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	rel := indexer.Release{
		Title:       "[TopSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:top03",
		Seeders:     500,
	}
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	blockRelease(t, h.st, id, "tophash", rel.Title, time.Now().Add(-time.Minute))

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedReleaseTitles(t, h.st, id); len(got) != 1 {
		t.Fatalf("grabbed %v, want the release whose block expired", got)
	}
}

// A permanent entry (NULL blocked_until) blocks with nothing to fall back to,
// and the pass must simply find nothing rather than fail.
func TestSweepGrabsNothingWhenEveryReleaseIsBlocklisted(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	rel := indexer.Release{
		Title:       "[TopSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:top03",
		Seeders:     500,
	}
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	blockRelease(t, h.st, id, "tophash", rel.Title, time.Time{})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedReleaseTitles(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed %v, want nothing", got)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download adds = %+v, want none", h.dl.Adds)
	}
}

// Scope is per-title: another title's entry must not suppress this one.
func TestSweepIgnoresAnotherTitlesBlocklistEntry(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	rel := indexer.Release{
		Title:       "[TopSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:top03",
		Seeders:     500,
	}
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	other := seedSweep(t, h.st, "Unrelated Show", false, sweepItem{number: 1})
	blockRelease(t, h.st, other, "tophash", rel.Title, time.Now().Add(24*time.Hour))

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedReleaseTitles(t, h.st, id); len(got) != 1 {
		t.Fatalf("grabbed %v, want the release (another series' block must not apply)", got)
	}
}
