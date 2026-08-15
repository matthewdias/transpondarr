package server_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type queueItemJSON struct {
	ID           int64    `json:"id"`
	TitleID      int64    `json:"title_id"`
	Title        string   `json:"title"`
	Format       string   `json:"format"`
	ItemNumber   int      `json:"item_number"`
	ReleaseTitle string   `json:"release_title"`
	InfoHash     string   `json:"infohash"`
	Status       string   `json:"status"`
	ImportError  string   `json:"import_error"`
	ClientState  string   `json:"client_state"`
	Progress     *float64 `json:"progress"`
	AbandonAt    string   `json:"abandon_at"`
	CreatedAt    string   `json:"created_at"`
}

type queueJSON struct {
	Items    []queueItemJSON `json:"items"`
	ClientOk bool            `json:"client_ok"`
}

// seedOpenGrab writes a grab for the title's item with the given number.
func seedOpenGrab(t *testing.T, st *store.Store, titleID int64, number int, hash, title, status string) db.Grab {
	t.Helper()
	ctx := context.Background()
	items, err := st.Q.ListWantedItems(ctx, titleID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	for _, it := range items {
		if it.Number.Int64 == int64(number) {
			g, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
				WantedItemID: it.ID, InfoHash: hash, ReleaseTitle: title, Status: "grabbed",
			})
			if err != nil {
				t.Fatalf("upsert grab: %v", err)
			}
			if status != "grabbed" {
				if err := st.Q.SetGrabStatus(ctx, db.SetGrabStatusParams{Status: status, ID: g.ID}); err != nil {
					t.Fatalf("set status: %v", err)
				}
			}
			return g
		}
	}
	t.Fatalf("no wanted item numbered %d in series %d", number, titleID)
	return db.Grab{}
}

func TestActivityQueueReportsOpenGrabsWithClientState(t *testing.T) {
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "H1", State: download.StatePaused, Progress: 0.42},
	}}
	h := newHarness(t, nil, dl)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 6)

	downloading := seedOpenGrab(t, h.store, titleID, 1, "h1", "[ExampleSubs] Placeholder Saga - 01 [1080p]", "grabbed")
	stuck := seedOpenGrab(t, h.store, titleID, 2, "h2", "[ExampleSubs] Placeholder Saga - 02 [1080p]", "grabbed")
	if err := h.store.Q.SetGrabLastError(context.Background(), db.SetGrabLastErrorParams{
		LastError: sql.NullString{String: "import failed: disk full", Valid: true}, ID: stuck.ID,
	}); err != nil {
		t.Fatalf("set last_error: %v", err)
	}
	deferred := seedOpenGrab(t, h.store, titleID, 3, "h3", "[ExampleSubs] Placeholder Saga - 03 [1080p]", "import_deferred")
	seedOpenGrab(t, h.store, titleID, 4, "h4", "settled", "imported")
	seedOpenGrab(t, h.store, titleID, 5, "h5", "settled", "failed")

	var got queueJSON
	if code := h.get(t, "/api/v1/activity/queue", &got); code != http.StatusOK {
		t.Fatalf("queue status = %d, want 200", code)
	}
	if !got.ClientOk {
		t.Error("client_ok = false, want true with a healthy client")
	}
	if len(got.Items) != 3 {
		t.Fatalf("items = %d, want the 3 open grabs (settled ones excluded)", len(got.Items))
	}
	// Newest first: same-second created_at falls back to id order.
	if got.Items[0].ID != deferred.ID || got.Items[1].ID != stuck.ID || got.Items[2].ID != downloading.ID {
		t.Errorf("order = %d,%d,%d; want %d,%d,%d (newest first)",
			got.Items[0].ID, got.Items[1].ID, got.Items[2].ID, deferred.ID, stuck.ID, downloading.ID)
	}

	byID := map[int64]queueItemJSON{}
	for _, it := range got.Items {
		byID[it.ID] = it
	}
	d := byID[downloading.ID]
	if d.Status != "downloading" || d.ClientState != "paused" {
		t.Errorf("downloading row = %+v, want status downloading with client_state paused", d)
	}
	if d.Progress == nil || *d.Progress != 0.42 {
		t.Errorf("progress = %v, want 0.42", d.Progress)
	}
	if d.TitleID != titleID || d.Title != "Placeholder Saga" || d.ItemNumber != 1 || d.CreatedAt == "" {
		t.Errorf("downloading row = %+v, want series fields and a created_at", d)
	}
	s := byID[stuck.ID]
	if s.Status != "stuck" || s.ImportError != "import failed: disk full" {
		t.Errorf("stuck row = %+v, want status stuck with the import error", s)
	}
	if s.ClientState != "" {
		t.Errorf("stuck row client_state = %q, want empty (hash h2 not reported)", s.ClientState)
	}
	if f := byID[deferred.ID]; f.Status != "deferred" {
		t.Errorf("deferred row status = %q, want deferred", f.Status)
	}
}

