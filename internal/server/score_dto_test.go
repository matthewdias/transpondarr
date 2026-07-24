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

// #17: the search DTO must expose the score, its per-axis breakdown, and
// eligibility — the surface a user watches to build trust before automation.
func TestSearchExposesScoreBreakdown(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[TrustedCorp] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:aaaa", Seeders: 5},
		{Title: "[BlockedCorp] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:bbbb", Seeders: 50},
	}}
	h := newHarness(t, idx, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	ctx := context.Background()
	def, err := h.store.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	for _, g := range []db.AddProfileGroupParams{
		{ProfileID: def.ID, Rank: 1, GroupName: "TrustedCorp"},
		{ProfileID: def.ID, Rank: 2, GroupName: "BlockedCorp", Blocked: 1},
	} {
		if _, err := h.store.Q.AddProfileGroup(ctx, g); err != nil {
			t.Fatalf("add group %s: %v", g.GroupName, err)
		}
	}

	var out struct {
		Results []struct {
			Title            string `json:"title"`
			Score            int    `json:"score"`
			Eligible         bool   `json:"eligible"`
			IneligibleReason string `json:"ineligible_reason"`
			ScoreParts       []struct {
				Label  string `json:"label"`
				Points int    `json:"points"`
			} `json:"score_parts"`
		} `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &out); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}

	top := out.Results[0]
	if top.Title != "[TrustedCorp] Placeholder Saga S1E03 [1080p]" {
		t.Fatalf("first result = %q, want the trusted group's release", top.Title)
	}
	if !top.Eligible || top.IneligibleReason != "" {
		t.Errorf("top result eligible=%v reason=%q, want eligible with no reason", top.Eligible, top.IneligibleReason)
	}
	if top.Score <= 0 || len(top.ScoreParts) == 0 {
		t.Fatalf("top result score=%d parts=%d, want a positive score with a breakdown", top.Score, len(top.ScoreParts))
	}
	sum := 0
	hasGroupPart := false
	for _, p := range top.ScoreParts {
		sum += p.Points
		if p.Points != 0 && p.Label != "" && (p.Label[:5] == "group") {
			hasGroupPart = true
		}
	}
	if sum != top.Score {
		t.Errorf("parts sum %d != score %d", sum, top.Score)
	}
	if !hasGroupPart {
		t.Errorf("breakdown %+v lacks a labelled group contribution", top.ScoreParts)
	}

	blocked := out.Results[1]
	if blocked.Eligible || blocked.IneligibleReason == "" {
		t.Errorf("blocked result eligible=%v reason=%q, want ineligible with a reason", blocked.Eligible, blocked.IneligibleReason)
	}
}
