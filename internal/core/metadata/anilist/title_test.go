package anilist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
	if got := itemNumbers(items); !slices.Equal(got, want) {
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
	if got := itemNumbers(items); !slices.Equal(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

// A published count is authoritative in both directions. This is the shape of a
// real entry (a 12-episode show whose schedule runs 2..13, missing episode 1's
// record and carrying one past the end): the floors must neither trim it to the
// window nor extend it to a phantom 13th item.
func TestGetTitleKeepsAPublishedCountOverTheSchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"Media":{
			"id": 9,
			"title": {"romaji": "Sample Show"},
			"format": "TV",
			"episodes": 12,
			"status": "RELEASING",
			"nextAiringEpisode": {"episode": 7},
			"airingSchedule": {"nodes": [{"episode":2},{"episode":3},{"episode":13}]}
		}}}`)
	}))
	defer srv.Close()

	_, items, err := stubClient(srv.URL).GetTitle(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if got := itemNumbers(items); !slices.Equal(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

// AniList retains only a recent window of schedule records for a null-count
// long-runner (One Piece's first page is 1123-1147 against a next broadcast of
// 1173), so the add materializes the whole run rather than that window — the
// back catalogue is otherwise created by nothing at all.
func TestGetTitleMaterializesALongRunnersWholeRun(t *testing.T) {
	window := make([]int, 0, 25)
	for n := 1123; n <= 1147; n++ {
		window = append(window, n)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, nullCountResponse(1173, window...))
	}))
	defer srv.Close()

	_, items, err := stubClient(srv.URL).GetTitle(context.Background(), 21)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if len(items) != 1173 || items[0].Number != 1 || items[len(items)-1].Number != 1173 {
		t.Errorf("items = %d spanning %d..%d, want 1173 spanning 1..1173",
			len(items), items[0].Number, items[len(items)-1].Number)
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
