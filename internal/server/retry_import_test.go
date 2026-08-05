package server_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

type payloadJSON struct {
	ReleaseTitle string `json:"release_title"`
	InfoHash     string `json:"infohash"`
	Items        []struct {
		GrabID     int64  `json:"grab_id"`
		ItemNumber int    `json:"item_number"`
		Status     string `json:"status"`
	} `json:"items"`
	Files []struct {
		Path          string `json:"path"`
		EpisodeStart  int    `json:"episode_start"`
		SuggestedItem int    `json:"suggested_item"`
	} `json:"files"`
	Archives []struct {
		Path  string `json:"path"`
		Parts int    `json:"parts"`
	} `json:"archives"`
}

type retryJSON struct {
	Results []struct {
		ItemNumber int    `json:"item_number"`
		Outcome    string `json:"outcome"`
		Detail     string `json:"detail"`
	} `json:"results"`
}

// deferredRelease grabs one release across two items, completes it with a
// payload whose second file is unreadable, and scans — leaving episode 2
// deferred, which is the state the fix dialog exists for.
func deferredRelease(t *testing.T, h *harness) (seriesID, deferredGrabID, importedGrabID int64) {
	t.Helper()
	seriesID = seedSeries(t, h.store, "Placeholder Saga", 6)
	seedOpenGrab(t, h.store, seriesID, 1, "packhash", "[SynthSubs] Placeholder Saga - 01-02 [Batch]", "grabbed")
	seedOpenGrab(t, h.store, seriesID, 2, "packhash", "[SynthSubs] Placeholder Saga - 01-02 [Batch]", "grabbed")

	dir := t.TempDir()
	for _, name := range []string{
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.dl.Statuses = []download.Status{
		{Hash: "packhash", State: download.StateComplete, ContentPath: dir},
	}
	if err := h.importer.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rows, err := h.store.Q.ListGrabsByInfoHash(context.Background(), "packhash")
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	for _, g := range rows {
		switch g.Status {
		case "import_deferred":
			deferredGrabID = g.ID
		case "imported":
			importedGrabID = g.ID
		}
	}
	if deferredGrabID == 0 || importedGrabID == 0 {
		t.Fatalf("grabs = %+v, want one imported and one deferred", rows)
	}
	return seriesID, deferredGrabID, importedGrabID
}

// deferredArchiveRelease is the same state over a payload nothing can read: a
// RAR set, which the dialog has to render or it is a dead end.
func deferredArchiveRelease(t *testing.T, h *harness) (deferredGrabID int64) {
	t.Helper()
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 6)
	seedOpenGrab(t, h.store, seriesID, 1, "rarhash", "placeholder.saga.s01e01.1080p.web.h264-synth", "grabbed")

	dir := t.TempDir()
	for _, name := range []string{
		"placeholder.saga.s01e01.1080p.web.h264-synth.rar",
		"placeholder.saga.s01e01.1080p.web.h264-synth.r00",
		"placeholder.saga.s01e01.1080p.web.h264-synth.r01",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.dl.Statuses = []download.Status{
		{Hash: "rarhash", State: download.StateComplete, ContentPath: dir},
	}
	if err := h.importer.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rows, err := h.store.Q.ListGrabsByInfoHash(context.Background(), "rarhash")
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	for _, g := range rows {
		if g.Status == "import_deferred" {
			return g.ID
		}
	}
	t.Fatalf("grabs = %+v, want the archive payload deferred", rows)
	return 0
}

// An empty file list was the dead end; the archive is what the dialog renders
// instead, and it is listed beside the files rather than among them.
func TestQueueItemPayloadListsArchivesItCannotUnpack(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	deferredID := deferredArchiveRelease(t, h)

	var out payloadJSON
	if code := h.get(t, fmt.Sprintf("/api/v1/activity/queue/%d/payload", deferredID), &out); code != http.StatusOK {
		t.Fatalf("payload status = %d, want 200", code)
	}
	if len(out.Files) != 0 {
		t.Errorf("files = %+v, want none from an archive payload", out.Files)
	}
	if len(out.Archives) != 1 || out.Archives[0].Parts != 3 ||
		out.Archives[0].Path != "placeholder.saga.s01e01.1080p.web.h264-synth.rar" {
		t.Fatalf("archives = %+v, want the one 3-part set", out.Archives)
	}
}

func TestQueueItemPayloadListsTheFilesAndItems(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	_, deferredID, _ := deferredRelease(t, h)

	var out payloadJSON
	if code := h.get(t, fmt.Sprintf("/api/v1/activity/queue/%d/payload", deferredID), &out); code != http.StatusOK {
		t.Fatalf("payload status = %d, want 200", code)
	}
	if out.InfoHash != "packhash" || out.ReleaseTitle == "" {
		t.Errorf("payload = %+v, want the release identified", out)
	}
	if len(out.Items) != 2 {
		t.Errorf("items = %+v, want both rows of the release", out.Items)
	}
	if len(out.Files) != 2 {
		t.Fatalf("files = %+v, want both payload files", out.Files)
	}
	for _, f := range out.Files {
		if f.Path == "b1946ac92492d2347c6235b4d2611184.mkv" && f.EpisodeStart != 0 {
			t.Errorf("file %+v, want nothing read from an unreadable name", f)
		}
	}
}

// The endpoints are for rows awaiting a fix; anything else is a 409 or a 404.
func TestQueueItemPayloadRefusesRowsWithNothingToFix(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	_, _, importedID := deferredRelease(t, h)

	if code := h.get(t, fmt.Sprintf("/api/v1/activity/queue/%d/payload", importedID), nil); code != http.StatusConflict {
		t.Errorf("payload status = %d, want 409 for an imported row", code)
	}
	if code := h.get(t, "/api/v1/activity/queue/999999/payload", nil); code != http.StatusNotFound {
		t.Errorf("payload status = %d, want 404 for an unknown grab", code)
	}
}

// A torrent the client forgot has no payload to fix, and saying so is more
// useful than an empty file list.
func TestQueueItemPayloadReportsAVanishedTorrent(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	_, deferredID, _ := deferredRelease(t, h)
	h.dl.Statuses = nil

	if code := h.get(t, fmt.Sprintf("/api/v1/activity/queue/%d/payload", deferredID), nil); code != http.StatusConflict {
		t.Errorf("payload status = %d, want 409 once the torrent is gone", code)
	}
}

// The escape hatch end to end: naming the file imports it, marks the item had,
// and records the history the Activity feed renders.
func TestRetryImportWithAnAssignmentImports(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	seriesID, deferredID, _ := deferredRelease(t, h)

	var out retryJSON
	code := h.postJSON(t, fmt.Sprintf("/api/v1/activity/queue/%d/retry-import", deferredID),
		map[string]any{"assignments": []map[string]any{
			{"file": "b1946ac92492d2347c6235b4d2611184.mkv", "item_number": 2},
		}}, &out)
	if code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", code)
	}
	if len(out.Results) != 1 || out.Results[0].ItemNumber != 2 || out.Results[0].Outcome != "imported" {
		t.Fatalf("results = %+v, want episode 2 imported", out.Results)
	}
	if got := itemStatus(t, h, seriesID, 2); got != "have" {
		t.Errorf("episode 2 status = %q, want have", got)
	}
	events, err := h.store.Q.ListSeriesGrabEvents(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var imported int
	for _, e := range events {
		if e.Event == "imported" {
			imported++
		}
	}
	if imported != 2 {
		t.Errorf("imported events = %d, want one per episode", imported)
	}
}

// An assignment the importer could not carry out is refused whole, so the
// dialog reports it rather than half-applying and leaving a mess.
func TestRetryImportRejectsAnUnusableAssignment(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	_, deferredID, _ := deferredRelease(t, h)

	code := h.postJSON(t, fmt.Sprintf("/api/v1/activity/queue/%d/retry-import", deferredID),
		map[string]any{"assignments": []map[string]any{
			{"file": "not-in-this-payload.mkv", "item_number": 2},
		}}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Errorf("retry status = %d, want 422 for a file that is not in the payload", code)
	}
}

// A retry against a row that already settled is the same 409 the payload read
// gives: there is nothing left to reopen.
func TestRetryImportRefusesASettledRow(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	_, _, importedID := deferredRelease(t, h)

	code := h.postJSON(t, fmt.Sprintf("/api/v1/activity/queue/%d/retry-import", importedID),
		map[string]any{}, nil)
	if code != http.StatusConflict {
		t.Errorf("retry status = %d, want 409", code)
	}
}
