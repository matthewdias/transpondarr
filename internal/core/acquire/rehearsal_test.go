package acquire_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// rehearsalHarness is the sweep harness plus a notifier, for #116's tests.
type rehearsalHarness struct {
	svc *acquire.Service
	st  *store.Store
	dl  *coretest.FakeDownload
	fn  *coretest.FakeNotifier
}

func newRehearsal(t *testing.T, releases []indexer.Release, cfg fakeConfig) *rehearsalHarness {
	t.Helper()
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: releases}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "swept", Outcome: download.AddSuccess}}
	reg := newRegistry(idx, dl)
	fn := withNotifier(reg)
	return &rehearsalHarness{
		svc: acquire.New(st, reg, fakeTitles{}, cfg, discardLogger(), nil),
		st:  st, dl: dl, fn: fn,
	}
}

func wantRehearsalEvent(t *testing.T, fn *coretest.FakeNotifier) notify.Event {
	t.Helper()
	select {
	case ev := <-fn.Events:
		if ev.Kind != notify.KindRehearsal {
			t.Fatalf("kind = %s, want rehearsal", ev.Kind)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the rehearsal event")
		return notify.Event{}
	}
}

// #116's headline acceptance: in notify-only the sweep searches and decides for
// real, names the release it would have grabbed, and nothing reaches the
// download client or the grab table.
func TestNotifyOnlySweepReportsInsteadOfGrabbing(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newRehearsal(t, []indexer.Release{episodeRelease("Placeholder Saga", 5)},
		fakeConfig{notifyOnly: true})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 5, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	ev := wantRehearsalEvent(t, h.fn)
	if ev.SeriesTitle != "Placeholder Saga" {
		t.Errorf("series = %q", ev.SeriesTitle)
	}
	if ev.ReleaseTitle != "[ExampleSubs] Placeholder Saga - 05 [1080p]" {
		t.Errorf("release = %q, want the release it would have grabbed", ev.ReleaseTitle)
	}
	if ev.ItemNumber != 5 {
		t.Errorf("item = %d, want 5", ev.ItemNumber)
	}
	if ev.Error != "would have grabbed" {
		t.Errorf("outcome = %q, want the would-grab spelled out", ev.Error)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times in notify-only, want 0", len(h.dl.Adds))
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabs recorded in notify-only: %v", got)
	}

	// The search cadence advances — a rehearsal must not re-decide the same item
	// every tick. The grab-driven reset is what it cannot rehearse: a real grab
	// would have made this series due next tick, a would-grab backs it off.
	state := readSearchState(t, h.st, id)
	if !state.lastSearched.Valid {
		t.Error("last_searched_at not advanced by a rehearsed pass")
	}
	if state.backoff == 0 || !state.nextSearchAt.Valid {
		t.Errorf("rehearsed pass left backoff=%d next=%v; a would-grab must not make the series due every tick",
			state.backoff, state.nextSearchAt)
	}
}

// The negative half: a series whose only coverage is ineligible reports what it
// refused and why, because that is exactly the misconfiguration a rehearsal
// exists to surface.
func TestNotifyOnlySweepReportsNothingEligible(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newRehearsal(t, []indexer.Release{packRelease("Placeholder Saga")},
		fakeConfig{notifyOnly: true})
	seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, airsAt: &past}, sweepItem{number: 2, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	ev := wantRehearsalEvent(t, h.fn)
	if ev.Error == "" {
		t.Fatal("a no-action rehearsal must say why nothing would have been grabbed")
	}
	if !strings.Contains(ev.ReleaseTitle, "[Batchers]") {
		t.Errorf("release = %q, want the refused pack named", ev.ReleaseTitle)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0", len(h.dl.Adds))
	}
}

// A search that finds nothing at all still reports, with the reason.
func TestNotifyOnlySweepReportsEmptySearch(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newRehearsal(t, nil, fakeConfig{notifyOnly: true})
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 5, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	ev := wantRehearsalEvent(t, h.fn)
	if !strings.Contains(ev.Error, "no matching release found") || ev.ReleaseTitle != "" {
		t.Errorf("empty search rehearsed as %+v, want a reason and no release", ev)
	}
	if ev.ItemNumber != 5 {
		t.Errorf("item = %d, want the single wanted episode", ev.ItemNumber)
	}
}

// A pinned-group hold is a decision too: the rehearsal names the release it is
// waiting out, which is how a user validates the hold window empirically (#62).
func TestNotifyOnlySweepReportsPinHold(t *testing.T) {
	aired := time.Now().Add(-time.Hour)
	h := newRehearsal(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)},
		fakeConfig{notifyOnly: true, pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	pinSeries(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	ev := wantRehearsalEvent(t, h.fn)
	if !strings.Contains(ev.Error, "pinned group") {
		t.Errorf("outcome = %q, want the pinned-group hold named", ev.Error)
	}
	// A push carries a human duration, not a database timestamp.
	if strings.Contains(ev.Error, "T") || strings.Contains(ev.Error, ":00") {
		t.Errorf("outcome = %q, want a duration rather than a raw timestamp", ev.Error)
	}
	if ev.ReleaseTitle == "" {
		t.Error("a held rehearsal should name the release being waited out")
	}
	// The hold window still writes the real cadence: due again when it closes.
	state := readSearchState(t, h.st, id)
	wantNextSearchNear(t, state.nextSearchAt, aired.Add(6*time.Hour))
}

// The feed poll shares grabPass, so notify-only must stop it grabbing too.
func TestNotifyOnlyFeedReportsInsteadOfGrabbing(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	st := coretest.NewStore(t)
	feed := &coretest.FakeFeed{Entries: []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "polled", Outcome: download.AddSuccess}}
	reg := clients.New()
	reg.SetIndexer(feed)
	reg.SetDownload(dl)
	fn := withNotifier(reg)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{notifyOnly: true}, discardLogger(), nil)
	id := seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	ev := wantRehearsalEvent(t, fn)
	if ev.ItemNumber != 3 || ev.Error != "would have grabbed" {
		t.Errorf("feed rehearsal = %+v, want a would-grab for episode 3", ev)
	}
	if len(dl.Adds) != 0 {
		t.Errorf("feed poll added %d torrents in notify-only, want 0", len(dl.Adds))
	}
	if got := grabbedItemNumbers(t, st, id); len(got) != 0 {
		t.Errorf("feed poll recorded grabs in notify-only: %v", got)
	}
}

