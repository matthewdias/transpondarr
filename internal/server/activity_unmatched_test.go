package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

type unmatchedItemJSON struct {
	InfoHash    string  `json:"infohash"`
	Name        string  `json:"name"`
	ClientState string  `json:"client_state"`
	Progress    float64 `json:"progress"`
	SavePath    string  `json:"save_path"`
	Size        int64   `json:"size"`
	AddedAt     string  `json:"added_at"`
}

type unmatchedJSON struct {
	Items    []unmatchedItemJSON `json:"items"`
	ClientOk bool                `json:"client_ok"`
	Scoped   bool                `json:"scoped"`
}

// Every grab status counts as a reference — imported torrents seed legitimately
// and a failed one is already visible in history — so only a torrent in our
// category that nothing at all points at is unmatched.
func TestUnmatchedDownloadsListsOnlyTorrentsNoGrabReferences(t *testing.T) {
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "AAAA1111", Name: "grabbed one", Category: "transpondarr", State: download.StateDownloading},
		{Hash: "bbbb2222", Name: "deferred one", Category: "transpondarr", State: download.StateComplete},
		{Hash: "cccc3333", Name: "imported one", Category: "transpondarr", State: download.StateComplete},
		{Hash: "dddd4444", Name: "failed one", Category: "transpondarr", State: download.StateError},
		{Hash: "eeee5555", Name: "the superseded orphan", Category: "transpondarr",
			State: download.StateDownloading, Progress: 0.25, SavePath: "/downloads"},
		{Hash: "ffff6666", Name: "the user's own torrent", Category: "movies", State: download.StateComplete},
		{Hash: "9999aaaa", Name: "uncategorized", State: download.StateComplete},
	}}
	h := newHarness(t, nil, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 6)
	seedOpenGrab(t, h.store, seriesID, 1, "aaaa1111", "rel 1", "grabbed")
	seedOpenGrab(t, h.store, seriesID, 2, "bbbb2222", "rel 2", "import_deferred")
	seedOpenGrab(t, h.store, seriesID, 3, "cccc3333", "rel 3", "imported")
	seedOpenGrab(t, h.store, seriesID, 4, "dddd4444", "rel 4", "failed")

	var got unmatchedJSON
	if code := h.get(t, "/api/v1/activity/unmatched", &got); code != http.StatusOK {
		t.Fatalf("unmatched status = %d, want 200", code)
	}
	if !got.Scoped || !got.ClientOk {
		t.Errorf("scoped = %v, client_ok = %v; want both true", got.Scoped, got.ClientOk)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %+v, want only the orphan in our category", got.Items)
	}
	item := got.Items[0]
	if item.InfoHash != "eeee5555" || item.Name != "the superseded orphan" {
		t.Errorf("item = %+v, want the eeee5555 orphan", item)
	}
	if item.ClientState != "downloading" || item.Progress != 0.25 || item.SavePath != "/downloads" {
		t.Errorf("item = %+v, want the live client detail carried through", item)
	}
}

// The motivating sequence, driven through the real manual grab path rather than
// seeded: two grabs for one episode, so UpsertGrab's ON CONFLICT overwrites the
// row and the first torrent is left referenced by nothing. Every other test here
// seeds grab rows, which would keep passing even if the supersede stopped
// happening — this one fails loudly if #197 ever changes the shape.
func TestASupersededGrabsTorrentBecomesUnmatched(t *testing.T) {
	const (
		firstURL  = "magnet:?xt=urn:btih:00000000000000000000000000000000000000aa"
		secondURL = "magnet:?xt=urn:btih:00000000000000000000000000000000000000bb"
	)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[FakeGroup] Placeholder Saga - 04 [720p]", DownloadURL: firstURL, Seeders: 10},
		{Title: "[FakeGroup] Placeholder Saga - 04 [1080p]", DownloadURL: secondURL, Seeders: 90},
	}}
	dl := &coretest.FakeDownload{}
	// The client answers each add with its own hash, as a real one would.
	byURL := map[string]string{firstURL: "aaaa1111", secondURL: "bbbb2222"}
	dl.AddHook = func(opts download.AddOptions) {
		dl.Result = download.AddResult{Hash: byURL[opts.URL], Outcome: download.AddSuccess}
	}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	for _, url := range []string{firstURL, secondURL} {
		if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
			map[string]any{"download_url": url}, nil); code != http.StatusCreated {
			t.Fatalf("grab %s = %d, want 201", url, code)
		}
	}
	// Both are in the client; only the second is still spoken for.
	dl.Statuses = []download.Status{
		{Hash: "aaaa1111", Name: "the superseded one", Category: "transpondarr", State: download.StateDownloading},
		{Hash: "bbbb2222", Name: "the one that replaced it", Category: "transpondarr", State: download.StateDownloading},
	}

	var got unmatchedJSON
	if code := h.get(t, "/api/v1/activity/unmatched", &got); code != http.StatusOK {
		t.Fatalf("unmatched status = %d, want 200", code)
	}
	if len(got.Items) != 1 || got.Items[0].InfoHash != "aaaa1111" {
		t.Fatalf("items = %+v, want only the superseded aaaa1111", got.Items)
	}
}

