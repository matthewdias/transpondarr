package server_test

import (
	"context"
	"encoding/json"
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
		Description  string   `json:"description"`
		Episodes     int      `json:"episodes"`
		Genres       []string `json:"genres"`
		AverageScore int      `json:"average_score"`
		Studio       string   `json:"studio"`
		CoverURL     string   `json:"cover_url"`
		NextEpisode  int      `json:"next_episode"`
		NextAirsAt   string   `json:"next_airs_at"`
		Tracked      bool     `json:"tracked"`
		SeriesID     int64    `json:"series_id"`
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
		"description": "A cached synopsis.",
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
	if e.Description != "A cached synopsis." {
		t.Errorf("Description = %q, want the cached synopsis served", e.Description)
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

	// The schema promises a non-nullable array, so an entry cached with no
	// genres must still serve "genres": [] — never omit it or emit null.
	var raw struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if code := h.get(t, "/api/v1/browse/season", &raw); code != http.StatusOK {
		t.Fatalf("GET browse season (raw) = %d, want 200", code)
	}
	genres, present := raw.Entries[0]["genres"]
	if !present || string(genres) != "[]" {
		t.Errorf("genres = %q (present=%t), want the empty array", genres, present)
	}
}

func TestBrowseSeasonRejectsUnknownSeason(t *testing.T) {
	h := newHarness(t, nil, nil)
	var out struct{}
	if code := h.get(t, "/api/v1/browse/season?season=autumn", &out); code != http.StatusUnprocessableEntity {
		t.Fatalf("GET browse season with a bad season = %d, want 422", code)
	}
}

// A chart entry already in the library is marked tracked, and its countdown is
// the local airing schedule, not the season-cache snapshot.
func TestBrowseSeasonMarksTrackedAndOverlaysAiring(t *testing.T) {
	h := newHarness(t, nil, nil)
	seedSeasonCache(t, h, "SPRING", 2026, `[
		{"provider_id": 101, "titles": {"romaji": "Tracked Show"},
		 "next_airing": {"number": 5, "airs_at": "2026-07-27T15:30:00Z"}},
		{"provider_id": 102, "titles": {"romaji": "Untracked Show"},
		 "next_airing": {"number": 2, "airs_at": "2026-07-29T15:30:00Z"}}
	]`)

	ctx := context.Background()
	seriesID := seedSeries(t, h.store, "Tracked Show", 6)
	localNext := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	if _, err := h.store.DB.ExecContext(ctx,
		`UPDATE series SET anilist_id = 101, airing_synced_at = datetime('now') WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("mark series tracked: %v", err)
	}
	if _, err := h.store.DB.ExecContext(ctx,
		`UPDATE wanted_items SET airs_at = ? WHERE series_id = ? AND number = 6`,
		localNext.Format("2006-01-02 15:04:05"), seriesID); err != nil {
		t.Fatalf("schedule item: %v", err)
	}

	var out browseSeasonResponse
	if code := h.get(t, "/api/v1/browse/season?season=spring&year=2026", &out); code != http.StatusOK {
		t.Fatalf("GET browse season = %d, want 200", code)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(out.Entries))
	}

	tracked := out.Entries[0]
	if !tracked.Tracked || tracked.SeriesID != seriesID {
		t.Errorf("entry 101 tracked=%t series_id=%d, want tracked with series %d", tracked.Tracked, tracked.SeriesID, seriesID)
	}
	if tracked.NextEpisode != 6 || tracked.NextAirsAt != localNext.Format(time.RFC3339) {
		t.Errorf("entry 101 next airing = ep %d at %q, want the local ep 6 at %s", tracked.NextEpisode, tracked.NextAirsAt, localNext.Format(time.RFC3339))
	}

	untracked := out.Entries[1]
	if untracked.Tracked || untracked.SeriesID != 0 {
		t.Errorf("entry 102 = %+v, want untracked", untracked)
	}
	if untracked.NextEpisode != 2 || untracked.NextAirsAt != "2026-07-29T15:30:00Z" {
		t.Errorf("entry 102 next airing = ep %d at %q, want the snapshot kept", untracked.NextEpisode, untracked.NextAirsAt)
	}
}
