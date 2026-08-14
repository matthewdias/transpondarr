package server_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type calendarResponse struct {
	Items []struct {
		ID          int64  `json:"id"`
		TitleID     int64  `json:"title_id"`
		Title       string `json:"title"`
		Monitored   bool   `json:"monitored"`
		Number      int    `json:"number"`
		Format      string `json:"format"`
		AirsAt      string `json:"airs_at"`
		Status      string `json:"status"`
		ImportError string `json:"import_error"`
	} `json:"items"`
	Unscheduled []struct {
		TitleID int64  `json:"title_id"`
		Title   string `json:"title"`
	} `json:"unscheduled"`
}

func setAirsAt(t *testing.T, st *store.Store, seriesID int64, number int, airsAt string) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET airs_at = ? WHERE series_id = ? AND number = ?`,
		airsAt, seriesID, number); err != nil {
		t.Fatalf("set airs_at: %v", err)
	}
}

func itemID(t *testing.T, st *store.Store, seriesID int64, number int) int64 {
	t.Helper()
	var id int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT id FROM wanted_items WHERE series_id = ? AND number = ?`,
		seriesID, number).Scan(&id); err != nil {
		t.Fatalf("look up item: %v", err)
	}
	return id
}

// The calendar returns only items whose air time falls in [start, end), and
// only for monitored series unless unmonitored is requested.
func TestCalendarRangeAndMonitoredFilter(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Airing Show", 5)
	setAirsAt(t, h.store, seriesID, 1, "2026-06-30 15:00:00") // before range
	setAirsAt(t, h.store, seriesID, 2, "2026-07-07 15:00:00") // in range
	// episode 3 has no air date: absent from the calendar, not an error
	setAirsAt(t, h.store, seriesID, 4, "2026-07-01 00:00:00") // exactly at start: included
	setAirsAt(t, h.store, seriesID, 5, "2026-08-01 00:00:00") // exactly at end: excluded

	otherID := seedSeries(t, h.store, "Unmonitored Show", 1)
	setAirsAt(t, h.store, otherID, 1, "2026-07-08 15:00:00")
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE series SET monitored = 0 WHERE id = ?`, otherID); err != nil {
		t.Fatalf("unmonitor series: %v", err)
	}

	var out calendarResponse
	if code := h.get(t, "/api/v1/calendar?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z", &out); code != http.StatusOK {
		t.Fatalf("GET calendar = %d, want 200", code)
	}
	// Ordered by air time: the start-boundary episode first, then ep 2.
	if len(out.Items) != 2 || out.Items[0].Number != 4 || out.Items[1].Number != 2 {
		t.Fatalf("items = %+v, want eps 4 and 2 (inclusive start, exclusive end)", out.Items)
	}
	got := out.Items[1]
	if got.TitleID != seriesID || got.Title != "Airing Show" {
		t.Errorf("item = %+v, want Airing Show ep 2", got)
	}
	if got.AirsAt != "2026-07-07T15:00:00Z" {
		t.Errorf("airs_at = %q, want the stored instant restored to RFC 3339 UTC", got.AirsAt)
	}
	if !got.Monitored {
		t.Errorf("monitored = false, want true")
	}

	var withUnmonitored calendarResponse
	if code := h.get(t, "/api/v1/calendar?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z&unmonitored=true", &withUnmonitored); code != http.StatusOK {
		t.Fatalf("GET calendar unmonitored = %d, want 200", code)
	}
	if len(withUnmonitored.Items) != 3 {
		t.Fatalf("items with unmonitored = %d, want 3: %+v", len(withUnmonitored.Items), withUnmonitored.Items)
	}
	for _, it := range withUnmonitored.Items {
		if it.TitleID == otherID && it.Monitored {
			t.Errorf("unmonitored series item flagged monitored")
		}
	}
}

// Calendar items carry the same derived status vocabulary as series detail.
func TestCalendarDerivesItemStatus(t *testing.T) {
	h := newHarness(t, nil, nil)
	ctx := context.Background()
	seriesID := seedSeries(t, h.store, "Status Show", 5)
	for n := 1; n <= 5; n++ {
		setAirsAt(t, h.store, seriesID, n, "2026-07-10 15:00:00")
	}

	if err := h.store.Q.SetWantedItemInLibrary(ctx, db.SetWantedItemInLibraryParams{
		InLibrary: 1, ID: itemID(t, h.store, seriesID, 1),
	}); err != nil {
		t.Fatalf("set in_library: %v", err)
	}
	grab := func(number int, status string) db.Grab {
		g, err := h.store.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: itemID(t, h.store, seriesID, number),
			InfoHash:     "hash", ReleaseTitle: "Release", Status: status,
		})
		if err != nil {
			t.Fatalf("upsert grab: %v", err)
		}
		return g
	}
	grab(2, "grabbed")
	grab(3, "import_deferred")
	stuck := grab(4, "grabbed")
	if err := h.store.Q.SetGrabLastError(ctx, db.SetGrabLastErrorParams{
		LastError: sql.NullString{String: "library offline", Valid: true}, ID: stuck.ID,
	}); err != nil {
		t.Fatalf("set last error: %v", err)
	}
	grab(5, "failed")

	var out calendarResponse
	if code := h.get(t, "/api/v1/calendar?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z", &out); code != http.StatusOK {
		t.Fatalf("GET calendar = %d, want 200", code)
	}
	want := map[int]string{1: "in_library", 2: "downloading", 3: "deferred", 4: "stuck", 5: "wanted"}
	if len(out.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(out.Items), len(want))
	}
	for _, it := range out.Items {
		if it.Status != want[it.Number] {
			t.Errorf("episode %d status = %q, want %q", it.Number, it.Status, want[it.Number])
		}
		if it.Number == 4 && it.ImportError != "library offline" {
			t.Errorf("stuck episode import_error = %q, want the reason", it.ImportError)
		}
	}
}

// A monitored series still missing episodes with no schedule data is surfaced
// in `unscheduled` rather than silently absent from the calendar.
func TestCalendarSurfacesUnscheduledSeries(t *testing.T) {
	h := newHarness(t, nil, nil)
	ctx := context.Background()

	noSchedule := seedSeries(t, h.store, "No Schedule Show", 2)

	complete := seedSeries(t, h.store, "Complete Show", 1)
	if err := h.store.Q.SetWantedItemInLibrary(ctx, db.SetWantedItemInLibraryParams{
		InLibrary: 1, ID: itemID(t, h.store, complete, 1),
	}); err != nil {
		t.Fatalf("set in_library: %v", err)
	}

	unmonitored := seedSeries(t, h.store, "Unmonitored NoSched", 1)
	if _, err := h.store.DB.ExecContext(ctx,
		`UPDATE series SET monitored = 0 WHERE id = ?`, unmonitored); err != nil {
		t.Fatalf("unmonitor series: %v", err)
	}

	var out calendarResponse
	if code := h.get(t, "/api/v1/calendar?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z", &out); code != http.StatusOK {
		t.Fatalf("GET calendar = %d, want 200", code)
	}
	if len(out.Unscheduled) != 1 {
		t.Fatalf("unscheduled = %+v, want only the monitored incomplete series", out.Unscheduled)
	}
	if out.Unscheduled[0].TitleID != noSchedule || out.Unscheduled[0].Title != "No Schedule Show" {
		t.Errorf("unscheduled[0] = %+v, want No Schedule Show", out.Unscheduled[0])
	}
}

// Format is what tells a film's premiere from an episode, and a film with a TV
// premiere reaches the calendar today: without it the entry renders as "Ep 01".
func TestCalendarCarriesFormat(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Airing Show", 1)
	setAirsAt(t, h.store, seriesID, 1, "2026-07-07 15:00:00")
	movieID := seedMovie(t, h.store, "Sample Film", 2026)
	setAirsAt(t, h.store, movieID, 1, "2026-07-08 15:00:00")

	var out calendarResponse
	if code := h.get(t, "/api/v1/calendar?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z", &out); code != http.StatusOK {
		t.Fatalf("GET calendar = %d, want 200", code)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %+v, want the series episode and the film", out.Items)
	}
	got := map[int64]string{}
	for _, it := range out.Items {
		got[it.TitleID] = it.Format
	}
	if got[seriesID] != "TV" {
		t.Errorf("series format = %q, want TV", got[seriesID])
	}
	if got[movieID] != "MOVIE" {
		t.Errorf("film format = %q, want MOVIE", got[movieID])
	}
}

// The two halves of the null-date rule: a film AniList dates is placed, and one
// it gives only a year for stays in the footer rather than being invented onto
// January 1st.
func TestCalendarPlacesADatedFilmAndFootnotesAYearOnlyOne(t *testing.T) {
	h := newHarness(t, nil, nil)
	dated := seedMovie(t, h.store, "Dated Film", 2026)
	setAirsAt(t, h.store, dated, 1, "2026-07-09 12:00:00")
	yearOnly := seedMovie(t, h.store, "Announced Film", 2027)

	var out calendarResponse
	if code := h.get(t, "/api/v1/calendar?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z", &out); code != http.StatusOK {
		t.Fatalf("GET calendar = %d, want 200", code)
	}
	if len(out.Items) != 1 || out.Items[0].TitleID != dated {
		t.Fatalf("items = %+v, want only the dated film", out.Items)
	}
	if len(out.Unscheduled) != 1 || out.Unscheduled[0].TitleID != yearOnly {
		t.Fatalf("unscheduled = %+v, want only the year-only film", out.Unscheduled)
	}
}

// Dating a film makes it ungrabbable until its premiere, which is right — but
// the library list must still count it as something being pursued, or the title
// reads "Nothing aired yet" from the day it gains a date.
func TestAnnouncedFilmStaysTracked(t *testing.T) {
	h := newHarness(t, nil, nil)
	movieID := seedMovie(t, h.store, "Announced Film", 2027)
	setAirsAt(t, h.store, movieID, 1, store.FormatTimestamp(time.Now().Add(90*24*time.Hour)))
	showID := seedSeries(t, h.store, "Airing Show", 2)
	setAirsAt(t, h.store, showID, 2, store.FormatTimestamp(time.Now().Add(48*time.Hour)))

	var list struct {
		Titles []struct {
			ID      int64 `json:"id"`
			Tracked int   `json:"tracked"`
			Total   int   `json:"total"`
		} `json:"titles"`
	}
	if code := h.get(t, "/api/v1/titles", &list); code != http.StatusOK {
		t.Fatalf("GET titles = %d, want 200", code)
	}
	got := map[int64]int{}
	for _, ti := range list.Titles {
		got[ti.ID] = ti.Tracked
	}
	if got[movieID] != 1 {
		t.Errorf("film tracked = %d, want 1: an announced film is still being waited on", got[movieID])
	}
	// The episodic cut is untouched: episode 2 has not aired, so it is not pursued.
	if got[showID] != 1 {
		t.Errorf("series tracked = %d, want 1 (only the aired episode)", got[showID])
	}
}

func TestCalendarRejectsInvalidRange(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := h.get(t, "/api/v1/calendar?start=2026-08-01T00:00:00Z&end=2026-07-01T00:00:00Z", nil); code != http.StatusUnprocessableEntity {
		t.Errorf("end before start = %d, want 422", code)
	}
	if code := h.get(t, "/api/v1/calendar", nil); code != http.StatusUnprocessableEntity {
		t.Errorf("missing range = %d, want 422", code)
	}
}
