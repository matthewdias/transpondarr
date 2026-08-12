package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seedGrab records a grab in the given status against the item numbered number.
func seedGrab(t *testing.T, st *store.Store, seriesID int64, number int, hash, status string) {
	t.Helper()
	ctx := context.Background()
	items, err := st.Q.ListWantedItems(ctx, seriesID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	for _, it := range items {
		if int(it.Number.Int64) == number {
			if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
				WantedItemID: it.ID, InfoHash: hash,
				ReleaseTitle: "[ExampleSubs] Placeholder Saga [1080p]", Status: status,
			}); err != nil {
				t.Fatalf("seed grab on item %d: %v", number, err)
			}
			return
		}
	}
	t.Fatalf("no item numbered %d", number)
}

func countRows(t *testing.T, st *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// Deleting a series removes it and everything hanging off it — wanted items,
// grabs, blocklist memory — and, without the flag, never touches the download
// client.
func TestDeleteSeriesRemovesEverythingAndLeavesTheClientAlone(t *testing.T) {
	dl := &coretest.FakeDownload{}
	h := newHarness(t, nil, dl)
	id := seedSeries(t, h.store, "Placeholder Saga", 3)
	// A second series the delete must leave alone, so the list assertion below
	// has something to find rather than passing on an empty page.
	survivor := seedSeries(t, h.store, "Second Saga", 2)
	seedGrab(t, h.store, id, 1, "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111", "grabbed")
	if _, err := h.store.DB.ExecContext(context.Background(),
		`INSERT INTO release_blocklist (series_id, info_hash, release_title, normalized_title, reason)
		 VALUES (?, '', '[ExampleSubs] Placeholder Saga [1080p]', 'placeholder saga 1080p', 'test')`,
		id); err != nil {
		t.Fatalf("seed blocklist entry: %v", err)
	}

	if code := do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/titles/%d", id), nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", code)
	}

	for _, tc := range []struct{ table, query string }{
		{"series", `SELECT COUNT(*) FROM series WHERE id = ?`},
		{"wanted_items", `SELECT COUNT(*) FROM wanted_items WHERE series_id = ?`},
		{"grabs", `SELECT COUNT(*) FROM grabs g JOIN wanted_items w ON w.id = g.wanted_item_id WHERE w.series_id = ?`},
		{"release_blocklist", `SELECT COUNT(*) FROM release_blocklist WHERE series_id = ?`},
	} {
		if n := countRows(t, h.store, tc.query, id); n != 0 {
			t.Errorf("%d %s rows survive the delete, want 0", n, tc.table)
		}
	}
	if len(dl.Removes) != 0 {
		t.Errorf("download Remove called %d times without the flag, want 0", len(dl.Removes))
	}

	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", id), nil); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
	var listOut struct {
		Titles []struct {
			ID int64 `json:"id"`
		} `json:"titles"`
	}
	if code := h.get(t, "/api/v1/titles", &listOut); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	listed := map[int64]bool{}
	for _, s := range listOut.Titles {
		listed[s.ID] = true
	}
	if listed[id] {
		t.Errorf("deleted series %d still in the list", id)
	}
	if !listed[survivor] {
		t.Errorf("series %d went missing from the list; the delete took more than it was asked for", survivor)
	}
}

func TestDeleteSeriesUnknownIDIs404(t *testing.T) {
	h := newHarness(t, nil, &coretest.FakeDownload{})
	if code := do(t, h, http.MethodDelete, "/api/v1/titles/404", nil, nil); code != http.StatusNotFound {
		t.Fatalf("delete status = %d, want 404", code)
	}
}

// remove_downloads collects every grab's hash except failed ones (failed means
// errored or already gone from the client), deduped — a batch writes one grab
// row per covered item sharing a hash — and lowercased, with the payload data.
func TestDeleteSeriesRemoveDownloadsCollectsHashes(t *testing.T) {
	dl := &coretest.FakeDownload{}
	h := newHarness(t, nil, dl)
	id := seedSeries(t, h.store, "Placeholder Saga", 5)
	seedGrab(t, h.store, id, 1, "AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111", "grabbed")
	seedGrab(t, h.store, id, 2, "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222", "imported")
	seedGrab(t, h.store, id, 3, "cccc3333cccc3333cccc3333cccc3333cccc3333", "import_deferred")
	seedGrab(t, h.store, id, 4, "dddd4444dddd4444dddd4444dddd4444dddd4444", "failed")
	// A batch: same hash as item 1, so it must be deduped, not sent twice.
	seedGrab(t, h.store, id, 5, "AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111", "grabbed")

	code := do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/titles/%d?remove_downloads=true", id), nil, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", code)
	}

	if len(dl.Removes) != 1 {
		t.Fatalf("download Remove called %d times, want exactly 1", len(dl.Removes))
	}
	call := dl.Removes[0]
	if !call.DeleteData {
		t.Error("DeleteData = false, want true — payload data goes with the entry")
	}
	want := map[string]bool{
		"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111": true,
		"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222": true,
		"cccc3333cccc3333cccc3333cccc3333cccc3333": true,
	}
	if len(call.Hashes) != len(want) {
		t.Fatalf("removed hashes = %v, want the three non-failed, deduped", call.Hashes)
	}
	for _, hsh := range call.Hashes {
		if !want[hsh] {
			t.Errorf("unexpected hash %q in the remove call (failed grabs are excluded, hashes lowercased)", hsh)
		}
	}
	if n := countRows(t, h.store, `SELECT COUNT(*) FROM series WHERE id = ?`, id); n != 0 {
		t.Errorf("series survives the delete")
	}
}

// The flag with no configured client is a 503 and the series survives — but only
// when there is actually something to remove; with no in-client grabs the client
// is never needed.
func TestDeleteSeriesRemoveDownloadsWithoutClient(t *testing.T) {
	t.Run("grabs in the client", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		id := seedSeries(t, h.store, "Placeholder Saga", 1)
		seedGrab(t, h.store, id, 1, "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111", "grabbed")

		code := do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/titles/%d?remove_downloads=true", id), nil, nil)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("delete status = %d, want 503", code)
		}
		if n := countRows(t, h.store, `SELECT COUNT(*) FROM series WHERE id = ?`, id); n != 1 {
			t.Errorf("series was deleted despite the failed removal")
		}
	})
	t.Run("nothing to remove", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		id := seedSeries(t, h.store, "Placeholder Saga", 1)
		seedGrab(t, h.store, id, 1, "dddd4444dddd4444dddd4444dddd4444dddd4444", "failed")

		code := do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/titles/%d?remove_downloads=true", id), nil, nil)
		if code != http.StatusNoContent {
			t.Fatalf("delete status = %d, want 204 — no hashes means no client needed", code)
		}
	})
}

// Remove-first ordering: a client that refuses the removal leaves the series
// intact and the whole delete retryable, rather than orphaning its torrents.
func TestDeleteSeriesClientFailureLeavesTheSeriesIntact(t *testing.T) {
	dl := &coretest.FakeDownload{RemoveErr: errors.New("qbit: refused")}
	h := newHarness(t, nil, dl)
	id := seedSeries(t, h.store, "Placeholder Saga", 1)
	seedGrab(t, h.store, id, 1, "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111", "grabbed")

	code := do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/titles/%d?remove_downloads=true", id), nil, nil)
	if code != http.StatusBadGateway {
		t.Fatalf("delete status = %d, want 502", code)
	}
	if n := countRows(t, h.store, `SELECT COUNT(*) FROM series WHERE id = ?`, id); n != 1 {
		t.Errorf("series was deleted despite the failed removal")
	}
}
