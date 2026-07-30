package server_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
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

// A blocked group must survive the store -> domain conversion too: its release
// sorts below an unknown group's despite far more seeders.
func TestSearchDemotesBlockedGroup(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[BadRipCo] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:cccc", Seeders: 900},
		{Title: "[FineSubs] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:dddd", Seeders: 1},
	}}
	h := newHarness(t, idx, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	ctx := context.Background()
	def, err := h.store.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	if _, err := h.store.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
		ProfileID: def.ID, Rank: 1, GroupName: "BadRipCo", Blocked: 1,
	}); err != nil {
		t.Fatalf("add blocked group: %v", err)
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
	if out.Results[0].Title != "[FineSubs] Placeholder Saga S1E03 [1080p]" {
		t.Errorf("first result = %q, want the unblocked group despite fewer seeders", out.Results[0].Title)
	}
}

// #61: a series' pinned group is an absolute tier above profile score — the
// pinned unlisted group outranks the profile's rank-1 group over HTTP.
func TestSearchRanksPinnedGroupFirst(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[TrustedCorp] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:eeee", Seeders: 900},
		{Title: "[RandomRip] Placeholder Saga S1E03 [720p]", DownloadURL: "magnet:?xt=urn:btih:ffff", Seeders: 1},
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
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/series/%d/pinned-group", seriesID),
		map[string]any{"group": "RandomRip"}, nil); code != http.StatusOK {
		t.Fatalf("pin status = %d, want 200", code)
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
	if out.Results[0].Title != "[RandomRip] Placeholder Saga S1E03 [720p]" {
		t.Errorf("first result = %q, want the pinned group over the profile's rank-1", out.Results[0].Title)
	}
	if !out.Results[0].Pinned || out.Results[1].Pinned {
		t.Errorf("pinned flags = %v/%v, want true/false", out.Results[0].Pinned, out.Results[1].Pinned)
	}
}

// A pin never overrides eligibility over HTTP either: pinned + blocked still
// ranks below the eligible release.
func TestSearchPinnedIneligibleStaysRankedBelow(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[BadRipCo] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:abab", Seeders: 900},
		{Title: "[FineSubs] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:cdcd", Seeders: 1},
	}}
	h := newHarness(t, idx, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	ctx := context.Background()
	def, err := h.store.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	if _, err := h.store.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
		ProfileID: def.ID, Rank: 1, GroupName: "BadRipCo", Blocked: 1,
	}); err != nil {
		t.Fatalf("add blocked group: %v", err)
	}
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/series/%d/pinned-group", seriesID),
		map[string]any{"group": "BadRipCo"}, nil); code != http.StatusOK {
		t.Fatalf("pin status = %d, want 200", code)
	}

	var out struct {
		Results []candidateDTO `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &out); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if out.Results[0].Title != "[FineSubs] Placeholder Saga S1E03 [1080p]" {
		t.Errorf("first result = %q, want the eligible release above the pinned-but-blocked one", out.Results[0].Title)
	}
	if !out.Results[1].Pinned {
		t.Error("the blocked release should still be flagged pinned for display")
	}
}

// #19: the profile informs, never blocks — an ineligible manual grab succeeds
// in one request, and the response says why it fell outside the profile.
func TestGrabIneligibleReleaseSucceedsWithReason(t *testing.T) {
	const url = "magnet:?xt=urn:btih:cccc"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[BadRipCo] Placeholder Saga S1E03 [1080p]", DownloadURL: url, Seeders: 10},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashX", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	ctx := context.Background()
	def, err := h.store.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	if _, err := h.store.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
		ProfileID: def.ID, Rank: 1, GroupName: "BadRipCo", Blocked: 1,
	}); err != nil {
		t.Fatalf("block group: %v", err)
	}

	var out struct {
		InfoHash         string `json:"infohash"`
		IneligibleReason string `json:"ineligible_reason"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": url}, &out)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201 — the profile informs, it does not gate", code)
	}
	if out.InfoHash != "hashX" {
		t.Errorf("infohash = %q, want hashX", out.InfoHash)
	}
	if !strings.Contains(out.IneligibleReason, "blocked") {
		t.Errorf("ineligible_reason = %q, want the blocked-group reason surfaced", out.IneligibleReason)
	}
	if len(dl.Adds) != 1 {
		t.Errorf("download Add called %d times, want 1", len(dl.Adds))
	}
}

// An eligible release grabs exactly as before — no acknowledgement, no reason.
func TestGrabEligibleReleaseUnchanged(t *testing.T) {
	const url = "magnet:?xt=urn:btih:dddd"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[FineSubs] Placeholder Saga S1E03 [1080p]", DownloadURL: url, Seeders: 10},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashY", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	var out struct {
		InfoHash         string `json:"infohash"`
		IneligibleReason string `json:"ineligible_reason"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": url}, &out)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}
	if out.IneligibleReason != "" {
		t.Errorf("ineligible_reason = %q, want empty for an eligible release", out.IneligibleReason)
	}
}