// A stalled row already reads as stalled; what it cannot say is that we are
// going to act on it and when, which is the part abandon_at adds (#242).
func TestActivityQueueReportsWhenAStallWillBeGivenUpOn(t *testing.T) {
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "h1", State: download.StateStalled, Progress: 0},
		{Hash: "h2", State: download.StateDownloading, Progress: 0.3},
		// h3 is deliberately not reported: the torrent was removed by hand.
		{Hash: "h4", State: download.StateDownloading, Progress: 0.4},
	}}
	h := newHarness(t, nil, dl)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 4)

	stalledGrab := seedOpenGrab(t, h.store, titleID, 1, "h1", "[ExampleSubs] Placeholder Saga - 01 [1080p]", "grabbed")
	healthy := seedOpenGrab(t, h.store, titleID, 2, "h2", "[ExampleSubs] Placeholder Saga - 02 [1080p]", "grabbed")
	gone := seedOpenGrab(t, h.store, titleID, 3, "h3", "[ExampleSubs] Placeholder Saga - 03 [1080p]", "grabbed")
	resumed := seedOpenGrab(t, h.store, titleID, 4, "h4", "[ExampleSubs] Placeholder Saga - 04 [1080p]", "grabbed")
	stalledFor := 2 * time.Hour
	for _, id := range []int64{stalledGrab.ID, gone.ID, resumed.ID} {
		if err := h.store.Q.SetGrabStalledSince(context.Background(), db.SetGrabStalledSinceParams{
			StalledSince: sql.NullString{String: store.FormatTimestamp(time.Now().Add(-stalledFor)), Valid: true},
			ID:           id,
		}); err != nil {
			t.Fatalf("set stalled_since: %v", err)
		}
	}

	var got queueJSON
	if code := h.get(t, "/api/v1/activity/queue", &got); code != http.StatusOK {
		t.Fatalf("queue status = %d, want 200", code)
	}
	byID := map[int64]queueItemJSON{}
	for _, it := range got.Items {
		byID[it.ID] = it
	}

	s := byID[stalledGrab.ID]
	if s.AbandonAt == "" {
		t.Fatal("abandon_at is empty for a stall the importer is counting")
	}
	at, err := time.Parse(time.RFC3339, s.AbandonAt)
	if err != nil {
		t.Fatalf("abandon_at %q is not RFC3339: %v", s.AbandonAt, err)
	}
	// The default timeout, measured from the stall rather than from now.
	want := time.Now().Add(domain.DefaultStallHours*time.Hour - stalledFor)
	if d := at.Sub(want); d > time.Minute || d < -time.Minute {
		t.Errorf("abandon_at = %v, want about %v (the stall's own clock, not this request's)", at, want)
	}
	if h := byID[healthy.ID]; h.AbandonAt != "" {
		t.Errorf("abandon_at = %q for a healthy download, want empty: nothing is going to happen to it", h.AbandonAt)
	}
	// A torrent that goes absent is settled on the grace period instead, and
	// absence wins by construction, so the stall's deadline stops being the truth.
	if g := byID[gone.ID]; g.AbandonAt != "" {
		t.Errorf("abandon_at = %q for a torrent the client no longer reports, want empty: the absence timer owns it now", g.AbandonAt)
	}
	// The stamp outlives the stall by up to one import scan, so the live status
	// is what says whether a deadline is still real.
	if r := byID[resumed.ID]; r.AbandonAt != "" {
		t.Errorf("abandon_at = %q for a download that resumed, want empty: its stamp is only waiting to be cleared", r.AbandonAt)
	}
}

