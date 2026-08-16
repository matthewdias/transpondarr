package server_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// The audit behind #227. A settings body is its section's whole state, so a
// field the service would fill in with a default is required and everything
// else — a secret, a switch-it-off string — is omitempty. The table is the rule
// made runnable: moving a field back to omitempty fails this unless someone
// also takes it out of the required list, which is the argument, in review,
// that the omission cannot select a value.
func TestSettingsInputsRequireEveryDefaultedField(t *testing.T) {
	for _, tc := range []struct {
		path     string
		body     map[string]any
		required []string
		optional []string
	}{
		{
			path: "/api/v1/settings/download",
			body: map[string]any{
				"url": "http://qbit:8080", "user": "admin", "password": "pw",
				"category": "anime", "stall_hours": 6,
			},
			required: []string{"category", "stall_hours"},
			optional: []string{"url", "user", "password"},
		},
		{
			path: "/api/v1/settings/indexer",
			body: map[string]any{
				"name": "prowlarr", "url": "http://prowlarr:9696/1/api",
				"apikey": "k", "categories": "5070",
			},
			required: []string{"name"},
			optional: []string{"url", "apikey", "categories"},
		},
		{
			path: "/api/v1/settings/library",
			body: map[string]any{
				"dir": "/media/Anime", "movies_dir": "/media/Films",
				"series_layout": "flat", "mode": "copy",
			},
			required: []string{"series_layout", "mode"},
			optional: []string{"dir", "movies_dir"},
		},
		{
			path:     "/api/v1/settings/automation",
			body:     map[string]any{"mode": "on", "pin_delay_hours": 6},
			required: []string{"mode", "pin_delay_hours"},
		},
		{
			path: "/api/v1/settings/notifications",
			body: notificationsBody("https://discord.example/api/webhooks/1/abc",
				"https://hook.example/x", "https://ntfy.example", "transpondarr", "tk_secret"),
			required: []string{"webhook", "ntfy.server", "discord.on_grabbed", "ntfy.on_stuck"},
			optional: []string{"discord.url", "ntfy.topic", "ntfy.token"},
		},
	} {
		t.Run(tc.path, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			if code := do(t, h, http.MethodPut, tc.path, tc.body, nil); code != http.StatusOK {
				t.Fatalf("PUT %s with a whole-section body = %d, want 200", tc.path, code)
			}
			stored := settingsSnapshot(t, h)

			for _, field := range tc.required {
				if code := do(t, h, http.MethodPut, tc.path, without(t, tc.body, field), nil); code != http.StatusUnprocessableEntity {
					t.Errorf("PUT %s omitting %s = %d, want 422", tc.path, field, code)
				}
				if got := settingsSnapshot(t, h); !reflect.DeepEqual(got, stored) {
					t.Errorf("PUT %s omitting %s changed the stored settings to %v", tc.path, field, got)
				}
			}

			for _, field := range tc.optional {
				if code := do(t, h, http.MethodPut, tc.path, without(t, tc.body, field), nil); code != http.StatusOK {
					t.Errorf("PUT %s omitting the optional %s = %d, want 200", tc.path, field, code)
				}
				if code := do(t, h, http.MethodPut, tc.path, tc.body, nil); code != http.StatusOK {
					t.Fatalf("restoring %s after omitting %s = %d, want 200", tc.path, field, code)
				}
			}
		})
	}
}

func settingsSnapshot(t *testing.T, h *harness) map[string]any {
	t.Helper()
	var out map[string]any
	if code := do(t, h, http.MethodGet, "/api/v1/settings", nil, &out); code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", code)
	}
	return out
}

// without deep-copies body with one field dropped, a dotted path reaching into a
// section (ntfy.server). A name not in the body is fatal rather than a no-op, or
// a typo'd field would make the omission case pass without omitting anything.
func without(t *testing.T, body map[string]any, path string) map[string]any {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var copied map[string]any
	if err := json.Unmarshal(buf, &copied); err != nil {
		t.Fatalf("copy body: %v", err)
	}
	keys := strings.Split(path, ".")
	m := copied
	for _, k := range keys[:len(keys)-1] {
		nested, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("%q: %q is not a section of the body", path, k)
		}
		m = nested
	}
	last := keys[len(keys)-1]
	if _, ok := m[last]; !ok {
		t.Fatalf("%q is not in the body to begin with", path)
	}
	delete(m, last)
	return copied
}
