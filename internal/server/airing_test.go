package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// Air dates must reach the episodes table, and reach it as an unambiguous
// timestamp: the stored form is SQLite's zone-less UTC, which a browser would
// otherwise read as local time and shift the row by hours.
func TestTitleDetailSurfacesAirDates(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 2)

	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET airs_at = '2026-01-04 15:30:00' WHERE series_id = ? AND number = 1`,
		titleID); err != nil {
		t.Fatalf("seed air date: %v", err)
	}

	var out struct {
		Items []struct {
			Number int    `json:"number"`
			AirsAt string `json:"airs_at"`
		} `json:"items"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", titleID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	if len(out.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(out.Items))
	}

	if got := out.Items[0].AirsAt; got != "2026-01-04T15:30:00Z" {
		t.Errorf("episode 1 airs_at = %q, want 2026-01-04T15:30:00Z", got)
	}
	// AniList's schedule coverage thins out badly before ~2015, so "no air date"
	// is a normal row, not an error.
	if got := out.Items[1].AirsAt; got != "" {
		t.Errorf("episode 2 airs_at = %q, want it omitted", got)
	}
}
