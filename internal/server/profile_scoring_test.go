package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// The search endpoint must rank by the series' assigned profile, not seeders —
// this exercises the whole store -> domain -> decide wiring over HTTP.
func TestSearchRanksByAssignedProfile(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[RandomRip] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:aaaa", Seeders: 900},
		{Title: "[TrustedCorp] Placeholder Saga S1E03 [720p]", DownloadURL: "magnet:?xt=urn:btih:bbbb", Seeders: 3},
	}}
	h := newHarness(t, idx, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	ctx := context.Background()
	def, err := h.store.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	if _, err := h.store.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
		ProfileID: def.ID, Rank: 1, GroupName: "TrustedCorp",
	}); err != nil {
		t.Fatalf("add group: %v", err)
	}

	var out struct {
		Results []candidateDTO `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &out); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	if out.Results[0].Title != "[TrustedCorp] Placeholder Saga S1E03 [720p]" {
		t.Errorf("first result = %q, want the trusted group's 720p over the better-seeded 1080p", out.Results[0].Title)
	}
}
