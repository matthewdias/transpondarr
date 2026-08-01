package acquire_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// levelRecorder counts records per level, so a test can assert that a supported
// configuration stayed off the error channel.
type levelRecorder struct {
	slog.Handler
	counts map[slog.Level]int
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.counts[rec.Level]++
	return nil
}

// feedEntry wraps a synthetic release with the feed metadata a Torznab item
// carries. published is when the indexer listed it, not when the episode aired.
func feedEntry(title string, number int, published time.Time) indexer.FeedEntry {
	rel := episodeRelease(title, number)
	return indexer.FeedEntry{
		Release:   rel,
		GUID:      fmt.Sprintf("guid-%s-%02d", title, number),
		Published: published,
	}
}

type feedHarness struct {
	svc  *acquire.Service
	st   *store.Store
	feed *coretest.FakeFeed
	dl   *coretest.FakeDownload
	log  *levelRecorder
}

// newFeedPoll wires a service whose indexer publishes a recent feed. The search
// side answers with the same releases, as one real endpoint serving both would.
func newFeedPoll(t *testing.T, entries []indexer.FeedEntry, cfg fakeConfig) *feedHarness {
	t.Helper()
	feed := &coretest.FakeFeed{Entries: entries}
	for _, e := range entries {
		feed.Releases = append(feed.Releases, e.Release)
	}
	h := newFeedPollWith(t, feed, cfg)
	h.feed = feed
	return h
}

// newFeedPollWith takes the indexer directly, so a test can supply one with no
// recent-feed capability at all.
func newFeedPollWith(t *testing.T, idx indexer.Indexer, cfg fakeConfig) *feedHarness {
	t.Helper()
	st := coretest.NewStore(t)
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "polled", Outcome: download.AddSuccess}}
	rec := &levelRecorder{counts: map[slog.Level]int{}}
	reg := clients.New()
	reg.SetIndexer(idx)
	reg.SetDownload(dl)
	return &feedHarness{
		svc: acquire.New(st, reg, fakeTitles{}, cfg, slog.New(rec), &fakeRecorder{}),
		st:  st, dl: dl, log: rec,
	}
}

var _ = newRegistry // keep the sweep helper referenced from one place

// The headline behaviour of #101: a release for an aired, wanted item is grabbed
// on the poll, and no per-series search is issued to find it.
func TestFeedPollGrabsAnAiredWantedItemWithoutSearching(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, have: true}, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3]", got)
	}
	if n := len(h.feed.Queries); n != 0 {
		t.Errorf("feed poll issued %d searches, want 0 — the feed is the whole request budget", n)
	}
	if h.feed.Polls != 1 {
		t.Errorf("Recent called %d times, want 1", h.feed.Polls)
	}
	// The feed is not a search, so it leaves the sweep's cadence alone.
	if state := readSearchState(t, h.st, id); state.lastSearched.Valid {
		t.Error("feed poll wrote search state")
	}
}

// Entries that match nothing grabbable buy nothing: eligibility is the sweep's,
// evaluated through the same Match.
func TestFeedPollGrabsNothingForIneligibleEntries(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	future := time.Now().Add(48 * time.Hour)

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, h *feedHarness) int64
	}{
		{"unmonitored series", func(t *testing.T, h *feedHarness) int64 {
			return seedSweep(t, h.st, "Placeholder Saga", false, sweepItem{number: 3, airsAt: &past})
		}},
		{"unaired item", func(t *testing.T, h *feedHarness) int64 {
			return seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &future})
		}},
		{"already had", func(t *testing.T, h *feedHarness) int64 {
			return seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, have: true})
		}},
		{"already grabbed", func(t *testing.T, h *feedHarness) int64 {
			return seedSweep(t, h.st, "Placeholder Saga", true,
				sweepItem{number: 3, airsAt: &past, grab: "grabbed"})
		}},
		{"another series", func(t *testing.T, h *feedHarness) int64 {
			return seedSweep(t, h.st, "Unrelated Show", true, sweepItem{number: 3, airsAt: &past})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFeedPoll(t, []indexer.FeedEntry{
				feedEntry("Placeholder Saga", 3, time.Now()),
			}, fakeConfig{})
			tc.setup(t, h)

			if err := h.svc.PollFeedOnce(context.Background()); err != nil {
				t.Fatalf("PollFeedOnce: %v", err)
			}
			if len(h.dl.Adds) != 0 {
				t.Errorf("download Add called %d times, want 0: %+v", len(h.dl.Adds), h.dl.Adds)
			}
		})
	}
}

