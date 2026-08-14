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
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

// The title page shows cover art from the cached snapshot, so GetTitle must
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
// long-runner, so the add materializes the whole run rather than that window —
// the back catalogue is otherwise created by nothing at all.
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

	_, items, err := stubClient(srv.URL).GetTitle(context.Background(), 207141)
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

// mediaResponse renders one Media as AniList would, with the year fields and
// episode count spelled by the caller (each a raw JSON literal, so "null" is
// expressible).
func mediaResponse(format, episodes, seasonYear, startYear string) string {
	return fmt.Sprintf(`{"data":{"Media":{
		"id": 4321,
		"title": {"romaji": "Sample Film"},
		"format": %q,
		"episodes": %s,
		"status": "FINISHED",
		"seasonYear": %s,
		"startDate": {"year": %s},
		"coverImage": {"large": ""},
		"nextAiringEpisode": null,
		"airingSchedule": {"nodes": []}
	}}}`, format, episodes, seasonYear, startYear)
}

func serveOnce(t *testing.T, body string, query *string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if query != nil {
			*query = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// datedMediaResponse renders a film whose startDate parts are spelled by the
// caller as raw JSON literals, so "null" is expressible.
func datedMediaResponse(year, month, day string) string {
	return fmt.Sprintf(`{"data":{"Media":{
		"id": 4321,
		"title": {"romaji": "Placeholder Legend"},
		"format": "MOVIE",
		"episodes": 1,
		"status": "NOT_YET_RELEASED",
		"seasonYear": null,
		"startDate": {"year": %s, "month": %s, "day": %s},
		"coverImage": {"large": ""},
		"nextAiringEpisode": null,
		"airingSchedule": {"nodes": []}
	}}}`, year, month, day)
}

// A film has no broadcast schedule, so its startDate is the only date we get.
// It rides the existing title query, costing no extra AniList request.
func TestGetTitleReadsAFullStartDate(t *testing.T) {
	var query string
	url := serveOnce(t, datedMediaResponse("2026", "3", "15"), &query)

	meta, _, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	want := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	if !meta.Premiere.Equal(want) {
		t.Errorf("Premiere = %v, want %v (noon UTC names the day in every real zone)", meta.Premiere, want)
	}
	if !strings.Contains(query, "month") || !strings.Contains(query, "day") {
		t.Errorf("title query does not request the full startDate: %s", query)
	}
}

// An announced film carries a year long before a day, and January 1st would be
// a wrong date on the calendar rather than an honest absence.
func TestGetTitlePremiereIsZeroWithoutAFullDate(t *testing.T) {
	url := serveOnce(t, datedMediaResponse("2027", "null", "null"), nil)

	meta, _, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if !meta.Premiere.IsZero() {
		t.Errorf("Premiere = %v, want the zero time for a year-only start date", meta.Premiere)
	}
	if meta.Year != 2027 {
		t.Errorf("Year = %d, want 2027 still read from the same field", meta.Year)
	}
}

// startDate.year is primary: AniList assigns a season later than a year becomes
// known, and its WINTER bucket spans December, so seasonYear can name the year
// after the premiere release names carry.
func TestGetTitlePrefersStartDateOverSeasonYear(t *testing.T) {
	var query string
	url := serveOnce(t, mediaResponse("MOVIE", "1", "2021", "2020"), &query)

	meta, _, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.Year != 2020 {
		t.Errorf("Year = %d, want 2020 (startDate.year wins)", meta.Year)
	}
	if !strings.Contains(query, "seasonYear") || !strings.Contains(query, "startDate") {
		t.Errorf("title query does not request both year fields: %s", query)
	}
}

// An announced film carries a year before it is assigned a season, which is the
// coverage startDate.year buys over seasonYear.
func TestGetTitleYearSurvivesANullSeasonYear(t *testing.T) {
	url := serveOnce(t, mediaResponse("MOVIE", "1", "null", "2027"), nil)

	meta, _, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.Year != 2027 {
		t.Errorf("Year = %d, want 2027", meta.Year)
	}
}

func TestGetTitleFallsBackToSeasonYear(t *testing.T) {
	url := serveOnce(t, mediaResponse("MOVIE", "1", "2021", "null"), nil)

	meta, _, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.Year != 2021 {
		t.Errorf("Year = %d, want 2021 (seasonYear, startDate.year being null)", meta.Year)
	}
}

func TestGetTitleYearUnknownWhenNeitherPublished(t *testing.T) {
	url := serveOnce(t, mediaResponse("MOVIE", "null", "null", "null"), nil)

	meta, _, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.Year != 0 {
		t.Errorf("Year = %d, want 0 (no year on record)", meta.Year)
	}
}

// An unreleased film has no count and no schedule, which today yields zero items.
func TestGetTitleExpandsMovieToOneItem(t *testing.T) {
	url := serveOnce(t, mediaResponse("MOVIE", "null", "null", "2027"), nil)

	_, items, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if got := itemNumbers(items); len(got) != 1 || got[0] != 1 {
		t.Errorf("items = %v, want [1]", got)
	}
}

// Format is the discriminator; the count is never consulted. Three shorts
// released as one film carry episodes: 3 and are still one acquirable item.
func TestGetTitleMovieIgnoresEpisodeCount(t *testing.T) {
	url := serveOnce(t, mediaResponse("MOVIE", "3", "2007", "2007"), nil)

	_, items, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if got := itemNumbers(items); len(got) != 1 || got[0] != 1 {
		t.Errorf("items = %v, want [1]", got)
	}
}

// The clamp keys on format, so a one-episode OVA stays title-shaped.
func TestGetTitleOVAWithOneEpisodeUnchanged(t *testing.T) {
	url := serveOnce(t, mediaResponse("OVA", "1", "2014", "2014"), nil)

	meta, items, err := stubClient(url).GetTitle(context.Background(), 4321)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if got := itemNumbers(items); len(got) != 1 || got[0] != 1 {
		t.Errorf("items = %v, want [1]", got)
	}
	if meta.Format != "OVA" {
		t.Errorf("Format = %q, want OVA", meta.Format)
	}
}

// The search row and the stored title must not disagree about a movie's year.
func TestSearchYearSurvivesANullSeasonYear(t *testing.T) {
	var query string
	url := serveOnce(t, `{"data":{"Page":{"media":[{
		"id": 4321,
		"title": {"romaji": "Sample Film"},
		"format": "MOVIE",
		"episodes": null,
		"status": "NOT_YET_RELEASED",
		"seasonYear": null,
		"startDate": {"year": 2027},
		"coverImage": {"large": ""}
	}]}}}`, &query)

	got, err := stubClient(url).Search(context.Background(), "sample film")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Year != 2027 {
		t.Fatalf("candidates = %+v, want one with Year 2027", got)
	}
	if !strings.Contains(query, "startDate") {
		t.Errorf("search query does not request startDate: %s", query)
	}
}
