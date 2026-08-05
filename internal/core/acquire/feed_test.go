package acquire_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// levelRecorder counts records per level and keeps their messages, so a test can
// assert that a supported configuration stayed off the error channel — and that
// a genuine warning was actually emitted.
type levelRecorder struct {
	slog.Handler
	mu       sync.Mutex
	counts   map[slog.Level]int
	messages []string
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[rec.Level]++
	r.messages = append(r.messages, rec.Message)
	return nil
}

// logged reports whether any record's message contains want.
func (r *levelRecorder) logged(want string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.messages {
		if strings.Contains(m, want) {
			return true
		}
	}
	return false
}

func (r *levelRecorder) count(l slog.Level) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[l]
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
	return newFeedPollWithTitles(t, entries, cfg, fakeTitles{})
}

// newFeedPollWithTitles is newFeedPoll over a chosen title source, so a test can
// vary how (and whether) variants are answered.
func newFeedPollWithTitles(t *testing.T, entries []indexer.FeedEntry, cfg fakeConfig, titles acquire.TitleSource) *feedHarness {
	t.Helper()
	feed := &coretest.FakeFeed{Entries: entries}
	for _, e := range entries {
		feed.Releases = append(feed.Releases, e.Release)
	}
	h := newFeedPollWith(t, feed, cfg, titles)
	h.feed = feed
	return h
}

// newFeedPollWith takes the indexer directly, so a test can supply one with no
// recent-feed capability at all.
func newFeedPollWith(t *testing.T, idx indexer.Indexer, cfg fakeConfig, titles acquire.TitleSource) *feedHarness {
	t.Helper()
	st := coretest.NewStore(t)
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "polled", Outcome: download.AddSuccess}}
	rec := &levelRecorder{counts: map[slog.Level]int{}}
	reg := clients.New()
	reg.SetIndexer(idx)
	reg.SetDownload(dl)
	return &feedHarness{
		svc: acquire.New(st, reg, titles, cfg, slog.New(rec), &fakeRecorder{}),
		st:  st, dl: dl, log: rec,
	}
}

// fakeCachedTitles answers variants from a fixed snapshot, counting each route so
// a test can tell a cache read from a provider fetch.
type fakeCachedTitles struct {
	cached      map[int64][]string
	err         error
	fetchCalls  int
	cachedCalls int
}

func (f *fakeCachedTitles) TitleVariants(_ context.Context, id int64) ([]string, error) {
	f.fetchCalls++
	return f.cached[id], nil
}

func (f *fakeCachedTitles) CachedTitleVariants(_ context.Context, id int64) ([]string, bool, error) {
	f.cachedCalls++
	if f.err != nil {
		return nil, false, f.err
	}
	v, ok := f.cached[id]
	return v, ok, nil
}

