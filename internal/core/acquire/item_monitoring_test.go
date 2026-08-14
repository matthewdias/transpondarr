package acquire_test

import (
	"context"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
)

// The headline of #188 on the sweep side: an unmonitored item is not a target,
// even with an eligible release sitting in the results.
func TestSweepNeverNewlyGrabsAnUnmonitoredItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
		episodeRelease("Placeholder Saga", 4),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past, unmonitored: true},
		sweepItem{number: 4, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 4 {
		t.Fatalf("grabbed items = %v, want only the monitored [4]", got)
	}
}

// The cost argument this issue rests on: monitoring is one more reason
// Grabbable is false, so maxItem is unaffected and a pack still matches and
// still covers the items that are monitored.
func TestSweepStillTakesAPackCoveringAMixOfMonitoredItems(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{packRelease("Placeholder Saga")}, fakeConfig{})
	items := make([]sweepItem, 0, 6)
	for n := 1; n <= 6; n++ {
		items = append(items, sweepItem{number: n, airsAt: &past, unmonitored: n <= 3})
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, items...)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	got := grabbedItemNumbers(t, h.st, id)
	if len(got) != 3 {
		t.Fatalf("grabbed items = %v, want the pack claiming exactly the three monitored ones", got)
	}
	for _, n := range got {
		if n <= 3 {
			t.Errorf("grabbed items = %v, want no row for an unmonitored episode", got)
		}
	}
}

// An unmonitored held item leaves the upgrade pool: the feed's due predicate
// drops it, and so must the pass that would otherwise re-grab it (#97).
func TestSweepDoesNotUpgradeAnUnmonitoredHeldItem(t *testing.T) {
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 1)}, fakeConfig{})
	ctx := context.Background()
	if _, err := h.st.DB.ExecContext(ctx,
		`UPDATE quality_profiles SET upgrades_enabled = 1, cutoff_score = 100000 WHERE id = 1`); err != nil {
		t.Fatalf("enable upgrades: %v", err)
	}
	seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{
		number: 1, inLibrary: true, grab: "imported", unmonitored: true,
		heldTitle: "[OtherSubs] Placeholder Saga - 01 [480p]",
	})

	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("added %d torrents, want none for an unmonitored held item", len(h.dl.Adds))
	}
}

// The feed is the other entry point through the same decision layer, so it must
// refuse the same item without a second gate written for it.
func TestFeedPollNeverNewlyGrabsAnUnmonitoredItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past, unmonitored: true})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed items = %v, want nothing for an unmonitored item", got)
	}
}

// A grab already in flight when the item is unmonitored is untouched (decision
// 8): the criterion is "never *newly* grabs". Nothing revokes the claim, so the
// payload still imports and the row still settles.
func TestSweepLeavesAnInFlightGrabOnAnUnmonitoredItemAlone(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past, grab: "grabbed", unmonitored: true},
		sweepItem{number: 4, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	grabs, err := h.st.Q.ListGrabsByTitle(context.Background(), id)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != "grabbed" {
		t.Fatalf("grabs = %+v, want the one in-flight row untouched", grabs)
	}
}

// #100's next-broadcast clamp is the sweep's only forward-looking reach, and an
// unwanted recap must not spend it: gating the clamp on grabbable rather than on
// monitored would delete it outright, since an unaired item is never grabbable.
func TestSweepDoesNotClampToAnUnmonitoredUpcomingBroadcast(t *testing.T) {
	now := time.Now()
	past := now.Add(-3 * time.Hour)
	recapSoon := now.Add(20 * time.Minute)
	realSoon := now.Add(10 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past},
		sweepItem{number: 4, airsAt: &recapSoon, unmonitored: true},
		sweepItem{number: 5, airsAt: &realSoon})

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	// The empty pass backs off an hour; the monitored broadcast is further out
	// than that, so the recap is the only thing that could have pulled it in.
	wantNextSearchNear(t, readSearchState(t, h.st, id).nextSearchAt, before.Add(time.Hour))
}

// The clamp still fires for a monitored broadcast, so the guard above narrows it
// rather than removing it.
func TestSweepStillClampsToAMonitoredUpcomingBroadcast(t *testing.T) {
	now := time.Now()
	past := now.Add(-3 * time.Hour)
	soon := now.Add(20 * time.Minute)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past},
		sweepItem{number: 4, airsAt: &soon})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	wantNextSearchNear(t, readSearchState(t, h.st, id).nextSearchAt, soon)
}

// The other cadence half: an unmonitored item that broadcast since the last
// search must not zero an accumulated backoff for a release nothing will grab.
func TestSweepDoesNotResetBackoffForAnUnmonitoredBroadcast(t *testing.T) {
	now := time.Now()
	justAired := now.Add(-30 * time.Minute)
	long := now.Add(-30 * 24 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &long},
		sweepItem{number: 4, airsAt: &justAired, unmonitored: true})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 6, last_searched_at = ?, next_search_at = NULL WHERE id = ?`,
		store.FormatTimestamp(now.Add(-3*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := readSearchState(t, h.st, id).backoff; got != 7 {
		t.Errorf("backoff = %d, want 7 -- an unmonitored broadcast is not news", got)
	}
}

// persistOutcomes already skips non-grabbable items, so an unmonitored one gets
// no row -- which is why the read side suppresses the tier rather than waiting
// for it to be invalidated (decision 9).
func TestSweepRecordsNoPassOutcomeForAnUnmonitoredItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past, unmonitored: true},
		sweepItem{number: 4, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if _, ok := passOutcome(t, h.st, id, 3); ok {
		t.Error("an unmonitored item recorded a pass outcome, which nothing would ever revisit")
	}
	wantOutcome(t, h.st, id, 4, "no_match")
}
