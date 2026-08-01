package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type blocklistEntryJSON struct {
	ID           int64  `json:"id"`
	ReleaseTitle string `json:"release_title"`
	InfoHash     string `json:"infohash"`
	Reason       string `json:"reason"`
	Failures     int    `json:"failures"`
	BlockedUntil string `json:"blocked_until"`
	Active       bool   `json:"active"`
	CreatedAt    string `json:"created_at"`
}

type blocklistJSON struct {
	Series  string               `json:"series"`
	Entries []blocklistEntryJSON `json:"entries"`
}

func seedBlocklistEntry(t *testing.T, st *store.Store, seriesID int64, title string, until sql.NullString) db.ReleaseBlocklist {
	t.Helper()
	e, err := st.Q.UpsertBlocklistEntry(context.Background(), db.UpsertBlocklistEntryParams{
		SeriesID:        seriesID,
		InfoHash:        "hash-" + title,
		ReleaseTitle:    title,
		NormalizedTitle: decide.NormalizeReleaseTitle(title),
		Reason:          "the download client reported an error",
		BlockedUntil:    until,
	})
	if err != nil {
		t.Fatalf("seed blocklist entry: %v", err)
	}
	return e
}

func TestListAndClearSeriesBlocklist(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	other := seedSeries(t, h.store, "Unrelated Show", 1)

	live := seedBlocklistEntry(t, h.store, seriesID, "[TopSubs] Placeholder Saga - 03 [1080p]",
		sql.NullString{String: store.FormatTimestamp(time.Now().Add(24 * time.Hour)), Valid: true})
	seedBlocklistEntry(t, h.store, seriesID, "[OldSubs] Placeholder Saga - 02 [1080p]",
		sql.NullString{String: store.FormatTimestamp(time.Now().Add(-time.Hour)), Valid: true})
	permanent := seedBlocklistEntry(t, h.store, seriesID, "[DeadSubs] Placeholder Saga - 01 [1080p]", sql.NullString{})
	elsewhere := seedBlocklistEntry(t, h.store, other, "[TopSubs] Unrelated Show - 01 [1080p]", sql.NullString{})

	var got blocklistJSON
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/blocklist", seriesID), &got); code != http.StatusOK {
		t.Fatalf("list blocklist status = %d, want 200", code)
	}
	if got.Series != "Placeholder Saga" {
		t.Errorf("series = %q", got.Series)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 (expired ones are history, not deleted)", len(got.Entries))
	}
	byID := map[int64]blocklistEntryJSON{}
	for _, e := range got.Entries {
		byID[e.ID] = e
	}
	if e := byID[live.ID]; !e.Active || e.BlockedUntil == "" || e.Failures != 1 {
		t.Errorf("live entry = %+v, want active with an expiry and 1 failure", e)
	}
	if e := byID[permanent.ID]; !e.Active || e.BlockedUntil != "" {
		t.Errorf("permanent entry = %+v, want active with no expiry", e)
	}
	if _, ok := byID[elsewhere.ID]; ok {
		t.Error("another series' entry leaked into this series' blocklist")
	}
	if byID[live.ID].Reason == "" {
		t.Error("reason not reported")
	}

	// Unblocking is scoped to the series.
	if code := do(t, h, http.MethodDelete,
		fmt.Sprintf("/api/v1/series/%d/blocklist/%d", seriesID, elsewhere.ID), nil, nil); code != http.StatusNotFound {
		t.Errorf("delete of another series' entry = %d, want 404", code)
	}
	if code := do(t, h, http.MethodDelete,
		fmt.Sprintf("/api/v1/series/%d/blocklist/%d", seriesID, live.ID), nil, nil); code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", code)
	}
	if code := do(t, h, http.MethodDelete,
		fmt.Sprintf("/api/v1/series/%d/blocklist/%d", seriesID, live.ID), nil, nil); code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", code)
	}

	var after blocklistJSON
	h.get(t, fmt.Sprintf("/api/v1/series/%d/blocklist", seriesID), &after)
	if len(after.Entries) != 2 {
		t.Errorf("entries after unblock = %d, want 2", len(after.Entries))
	}
}

func TestBlocklistOfUnknownSeriesIs404(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := h.get(t, "/api/v1/series/9999/blocklist", nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}
