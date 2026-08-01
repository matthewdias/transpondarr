package acquire_test

import (
	"context"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// newSweepWithFeed is newSweep against an indexer that also publishes a recent
// feed, so the sweep sees a configured feed covering the airing window.
func newSweepWithFeed(t *testing.T, releases []indexer.Release, cfg fakeConfig) *sweepHarness {
	t.Helper()
	st := coretest.NewStore(t)
	idx := &coretest.FakeFeed{FakeIndexer: coretest.FakeIndexer{Releases: releases}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "swept", Outcome: download.AddSuccess}}
	rec := &fakeRecorder{}
	return &sweepHarness{
		svc: acquire.New(st, newRegistry(idx, dl), fakeTitles{}, cfg, discardLogger(), rec),
		st:  st, idx: &idx.FakeIndexer, dl: dl, rec: rec,
	}
}

// With a feed configured, a newly aired episode no longer resets the sweep's
// backoff: the feed is what covers the broadcast window, and resetting would aim
// the sweep at exactly the moments the feed already handles.
func TestSweepWithFeedDoesNotResetBackoffOnANewlyAiredItem(t *testing.T) {
	now := time.Now()
	justAired := now.Add(-30 * time.Minute)
	h := newSweepWithFeed(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &justAired})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 6, last_searched_at = ?, next_search_at = NULL WHERE id = ?`,
		store.FormatTimestamp(now.Add(-3*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 7 {
		t.Errorf("backoff = %d, want 7 — the broadcast reset belongs to the feedless world", state.backoff)
	}
	// Backoff 7 is past the doubling ladder's cap.
	wantNextSearchNear(t, state.nextSearchAt, before.Add(24*time.Hour))
}

// Nor does an upcoming broadcast clamp the next search: the sweep sleeps on its
// backoff and the feed catches the release when it is published.
func TestSweepWithFeedDoesNotClampToTheNextAirDate(t *testing.T) {
	now := time.Now()
	past := now.Add(-3 * time.Hour)
	soon := now.Add(20 * time.Minute)
	h := newSweepWithFeed(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past},
		sweepItem{number: 4, airsAt: &soon})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 10, last_searched_at = ? WHERE id = ?`,
		store.FormatTimestamp(now.Add(-1*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	wantNextSearchNear(t, readSearchState(t, h.st, id).nextSearchAt, before.Add(24*time.Hour))
}

// The pin-delay window is the sweep's own business — the release already exists,
// so no feed poll will produce it sooner. Only the broadcast reach is dropped.
func TestSweepWithFeedStillWaitsOutThePinDelay(t *testing.T) {
	now := time.Now()
	aired := now.Add(-time.Hour)
	soon := now.Add(20 * time.Minute)
	h := newSweepWithFeed(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)},
		fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &aired}, sweepItem{number: 4, airsAt: &soon})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET pinned_group = 'PinnedSubs' WHERE id = ?`, id); err != nil {
		t.Fatalf("pin a group: %v", err)
	}

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed %v, want nothing while the pin delay holds", got)
	}
	// The hold expires 6h after the covered item's broadcast, and the nearer
	// upcoming air date must not pull it in.
	wantNextSearchNear(t, readSearchState(t, h.st, id).nextSearchAt, aired.Add(6*time.Hour))
}
