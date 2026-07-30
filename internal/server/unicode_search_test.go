package server_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Issue #107: AniList typography (×, ・, ☆, …) never appears in release names,
// so it must not reach the indexer. All fixtures are invented titles.

// The stored title is sanitized before it hits the indexer: × becomes "x" and
// the parenthesized year loses its parens, or the full-text search finds nothing.
func TestSearchSanitizesTitleTypography(t *testing.T) {
	const sanitized = "RANGER x RANGER 2013"
	idx := &coretest.FakeIndexer{ByTerm: map[string][]indexer.Release{
		sanitized: {{Title: "[ExampleSubs] Ranger x Ranger 2013 - 03 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:aa03", Seeders: 100}},
	}}
	h := newHarness(t, idx, nil)
	seriesID := seedSeries(t, h.store, "RANGER×RANGER (2013)", 12)

	var out struct {
		Term    string         `json:"term"`
		Results []candidateDTO `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &out); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(idx.Queries) != 1 || idx.Queries[0].Term != sanitized {
		t.Fatalf("indexer queried with %+v, want one search for %q", idx.Queries, sanitized)
	}
	if out.Term != sanitized {
		t.Errorf("reported term = %q, want the sanitized %q", out.Term, sanitized)
	}
	if len(out.Results) != 1 || !out.Results[0].Matched || len(out.Results[0].Items) != 1 || out.Results[0].Items[0] != 3 {
		t.Errorf("results = %+v, want one release matched to item 3", out.Results)
	}
}

// When the sanitized primary term finds nothing, the search retries with the
// title variants (english, then native) before reporting zero results.
func TestSearchFallsBackToVariantOnZeroResults(t *testing.T) {
	const english = "Fixture of the Sky Side Story"
	idx := &coretest.FakeIndexer{ByTerm: map[string][]indexer.Release{
		english: {{Title: "[ExampleSubs] Fixture of the Sky Side Story - 03 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:bb03", Seeders: 50}},
	}}
	provider := variantProvider{meta: metadata.TitleMeta{
		ProviderID: 42,
		Titles:     metadata.Titles{Romaji: "Sora・no・Fixture Gaiden", English: english},
	}}
	h := newHarnessWithProvider(t, idx, nil, provider)
	seriesID := seedAnilistSeries(t, h, "Sora・no・Fixture Gaiden", 42, 12)

	var out struct {
		Term    string         `json:"term"`
		Results []candidateDTO `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &out); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(idx.Queries) != 2 ||
		idx.Queries[0].Term != "Sora no Fixture Gaiden" || idx.Queries[1].Term != english {
		t.Fatalf("indexer queried with %+v, want the sanitized romaji then the english variant", idx.Queries)
	}
	if out.Term != english {
		t.Errorf("reported term = %q, want the variant that produced results %q", out.Term, english)
	}
	if len(out.Results) != 1 || !out.Results[0].Matched {
		t.Errorf("results = %+v, want one matched release from the variant search", out.Results)
	}
}

// variantProvider answers GetTitle with fixed metadata so TitleVariants works.
type variantProvider struct{ meta metadata.TitleMeta }

func (variantProvider) Name() string { return "variant-stub" }

func (variantProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, errors.New("variant-stub: unexpected Search call")
}

func (p variantProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return p.meta, nil, nil
}

// seedAnilistSeries is seedSeries plus an AniList id, so the handler consults
// the metadata provider for title variants.
func seedAnilistSeries(t *testing.T, h *harness, title string, anilistID int64, count int) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := h.store.Q.CreateSeries(ctx, db.CreateSeriesParams{
		Title: title, Format: "TV", Monitored: 1,
		AnilistID: sql.NullInt64{Int64: anilistID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for n := 1; n <= count; n++ {
		if _, err := h.store.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(n), Valid: true},
		}); err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
	}
	return s.ID
}
