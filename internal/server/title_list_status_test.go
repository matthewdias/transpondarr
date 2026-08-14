package server_test

import (
	"net/http"
	"testing"
)

type listedTitleJSON struct {
	ID          int64  `json:"id"`
	Format      string `json:"format"`
	ItemStatus  string `json:"item_status"`
	ImportError string `json:"import_error"`
}

func listedTitles(t *testing.T, h *harness) map[int64]listedTitleJSON {
	t.Helper()
	var out struct {
		Titles []listedTitleJSON `json:"titles"`
	}
	if code := h.get(t, "/api/v1/titles", &out); code != http.StatusOK {
		t.Fatalf("GET /titles = %d, want 200", code)
	}
	got := make(map[int64]listedTitleJSON, len(out.Titles))
	for _, s := range out.Titles {
		got[s.ID] = s
	}
	return got
}

// The defect (#229): a film downloading, deferred or import-blocked had only
// in_library and monitored to read from, so every one of them said "Wanted".
func TestTitleListCarriesAFilmsItemState(t *testing.T) {
	h := newHarness(t, nil, nil)

	wanted := seedMovie(t, h.store, "Placeholder Wanted", 2019)

	held := seedMovie(t, h.store, "Placeholder Held", 2020)
	holdItem(t, h.store, held, 1, "[SynthSubs] Placeholder Held (2020) [1080p]")

	downloading := seedMovie(t, h.store, "Placeholder Downloading", 2021)
	grabItem(t, h.store, downloading, 1, "grabbed", "")

	deferred := seedMovie(t, h.store, "Placeholder Deferred", 2022)
	grabItem(t, h.store, deferred, 1, "import_deferred", "")

	stuck := seedMovie(t, h.store, "Placeholder Stuck", 2023)
	grabItem(t, h.store, stuck, 1, "grabbed", "no movies root configured")

	got := listedTitles(t, h)
	for _, tc := range []struct {
		name   string
		id     int64
		status string
	}{
		{"wanted", wanted, "wanted"},
		{"held", held, "in_library"},
		{"downloading", downloading, "downloading"},
		{"deferred", deferred, "deferred"},
		{"stuck", stuck, "stuck"},
	} {
		if got[tc.id].ItemStatus != tc.status {
			t.Errorf("%s film item_status = %q, want %q", tc.name, got[tc.id].ItemStatus, tc.status)
		}
	}
	if got[stuck].ImportError != "no movies root configured" {
		t.Errorf("stuck film import_error = %q, want the stored reason", got[stuck].ImportError)
	}
}

// A failed grab reverts the item to wanted (deriveItemState's rule), and the
// list must not diverge from the detail page about it.
func TestTitleListReadsAFailedGrabAsWanted(t *testing.T) {
	h := newHarness(t, nil, nil)
	movieID := seedMovie(t, h.store, "Placeholder Failed", 2019)
	grabItem(t, h.store, movieID, 1, "failed", "")

	if got := listedTitles(t, h)[movieID].ItemStatus; got != "wanted" {
		t.Errorf("failed-grab film item_status = %q, want wanted", got)
	}
}

// The state is per item and the row is per title, so it is published only where
// format guarantees the two are the same thing (#208) -- absent, not "wanted",
// for a series whose progress column is the count it always was.
func TestTitleListOmitsItemStatusForASeries(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 12)
	grabItem(t, h.store, titleID, 1, "grabbed", "")

	var raw struct {
		Titles []map[string]any `json:"titles"`
	}
	if code := h.get(t, "/api/v1/titles", &raw); code != http.StatusOK {
		t.Fatalf("GET /titles = %d, want 200", code)
	}
	if len(raw.Titles) != 1 {
		t.Fatalf("titles = %+v, want the one series", raw.Titles)
	}
	if v, present := raw.Titles[0]["item_status"]; present {
		t.Errorf("series item_status present as %v, want it omitted (title %d)", v, titleID)
	}
}
