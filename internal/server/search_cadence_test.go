package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store"
)

// searchCadence reads a title' accumulated sweep backoff and next-due stamp.
func searchCadence(t *testing.T, h *harness, titleID int64) (int, *string) {
	t.Helper()
	var backoff int
	var next *string
	if err := h.store.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, titleID).
		Scan(&backoff, &next); err != nil {
		t.Fatalf("read search cadence: %v", err)
	}
	return backoff, next
}

// Re-monitoring a title is a request for it to be looked after now, not once
// the backoff it accumulated before being paused has run down.
func TestEnableMonitoringResetsSearchCadence(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 3)
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE series SET monitored = 0, search_backoff = 9, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), titleID); err != nil {
		t.Fatalf("seed a paused, backed-off series: %v", err)
	}

	if code := do(t, h, "PATCH", fmt.Sprintf("/api/v1/titles/%d", titleID),
		map[string]any{"monitored": true}, nil); code != http.StatusOK {
		t.Fatalf("monitor status = %d, want 200", code)
	}

	if backoff, next := searchCadence(t, h, titleID); backoff != 0 || next != nil {
		t.Errorf("cadence = backoff %d, next %v; want it reset on re-monitoring", backoff, next)
	}
}

// Changing the pin changes what the sweep is waiting for, so the wait it is
// currently serving is stale. Without the reset, dropping a 48h wait to 2h — or
// pinning an entirely different group — does nothing until the old window
// closes, which reads as the setting having been ignored.
func TestRepinningResetsSearchCadence(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"a shorter wait", map[string]any{"group": "ShinyRip", "delay_hours": 2}},
		{"a different group", map[string]any{"group": "OtherSubs"}},
		{"clearing the pin", map[string]any{"group": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			titleID := seedTitle(t, h.store, "Placeholder Saga", 3)
			if _, err := h.store.DB.ExecContext(context.Background(),
				`UPDATE series SET pinned_group = 'ShinyRip', pin_delay_hours = 48,
				        search_backoff = 4, next_search_at = ? WHERE id = ?`,
				store.FormatTimestamp(time.Now().Add(48*time.Hour)), titleID); err != nil {
				t.Fatalf("seed a held, backed-off series: %v", err)
			}

			if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/titles/%d/pinned-group", titleID),
				tc.body, nil); code != http.StatusOK {
				t.Fatalf("pin status = %d, want 200", code)
			}

			if backoff, next := searchCadence(t, h, titleID); backoff != 0 || next != nil {
				t.Errorf("cadence = backoff %d, next %v; want it reset so the new pin applies now",
					backoff, next)
			}
		})
	}
}

// Unmonitoring is not a reason to clear the cadence: the due query already
// excludes the title, and discarding the backoff would lose real information.
func TestDisableMonitoringLeavesSearchCadence(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 3)
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 9 WHERE id = ?`, titleID); err != nil {
		t.Fatalf("seed a backed-off series: %v", err)
	}

	if code := do(t, h, "PATCH", fmt.Sprintf("/api/v1/titles/%d", titleID),
		map[string]any{"monitored": false}, nil); code != http.StatusOK {
		t.Fatalf("unmonitor status = %d, want 200", code)
	}

	if backoff, _ := searchCadence(t, h, titleID); backoff != 9 {
		t.Errorf("search_backoff = %d, want the accumulated 9", backoff)
	}
}