// setSeriesAnilistID gives a seeded series a provider id, which is what makes the
// variant lookup reachable at all.
func setSeriesAnilistID(t *testing.T, st *store.Store, seriesID, anilistID int64) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET anilist_id = ? WHERE id = ?`, anilistID, seriesID); err != nil {
		t.Fatalf("set anilist_id on series %d: %v", seriesID, err)
	}
}

// The acceptance criterion of #139: the poll matches on a variant it read from the
// metadata cache, and spends no provider request doing it.
func TestFeedPollMatchesOnCachedVariantWithoutFetching(t *testing.T) {
	const english = "Fixture of the Sky"
	past := time.Now().Add(-2 * time.Hour)
	titles := &fakeCachedTitles{cached: map[int64][]string{42: {english}}}
	h := newFeedPollWithTitles(t, []indexer.FeedEntry{
		feedEntry(english, 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{}, titles)
	id := seedSweep(t, h.st, "Sora no Fixture", true, sweepItem{number: 3, airsAt: &past})
	setSeriesAnilistID(t, h.st, id, 42)

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] — the cached variant should match", got)
	}
	if titles.fetchCalls != 0 {
		t.Errorf("poll made %d fetching variant lookups, want 0", titles.fetchCalls)
	}
	if titles.cachedCalls != 1 {
		t.Errorf("poll made %d cache-only variant lookups, want 1", titles.cachedCalls)
	}
}

// No snapshot degrades to the stored title, never to the fetching path.
func TestFeedPollCacheMissStillMatchesStoredTitle(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	titles := &fakeCachedTitles{}
	h := newFeedPollWithTitles(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{}, titles)
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	setSeriesAnilistID(t, h.st, id, 42)

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] — the stored title still matches", got)
	}
	if titles.fetchCalls != 0 {
		t.Errorf("a cache miss made %d fetching lookups, want 0", titles.fetchCalls)
	}
}

// An unreadable cache degrades like a miss, and says so at debug level so a
// persistently broken read is not silent.
func TestFeedPollCacheErrorStillMatchesStoredTitle(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	titles := &fakeCachedTitles{err: errors.New("db down")}
	h := newFeedPollWithTitles(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{}, titles)
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	setSeriesAnilistID(t, h.st, id, 42)

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] — the stored title still matches", got)
	}
	if titles.fetchCalls != 0 {
		t.Errorf("a cache error made %d fetching lookups, want 0", titles.fetchCalls)
	}
	if !h.log.logged("cached title variants unreadable") {
		t.Error("the degradation was not logged")
	}
}

// A title source without the cache capability degrades the same way: the
// cross-language match waits for the bounded sweep rather than spending a request.
func TestFeedPollWithoutCacheCapabilityUsesStoredTitleOnly(t *testing.T) {
	const english = "Fixture of the Sky"
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPollWithTitles(t, []indexer.FeedEntry{
		feedEntry(english, 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{}, fakeTitles{variants: map[int64][]string{42: {english}}})
	id := seedSweep(t, h.st, "Sora no Fixture", true, sweepItem{number: 3, airsAt: &past})
	setSeriesAnilistID(t, h.st, id, 42)

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed items = %v, want none — the variant must not be fetched", got)
	}
}

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
	// The feed is not a search, so it leaves the sweep's cadence alone — a
	// detected gap resets it (#140), but a poll that recognised its page had none.
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

// Eligibility lives in the shared path, so the feed inherits #126's lift too: a
// pack is a candidate at either entry point, not just in the sweep.
func TestFeedPollGrabsASeasonPack(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	pack := packRelease("Placeholder Saga")
	h := newFeedPoll(t, []indexer.FeedEntry{
		{Release: pack, GUID: "guid-pack", Published: time.Now()},
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, airsAt: &past}, sweepItem{number: 2, airsAt: &past},
		sweepItem{number: 3, airsAt: &past}, sweepItem{number: 4, airsAt: &past},
		sweepItem{number: 5, airsAt: &past}, sweepItem{number: 6, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want 1", len(h.dl.Adds))
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 6 {
		t.Errorf("grabbed items = %v, want all six under the one pack", got)
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
	h := newFeedPollWith(t, idx, fakeConfig{}, fakeTitles{})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 || len(idx.Queries) != 0 {
		t.Errorf("poll acted without a feed: %d adds, %d searches", len(h.dl.Adds), len(idx.Queries))
	}
	if n := h.log.count(slog.LevelError); n != 0 {
		t.Errorf("logged %d error records; a missing capability is not a failure", n)
	}
	if n := h.log.count(slog.LevelWarn); n != 0 {
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

// A hand-triggered poll is explicit intent, so it passes the kill switch the way
// a manual grab passes eligibility (PR #57).
func TestFeedPollRunsWithAutomationDisabledWhenTriggeredByHand(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now()),
	}, fakeConfig{automationOff: true})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(jobs.WithManualRun(context.Background())); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if h.feed.Polls != 1 {
		t.Errorf("a manually triggered poll fetched %d feeds, want 1", h.feed.Polls)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Errorf("grabbed items = %v, want [3]", got)
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

// A failed feed fetch is an indexer fault, reported as one — and it must not
// advance the mark, or the page it never saw would be skipped forever.
func TestFeedPollReportsAFetchFailureAndKeepsTheMark(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now()),
	}, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	h.feed.FeedErr = errors.New("prowlarr: 502 bad gateway")

	ctx := context.Background()
	err := h.svc.PollFeedOnce(ctx)
	if !errors.Is(err, acquire.ErrIndexerSearch) {
		t.Fatalf("err = %v, want it to wrap ErrIndexerSearch", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("a failed fetch grabbed %+v", h.dl.Adds)
	}

	// The mark never moved, so the page is still new once the indexer recovers.
	h.feed.FeedErr = nil
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("recovered PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Errorf("download Add called %d times after recovery, want 1", len(h.dl.Adds))
	}
}

// Recognising nothing on a page means the mark scrolled off it: the feed moved
// further than one page between polls, and the sweep owns the gap.
func TestFeedPollWarnsWhenTheMarkScrolledOff(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 1, time.Now().Add(-time.Hour)),
	}, fakeConfig{})
	ctx := context.Background()
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("first PollFeedOnce: %v", err)
	}
	if h.log.logged("moved more than one page") {
		t.Error("warned on the first poll, when there was no mark to scroll off")
	}

	// A wholly different page: nothing on it is recognised.
	h.feed.Entries = []indexer.FeedEntry{feedEntry("Placeholder Saga", 9, time.Now())}
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	if !h.log.logged("moved more than one page") {
		t.Error("no gap warning when the whole page was unrecognised")
	}
	if h.log.logged("front of the sweep") {
		t.Error("the warning claimed a reset when nothing qualified for one")
	}
	if !h.log.logged("waits for the sweep's backoff") {
		t.Error("an empty recovery should say the sweep's backoff still owns the gap")
	}
}

// seedGapCadence backs a series off to a long wait, which is what a gap recovery
// has to undo — and what makes a reset observable at all.
func seedGapCadence(t *testing.T, st *store.Store, id int64, backoff int, next time.Time) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = ?, next_search_at = ? WHERE id = ?`,
		backoff, store.FormatTimestamp(next), id); err != nil {
		t.Fatalf("seed cadence on series %d: %v", id, err)
	}
}