type activityEventJSON struct {
	ID           int64  `json:"id"`
	TitleID      int64  `json:"title_id"`
	Title        string `json:"title"`
	ItemNumber   int    `json:"item_number"`
	ReleaseTitle string `json:"release_title"`
	InfoHash     string `json:"infohash"`
	Status       string `json:"status"`
	Detail       string `json:"detail"`
	CreatedAt    string `json:"created_at"`
}

type activityHistoryJSON struct {
	Events     []activityEventJSON `json:"events"`
	NextCursor string              `json:"next_cursor"`
}

// seedActivityEvent inserts a grab event with a controlled created_at.
func seedActivityEvent(t *testing.T, st *store.Store, titleID int64, number int, event, detail, createdAt string) {
	t.Helper()
	if _, err := st.DB.Exec(
		`INSERT INTO grab_events (series_id, wanted_item_id, item_number, item_kind, info_hash, release_title, event, detail, created_at)
		 VALUES (?, ?, ?, 'episode', 'hash', 'rel', ?, ?, ?)`,
		titleID, number, number, event, detail, createdAt,
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func TestActivityHistoryPaginatesToExhaustion(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := seedTitle(t, h.store, "Placeholder Saga", 3)
	second := seedTitle(t, h.store, "Unrelated Show", 2)

	// Three share a created_at, so pagination has to tie-break on id.
	seedActivityEvent(t, h.store, first, 1, "grabbed", "", "2026-01-01 10:00:00")
	seedActivityEvent(t, h.store, first, 1, "failed", "the download vanished from the client", "2026-01-02 10:00:00")
	seedActivityEvent(t, h.store, first, 2, "grabbed", "", "2026-01-02 10:00:00")
	seedActivityEvent(t, h.store, second, 1, "grabbed", "", "2026-01-02 10:00:00")
	seedActivityEvent(t, h.store, second, 1, "imported", "", "2026-01-03 10:00:00")

	var page activityHistoryJSON
	if code := h.get(t, "/api/v1/activity/history?limit=2", &page); code != http.StatusOK {
		t.Fatalf("history status = %d, want 200", code)
	}
	if len(page.Events) != 2 {
		t.Fatalf("first page = %d events, want 2", len(page.Events))
	}
	if page.Events[0].Status != "imported" || page.Events[0].Title != "Unrelated Show" {
		t.Errorf("newest event = %+v, want the imported Unrelated Show one", page.Events[0])
	}
	if page.NextCursor == "" {
		t.Fatal("next_cursor empty with more events remaining")
	}

	seen := map[int64]bool{}
	for _, e := range page.Events {
		seen[e.ID] = true
	}
	cursor := page.NextCursor
	for cursor != "" {
		var next activityHistoryJSON
		if code := h.get(t, "/api/v1/activity/history?limit=2&cursor="+cursor, &next); code != http.StatusOK {
			t.Fatalf("history page status = %d, want 200", code)
		}
		for _, e := range next.Events {
			if seen[e.ID] {
				t.Fatalf("event %d returned twice", e.ID)
			}
			seen[e.ID] = true
		}
		cursor = next.NextCursor
	}
	if len(seen) != 5 {
		t.Errorf("paged through %d events, want all 5", len(seen))
	}
}

func TestActivityHistoryCarriesDetailAndTitleName(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 3)
	seedActivityEvent(t, h.store, titleID, 2, "failed", "the download client reported an error", "2026-01-01 10:00:00")

	var page activityHistoryJSON
	if code := h.get(t, "/api/v1/activity/history", &page); code != http.StatusOK {
		t.Fatalf("history status = %d, want 200", code)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(page.Events))
	}
	e := page.Events[0]
	if e.Status != "failed" || e.Detail != "the download client reported an error" {
		t.Errorf("event = %+v, want the failed detail carried through", e)
	}
	if e.TitleID != titleID || e.Title != "Placeholder Saga" || e.ItemNumber != 2 {
		t.Errorf("event = %+v, want the series fields", e)
	}
	if page.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty on the last page", page.NextCursor)
	}
}

