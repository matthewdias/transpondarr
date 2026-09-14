package acquire_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/library/mediaserver"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// The default profile lists no release groups, so a held release scores on resolution
// alone: 1080p 400, 720p 300, 480p 200.
const heldSD = "[ExampleSubs] Placeholder Saga - 03 [480p]"
const heldHD = "[ExampleSubs] Placeholder Saga - 03 [1080p]"

// enableUpgrades opts the default profile in, at the given cutoff.
func enableUpgrades(t *testing.T, st *store.Store, cutoff int) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET upgrades_enabled = 1, cutoff_score = ? WHERE id = 1`, cutoff); err != nil {
		t.Fatalf("enable upgrades: %v", err)
	}
}

// grabFor returns the release and status recorded against one item's grab row.
func grabFor(t *testing.T, st *store.Store, titleID int64, number int) (string, string) {
	t.Helper()
	var release, status string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT g.release_title, g.status FROM grabs g
		 JOIN wanted_items w ON w.id = g.wanted_item_id
		 WHERE w.series_id = ? AND w.number = ?`, titleID, number).Scan(&release, &status); err != nil {
		t.Fatalf("read grab for item %d: %v", number, err)
	}
	return release, status
}

// heldTitleOf reads the release name the store has for a title's only item.
func heldTitleOf(t *testing.T, st *store.Store, titleID int64) string {
	t.Helper()
	var title string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT held_release_title FROM wanted_items WHERE series_id = ?`, titleID).Scan(&title); err != nil {
		t.Fatalf("read held_release_title: %v", err)
	}
	return title
}

// The headline behaviour of #97: a complete title whose profile opts in grabs a
// better release off the feed, with no wanted item anywhere.
func TestFeedPollUpgradesAHeldItem(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	enableUpgrades(t, h.st, 400)
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want 1", len(h.dl.Adds))
	}
	release, status := grabFor(t, h.st, id, 3)
	if release != "[ExampleSubs] Placeholder Saga - 03 [1080p]" || status != "grabbed" {
		t.Errorf("grab = %q/%q, want the upgrade release in flight", release, status)
	}
	// The library still has the old file until the import replaces it.
	if got := heldTitleOf(t, h.st, id); got != heldSD {
		t.Errorf("held release = %q, want it untouched until the import lands", got)
	}
}

// Cutoff, not chase: past the cutoff the held release is good enough, so a better
// release changes nothing.
func TestFeedPollLeavesACutoffMetItemAlone(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	enableUpgrades(t, h.st, 400)
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldHD, grab: "imported"})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0 — the held release meets the cutoff", len(h.dl.Adds))
	}
}

// Opt-in is per profile: a default install upgrades nothing.
func TestFeedPollLeavesHeldItemsAloneWhenUpgradesAreOff(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0 — the profile never opted in", len(h.dl.Adds))
	}
}

// A failed upgrade leaves the item held, so the next poll may try again — which
// is what puts a 'failed' grab row back in the pool.
func TestFeedPollRetriesAfterAFailedUpgrade(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	enableUpgrades(t, h.st, 400)
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "failed"})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1 — a failed upgrade re-enters the pool", len(h.dl.Adds))
	}
}

// An upgrade already in flight, or deferred for a human, is settled: nothing
// re-grabs it.
func TestFeedPollLeavesUnsettledUpgradesAlone(t *testing.T) {
	for _, status := range []string{"grabbed", "import_deferred"} {
		t.Run(status, func(t *testing.T) {
			h := newFeedPoll(t, []indexer.FeedEntry{
				feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
			}, fakeConfig{})
			enableUpgrades(t, h.st, 400)
			seedSweep(t, h.st, "Placeholder Saga", true,
				sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: status})

			if err := h.svc.PollFeedOnce(context.Background()); err != nil {
				t.Fatalf("PollFeedOnce: %v", err)
			}
			if len(h.dl.Adds) != 0 {
				t.Errorf("download Add called %d times, want 0 for a %s grab", len(h.dl.Adds), status)
			}
		})
	}
}

// The sweep issues no search for an upgrade, but a search it issued anyway passes
// its page of search results to the same decision layer, so a held item is evaluated for free.
func TestSweepUpgradesHeldItemsItSearchedForAnyway(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
		episodeRelease("Placeholder Saga", 5),
	}, fakeConfig{})
	enableUpgrades(t, h.st, 400)
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"},
		sweepItem{number: 5, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.dl.Adds) != 2 {
		t.Fatalf("download Add called %d times, want the wanted item and the free-riding upgrade", len(h.dl.Adds))
	}
	if release, status := grabFor(t, h.st, id, 3); release != "[ExampleSubs] Placeholder Saga - 03 [1080p]" || status != "grabbed" {
		t.Errorf("held item's grab = %q/%q, want the upgrade in flight", release, status)
	}
	if got := grabbedItemNumbers(t, h.st, id); !slices.Contains(got, 5) {
		t.Errorf("grabbed items = %v, want the wanted item too", got)
	}
}

// A complete title is not worth a search of its own: the sweep's budget is one
// search per title, so upgrades use the flat-cost feed alone.
func TestSweepDoesNotSearchForUpgradesAlone(t *testing.T) {
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	enableUpgrades(t, h.st, 400)
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.idx.Queries) != 0 {
		t.Errorf("sweep issued %d searches for a complete title, want 0", len(h.idx.Queries))
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0", len(h.dl.Adds))
	}
}

// Notify-only rehearses an upgrade like any other take: it reports, and nothing
// is sent to the download client.
func TestNotifyOnlyRehearsesAnUpgrade(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{notifyOnly: true})
	enableUpgrades(t, h.st, 400)
	fn := withNotifier(h.reg)
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Fatalf("a rehearsal added %d torrents, want 0", len(h.dl.Adds))
	}
	select {
	case ev := <-fn.Events:
		if ev.Kind != notify.KindRehearsal || ev.ItemNumber != 3 {
			t.Errorf("event = %+v, want a rehearsal for item 3", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the rehearsed upgrade")
	}
}

// A manual search offers releases for what is already in the library: profiles inform
// manual actions, they restrict only automation (PR #57).
func TestManualMatchOffersReleasesForHeldItems(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{})
	id := seedSweep(t, st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	m, err := svc.MatchTitle(context.Background(), id)
	if err != nil {
		t.Fatalf("MatchSeries: %v", err)
	}
	if len(m.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(m.Candidates))
	}
	c := m.Candidates[0]
	if !c.Matched || len(c.Items) != 1 || c.Items[0] != 3 {
		t.Fatalf("candidate = %+v, want it matched to the held item", c)
	}
	// Automation's view is reported, never enforced here.
	if reason := c.UpgradeBlocked[3]; reason == "" {
		t.Error("no refusal recorded for a profile that never opted in")
	}
	if len(c.TakeItems()) != 0 {
		t.Errorf("TakeItems() = %v, want automation to take nothing", c.TakeItems())
	}
}

// The feature end to end, over a real library layout: a held 480p file, a better
// release off the feed, and the same file replaced in place with the store now
// naming the release in the library. The last poll proves it converges — the
// upgraded item meets the cutoff, so offering it again changes nothing.
func TestUpgradeLifecycleReplacesTheHeldFile(t *testing.T) {
	ctx := context.Background()
	st := coretest.NewStore(t)
	root := t.TempDir()
	dir := filepath.Join(root, "Placeholder Saga", "Season 01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	libFile := filepath.Join(dir, "Placeholder Saga - S01E03.mkv")
	if err := os.WriteFile(libFile, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute))
	feed := &coretest.FakeFeed{Entries: []indexer.FeedEntry{entry}}
	feed.Releases = []indexer.Release{entry.Release}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "upgrade", Outcome: download.AddSuccess}}
	reg := clients.New()
	reg.SetIndexer(feed)
	reg.SetDownload(dl)
	reg.SetLibrary(mediaserver.New(mediaserver.Roots{Series: root}, mediaserver.LayoutSeasonFolders, "copy", nil))

	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)
	enableUpgrades(t, st, 400)
	id := seedSweep(t, st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	if err := svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want the upgrade", len(dl.Adds))
	}

	// The upgrade completes: a smaller file, which is what the size check
	// would otherwise reject.
	src := filepath.Join(t.TempDir(), "upgrade.mkv")
	if err := os.WriteFile(src, make([]byte, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	dl.Statuses = []download.Status{{Hash: "upgrade", State: download.StateComplete, ContentPath: src}}
	if err := importer.New(st, reg, discardLogger(), blocklist.New(st, nil), nil).ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	info, err := os.Stat(libFile)
	if err != nil {
		t.Fatalf("the library file is gone: %v", err)
	}
	if info.Size() != 128 {
		t.Errorf("library file size = %d, want the upgrade in its place", info.Size())
	}
	if release, status := grabFor(t, st, id, 3); status != "imported" || release != entry.Release.Title {
		t.Errorf("grab = %q/%q, want the upgrade imported", release, status)
	}
	if got := heldTitleOf(t, st, id); got != entry.Release.Title {
		t.Errorf("held release = %q, want the release that just landed", got)
	}
	var inLibrary int64
	if err := st.DB.QueryRowContext(ctx,
		`SELECT in_library FROM wanted_items WHERE series_id = ?`, id).Scan(&inLibrary); err != nil {
		t.Fatalf("read in_library: %v", err)
	}
	if inLibrary != 1 {
		t.Errorf("in_library = %d, want the item still in the library", inLibrary)
	}

	// Clear the feed mark, so what stops a second grab is the cutoff rather than
	// the feed's dedupe.
	if _, err := st.DB.ExecContext(ctx, `DELETE FROM settings WHERE key LIKE 'feed.seen.%'`); err != nil {
		t.Fatalf("clear the feed mark: %v", err)
	}
	if err := svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	if len(dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want the upgraded item to have converged", len(dl.Adds))
	}
}