// An unmatched torrent has no grab row behind it, so the listing is the only
// place a human can identify it from — which takes size and age, not just a name
// and a hash (#131).
func TestUnmatchedDownloadsCarrySizeAndAddedTime(t *testing.T) {
	added := time.Date(2025, 8, 7, 12, 0, 0, 0, time.UTC)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "eeee5555", Name: "the orphan", Category: "transpondarr",
			State: download.StateDownloading, Size: 734003200, AddedAt: added},
		{Hash: "ffff6666", Name: "no add time reported", Category: "transpondarr",
			State: download.StateComplete, Size: 100},
	}}
	h := newHarness(t, nil, dl)

	var got unmatchedJSON
	if code := h.get(t, "/api/v1/activity/unmatched", &got); code != http.StatusOK {
		t.Fatalf("unmatched status = %d, want 200", code)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %+v, want both orphans", got.Items)
	}
	if got.Items[0].Size != 734003200 {
		t.Errorf("size = %d, want 734003200", got.Items[0].Size)
	}
	if got.Items[0].AddedAt != "2025-08-07T12:00:00Z" {
		t.Errorf("added_at = %q, want RFC3339 UTC", got.Items[0].AddedAt)
	}
	// Omitted rather than sent as the epoch, which would read as a 1970 torrent.
	if got.Items[1].AddedAt != "" {
		t.Errorf("added_at = %q, want empty when the client reports none", got.Items[1].AddedAt)
	}
}

// A referenced hash is matched however the client cases it: identity is the
// lowercase info hash throughout the pipeline.
func TestUnmatchedDownloadsMatchHashesCaseInsensitively(t *testing.T) {
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "AAAA1111AAAA1111", Name: "grabbed one", Category: "transpondarr", State: download.StateDownloading},
	}}
	h := newHarness(t, nil, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 2)
	seedOpenGrab(t, h.store, seriesID, 1, "aaaa1111aaaa1111", "rel 1", "grabbed")

	var got unmatchedJSON
	if code := h.get(t, "/api/v1/activity/unmatched", &got); code != http.StatusOK {
		t.Fatalf("unmatched status = %d, want 200", code)
	}
	if len(got.Items) != 0 {
		t.Errorf("items = %+v, want none — the grab references that hash", got.Items)
	}
}

// Client trouble degrades to an empty list, matching the queue's stance that it
// answers even when the client cannot. Guessing would be worse than silence: an
// unanswered client cannot say whose torrents these are.
func TestUnmatchedDownloadsDegradeWhenTheClientCannotAnswer(t *testing.T) {
	t.Run("no client configured", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		var got unmatchedJSON
		if code := h.get(t, "/api/v1/activity/unmatched", &got); code != http.StatusOK {
			t.Fatalf("unmatched status = %d, want 200 with no client", code)
		}
		if got.ClientOk || !got.Scoped || len(got.Items) != 0 {
			t.Errorf("got %+v, want scoped with client_ok false and no items", got)
		}
	})
	t.Run("client errors", func(t *testing.T) {
		dl := &coretest.FakeDownload{StatusErr: errors.New("client unreachable")}
		h := newHarness(t, nil, dl)
		var got unmatchedJSON
		if code := h.get(t, "/api/v1/activity/unmatched", &got); code != http.StatusOK {
			t.Fatalf("unmatched status = %d, want 200 despite the client error", code)
		}
		if got.ClientOk || len(got.Items) != 0 {
			t.Errorf("got %+v, want client_ok false and no items", got)
		}
	})
}

