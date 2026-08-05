package server_test

import (
	"net/http"
	"testing"
)

type automationJSON struct {
	Mode          string `json:"mode"`
	PinDelayHours int    `json:"pin_delay_hours"`
}

type indexerJSON struct {
	URL        string `json:"url"`
	Categories string `json:"categories"`
}

type settingsJSON struct {
	Automation automationJSON `json:"automation"`
	Indexer    indexerJSON    `json:"indexer"`
}

func getAutomation(t *testing.T, h *harness) automationJSON {
	t.Helper()
	var out settingsJSON
	if code := do(t, h, http.MethodGet, "/api/v1/settings", nil, &out); code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", code)
	}
	return out.Automation
}

// The Settings UI renders the toggle from the snapshot and saves through this
// route, so the round trip is the contract: what a save reports is what the next
// read returns.
func TestAutomationSettingsRoundTrip(t *testing.T) {
	h := newHarness(t, nil, nil)

	if got := getAutomation(t, h); got.Mode != "off" {
		t.Errorf("automation = %+v on a fresh install, want off until configured", got)
	}

	for _, mode := range []string{"on", "notify_only", "off"} {
		var saved settingsJSON
		code := do(t, h, http.MethodPut, "/api/v1/settings/automation",
			map[string]any{"mode": mode, "pin_delay_hours": 6}, &saved)
		if code != http.StatusOK {
			t.Fatalf("PUT /settings/automation mode=%s = %d, want 200", mode, code)
		}
		if want := (automationJSON{Mode: mode, PinDelayHours: 6}); saved.Automation != want {
			t.Errorf("save returned %+v, want %+v", saved.Automation, want)
		}
		if got := getAutomation(t, h); got != (automationJSON{Mode: mode, PinDelayHours: 6}) {
			t.Errorf("automation after save = %+v, want mode %s with a 6h pin delay", got, mode)
		}
	}
}

// The mode is a closed enum: a typo'd client value must not silently become off.
func TestAutomationSettingsRejectsUnknownMode(t *testing.T) {
	h := newHarness(t, nil, nil)
	code := do(t, h, http.MethodPut, "/api/v1/settings/automation",
		map[string]any{"mode": "loud", "pin_delay_hours": 0}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /settings/automation with an unknown mode = %d, want 422", code)
	}
}

// An hour count this size wraps int64 into a negative duration when multiplied
// out, so the write path clamps rather than trusting the client.
func TestAutomationSettingsClampsPinDelay(t *testing.T) {
	h := newHarness(t, nil, nil)

	for _, tc := range []struct{ send, want int }{
		{-3, 0},
		{3_000_000, 24 * 365},
	} {
		var saved settingsJSON
		if code := do(t, h, http.MethodPut, "/api/v1/settings/automation",
			map[string]any{"mode": "on", "pin_delay_hours": tc.send}, &saved); code != http.StatusOK {
			t.Fatalf("PUT /settings/automation with %d = %d, want 200", tc.send, code)
		}
		if saved.Automation.PinDelayHours != tc.want {
			t.Errorf("pin_delay_hours %d saved as %d, want %d",
				tc.send, saved.Automation.PinDelayHours, tc.want)
		}
	}
}

// The category filter (#142) round-trips through the same snapshot the UI
// renders, and a non-numeric id is a client error rather than a 500.
func TestIndexerCategoriesRoundTrip(t *testing.T) {
	h := newHarness(t, nil, nil)

	var saved settingsJSON
	code := do(t, h, http.MethodPut, "/api/v1/settings/indexer",
		map[string]any{"url": "http://prowlarr:9696/1/api", "categories": " 5070, 127720 "}, &saved)
	if code != http.StatusOK {
		t.Fatalf("PUT /settings/indexer = %d, want 200", code)
	}
	if saved.Indexer.Categories != "5070,127720" {
		t.Errorf("save returned categories %q, want the normalized 5070,127720", saved.Indexer.Categories)
	}

	var got settingsJSON
	if code := do(t, h, http.MethodGet, "/api/v1/settings", nil, &got); code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", code)
	}
	if got.Indexer.Categories != "5070,127720" {
		t.Errorf("categories after save = %q, want 5070,127720", got.Indexer.Categories)
	}

	code = do(t, h, http.MethodPut, "/api/v1/settings/indexer",
		map[string]any{"url": "http://prowlarr:9696/1/api", "categories": "anime"}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /settings/indexer with a non-numeric category = %d, want 422", code)
	}
}
