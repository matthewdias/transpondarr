package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/browse"
)

type browseSeasonResponse struct {
	Season  string `json:"season"`
	Year    int    `json:"year"`
	Entries []struct {
		AniListID    int64    `json:"anilist_id"`
		Romaji       string   `json:"romaji"`
		English      string   `json:"english"`
		Format       string   `json:"format"`
		Status       string   `json:"status"`
		Episodes     int      `json:"episodes"`
		Genres       []string `json:"genres"`
		AverageScore int      `json:"average_score"`
		Studio       string   `json:"studio"`
		CoverURL     string   `json:"cover_url"`
		NextEpisode  int      `json:"next_episode"`
		NextAirsAt   string   `json:"next_airs_at"`
	} `json:"entries"`
}

func seedSeasonCache(t *testing.T, h *harness, season string, year int, raw string) {
	t.Helper()
	// provider = 'stub': the harness provider cannot browse, so a served chart
	// can only have come from this row.
	if _, err := h.store.DB.ExecContext(context.Background(),
		`INSERT INTO season_cache (provider, season, year, raw) VALUES ('stub', ?, ?, ?)`,
		season, year, raw); err != nil {
		t.Fatalf("seed season cache: %v", err)
	}
}

// The acceptance-critical path: a cached season is served without any provider
// round trip (the harness provider fails loudly on any call, and cannot browse
// at all).
func TestBrowseSeasonServedFromCache(t *testing.T) {
	h := newHarness(t, nil, nil)
	// "popularity" is no longer a SeasonEntry field: a blob cached before the drop
	// must still unmarshal, ignoring it.
	seedSeasonCache(t, h, "SPRING", 2026, `[{
		"provider_id": 101,
		"titles": {"romaji": "Cached Show", "english": "Cached Show EN"},
		"format": "TV",
		"status": "RELEASING",
		"episodes": 12,
		"genres": ["Action", "Comedy"],
		"average_score": 78,
		"popularity": 4321,
		"studio": "Studio Alpha",
		"cover_url": "https://img.example/101.png",
		"next_airing": {"number": 6, "airs_at": "2026-07-30T15:30:00Z"}
	}]`)

	var out browseSeasonResponse
	if code := h.get(t, "/api/v1/browse/season?season=spring&year=2026", &out); code != http.StatusOK {
		t.Fatalf("GET browse season = %d, want 200", code)
	}
	if out.Season != "spring" || out.Year != 2026 {
		t.Errorf("echoed season = %s %d, want spring 2026", out.Season, out.Year)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(out.Entries))
	}
	e := out.Entries[0]
	if e.AniListID != 101 || e.Romaji != "Cached Show" || e.English != "Cached Show EN" {
		t.Errorf("identity wrong: %+v", e)
	}
	if e.Format != "TV" || e.Status != "RELEASING" || e.Episodes != 12 {
		t.Errorf("format/status/episodes wrong: %+v", e)
	}
	if len(e.Genres) != 2 || e.AverageScore != 78 || e.Studio != "Studio Alpha" {
		t.Errorf("chart fields wrong: %+v", e)
	}
	if e.CoverURL != "https://img.example/101.png" {
		t.Errorf("CoverURL = %q", e.CoverURL)
	}
	if e.NextEpisode != 6 || e.NextAirsAt != "2026-07-30T15:30:00Z" {
		t.Errorf("next airing = ep %d at %q, want ep 6 at 2026-07-30T15:30:00Z", e.NextEpisode, e.NextAirsAt)
	}
}

// Omitted params default to the current season.
func TestBrowseSeasonDefaultsToCurrent(t *testing.T) {
	h := newHarness(t, nil, nil)
	season, year := browse.CurrentSeason(time.Now())
	seedSeasonCache(t, h, string(season), year, `[{"provider_id": 7, "titles": {"romaji": "Now Airing"}}]`)

	var out browseSeasonResponse
	if code := h.get(t, "/api/v1/browse/season", &out); code != http.StatusOK {
		t.Fatalf("GET browse season = %d, want 200", code)
	}
	if out.Season != strings.ToLower(string(season)) || out.Year != year {
		t.Errorf("defaulted to %s %d, want the current %s %d", out.Season, out.Year, season, year)
	}
	if len(out.Entries) != 1 || out.Entries[0].AniListID != 7 {
		t.Errorf("entries = %+v, want the cached current-season chart", out.Entries)
	}
}

func TestBrowseSeasonRejectsUnknownSeason(t *testing.T) {
	h := newHarness(t, nil, nil)
	var out struct{}
	if code := h.get(t, "/api/v1/browse/season?season=autumn", &out); code != http.StatusUnprocessableEntity {
		t.Fatalf("GET browse season with a bad season = %d, want 422", code)
	}
}
