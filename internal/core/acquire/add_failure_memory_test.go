package acquire_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// A refused add writes no grab row, so the importer never sees it and #118's
// blocklist could not reach this path at all (#120).
func TestSweepRemembersAReleaseTheClientCouldNotResolve(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	dead := episodeRelease("Placeholder Saga", 3)
	h := newSweep(t, []indexer.Release{dead}, fakeConfig{})
	h.dl.FailURLs = map[string]error{
		dead.DownloadURL: fmt.Errorf("%w: 404 fetching the torrent", download.ErrBadRelease),
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if len(h.rec.calls) != 1 {
		t.Fatalf("blocklist records = %d, want the dead release remembered once", len(h.rec.calls))
	}
	got := h.rec.calls[0]
	if got.titleID != id || got.releaseTitle != dead.Title {
		t.Errorf("recorded %+v, want series %d and release %q", got, id, dead.Title)
	}
	if got.reason == "" {
		t.Error("recorded no reason; the Releases tab shows it as the ineligible reason")
	}
	// The covered items are the breaker's evidence of breadth, so a refused add
	// has to report which ones it was for.
	items, err := h.st.Q.ListWantedItems(context.Background(), id)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(got.itemIDs) != 1 || got.itemIDs[0] != items[0].ID {
		t.Errorf("recorded item ids = %v, want the covered item %d", got.itemIDs, items[0].ID)
	}
}

// A client that is down refuses every release, so remembering its refusals
// would blocklist a healthy candidate pool for a fault that is not the
// releases'.
func TestSweepDoesNotRememberAClientSideRefusal(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	rel := episodeRelease("Placeholder Saga", 3)
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	h.dl.FailURLs = map[string]error{rel.DownloadURL: errors.New("qbit: connection refused")}
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.rec.calls) != 0 {
		t.Errorf("recorded %+v, want nothing: a sick client says nothing about the release", h.rec.calls)
	}
}

// Failure memory is automation's policy, like eligibility (PR #57): a manual
// grab must not leave a block behind for the sweep to obey.
func TestManualGrabRemembersNothing(t *testing.T) {
	rel := episodeRelease("Placeholder Saga", 3)
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	h.dl.FailURLs = map[string]error{
		rel.DownloadURL: fmt.Errorf("%w: 404 fetching the torrent", download.ErrBadRelease),
	}

	_, err := h.svc.Grab(context.Background(), 1,
		decide.Candidate{Release: rel, Items: []int{3}},
		[]domain.WantedItem{{ID: 1, Kind: domain.KindEpisode, Number: 3}}, false)
	if !errors.Is(err, acquire.ErrDownloadAdd) {
		t.Fatalf("Grab error = %v, want it to report the refused add", err)
	}
	if len(h.rec.calls) != 0 {
		t.Errorf("recorded %+v, want nothing from a manual grab", h.rec.calls)
	}
}
