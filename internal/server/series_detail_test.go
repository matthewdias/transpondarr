package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// The detail read model enriches best-effort from the cached AniList snapshot:
// english/native titles, provider status, and cover art.
func TestSeriesDetailEnrichedFromMetadataCache(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Enriched Show", 2)
	ctx := context.Background()
	if _, err := h.store.DB.ExecContext(ctx,
		`UPDATE series SET anilist_id = 42 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("set anilist id: %v", err)
	}
	if _, err := h.store.DB.ExecContext(ctx,
		`INSERT INTO metadata_cache (provider, provider_id, status, format, title, raw) VALUES ('anilist', 42, 'RELEASING', 'TV', 'Enriched Show', ?)`,
		`{"title": {"ProviderID": 42, "Titles": {"romaji": "Enriched Show", "english": "Enriched Show EN", "native": "ES"}, "Status": "RELEASING", "CoverURL": "https://img.example/42.png"}, "items": []}`); err != nil {
		t.Fatalf("seed metadata cache: %v", err)
	}

	var out struct {
		English  string `json:"english"`
		Native   string `json:"native"`
		Status   string `json:"status"`
		CoverURL string `json:"cover_url"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d", seriesID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	if out.English != "Enriched Show EN" || out.Native != "ES" || out.Status != "RELEASING" {
		t.Errorf("enrichment = %+v, want the cached titles and status", out)
	}
	if out.CoverURL != "https://img.example/42.png" {
		t.Errorf("cover_url = %q, want the cached cover served", out.CoverURL)
	}
}
