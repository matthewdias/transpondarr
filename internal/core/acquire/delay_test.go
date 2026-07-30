package acquire_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
)

// pinSeries sets a series' pinned group and, when hours >= 0, its per-series
// delay override (a negative value leaves the column NULL = use the global).
func pinSeries(t *testing.T, st *store.Store, id int64, group string, hours int) {
	t.Helper()
	var delay sql.NullInt64
	if hours >= 0 {
		delay = sql.NullInt64{Int64: int64(hours), Valid: true}
	}
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET pinned_group = ?, pin_delay_hours = ? WHERE id = ?`,
		sql.NullString{String: group, Valid: group != ""}, delay, id); err != nil {
		t.Fatalf("pin series: %v", err)
	}
}

// The pinned group is what the delay is waiting for, so it never waits.
func TestSweepGrabsPinnedReleaseImmediately(t *testing.T) {
	aired := time.Now().Add(-time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	pinSeries(t, h.st, id, "ExampleSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] — the pinned group never waits", got)
	}
}

// #62: within the window, another group's release is held rather than taken, and
// the series comes back exactly when the window closes.
func TestSweepHoldsNonPinnedReleaseInsideTheDelay(t *testing.T) {
	aired := time.Now().Add(-time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	pinSeries(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed %v inside the pin delay, want nothing", got)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 0 {
		t.Errorf("backoff = %d, want 0 — a held item is waiting, not failing", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, aired.Add(6*time.Hour))
}

// Once the window closes the wait is over: the best eligible release wins even
// though it is not the pinned group.
func TestSweepGrabsNonPinnedReleaseAfterTheDelay(t *testing.T) {
	aired := time.Now().Add(-7 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	pinSeries(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] once the window has closed", got)
	}
}

// The window measures time since broadcast, so with no broadcast time there is
// no interval to wait out — grab the best eligible release now.
func TestSweepDelayIsInapplicableWithoutAnAirDate(t *testing.T) {
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3})
	pinSeries(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] — no broadcast time, no window", got)
	}
}

// A per-series delay overrides the global default in both directions.
func TestSweepPerSeriesDelayOverridesTheGlobalDefault(t *testing.T) {
	aired := time.Now().Add(-time.Hour)

	t.Run("series waits where the global would not", func(t *testing.T) {
		h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
		id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
		pinSeries(t, h.st, id, "OtherSubs", 6)

		if err := h.svc.SweepOnce(context.Background()); err != nil {
			t.Fatalf("SweepOnce: %v", err)
		}
		if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
			t.Errorf("grabbed %v, want nothing — the series overrides the global 0", got)
		}
	})

	t.Run("series grabs where the global would wait", func(t *testing.T) {
		h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{pinDelay: 6 * time.Hour})
		id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
		pinSeries(t, h.st, id, "OtherSubs", 0)

		if err := h.svc.SweepOnce(context.Background()); err != nil {
			t.Fatalf("SweepOnce: %v", err)
		}
		if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 {
			t.Errorf("grabbed %v, want the release — the series overrides the global 6h", got)
		}
	})
}

// With no pinned group there is nothing to wait for, whatever the global delay.
func TestSweepDelayNeedsAPinnedGroup(t *testing.T) {
	aired := time.Now().Add(-time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] — no pin, no delay", got)
	}
}
