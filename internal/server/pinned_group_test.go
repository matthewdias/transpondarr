package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestPinnedGroupAssignmentRoundTrip(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)

	var set struct {
		SeriesID    int64  `json:"series_id"`
		PinnedGroup string `json:"pinned_group"`
	}
	code := do(t, h, "PUT", fmt.Sprintf("/api/v1/series/%d/pinned-group", seriesID),
		map[string]any{"group": "ShinyRip"}, &set)
	if code != http.StatusOK {
		t.Fatalf("pin status = %d, want 200", code)
	}
	if set.SeriesID != seriesID || set.PinnedGroup != "ShinyRip" {
		t.Errorf("echo = %+v, want the series id and pinned group back", set)
	}

	var detail struct {
		PinnedGroup string `json:"pinned_group"`
	}
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/series/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinnedGroup != "ShinyRip" {
		t.Errorf("detail pinned_group = %q, want ShinyRip", detail.PinnedGroup)
	}

	// A whitespace-only group clears the pin.
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/series/%d/pinned-group", seriesID),
		map[string]any{"group": "  "}, nil); code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200", code)
	}
	detail.PinnedGroup = "sentinel"
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/series/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinnedGroup != "sentinel" {
		t.Errorf("detail pinned_group = %q, want the field omitted after clearing", detail.PinnedGroup)
	}
}

func TestPinUnknownSeriesRejected(t *testing.T) {
	h := newHarness(t, nil, nil)
	code := do(t, h, "PUT", "/api/v1/series/999/pinned-group",
		map[string]any{"group": "ShinyRip"}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("pin unknown series status = %d, want 404", code)
	}
}
