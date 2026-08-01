package server_test

import (
	"net/http"
	"testing"
)

type automationJSON struct {
	Enabled       bool `json:"enabled"`
	PinDelayHours int  `json:"pin_delay_hours"`
}

type settingsJSON struct {
	Automation automationJSON `json:"automation"`
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

	if got := getAutomation(t, h); got.Enabled {
		t.Errorf("automation = %+v on a fresh install, want off until configured", got)
	}

	var saved settingsJSON
	code := do(t, h, http.MethodPut, "/api/v1/settings/automation",
		map[string]any{"enabled": true, "pin_delay_hours": 6}, &saved)
	if code != http.StatusOK {
		t.Fatalf("PUT /settings/automation = %d, want 200", code)
	}
	if want := (automationJSON{Enabled: true, PinDelayHours: 6}); saved.Automation != want {
		t.Errorf("save returned %+v, want %+v", saved.Automation, want)
	}
	if got := getAutomation(t, h); got != (automationJSON{Enabled: true, PinDelayHours: 6}) {
		t.Errorf("automation after save = %+v, want it enabled with a 6h pin delay", got)
	}

	if code := do(t, h, http.MethodPut, "/api/v1/settings/automation",
		map[string]any{"enabled": false, "pin_delay_hours": 6}, &saved); code != http.StatusOK {
		t.Fatalf("PUT /settings/automation (disable) = %d, want 200", code)
	}
	if got := getAutomation(t, h); got.Enabled {
		t.Error("automation still enabled after being turned off")
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
			map[string]any{"enabled": true, "pin_delay_hours": tc.send}, &saved); code != http.StatusOK {
			t.Fatalf("PUT /settings/automation with %d = %d, want 200", tc.send, code)
		}
		if saved.Automation.PinDelayHours != tc.want {
			t.Errorf("pin_delay_hours %d saved as %d, want %d",
				tc.send, saved.Automation.PinDelayHours, tc.want)
		}
	}
}
