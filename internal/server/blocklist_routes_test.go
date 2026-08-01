package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
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

	// Assert the status: an error body decodes into blocklistJSON as zero entries,
	// which would misreport a broken endpoint as an over-eager delete.
	var after blocklistJSON
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/blocklist", seriesID), &after); code != http.StatusOK {
		t.Fatalf("re-list status = %d, want 200", code)
	}
	if len(after.Entries) != 2 {
		t.Errorf("entries after unblock = %d, want 2", len(after.Entries))
	}
}

// The visibility win the blocklist buys by reusing ineligible_reason: the
// Releases tab says why a release was passed over, and a manual grab of it still
// succeeds (PR #57) rather than being refused.
func TestBlocklistedReleaseSurfacesAsIneligibleButStillGrabs(t *testing.T) {
	const url = "magnet:?xt=urn:btih:blocked"
	const title = "[TopSubs] Placeholder Saga - 03 [1080p]"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: title, DownloadURL: url, Seeders: 500},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashZ", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)
	seedBlocklistEntry(t, h.store, seriesID, title,
		sql.NullString{String: store.FormatTimestamp(time.Now().Add(24 * time.Hour)), Valid: true})

	var searchOut struct {
		Results []struct {
			Matched          bool   `json:"matched"`
			Eligible         bool   `json:"eligible"`
			IneligibleReason string `json:"ineligible_reason"`
		} `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &searchOut); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(searchOut.Results) != 1 {
		t.Fatalf("results = %+v, want the blocklisted release listed, not hidden", searchOut.Results)
	}
	got := searchOut.Results[0]
	// Blocking is eligibility, not matching: the release still maps to its item.
	if !got.Matched || got.Eligible {
		t.Errorf("matched = %v, eligible = %v; want matched but ineligible", got.Matched, got.Eligible)
	}
	if !strings.Contains(got.IneligibleReason, "release previously failed") {
		t.Errorf("ineligible_reason = %q, want the blocklist reason", got.IneligibleReason)
	}
	if !strings.Contains(got.IneligibleReason, "the download client reported an error") {
		t.Errorf("ineligible_reason = %q, want the recorded failure carried through", got.IneligibleReason)
	}

	var grabOut struct {
		IneligibleReason string `json:"ineligible_reason"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": url}, &grabOut)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201 — a manual grab is never refused", code)
	}
	if !strings.Contains(grabOut.IneligibleReason, "release previously failed") {
		t.Errorf("ineligible_reason = %q, want the blocklist reason reported on the 201", grabOut.IneligibleReason)
	}
	if len(dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1", len(dl.Adds))
	}
}

// Bulk unblock, the affordance a fan-out needs: one click per series rather
// than one per entry.
func TestClearSeriesBlocklistInBulk(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	other := seedSeries(t, h.store, "Unrelated Show", 1)

	seedBlocklistEntry(t, h.store, seriesID, "[TopSubs] Placeholder Saga - 03 [1080p]",
		sql.NullString{String: store.FormatTimestamp(time.Now().Add(24 * time.Hour)), Valid: true})
	seedBlocklistEntry(t, h.store, seriesID, "[OldSubs] Placeholder Saga - 02 [1080p]",
		sql.NullString{String: store.FormatTimestamp(time.Now().Add(-time.Hour)), Valid: true})
	seedBlocklistEntry(t, h.store, seriesID, "[DeadSubs] Placeholder Saga - 01 [1080p]", sql.NullString{})
	seedBlocklistEntry(t, h.store, other, "[TopSubs] Unrelated Show - 01 [1080p]", sql.NullString{})

	var cleared struct {
		Cleared int `json:"cleared"`
	}
	if code := do(t, h, http.MethodDelete,
		fmt.Sprintf("/api/v1/series/%d/blocklist?expired=true", seriesID), nil, &cleared); code != http.StatusOK {
		t.Fatalf("clear expired status = %d, want 200", code)
	}
	if cleared.Cleared != 1 {
		t.Errorf("cleared = %d, want only the 1 lapsed entry", cleared.Cleared)
	}
	var after blocklistJSON
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/blocklist", seriesID), &after); code != http.StatusOK {
		t.Fatalf("re-list status = %d, want 200", code)
	}
	if len(after.Entries) != 2 {
		t.Errorf("entries after clearing expired = %d, want the live and permanent ones", len(after.Entries))
	}

	if code := do(t, h, http.MethodDelete,
		fmt.Sprintf("/api/v1/series/%d/blocklist", seriesID), nil, &cleared); code != http.StatusOK {
		t.Fatalf("clear all status = %d, want 200", code)
	}
	if cleared.Cleared != 2 {
		t.Errorf("cleared = %d, want the 2 that were left", cleared.Cleared)
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/blocklist", seriesID), &after); code != http.StatusOK {
		t.Fatalf("re-list status = %d, want 200", code)
	}
	if len(after.Entries) != 0 {
		t.Errorf("entries after clearing the series = %d, want none", len(after.Entries))
	}

	// A bulk clear stops at its series, like the single-entry one.
	var elsewhere blocklistJSON
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/blocklist", other), &elsewhere); code != http.StatusOK {
		t.Fatalf("list other series status = %d, want 200", code)
	}
	if len(elsewhere.Entries) != 1 {
		t.Errorf("other series has %d entries, want its own 1 untouched", len(elsewhere.Entries))
	}
}

func TestBulkClearOfUnknownSeriesIs404(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := do(t, h, http.MethodDelete, "/api/v1/series/9999/blocklist", nil, nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestBlocklistOfUnknownSeriesIs404(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := h.get(t, "/api/v1/series/9999/blocklist", nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}
