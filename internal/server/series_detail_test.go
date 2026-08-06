package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

// The detail read model enriches best-effort from the cached AniList snapshot:
// english/native titles, provider status, and cover art.
func TestSeriesDetailEnrichedFromMetadataCache(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Enriched Show", 2)
	ctx := context.Background()
	if _, err := h.store.DB.ExecContext(ctx,
		`UPDATE series SET provider = 'anilist', provider_id = 42 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("set anilist id: %v", err)
	}
	if _, err := h.store.DB.ExecContext(ctx,
		`INSERT INTO metadata_cache (provider, provider_id, status, format, title, raw) VALUES ('anilist', 42, 'RELEASING', 'TV', 'Enriched Show', ?)`,
		`{"title": {"ProviderID": 42, "Titles": {"romaji": "Enriched Show", "english": "Enriched Show EN", "native": "ES"}, "Status": "RELEASING", "CoverURL": "https://img.example/42.png"}, "items": []}`); err != nil {
		t.Fatalf("seed metadata cache: %v", err)
	}

	var out struct {
		Provider   string `json:"provider"`
		ProviderID int64  `json:"provider_id"`
		English    string `json:"english"`
		Native     string `json:"native"`
		Status     string `json:"status"`
		CoverURL   string `json:"cover_url"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d", seriesID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	// The enrichment is looked up on the series' own provider, not a literal.
	if out.Provider != "anilist" || out.ProviderID != 42 {
		t.Errorf("identity = (%q, %d), want (anilist, 42)", out.Provider, out.ProviderID)
	}
	if out.English != "Enriched Show EN" || out.Native != "ES" || out.Status != "RELEASING" {
		t.Errorf("enrichment = %+v, want the cached titles and status", out)
	}
	if out.CoverURL != "https://img.example/42.png" {
		t.Errorf("cover_url = %q, want the cached cover served", out.CoverURL)
	}
}

// The add endpoint takes the pair and echoes it back; a clean break, so a
// request in the old anilist_id shape is rejected rather than quietly defaulted.
func TestAddSeriesTakesTheProviderPair(t *testing.T) {
	provider := variantProvider{meta: metadata.TitleMeta{
		Titles: metadata.Titles{Romaji: "Paired Show"}, Format: "TV",
	}}
	h := newHarnessWithProvider(t, nil, nil, provider)

	var out struct {
		ID         int64  `json:"id"`
		Provider   string `json:"provider"`
		ProviderID int64  `json:"provider_id"`
	}
	if code := do(t, h, http.MethodPost, "/api/v1/series",
		map[string]any{"provider": "anilist", "provider_id": 4321}, &out); code != http.StatusCreated {
		t.Fatalf("POST /series = %d, want 201", code)
	}
	if out.Provider != "anilist" || out.ProviderID != 4321 {
		t.Errorf("response identity = (%q, %d), want (anilist, 4321)", out.Provider, out.ProviderID)
	}

	var ignored struct{}
	if code := do(t, h, http.MethodPost, "/api/v1/series",
		map[string]any{"anilist_id": 99}, &ignored); code != http.StatusUnprocessableEntity {
		t.Errorf("POST /series in the old shape = %d, want 422", code)
	}
	if code := do(t, h, http.MethodPost, "/api/v1/series",
		map[string]any{"provider": "mal", "provider_id": 99}, &ignored); code != http.StatusUnprocessableEntity {
		t.Errorf("POST /series naming an unconfigured provider = %d, want 422", code)
	}
}
