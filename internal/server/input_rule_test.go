package server_test

import (
	"encoding/json"
	"fmt"
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

// The same rule on the quality-profile body, which is one body for create and
// update (#227). Omission-means-default cannot be the create idiom here: the
// create statement writes every column explicitly, so a body that never
// mentioned resolution_order or upgrade_v2_above_cutoff overwrites the schema's
// own default rather than taking it. On update it is the settings case exactly —
// a save that never mentioned upgrades switching them off.
func TestProfileInputRequiresEveryDefaultedField(t *testing.T) {
	h := newHarness(t, nil, nil)
	body := profileInput("Trusted", map[string]any{
		"resolution_order":  []string{"1080p", "720p"},
		"preferred_source":  "web",
		"sub_pref":          "softsub",
		"prefer_dual_audio": true,
		"codec_pref":        "h265",
		"hard_excludes":     []string{"hardsub"},
		"min_score":         100,
		"groups": []profileGroupJSON{
			{Name: "FirstChoice"}, {Name: "BadRipCo", Blocked: true},
		},
		"upgrades_enabled":        true,
		"cutoff_score":            250,
		"upgrade_v2_above_cutoff": true,
	})
	required := []string{
		"name", "resolution_order", "prefer_dual_audio", "min_score",
		"upgrades_enabled", "cutoff_score", "upgrade_v2_above_cutoff",
	}
	// Empty is the value itself for each of these: no preference, no excludes,
	// no ranked groups.
	optional := []string{"preferred_source", "sub_pref", "codec_pref", "hard_excludes", "groups"}

	var created profileJSON
	if code := do(t, h, http.MethodPost, "/api/v1/profiles", body, &created); code != http.StatusCreated {
		t.Fatalf("POST /profiles with a whole body = %d, want 201", code)
	}
	path := fmt.Sprintf("/api/v1/profiles/%d", created.ID)

	for _, field := range required {
		short := without(t, body, field)
		if field != "name" {
			short["name"] = "Another " + field // a 409 would mask the 422 under test
		}
		if code := do(t, h, http.MethodPost, "/api/v1/profiles", short, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("POST /profiles omitting %s = %d, want 422", field, code)
		}
		if code := do(t, h, http.MethodPut, path, without(t, body, field), nil); code != http.StatusUnprocessableEntity {
			t.Errorf("PUT %s omitting %s = %d, want 422", path, field, code)
		}
		if got := profileByID(t, h, created.ID); !reflect.DeepEqual(got, created) {
			t.Errorf("PUT omitting %s changed the stored profile to %+v, want %+v", field, got, created)
		}
	}

	for _, field := range optional {
		if code := do(t, h, http.MethodPut, path, without(t, body, field), nil); code != http.StatusOK {
			t.Errorf("PUT %s omitting the optional %s = %d, want 200", path, field, code)
		}
		if code := do(t, h, http.MethodPut, path, body, nil); code != http.StatusOK {
			t.Fatalf("restoring the profile after omitting %s = %d, want 200", field, code)
		}
	}

	// A group row's own switch takes the rule too: under omitempty no client
	// could state "not blocked" — this test's own body type could not, until it
	// was fixed with the DTO.
	unstated := profileInput("Trusted", map[string]any{
		"groups": []map[string]any{{"name": "FirstChoice"}},
	})
	if code := do(t, h, http.MethodPut, path, unstated, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("PUT %s with a group row omitting blocked = %d, want 422", path, code)
	}
}

func profileByID(t *testing.T, h *harness, id int64) profileJSON {
	t.Helper()
	var list struct {
		Profiles []profileJSON `json:"profiles"`
	}
	if code := do(t, h, http.MethodGet, "/api/v1/profiles", nil, &list); code != http.StatusOK {
		t.Fatalf("GET /profiles = %d, want 200", code)
	}
	for _, p := range list.Profiles {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("profile %d is gone from the listing", id)
	return profileJSON{}
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
