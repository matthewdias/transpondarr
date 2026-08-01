package acquire_test

import (
	"context"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// packRelease builds a synthetic season pack covering a whole series.
func packRelease(title string) indexer.Release {
	return indexer.Release{
		Title:       "[Batchers] " + title + " S1 (01-06) [1080p][Batch]",
		DownloadURL: "magnet:?xt=urn:btih:pack",
		Seeders:     900, // outranks every single on seeders alone
	}
}

// #125's headline case: a back-catalog series whose results carry both singles
// and a season pack ends up with the singles, never the pack the importer could
// only defer — and deferred is settled, so the pack would park the whole season.
func TestSweepPrefersSinglesOverASeasonPack(t *testing.T) {
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
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 3 {
		t.Errorf("grabbed items = %v, want the three singles", got)
	}
	for _, add := range h.dl.Adds {
		if strings.Contains(add.URL, "pack") {
			t.Errorf("sweep grabbed the season pack: %+v", add)
		}
	}
	// Items 4-6 stay wanted and visible rather than parked behind a deferred grab.
	if len(h.dl.Adds) != 3 {
		t.Errorf("download Add called %d times, want 3", len(h.dl.Adds))
	}
}

// A pack that is the only coverage is skipped outright: the documented policy is
// "never automatically", because grabbing it downloads a season only to park it.
func TestSweepSkipsASeasonPackThatIsTheOnlyCoverage(t *testing.T) {
	h := newSweep(t, []indexer.Release{packRelease("Placeholder Saga")}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1}, sweepItem{number: 2}, sweepItem{number: 3})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed items = %v, want none — the pack is never taken unattended", got)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0", len(h.dl.Adds))
	}
}
