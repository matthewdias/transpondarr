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

// A torrent whose format we cannot derive an info hash for is remembered — we
// cannot use it and retrying would only repeat that — but under its own reason.
// The blocklist entry is what a user reads to understand why a release was
// blocked, and this cause is neither the fetch failure the other string names
// nor anything the download client did wrong (#165).
func TestSweepRemembersAnUnsupportedTorrentUnderItsOwnReason(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	v2 := episodeRelease("Placeholder Saga", 3)
	h := newSweep(t, []indexer.Release{v2}, fakeConfig{})
	// The shape resolveAdd produces: badRelease() wraps whatever InfoHashFromMeta
	// returned, so the new sentinel rides inside ErrBadRelease rather than replacing
	// it — which is what keeps AutoGrab recording at all.
	addErr := fmt.Errorf("%w: %w", download.ErrBadRelease,
		fmt.Errorf(`%w: its "info" dictionary carries no v1 pieces`, download.ErrNoV1InfoHash))
	if !errors.Is(addErr, download.ErrBadRelease) || !errors.Is(addErr, download.ErrNoV1InfoHash) {
		t.Fatalf("the add error must satisfy both sentinels, got %v", addErr)
	}
	h.dl.FailURLs = map[string]error{v2.DownloadURL: addErr}
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if len(h.rec.calls) != 1 {
		t.Fatalf("blocklist records = %d, want the release remembered once", len(h.rec.calls))
	}
	got := h.rec.calls[0].reason
	if got == "the download URL could not be fetched or parsed" {
		t.Errorf("reason = %q, which misattributes it: the URL fetched and parsed fine", got)
	}
	// The client accepted this torrent and is managing it perfectly well, so a
	// reason blaming it sends the reader to debug the one place nothing is wrong.
	if got != "the torrent's format is not supported" {
		t.Errorf("reason = %q, want the unsupported format", got)
	}
}

// The loop breaker for a torrent the client holds with its data gone (#241):
// converging on it would report a grab that can never deliver, so the same
// release would rank first and "grab" every pass forever. Refusing the add makes
// it an add failure, which the walk answers with the next-best release in the
// same pass -- and records nothing, because the client's disk is not the
// release's fault.
func TestSweepTakesTheNextReleaseWhenADuplicatesDataIsMissing(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	// Distinct group names, or the two releases share a title and "the next-best
	// was grabbed" passes for either candidate.
	held := episodeRelease("Placeholder Saga", 3)
	held.Title = "[TopSubs] Placeholder Saga - 03 [1080p]"
	held.DownloadURL = "magnet:?xt=urn:btih:held"
	held.Seeders = 999 // ranks first, so it is tried first
	next := episodeRelease("Placeholder Saga", 3)
	next.Title = "[NextSubs] Placeholder Saga - 03 [1080p]"
	next.DownloadURL = "magnet:?xt=urn:btih:next"

	h := newSweep(t, []indexer.Release{held, next}, fakeConfig{})
	h.dl.FailURLs = map[string]error{
		held.DownloadURL: fmt.Errorf("qbittorrent: add: %w", download.ErrDataMissing),
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if n := h.dl.AddCount(); n != 2 {
		t.Fatalf("download Add called %d times, want 2 -- the next candidate must be tried", n)
	}
	if h.dl.Adds[1].URL != next.DownloadURL {
		t.Errorf("second add = %q, want the next-best release %q", h.dl.Adds[1].URL, next.DownloadURL)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Errorf("grabbed items = %v, want [3] from the second candidate", got)
	}
	grabs, err := h.st.Q.ListGrabsByTitle(context.Background(), id)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].ReleaseTitle != next.Title {
		t.Errorf("grab = %+v, want the next-best release %q", grabs, next.Title)
	}
	// Environmental, so it must not reach the blocklist the way ErrBadRelease does.
	if len(h.rec.calls) != 0 {
		t.Errorf("recorded %+v, want nothing: the client's disk is not the release's fault", h.rec.calls)
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