// The removal is opt-in on the data, and the flag reaches the client unchanged.
func TestRemoveUnmatchedDownloadPassesDeleteDataThrough(t *testing.T) {
	for _, c := range []struct {
		name  string
		query string
		want  bool
	}{
		{"defaults to taking the data", "", true},
		{"keeps the data when asked", "?delete_data=false", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dl := &coretest.FakeDownload{Statuses: []download.Status{
				{Hash: "EEEE5555", Name: "orphan", Category: "transpondarr", State: download.StateDownloading},
			}}
			h := newHarness(t, nil, dl)

			code := do(t, h, http.MethodDelete, "/api/v1/activity/unmatched/eeee5555"+c.query, nil, nil)
			if code != http.StatusNoContent {
				t.Fatalf("delete status = %d, want 204", code)
			}
			if len(dl.Removes) != 1 {
				t.Fatalf("Remove called %d times, want 1", len(dl.Removes))
			}
			call := dl.Removes[0]
			if len(call.Hashes) != 1 || call.Hashes[0] != "eeee5555" {
				t.Errorf("removed hashes = %v, want the one lowercased hash", call.Hashes)
			}
			if call.DeleteData != c.want {
				t.Errorf("DeleteData = %v, want %v", call.DeleteData, c.want)
			}
		})
	}
}

// The single most important guard: between the list rendering and the click, a
// scan or a grab can adopt the hash. A stale UI must never delete a live grab's
// torrent.
func TestRemoveUnmatchedDownloadRefusesAHashThatBecameReferenced(t *testing.T) {
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "eeee5555", Name: "no longer an orphan", Category: "transpondarr", State: download.StateDownloading},
	}}
	h := newHarness(t, nil, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 2)
	seedOpenGrab(t, h.store, seriesID, 1, "eeee5555", "rel", "grabbed")

	code := do(t, h, http.MethodDelete, "/api/v1/activity/unmatched/eeee5555", nil, nil)
	if code != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409", code)
	}
	if len(dl.Removes) != 0 {
		t.Errorf("Remove was called %v for a referenced hash", dl.Removes)
	}
}

// The category is the entire safety boundary: a torrent outside it is the
// user's, and this endpoint cannot touch it whatever hash it is handed.
func TestRemoveUnmatchedDownloadRefusesATorrentOutsideOurCategory(t *testing.T) {
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "ffff6666", Name: "the user's own torrent", Category: "movies", State: download.StateComplete},
	}}
	h := newHarness(t, nil, dl)

	for _, hash := range []string{"ffff6666", "0000nothing"} {
		code := do(t, h, http.MethodDelete, "/api/v1/activity/unmatched/"+hash, nil, nil)
		if code != http.StatusNotFound {
			t.Errorf("delete %s status = %d, want 404", hash, code)
		}
	}
	if len(dl.Removes) != 0 {
		t.Errorf("Remove was called %v for a torrent outside our category", dl.Removes)
	}
}

// A client that refuses the delete is a 502, as the series removal is: the
// request was fine, the client said no.
func TestRemoveUnmatchedDownloadReportsAClientRefusal(t *testing.T) {
	dl := &coretest.FakeDownload{
		Statuses:  []download.Status{{Hash: "eeee5555", Category: "transpondarr", State: download.StateDownloading}},
		RemoveErr: errors.New("qbit: refused"),
	}
	h := newHarness(t, nil, dl)

	if code := do(t, h, http.MethodDelete, "/api/v1/activity/unmatched/eeee5555", nil, nil); code != http.StatusBadGateway {
		t.Errorf("delete status = %d, want 502", code)
	}
}

// With no client there is nothing to enumerate, so the delete cannot establish
// that the hash is ours — a 503, not a blind removal.
func TestRemoveUnmatchedDownloadWithoutAClient(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := do(t, h, http.MethodDelete, "/api/v1/activity/unmatched/eeee5555", nil, nil); code != http.StatusServiceUnavailable {
		t.Errorf("delete status = %d, want 503", code)
	}
}
