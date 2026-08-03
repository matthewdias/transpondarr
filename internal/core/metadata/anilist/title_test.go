package anilist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

// The series page shows cover art from the cached snapshot, so GetTitle must
// request and map it.
func TestGetTitleMapsCover(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"Media":{
			"id": 9,
			"title": {"romaji": "Sample Show"},
			"format": "TV",
			"episodes": 2,
			"status": "RELEASING",
			"coverImage": {"large": "https://img.example/9.png"}
		}}}`)
	}))
	defer srv.Close()

	meta, _, err := stubClient(srv.URL).GetTitle(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.CoverURL != "https://img.example/9.png" {
		t.Errorf("CoverURL = %q, want the cover mapped", meta.CoverURL)
	}
	if !strings.Contains(query, "coverImage") {
		t.Error("title query does not request coverImage")
	}
}

// nullCountResponse renders one Media as AniList would, with episodes null.
func nullCountResponse(nextEpisode int, scheduleNumbers ...int) string {
	nodes := make([]string, 0, len(scheduleNumbers))
	for _, n := range scheduleNumbers {
		nodes = append(nodes, fmt.Sprintf(`{"episode":%d}`, n))
	}
	next := "null"
	if nextEpisode > 0 {
		next = fmt.Sprintf(`{"episode":%d}`, nextEpisode)
	}
	return fmt.Sprintf(`{"data":{"Media":{
		"id": 207141,
		"title": {"romaji": "Sample Show"},
		"format": "TV",
		"episodes": null,
		"status": "RELEASING",
		"coverImage": {"large": ""},
		"nextAiringEpisode": %s,
		"airingSchedule": {"nodes": [%s]}
	}}}`, next, strings.Join(nodes, ","))
}

func itemNumbers(items []metadata.ItemMeta) []int {
	out := make([]int, 0, len(items))
	for _, it := range items {
		out = append(out, it.Number)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A releasing title whose count AniList never publishes must still come back
// with its known items — from the one request the add already pays for — and the
// episode the schedule skips (episodes 1 and 2 sharing a broadcast slot) must be
// filled in rather than silently absent.
func TestGetTitleFillsANullCountTitleFromOneRequest(t *testing.T) {
	var requests int
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		query = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, nullCountResponse(6, 1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12))
	}))
	defer srv.Close()

	meta, items, err := stubClient(srv.URL).GetTitle(context.Background(), 207141)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if requests != 1 {
		t.Errorf("made %d requests, want the schedule page to ride along in 1", requests)
	}
	if !strings.Contains(query, "airingSchedule") {
		t.Error("title query does not request airingSchedule")
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if got := itemNumbers(items); !equalInts(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	// The count itself is still unknown; only the items it would have produced
	// are recovered.
	if meta.Episodes != 0 {
		t.Errorf("Episodes = %d, want the null count reported as 0", meta.Episodes)
	}
}

// The next broadcast is the weaker floor: it answers for a title whose schedule
// AniList has not filled in.
func TestGetTitleFallsBackToTheNextBroadcast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, nullCountResponse(6))
	}))
	defer srv.Close()

	_, items, err := stubClient(srv.URL).GetTitle(context.Background(), 207141)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	want := []int{1, 2, 3, 4, 5, 6}
	if got := itemNumbers(items); !equalInts(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

// A published count is authoritative. Sequel entries whose schedule continues the
// previous season's numbering would otherwise double a 12-episode season.
func TestGetTitleKeepsAPublishedCountOverTheSchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"Media":{
			"id": 9,
			"title": {"romaji": "Sample Show"},
			"format": "TV",
			"episodes": 3,
			"status": "RELEASING",
			"nextAiringEpisode": {"episode": 16},
			"airingSchedule": {"nodes": [{"episode":13},{"episode":14},{"episode":15}]}
		}}}`)
	}))
	defer srv.Close()

	_, items, err := stubClient(srv.URL).GetTitle(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if got := itemNumbers(items); !equalInts(got, []int{1, 2, 3}) {
		t.Errorf("items = %v, want [1 2 3]", got)
	}
}

// A title with neither a count nor any schedule is a normal outcome, not an error.
func TestGetTitleWithNothingToGoOnReturnsNoItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, nullCountResponse(0))
	}))
	defer srv.Close()

	_, items, err := stubClient(srv.URL).GetTitle(context.Background(), 207141)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items = %v, want none", itemNumbers(items))
	}
}