// The feed page mostly holds other series' releases, so a poll that would take
// nothing for a series stays silent — the searched sweep owns "here's why not".
func TestNotifyOnlyFeedStaysSilentWhenNothingWouldBeTaken(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	st := coretest.NewStore(t)
	feed := &coretest.FakeFeed{Entries: []indexer.FeedEntry{
		feedEntry("Unrelated Show", 9, time.Now().Add(-10*time.Minute)),
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "polled", Outcome: download.AddSuccess}}
	reg := clients.New()
	reg.SetIndexer(feed)
	reg.SetDownload(dl)
	fn := withNotifier(reg)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{notifyOnly: true}, discardLogger(), nil)
	seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	select {
	case ev := <-fn.Events:
		t.Fatalf("feed poll dispatched %+v for a page with nothing to take", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// A rehearsed entry is consumed like any other: the mark advances, so a quiet
// feed stays one request and the 15-minute poll is not a repeating firehose.
// What that costs — the entry never comes around again — is why switching on
// resets the search cadence, which the sweep then re-finds by searching.
func TestNotifyOnlyFeedAdvancesItsMark(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	st := coretest.NewStore(t)
	feed := &coretest.FakeFeed{Entries: []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "polled", Outcome: download.AddSuccess}}
	reg := clients.New()
	reg.SetIndexer(feed)
	reg.SetDownload(dl)
	fn := withNotifier(reg)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{notifyOnly: true}, discardLogger(), nil)
	seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	for i := range 2 {
		if err := svc.PollFeedOnce(context.Background()); err != nil {
			t.Fatalf("PollFeedOnce %d: %v", i, err)
		}
	}
	wantRehearsalEvent(t, fn)
	select {
	case ev := <-fn.Events:
		t.Fatalf("the second poll re-reported %+v; a rehearsed entry is still seen", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// A hold covers only its own items. Episodes nothing matched at all must still
// be reported, or an unrelated pin delay hides exactly the gap being rehearsed.
func TestNotifyOnlySweepReportsUncoveredItemsBesideAHold(t *testing.T) {
	aired := time.Now().Add(-time.Hour)
	h := newRehearsal(t, []indexer.Release{episodeRelease("Placeholder Saga", 1)},
		fakeConfig{notifyOnly: true, pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, airsAt: &aired},
		sweepItem{number: 2, airsAt: &aired},
		sweepItem{number: 3, airsAt: &aired})
	pinSeries(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	var held, unmatched bool
	for range 2 {
		ev := wantRehearsalEvent(t, h.fn)
		switch {
		case strings.Contains(ev.Error, "pinned group"):
			held = true
		case strings.Contains(ev.Error, "no matching release found"):
			unmatched = true
		}
	}
	if !held || !unmatched {
		t.Errorf("held=%t unmatched=%t; both the hold and the unmatched episodes must be reported",
			held, unmatched)
	}
}

// A hand-triggered run rehearses too: Run now decides when the job runs, never
// whether it may grab — notify-only means nothing reaches the download client.
func TestNotifyOnlyManualRunStillRehearses(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newRehearsal(t, []indexer.Release{episodeRelease("Placeholder Saga", 5)},
		fakeConfig{notifyOnly: true})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 5, airsAt: &past})

	if err := h.svc.SweepOnce(jobs.WithManualRun(context.Background())); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	wantRehearsalEvent(t, h.fn)
	if len(h.dl.Adds) != 0 || len(grabbedItemNumbers(t, h.st, id)) != 0 {
		t.Error("a manual run grabbed for real in notify-only")
	}
}

// #116's closing acceptance, against the real settings service: flipping
// notify-only to on grabs on the next pass with no other change, and flipping
// to off silences the rehearsal.
func TestNotifyOnlyFlipsToOnAndOffLive(t *testing.T) {
	ctx := context.Background()
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{episodeRelease("Placeholder Saga", 5)}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "swept", Outcome: download.AddSuccess}}
	reg := newRegistry(nil, nil)
	cfg, err := settings.New(ctx, st, &config.Config{AutomationEnabled: "notify_only"}, reg, discardLogger())
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	reg.SetIndexer(idx)
	reg.SetDownload(dl)
	fn := withNotifier(reg)
	svc := acquire.New(st, reg, fakeTitles{}, cfg, discardLogger(), nil)
	past := time.Now().Add(-2 * time.Hour)
	id := seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 5, airsAt: &past})

	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce in notify-only: %v", err)
	}
	wantRehearsalEvent(t, fn)
	if len(dl.Adds) != 0 {
		t.Fatalf("notify-only added %d torrents", len(dl.Adds))
	}

	// No help from the test: a rehearsed pass leaves the series backed off, and
	// flipping to on is what has to clear that (#116) — a user has no way to
	// reach in and make the series due.
	if state := readSearchState(t, st, id); state.backoff == 0 {
		t.Fatal("precondition: the rehearsed pass did not back the series off")
	}
	if err := cfg.UpdateAutomation(ctx, settings.AutomationConfig{Mode: settings.AutomationOn}); err != nil {
		t.Fatalf("flip to on: %v", err)
	}
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce after flipping on: %v", err)
	}
	if got := grabbedItemNumbers(t, st, id); len(got) != 1 || got[0] != 5 {
		t.Fatalf("grabbed items after flipping on = %v, want [5]", got)
	}

	if err := cfg.UpdateAutomation(ctx, settings.AutomationConfig{Mode: settings.AutomationOff}); err != nil {
		t.Fatalf("flip to off: %v", err)
	}
	// Due again, so "off" is proven to stop a series that would otherwise search.
	makeSeriesDue(t, st, id)
	drainEvents(fn)
	searches := len(idx.Queries)
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce after flipping off: %v", err)
	}
	if len(idx.Queries) != searches {
		t.Error("the sweep kept searching after automation was turned off")
	}
	select {
	case ev := <-fn.Events:
		t.Fatalf("off still dispatched %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func makeSeriesDue(t *testing.T, st *store.Store, id int64) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET next_search_at = NULL WHERE id = ?`, id); err != nil {
		t.Fatalf("clear next_search_at: %v", err)
	}
}

func drainEvents(fn *coretest.FakeNotifier) {
	for {
		select {
		case <-fn.Events:
		default:
			return
		}
	}
}
