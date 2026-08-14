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
func TestTitleDetailEnrichedFromMetadataCache(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Enriched Show", 2)
	ctx := context.Background()
	if _, err := h.store.DB.ExecContext(ctx,
		`UPDATE series SET provider = 'anilist', provider_id = 42 WHERE id = ?`, titleID); err != nil {
		t.Fatalf("set provider identity: %v", err)
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
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", titleID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	// The enrichment is looked up on the title's own provider, not a literal.
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
func TestAddTitleTakesTheProviderPair(t *testing.T) {
	provider := variantProvider{meta: metadata.TitleMeta{
		Titles: metadata.Titles{Romaji: "Paired Show"}, Format: "TV",
	}}
	h := newHarnessWithProvider(t, nil, nil, provider)

	var out struct {
		ID         int64  `json:"id"`
		Provider   string `json:"provider"`
		ProviderID int64  `json:"provider_id"`
	}
	if code := do(t, h, http.MethodPost, "/api/v1/titles",
		map[string]any{"provider": "anilist", "provider_id": 4321}, &out); code != http.StatusCreated {
		t.Fatalf("POST /series = %d, want 201", code)
	}
	if out.Provider != "anilist" || out.ProviderID != 4321 {
		t.Errorf("response identity = (%q, %d), want (anilist, 4321)", out.Provider, out.ProviderID)
	}

	var ignored struct{}
	if code := do(t, h, http.MethodPost, "/api/v1/titles",
		map[string]any{"anilist_id": 99}, &ignored); code != http.StatusUnprocessableEntity {
		t.Errorf("POST /series in the old shape = %d, want 422", code)
	}
	if code := do(t, h, http.MethodPost, "/api/v1/titles",
		map[string]any{"provider": "mal", "provider_id": 99}, &ignored); code != http.StatusUnprocessableEntity {
		t.Errorf("POST /series naming an unconfigured provider = %d, want 422", code)
	}
}

// The profile travels in the add request, so the add is atomic: the alternative
// is a client-side add-then-assign whose second half can fail on its own.
func TestAddTitleTakesTheQualityProfile(t *testing.T) {
	provider := variantProvider{meta: metadata.TitleMeta{
		Titles: metadata.Titles{Romaji: "Profiled Show"}, Format: "TV",
	}}
	h := newHarnessWithProvider(t, nil, nil, provider)

	var created profileJSON
	if code := do(t, h, http.MethodPost, "/api/v1/profiles",
		map[string]any{"name": "Sharper"}, &created); code != http.StatusCreated {
		t.Fatalf("POST /profiles = %d, want 201", code)
	}

	var added struct {
		ID int64 `json:"id"`
	}
	if code := do(t, h, http.MethodPost, "/api/v1/titles", map[string]any{
		"provider": "anilist", "provider_id": 4321, "quality_profile_id": created.ID,
	}, &added); code != http.StatusCreated {
		t.Fatalf("POST /series with a profile = %d, want 201", code)
	}
	var detail struct {
		QualityProfileID int64 `json:"quality_profile_id"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", added.ID), &detail); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	if detail.QualityProfileID != created.ID {
		t.Errorf("quality_profile_id = %d, want the requested %d", detail.QualityProfileID, created.ID)
	}

	var ignored struct{}
	if code := do(t, h, http.MethodPost, "/api/v1/titles", map[string]any{
		"provider": "anilist", "provider_id": 4322, "quality_profile_id": 9999,
	}, &ignored); code != http.StatusUnprocessableEntity {
		t.Errorf("POST /series naming an unknown profile = %d, want 422", code)
	}
}

// The year reaches every title surface: decide matches on it (#209) and Place
// names the folder with it (#198), so a client can show what was stored.
func TestTitleYearOnEveryTitleSurface(t *testing.T) {
	provider := variantProvider{meta: metadata.TitleMeta{
		Titles: metadata.Titles{Romaji: "Sample Film"}, Format: "MOVIE", Year: 2020,
	}}
	h := newHarnessWithProvider(t, nil, nil, provider)

	var added struct {
		ID   int64 `json:"id"`
		Year int   `json:"year"`
	}
	if code := do(t, h, http.MethodPost, "/api/v1/titles",
		map[string]any{"provider": "anilist", "provider_id": 4321}, &added); code != http.StatusCreated {
		t.Fatalf("POST /titles = %d, want 201", code)
	}
	if added.Year != 2020 {
		t.Errorf("add response year = %d, want 2020", added.Year)
	}

	var detail struct {
		Year int `json:"year"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", added.ID), &detail); code != http.StatusOK {
		t.Fatalf("GET title detail = %d, want 200", code)
	}
	if detail.Year != 2020 {
		t.Errorf("detail year = %d, want 2020", detail.Year)
	}

	var list struct {
		Titles []struct {
			ID   int64 `json:"id"`
			Year int   `json:"year"`
		} `json:"titles"`
	}
	if code := h.get(t, "/api/v1/titles", &list); code != http.StatusOK {
		t.Fatalf("GET titles = %d, want 200", code)
	}
	if len(list.Titles) != 1 || list.Titles[0].Year != 2020 {
		t.Errorf("list = %+v, want one title with year 2020", list.Titles)
	}
}

// Zero means "not on record", so it is omitted rather than published as 0.
func TestTitleYearOmittedWhenUnknown(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Undated Show", 1)

	var raw map[string]any
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", titleID), &raw); code != http.StatusOK {
		t.Fatalf("GET title detail = %d, want 200", code)
	}
	if _, present := raw["year"]; present {
		t.Errorf("year present as %v on a title with none on record, want it omitted", raw["year"])
	}
}
