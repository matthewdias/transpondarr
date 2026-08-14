package acquire_test

import (
	"context"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// packRelease builds a synthetic season pack covering a whole title.
func packRelease(title string) indexer.Release {
	return indexer.Release{
		Title:       "[Batchers] " + title + " S1 (01-06) [1080p][Batch]",
		DownloadURL: "magnet:?xt=urn:btih:pack",
		Seeders:     900,
	}
}

// The inversion #126 buys, and #125's case read the other way round: a
// back-catalog title whose results carry both singles and a pack takes the
// pack, because the importer now places it file by file. One grab, six items.
func TestSweepPrefersASeasonPackOverSingles(t *testing.T) {
	h := newSweep(t, []indexer.Release{
		packRelease("Placeholder Saga"),
		episodeRelease("Placeholder Saga", 1),
		episodeRelease("Placeholder Saga", 2),
		episodeRelease("Placeholder Saga", 3),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1}, sweepItem{number: 2}, sweepItem{number: 3},
		sweepItem{number: 4}, sweepItem{number: 5}, sweepItem{number: 6})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 6 {
		t.Errorf("grabbed items = %v, want all six under the pack", got)
	}
	if len(h.dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want 1 — the pack covers everything", len(h.dl.Adds))
	}
	if !strings.Contains(h.dl.Adds[0].URL, "pack") {
		t.Errorf("sweep grabbed %+v, want the pack", h.dl.Adds[0])
	}
}

// A pack that is the only coverage is now simply taken; before #126 the sweep
// skipped it, because a grabbed pack parked its whole season as deferred.
func TestSweepGrabsASeasonPackThatIsTheOnlyCoverage(t *testing.T) {
	h := newSweep(t, []indexer.Release{packRelease("Placeholder Saga")}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1}, sweepItem{number: 2}, sweepItem{number: 3},
		sweepItem{number: 4}, sweepItem{number: 5}, sweepItem{number: 6})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 6 {
		t.Errorf("grabbed items = %v, want the six the pack covers", got)
	}
	if len(h.dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1", len(h.dl.Adds))
	}
}