func TestActivityHistoryRejectsGarbageCursor(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := h.get(t, "/api/v1/activity/history?cursor=%21%21not-base64%21%21", nil); code != http.StatusBadRequest {
		t.Errorf("garbage cursor status = %d, want 400", code)
	}
}

func TestActivityHistoryEmpty(t *testing.T) {
	h := newHarness(t, nil, nil)
	var page activityHistoryJSON
	if code := h.get(t, "/api/v1/activity/history", &page); code != http.StatusOK {
		t.Fatalf("history status = %d, want 200", code)
	}
	if page.Events == nil || len(page.Events) != 0 {
		t.Errorf("events = %v, want an empty array", page.Events)
	}
	if page.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty", page.NextCursor)
	}
}

// A failed attempt survives a re-grab in per-title history — the bug the
// grabs-table upsert baked in (each attempt overwrote the last).
func TestTitleHistoryKeepsBothAttemptsAcrossRegrab(t *testing.T) {
	const url = "magnet:?xt=urn:btih:00000000000000000000000000000000000000aa"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 04 [1080p]", DownloadURL: url, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashA", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 12)

	if code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", titleID),
		map[string]any{"download_url": url}, nil); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}
	dl.Statuses = []download.Status{{Hash: "hashA", State: download.StateError}}
	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", titleID),
		map[string]any{"download_url": url}, nil); code != http.StatusCreated {
		t.Fatalf("re-grab status = %d, want 201", code)
	}

	var hist struct {
		Title  string `json:"title"`
		Events []struct {
			ItemNumber int    `json:"item_number"`
			Status     string `json:"status"`
			Detail     string `json:"detail"`
			CreatedAt  string `json:"created_at"`
		} `json:"events"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d/grabs", titleID), &hist); code != http.StatusOK {
		t.Fatalf("GET grabs = %d, want 200", code)
	}
	if len(hist.Events) != 3 {
		t.Fatalf("events = %+v, want grabbed + failed + grabbed", hist.Events)
	}
	byStatus := map[string]int{}
	for _, e := range hist.Events {
		byStatus[e.Status]++
		if e.ItemNumber != 4 {
			t.Errorf("event item = %d, want 4", e.ItemNumber)
		}
		if e.Status == "failed" && e.Detail != "the download client reported an error" {
			t.Errorf("failed event detail = %q, want the client-error reason", e.Detail)
		}
	}
	if byStatus["grabbed"] != 2 || byStatus["failed"] != 1 {
		t.Errorf("statuses = %v, want 2 grabbed and 1 failed", byStatus)
	}
}

func TestActivityQueueWithoutDownloadClient(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 3)
	seedOpenGrab(t, h.store, titleID, 1, "h1", "[ExampleSubs] Placeholder Saga - 01 [1080p]", "grabbed")

	var got queueJSON
	if code := h.get(t, "/api/v1/activity/queue", &got); code != http.StatusOK {
		t.Fatalf("queue status = %d, want 200 with no client configured", code)
	}
	if got.ClientOk {
		t.Error("client_ok = true, want false with no client")
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want the grab-state row without client detail", len(got.Items))
	}
	if got.Items[0].ClientState != "" || got.Items[0].Progress != nil {
		t.Errorf("row = %+v, want no client state without a client", got.Items[0])
	}
}

func TestActivityQueueDegradesOnClientError(t *testing.T) {
	dl := &coretest.FakeDownload{StatusErr: errors.New("client unreachable")}
	h := newHarness(t, nil, dl)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 3)
	seedOpenGrab(t, h.store, titleID, 1, "h1", "[ExampleSubs] Placeholder Saga - 01 [1080p]", "grabbed")

	var got queueJSON
	if code := h.get(t, "/api/v1/activity/queue", &got); code != http.StatusOK {
		t.Fatalf("queue status = %d, want 200 despite the client error", code)
	}
	if got.ClientOk {
		t.Error("client_ok = true, want false when Status errors")
	}
	if len(got.Items) != 1 || got.Items[0].ClientState != "" {
		t.Errorf("items = %+v, want the grab-state row with no client detail", got.Items)
	}
}
