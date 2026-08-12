package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

func TestPinnedGroupAssignmentRoundTrip(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)

	var set struct {
		TitleID     int64  `json:"title_id"`
		PinnedGroup string `json:"pinned_group"`
	}
	code := do(t, h, "PUT", fmt.Sprintf("/api/v1/titles/%d/pinned-group", seriesID),
		map[string]any{"group": "ShinyRip"}, &set)
	if code != http.StatusOK {
		t.Fatalf("pin status = %d, want 200", code)
	}
	if set.TitleID != seriesID || set.PinnedGroup != "ShinyRip" {
		t.Errorf("echo = %+v, want the series id and pinned group back", set)
	}

	var detail struct {
		PinnedGroup string `json:"pinned_group"`
	}
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/titles/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinnedGroup != "ShinyRip" {
		t.Errorf("detail pinned_group = %q, want ShinyRip", detail.PinnedGroup)
	}

	// A whitespace-only group clears the pin.
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/titles/%d/pinned-group", seriesID),
		map[string]any{"group": "  "}, nil); code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200", code)
	}
	detail.PinnedGroup = "sentinel"
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/titles/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinnedGroup != "sentinel" {
		t.Errorf("detail pinned_group = %q, want the field omitted after clearing", detail.PinnedGroup)
	}
}

// The pin delay is PUT-replace like the group it belongs to: omitting it clears
// the override back to the global default, and clearing the group clears both.
func TestSetPinnedGroupStoresDelayAndPutReplaces(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	path := fmt.Sprintf("/api/v1/titles/%d/pinned-group", seriesID)

	if code := do(t, h, "PUT", path, map[string]any{"group": "ShinyRip", "delay_hours": 6}, nil); code != http.StatusOK {
		t.Fatalf("pin status = %d, want 200", code)
	}
	var detail struct {
		PinnedGroup   string `json:"pinned_group"`
		PinDelayHours *int   `json:"pin_delay_hours"`
	}
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/titles/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinDelayHours == nil || *detail.PinDelayHours != 6 {
		t.Fatalf("pin_delay_hours = %v, want 6", detail.PinDelayHours)
	}

	// Re-pinning without a delay drops the override.
	if code := do(t, h, "PUT", path, map[string]any{"group": "ShinyRip"}, nil); code != http.StatusOK {
		t.Fatalf("re-pin status = %d, want 200", code)
	}
	detail.PinDelayHours = nil
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/titles/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinDelayHours != nil {
		t.Errorf("pin_delay_hours = %v, want absent after a PUT that omitted it", *detail.PinDelayHours)
	}

	// A zero delay is a real value, not an absent one.
	if code := do(t, h, "PUT", path, map[string]any{"group": "ShinyRip", "delay_hours": 0}, nil); code != http.StatusOK {
		t.Fatalf("zero-delay status = %d, want 200", code)
	}
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/titles/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinDelayHours == nil || *detail.PinDelayHours != 0 {
		t.Fatalf("pin_delay_hours = %v, want an explicit 0", detail.PinDelayHours)
	}

	// Clearing the group clears the delay with it: a delay with nothing to wait
	// for is meaningless.
	if code := do(t, h, "PUT", path, map[string]any{"group": ""}, nil); code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200", code)
	}
	// Both fields are omitempty, so reset them: a stale value would read as absent.
	detail.PinnedGroup, detail.PinDelayHours = "", nil
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/titles/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.PinnedGroup != "" || detail.PinDelayHours != nil {
		t.Errorf("after clearing: group %q delay %v, want both gone", detail.PinnedGroup, detail.PinDelayHours)
	}
}

// A delay past the duration ceiling wraps int64 downstream and silently becomes
// no delay, so the bound is enforced at the edge rather than papered over later.
func TestSetPinnedGroupRejectsAnOutOfRangeDelay(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	path := fmt.Sprintf("/api/v1/titles/%d/pinned-group", seriesID)

	for _, hours := range []int{-1, domain.MaxPinDelayHours + 1, 3000000} {
		body := map[string]any{"group": "ShinyRip", "delay_hours": hours}
		if code := do(t, h, "PUT", path, body, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("delay_hours %d status = %d, want 422", hours, code)
		}
	}
	// The ceiling itself is a legal value.
	body := map[string]any{"group": "ShinyRip", "delay_hours": domain.MaxPinDelayHours}
	if code := do(t, h, "PUT", path, body, nil); code != http.StatusOK {
		t.Errorf("delay_hours at the ceiling status = %d, want 200", code)
	}
}

func TestPinUnknownSeriesRejected(t *testing.T) {
	h := newHarness(t, nil, nil)
	code := do(t, h, "PUT", "/api/v1/titles/999/pinned-group",
		map[string]any{"group": "ShinyRip"}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("pin unknown series status = %d, want 404", code)
	}
}
