package acquire_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// blockFirstAdd makes the first Add sent to the download client block until the
// returned release func is called, and signals when it did. Later adds pass
// straight through, so a test that fails still fails on its assertion rather than
// deadlocking.
func blockFirstAdd(dl *coretest.FakeDownload) (entered <-chan struct{}, release func()) {
	in := make(chan struct{})
	hold := make(chan struct{})
	var once sync.Once
	dl.AddHook = func(download.AddOptions) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(in)
			<-hold
		}
	}
	return in, sync.OnceFunc(func() { close(hold) })
}

// The sweep and the feed poll are phase-locked on a 15-minute tick and both read
// grab state before either writes, so the same just-aired episode is the case
// they collide on. Exactly one add may be sent to the download client.
func TestConcurrentSweepAndFeedPollGrabItemOnce(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-5*time.Minute)),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	entered, release := blockFirstAdd(h.dl)
	ctx := t.Context()

	var wg sync.WaitGroup
	var pollErr error
	wg.Go(func() { pollErr = h.svc.PollFeedOnce(ctx) })

	<-entered // the feed poll now has the claim, inside the client
	sweepErr := h.svc.SweepOnce(ctx)
	release()
	wg.Wait()

	if pollErr != nil || sweepErr != nil {
		t.Fatalf("poll err = %v, sweep err = %v", pollErr, sweepErr)
	}
	if n := h.dl.AddCount(); n != 1 {
		t.Errorf("download Add called %d times, want exactly 1", n)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Errorf("grabbed items = %v, want [3]", got)
	}
}

// Automation skips a claimed item; a human never does. This is PR #57's never-refuse rule
// expressed against the claim registry, and the guard against someone later
// "improving" the registry into a block on the manual path.
func TestManualGrabIgnoresAnInFlightClaim(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now()),
	}, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	items, err := h.st.Q.ListWantedItems(t.Context(), 1)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}

	entered, release := blockFirstAdd(h.dl)
	ctx := t.Context()

	var wg sync.WaitGroup
	wg.Go(func() { _ = h.svc.PollFeedOnce(ctx) })
	<-entered // automation has the claim on item 3

	cand := decide.Candidate{
		Release: indexer.Release{Title: "[OtherSubs] Placeholder Saga - 03 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:manual"},
		Matched: true, Items: []int{3},
	}
	want := []domain.WantedItem{{ID: items[0].ID, Kind: domain.KindEpisode, Number: 3}}
	if _, err := h.svc.Grab(ctx, 1, cand, want, false); err != nil {
		t.Fatalf("manual Grab refused while automation held the claim: %v", err)
	}

	release()
	wg.Wait()
	if n := h.dl.AddCount(); n != 2 {
		t.Errorf("download Add called %d times, want 2 — the manual grab must not be blocked", n)
	}
}

// A claim must be released on a failed add, or one dead release would exclude
// its item from the rest of the pass.
func TestClaimIsReleasedWhenAnAddFails(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	dead := episodeRelease("Placeholder Saga", 3)
	dead.DownloadURL = "magnet:?xt=urn:btih:dead"
	dead.Seeders = 999 // ranks first, so it is tried first
	live := episodeRelease("Placeholder Saga", 3)

	h := newFeedPoll(t, []indexer.FeedEntry{
		{Release: dead, GUID: "guid-dead", Published: time.Now()},
		{Release: live, GUID: "guid-live", Published: time.Now()},
	}, fakeConfig{})
	h.dl.FailURLs = map[string]error{dead.DownloadURL: errors.New("qbit: refused")}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(t.Context()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if n := h.dl.AddCount(); n != 2 {
		t.Fatalf("download Add called %d times, want 2 — the next candidate must be tried", n)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Errorf("grabbed items = %v, want [3] from the second candidate", got)
	}
}

// The sequential interleaving the claim alone does not close: the sweep reads
// its item list, makes network requests for seconds, and by the time it grabs,
// the poll has already grabbed the item and released its claim. Nothing in the
// claim stops the sweep acting on that stale read — only re-checking does.
//
// The two pick different releases here (the feed page and a title search return
// different candidate sets), which is what makes the duplicate an orphaned
// torrent rather than a benign convergence: UpsertGrab is keyed on
// wanted_item_id, so the second write leaves the first torrent referenced by
// nothing.
func TestSweepDoesNotRegrabAnItemThePollTookMidSearch(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	fromFeed := episodeRelease("Placeholder Saga", 3)
	fromFeed.DownloadURL = "magnet:?xt=urn:btih:fromfeed"
	fromSearch := episodeRelease("Placeholder Saga", 3)
	fromSearch.DownloadURL = "magnet:?xt=urn:btih:fromsearch"

	h := newFeedPoll(t, []indexer.FeedEntry{
		{Release: fromFeed, GUID: "guid-feed", Published: time.Now()},
	}, fakeConfig{})
	// The search side returns a different release for the same episode.
	h.feed.Releases = []indexer.Release{fromSearch}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	ctx := t.Context()
	// The poll lands while the sweep's search is in flight, after the sweep has
	// already read item 3 as grabbable.
	pollFeed := sync.OnceFunc(func() {
		if err := h.svc.PollFeedOnce(ctx); err != nil {
			t.Errorf("PollFeedOnce: %v", err)
		}
	})
	h.feed.SearchHook = func(indexer.Query) { pollFeed() }

	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n := h.dl.AddCount(); n != 1 {
		t.Fatalf("download Add called %d times, want 1 — the sweep acted on a stale read", n)
	}
	if got := h.dl.Adds[0].URL; got != fromFeed.DownloadURL {
		t.Errorf("added %q, want the release the poll took (%q)", got, fromFeed.DownloadURL)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Errorf("grabbed items = %v, want [3]", got)
	}
}
