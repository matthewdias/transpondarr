package anilist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

// browseResponse renders one seasonal Page as AniList would.
func browseResponse(hasNext bool, entries ...string) string {
	return fmt.Sprintf(`{"data":{"Page":{"pageInfo":{"hasNextPage":%t},"media":[%s]}}}`,
		hasNext, strings.Join(entries, ","))
}

func browseMedia(id int64) string {
	return fmt.Sprintf(`{
		"id": %d,
		"title": {"romaji": "Sample Show %d", "english": "Sample Show %d EN", "native": "SS%d"},
		"format": "TV",
		"episodes": 12,
		"status": "RELEASING",
		"genres": ["Action", "Comedy"],
		"averageScore": 81,
		"coverImage": {"large": "https://img.example/%d.png"},
		"studios": {"nodes": [{"name": "Studio Alpha"}]},
		"nextAiringEpisode": {"episode": 5, "airingAt": 1700000000}
	}`, id, id, id, id, id)
}

func TestBrowseSeasonPagesUntilExhausted(t *testing.T) {
	var vars []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := requestVars(t, r)
		vars = append(vars, v)
		w.Header().Set("Content-Type", "application/json")
		switch v["page"].(float64) {
		case 1:
			_, _ = io.WriteString(w, browseResponse(true, browseMedia(1), browseMedia(2)))
		default:
			_, _ = io.WriteString(w, browseResponse(false, browseMedia(3)))
		}
	}))
	defer srv.Close()

	got, err := stubClient(srv.URL).BrowseSeason(context.Background(), metadata.SeasonSpring, 2026)
	if err != nil {
		t.Fatalf("BrowseSeason: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	for i, want := range []int64{1, 2, 3} {
		if got[i].ProviderID != want {
			t.Errorf("entry %d: ProviderID = %d, want %d", i, got[i].ProviderID, want)
		}
	}
	if len(vars) != 2 || vars[0]["page"].(float64) != 1 || vars[1]["page"].(float64) != 2 {
		t.Errorf("requested %d pages, want pages [1 2]", len(vars))
	}
	if vars[0]["season"] != "SPRING" || vars[0]["seasonYear"].(float64) != 2026 {
		t.Errorf("season vars = %v/%v, want SPRING/2026", vars[0]["season"], vars[0]["seasonYear"])
	}
}

func TestBrowseSeasonMapsEveryField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, browseResponse(false, browseMedia(9)))
	}))
	defer srv.Close()

	got, err := stubClient(srv.URL).BrowseSeason(context.Background(), metadata.SeasonWinter, 2025)
	if err != nil {
		t.Fatalf("BrowseSeason: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.ProviderID != 9 || e.Titles.Romaji != "Sample Show 9" || e.Titles.English != "Sample Show 9 EN" {
		t.Errorf("identity/titles wrong: %+v", e)
	}
	if e.Format != "TV" || e.Status != "RELEASING" || e.Episodes != 12 {
		t.Errorf("format/status/episodes wrong: %+v", e)
	}
	if len(e.Genres) != 2 || e.Genres[0] != "Action" {
		t.Errorf("Genres = %v, want [Action Comedy]", e.Genres)
	}
	if e.AverageScore != 81 {
		t.Errorf("AverageScore = %d, want 81", e.AverageScore)
	}
	if e.Studio != "Studio Alpha" {
		t.Errorf("Studio = %q, want Studio Alpha", e.Studio)
	}
	if e.CoverURL != "https://img.example/9.png" {
		t.Errorf("CoverURL = %q", e.CoverURL)
	}
	if e.NextAiring == nil || e.NextAiring.Number != 5 || !e.NextAiring.AirsAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("NextAiring = %+v, want episode 5 at 1700000000", e.NextAiring)
	}
}

// A finished title has no nextAiringEpisode and may have null episodes/score;
// the entry still maps, with zero values rather than an invented airing.
func TestBrowseSeasonSparseEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, browseResponse(false, `{
			"id": 4,
			"title": {"romaji": "Quiet Show"},
			"format": "TV",
			"episodes": null,
			"status": "NOT_YET_RELEASED",
			"genres": [],
			"averageScore": null,
			"coverImage": {"large": ""},
			"studios": {"nodes": []},
			"nextAiringEpisode": null
		}`))
	}))
	defer srv.Close()

	got, err := stubClient(srv.URL).BrowseSeason(context.Background(), metadata.SeasonFall, 2026)
	if err != nil {
		t.Fatalf("BrowseSeason: %v", err)
	}
	e := got[0]
	if e.Episodes != 0 || e.AverageScore != 0 || e.Studio != "" || e.NextAiring != nil {
		t.Errorf("sparse entry mapped wrong: %+v", e)
	}
}

// An empty season is a normal outcome, not an error.
func TestBrowseSeasonEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, browseResponse(false))
	}))
	defer srv.Close()

	got, err := stubClient(srv.URL).BrowseSeason(context.Background(), metadata.SeasonSummer, 1996)
	if err != nil {
		t.Fatalf("BrowseSeason: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0", len(got))
	}
}

// A server that always claims another page must not page forever.
func TestBrowseSeasonCapsPaging(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, browseResponse(true, browseMedia(int64(requests))))
	}))
	defer srv.Close()

	if _, err := stubClient(srv.URL).BrowseSeason(context.Background(), metadata.SeasonSpring, 2026); err != nil {
		t.Fatalf("BrowseSeason: %v", err)
	}
	if requests != maxBrowsePages {
		t.Errorf("made %d requests, want the %d-page cap", requests, maxBrowsePages)
	}
}

func TestBrowseSeasonErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := stubClient(srv.URL).BrowseSeason(context.Background(), metadata.SeasonSpring, 2026); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}
