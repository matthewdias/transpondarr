package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store"
)

// searchCadence reads a series' accumulated sweep backoff and next-due stamp.
func searchCadence(t *testing.T, h *harness, seriesID int64) (int, *string) {
	t.Helper()
	var backoff int
	var next *string
	if err := h.store.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, seriesID).
		Scan(&backoff, &next); err != nil {
		t.Fatalf("read search cadence: %v", err)
	}
	return backoff, next
}

// Re-monitoring a series is a request for it to be looked after now, not once
// the backoff it accumulated before being paused has run down.
func TestEnableMonitoringResetsSearchCadence(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE series SET monitored = 0, search_backoff = 9, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), seriesID); err != nil {
		t.Fatalf("seed a paused, backed-off series: %v", err)
	}

	if code := do(t, h, "PATCH", fmt.Sprintf("/api/v1/series/%d", seriesID),
		map[string]any{"monitored": true}, nil); code != http.StatusOK {
		t.Fatalf("monitor status = %d, want 200", code)
	}

	if backoff, next := searchCadence(t, h, seriesID); backoff != 0 || next != nil {
		t.Errorf("cadence = backoff %d, next %v; want it reset on re-monitoring", backoff, next)
	}
}

// Unmonitoring is not a reason to clear the cadence: the due query already
// excludes the series, and discarding the backoff would lose real information.
func TestDisableMonitoringLeavesSearchCadence(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 9 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("seed a backed-off series: %v", err)
	}

	if code := do(t, h, "PATCH", fmt.Sprintf("/api/v1/series/%d", seriesID),
		map[string]any{"monitored": false}, nil); code != http.StatusOK {
		t.Fatalf("unmonitor status = %d, want 200", code)
	}

	if backoff, _ := searchCadence(t, h, seriesID); backoff != 9 {
		t.Errorf("search_backoff = %d, want the accumulated 9", backoff)
	}
}
