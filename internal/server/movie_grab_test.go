package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seedMovie is #208's shape: one title, one wanted item, and a year.
func seedMovie(t *testing.T, st *store.Store, title string, year int64) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{
		Title: title, Format: "MOVIE", Monitored: 1, Year: year,
	})
	if err != nil {
		t.Fatalf("create movie: %v", err)
	}
	if _, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: "movie", Number: sql.NullInt64{Int64: 1, Valid: true}, Monitored: 1,
	}); err != nil {
		t.Fatalf("create movie item: %v", err)
	}
	return s.ID
}

type movieSearchResult struct {
	Matched          bool   `json:"matched"`
	Eligible         bool   `json:"eligible"`
	Items            []int  `json:"items"`
	Reason           string `json:"reason"`
	IneligibleReason string `json:"ineligible_reason"`
}

func movieSearch(t *testing.T, h *harness, seriesID int64) []movieSearchResult {
	t.Helper()
	var out struct {
		Results []movieSearchResult `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d/search", seriesID), &out); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	return out.Results
}

// The acceptance criterion: a film matches by title and year over the API and
// one request grabs it.
func TestMovieSearchesAndGrabs(t *testing.T) {
	const url = "magnet:?xt=urn:btih:samplefilm"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (2019) [BD 1080p][HEVC]", DownloadURL: url, Seeders: 900},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "filmhash", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedMovie(t, h.store, "Sample Film", 2019)

	results := movieSearch(t, h, seriesID)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the film listed", results)
	}
	if !results[0].Matched || !results[0].Eligible {
		t.Errorf("matched = %v, eligible = %v (%q); want matched and eligible",
			results[0].Matched, results[0].Eligible, results[0].IneligibleReason)
	}
	if len(results[0].Items) != 1 || results[0].Items[0] != 1 {
		t.Errorf("items = %v, want the film's single item", results[0].Items)
	}

	var grabOut struct {
		Items            []int  `json:"items"`
		IneligibleReason string `json:"ineligible_reason"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", seriesID),
		map[string]any{"download_url": url}, &grabOut)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}
	if grabOut.IneligibleReason != "" {
		t.Errorf("ineligible_reason = %q, want none on the 201", grabOut.IneligibleReason)
	}
	if len(grabOut.Items) != 1 {
		t.Errorf("items = %v, want the film recorded against its item", grabOut.Items)
	}
	if len(dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1", len(dl.Adds))
	}
}

// A film with no year on record is still manually grabbable — the reason rides
// the 201 advisorily, the PR #57 shape — so only automation is held back.
func TestMovieWithNoYearIsGrabbableButFlagged(t *testing.T) {
	const url = "magnet:?xt=urn:btih:noyear"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Sample Film [BD 1080p][HEVC]", DownloadURL: url, Seeders: 900},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "noyearhash", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedMovie(t, h.store, "Sample Film", 0)

	results := movieSearch(t, h, seriesID)
	if len(results) != 1 || !results[0].Matched {
		t.Fatalf("results = %+v, want the film matched", results)
	}
	if results[0].Eligible {
		t.Error("eligible = true, want a null-year film withheld from automation")
	}
	if results[0].IneligibleReason != "the movie has no year on record" {
		t.Errorf("ineligible_reason = %q, want the null-year reason", results[0].IneligibleReason)
	}

	var grabOut struct {
		IneligibleReason string `json:"ineligible_reason"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", seriesID),
		map[string]any{"download_url": url}, &grabOut)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201 — a manual grab is never refused", code)
	}
	if grabOut.IneligibleReason != "the movie has no year on record" {
		t.Errorf("ineligible_reason = %q, want the reason carried on the 201", grabOut.IneligibleReason)
	}
	if len(dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1", len(dl.Adds))
	}
}

// A wrong-year release does not match, so the grab is the 422 any unmatched
// release gets — and the reason names the year.
func TestMovieWrongYearIsRefusedWithAReason(t *testing.T) {
	const url = "magnet:?xt=urn:btih:wrongyear"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (2021) [BD 1080p][HEVC]", DownloadURL: url, Seeders: 900},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "wrongyearhash", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedMovie(t, h.store, "Sample Film", 2019)

	results := movieSearch(t, h, seriesID)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the release listed", results)
	}
	if results[0].Matched {
		t.Errorf("matched = true over %v, want the wrong year refused", results[0].Items)
	}
	if results[0].Reason != "year 2021 does not match this entry (year 2019)" {
		t.Errorf("reason = %q, want the year mismatch surfaced", results[0].Reason)
	}

	var grabOut struct {
		Detail string `json:"detail"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", seriesID),
		map[string]any{"download_url": url}, &grabOut)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("grab status = %d, want 422 for an unmatched release", code)
	}
	if len(dl.Adds) != 0 {
		t.Errorf("download Add called %d times, want 0", len(dl.Adds))
	}
}