// The profile floor refuses the release, and the feed inherits that refusal
// because it drives the same decide layer.
func TestFeedPollHonoursTheProfileFloor(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now()),
	}, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0 — the release is below the floor", len(h.dl.Adds))
	}
}

// #125 is in the shared path, so the feed inherits it: a pack is never taken
// unattended, whichever entry point saw it.
func TestFeedPollNeverGrabsASeasonPack(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	pack := packRelease("Placeholder Saga")
	h := newFeedPoll(t, []indexer.FeedEntry{
		{Release: pack, GUID: "guid-pack", Published: time.Now()},
	}, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, airsAt: &past}, sweepItem{number: 2, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("feed poll grabbed a season pack: %+v", h.dl.Adds)
	}
}

// The high-water mark's whole purpose. Seeding the series only *after* the first
// poll is what makes the dedupe observable: had the entry been re-processed, it
// would grab on the second poll. A grab row would otherwise absorb the evidence,
// since a grabbed item stops being a candidate anyway.
func TestFeedPollDoesNotReprocessASeenEntry(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	published := time.Now().Add(-10 * time.Minute)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, published),
	}, fakeConfig{})

	ctx := context.Background()
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("first PollFeedOnce: %v", err)
	}
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("second poll added %+v, want none — the entry was already seen", h.dl.Adds)
	}
	if h.feed.Polls != 2 {
		t.Errorf("Recent called %d times, want 2", h.feed.Polls)
	}
	// And the sweep is exactly the safety net that covers what the feed skipped.
	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Errorf("sweep added %d, want 1 — it must still cover a series the feed passed", len(h.dl.Adds))
	}
}

// A feed that publishes no pubDate at all still dedupes, on entry ids alone.
func TestFeedPollDedupesAFeedWithoutPublishDates(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Time{}),
	}, fakeConfig{})

	ctx := context.Background()
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("first PollFeedOnce: %v", err)
	}
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("second poll added %+v, want none — an undated entry dedupes on its id", h.dl.Adds)
	}
}

// An indexer with no recent feed is a supported configuration: sweep behaviour
// is untouched and nothing is logged at error level.
func TestFeedPollWithoutTheCapabilityIsAQuietNoOp(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{episodeRelease("Placeholder Saga", 3)}}
	h := newFeedPollWith(t, idx, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 || len(idx.Queries) != 0 {
		t.Errorf("poll acted without a feed: %d adds, %d searches", len(h.dl.Adds), len(idx.Queries))
	}
	if n := h.log.counts[slog.LevelError]; n != 0 {
		t.Errorf("logged %d error records; a missing capability is not a failure", n)
	}
	if n := h.log.counts[slog.LevelWarn]; n != 0 {
		t.Errorf("logged %d warnings; a missing capability is not a failure", n)
	}

	// The sweep still finds the same item, unchanged.
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Errorf("sweep added %d, want 1 — sweep behaviour must be unchanged", len(h.dl.Adds))
	}
}

// The kill switch is read per run, so the job stays registered and inert.
func TestFeedPollNoOpsWhenAutomationDisabled(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now()),
	}, fakeConfig{automationOff: true})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if h.feed.Polls != 0 || len(h.dl.Adds) != 0 {
		t.Errorf("disabled poll fetched %d feeds and added %d torrents, want none",
			h.feed.Polls, len(h.dl.Adds))
	}
}

// An unconfigured integration is a supported state: the poll waits for Settings
// to supply both clients rather than erroring every tick.
func TestFeedPollNoOpsWithoutADownloadClient(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	st := coretest.NewStore(t)
	feed := &coretest.FakeFeed{Entries: []indexer.FeedEntry{feedEntry("Placeholder Saga", 3, time.Now())}}
	reg := clients.New()
	reg.SetIndexer(feed)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)
	seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if feed.Polls != 0 {
		t.Errorf("Recent called %d times without a download client, want 0", feed.Polls)
	}
}