// pollThenGap runs a first poll to lay down a mark at since, then swaps in a
// wholly unrecognised page so the next poll reads as a gap.
func pollThenGap(t *testing.T, h *feedHarness, since time.Time) {
	t.Helper()
	h.feed.Entries = []indexer.FeedEntry{feedEntry("Placeholder Saga", 1, since)}
	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("first PollFeedOnce: %v", err)
	}
	h.feed.Entries = []indexer.FeedEntry{feedEntry("Unrelated Show", 9, time.Now())}
}

// The acceptance criterion of #140: a release that fell through a feed gap is
// searched materially sooner than the backoff cap, because the poll resets the
// sweep for the series whose broadcast happened inside the gap.
func TestFeedPollGapResetsASeriesThatAiredInsideIt(t *testing.T) {
	now := time.Now()
	h := newFeedPoll(t, nil, fakeConfig{})
	pollThenGap(t, h, now.Add(-2*time.Hour))

	aired := now.Add(-time.Hour)
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	seedGapCadence(t, h.st, id, 6, now.Add(20*time.Hour))

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	if !h.log.logged("front of the sweep") {
		t.Error("no gap warning naming the recovery when a series was reset")
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 0 || state.nextSearchAt.Valid {
		t.Errorf("backoff = %d, next_search_at = %+v; want 0 and NULL — the gap must put the series back in the sweep's queue",
			state.backoff, state.nextSearchAt)
	}
}

// The other half of #140's acceptance: a routine gap on a high-volume indexer
// must not become a library-wide reset, so a series whose broadcast is nowhere
// near the gap keeps its place on the ladder.
func TestFeedPollGapLeavesSeriesOutsideTheWindowAlone(t *testing.T) {
	now := time.Now()
	h := newFeedPoll(t, nil, fakeConfig{})
	pollThenGap(t, h, now.Add(-2*time.Hour))

	aired := now.Add(-72 * time.Hour)
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	seedGapCadence(t, h.st, id, 6, now.Add(20*time.Hour))

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 6 {
		t.Errorf("backoff = %d, want 6 — a back-catalogue series never fell through this gap", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, now.Add(20*time.Hour))
}

// A rip is published after its episode airs, so an item that aired shortly
// before the mark can still have fallen through the gap behind it.
func TestFeedPollGapResetCoversPublishLagBeforeTheMark(t *testing.T) {
	now := time.Now()
	since := now.Add(-2 * time.Hour)
	h := newFeedPoll(t, nil, fakeConfig{})
	pollThenGap(t, h, since)

	aired := since.Add(-30 * time.Minute)
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	seedGapCadence(t, h.st, id, 6, now.Add(20*time.Hour))

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	if state := readSearchState(t, h.st, id); state.backoff != 0 || state.nextSearchAt.Valid {
		t.Errorf("backoff = %d, next_search_at = %+v; want 0 and NULL — publish lag puts this item inside the gap",
			state.backoff, state.nextSearchAt)
	}
}

// One gap event resets at most what one sweep pass can search, furthest-
// postponed first: the gap is routine on a busy aggregating indexer, so an
// unbounded reset would queue searches the sweep cannot spend.
func TestFeedPollGapResetIsBoundedToOnePass(t *testing.T) {
	now := time.Now()
	h := newFeedPoll(t, nil, fakeConfig{})
	pollThenGap(t, h, now.Add(-2*time.Hour))

	aired := now.Add(-time.Hour)
	ids := make([]int64, 7)
	for i := range ids {
		ids[i] = seedSweep(t, h.st, fmt.Sprintf("Placeholder Saga %d", i), true,
			sweepItem{number: 3, airsAt: &aired})
		seedGapCadence(t, h.st, ids[i], 6, now.Add(time.Duration(i+1)*time.Hour))
	}

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}
	var reset int
	for i, id := range ids {
		state := readSearchState(t, h.st, id)
		if state.backoff == 0 && !state.nextSearchAt.Valid {
			reset++
			continue
		}
		// The two nearest-due series are the ones the ladder makes wait least.
		if i > 1 {
			t.Errorf("series %d was left postponed; the furthest-postponed series come first", i)
		}
	}
	if reset != 5 {
		t.Errorf("reset %d series, want 5 — one gap event may not outrun one sweep pass", reset)
	}
}

// A mark that will not decode costs one re-processed page, never a dead feed —
// the failure mode Sonarr's equivalent field actually has.
func TestFeedPollToleratesACorruptMark(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now()),
	}, fakeConfig{})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})
	ctx := context.Background()
	if err := h.st.Q.UpsertSetting(ctx, db.UpsertSettingParams{
		Key: "feed.seen." + h.feed.Name(), Value: "{not json",
	}); err != nil {
		t.Fatalf("seed a corrupt mark: %v", err)
	}

	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1 — a bad mark reads as a new page", len(h.dl.Adds))
	}
	if !h.log.logged("feed mark unreadable") {
		t.Error("a corrupt mark was swallowed without a warning")
	}
	if n := h.log.count(slog.LevelError); n != 0 {
		t.Errorf("logged %d error records; a corrupt mark is recoverable", n)
	}
}

// An indexer that transiently serves an older page must not rewind the window:
// everything published since would be re-processed on the poll after.
func TestFeedPollDoesNotRewindOnAnOlderPage(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	newer := feedEntry("Placeholder Saga", 3, time.Now())
	older := feedEntry("Placeholder Saga", 4, time.Now().Add(-6*time.Hour))

	h := newFeedPoll(t, []indexer.FeedEntry{newer}, fakeConfig{})
	ctx := context.Background()
	if err := h.svc.PollFeedOnce(ctx); err != nil { // sees the newer page, no series yet
		t.Fatalf("first PollFeedOnce: %v", err)
	}

	h.feed.Entries = []indexer.FeedEntry{older} // a stale page from the indexer
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("second PollFeedOnce: %v", err)
	}

	// Now the series exists and the indexer serves the original page again. The
	// newer entry was already seen, so it must not be taken a second time.
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past}, sweepItem{number: 4, airsAt: &past})
	h.feed.Entries = []indexer.FeedEntry{newer}
	if err := h.svc.PollFeedOnce(ctx); err != nil {
		t.Fatalf("third PollFeedOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("the older page rewound the window: re-grabbed %+v", h.dl.Adds)
	}
}
