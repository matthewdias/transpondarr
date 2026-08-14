package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// #125 showed a pack as matched-but-ineligible; #126's per-file import lifts the
// refusal, so it is now matched *and* eligible over the API — the Releases tab
// still names the episodes it covers, and one request still grabs it.
func TestSeasonPackIsMatchedAndEligibleAndGrabs(t *testing.T) {
	const url = "magnet:?xt=urn:btih:seasonpack"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[Batchers] Placeholder Saga S1 (01-06) [1080p][Batch]", DownloadURL: url, Seeders: 900},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "packhash", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 12)

	var searchOut struct {
		Results []struct {
			Matched          bool   `json:"matched"`
			Eligible         bool   `json:"eligible"`
			Items            []int  `json:"items"`
			IneligibleReason string `json:"ineligible_reason"`
		} `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d/search", titleID), &searchOut); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(searchOut.Results) != 1 {
		t.Fatalf("results = %+v, want the pack listed", searchOut.Results)
	}
	got := searchOut.Results[0]
	if !got.Matched || !got.Eligible {
		t.Errorf("matched = %v, eligible = %v; want matched and eligible", got.Matched, got.Eligible)
	}
	if len(got.Items) != 6 {
		t.Errorf("items = %v, want the six episodes the pack covers", got.Items)
	}
	if got.IneligibleReason != "" {
		t.Errorf("ineligible_reason = %q, want none", got.IneligibleReason)
	}

	var grabOut struct {
		IneligibleReason string `json:"ineligible_reason"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", titleID),
		map[string]any{"download_url": url}, &grabOut)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201 — a manual grab is never refused", code)
	}
	if grabOut.IneligibleReason != "" {
		t.Errorf("ineligible_reason = %q, want none on the 201", grabOut.IneligibleReason)
	}
	if len(dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1", len(dl.Adds))
	}
}
