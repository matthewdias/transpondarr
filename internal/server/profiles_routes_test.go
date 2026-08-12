package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

type profileGroupJSON struct {
	Name    string `json:"name"`
	Blocked bool   `json:"blocked,omitempty"`
}

type profileJSON struct {
	ID              int64              `json:"id"`
	Name            string             `json:"name"`
	IsDefault       bool               `json:"is_default"`
	ResolutionOrder []string           `json:"resolution_order"`
	PreferredSource string             `json:"preferred_source"`
	SubPref         string             `json:"sub_pref"`
	PreferDualAudio bool               `json:"prefer_dual_audio"`
	CodecPref       string             `json:"codec_pref"`
	HardExcludes    []string           `json:"hard_excludes"`
	MinScore        int                `json:"min_score"`
	Groups          []profileGroupJSON `json:"groups"`
	SeriesCount     int64              `json:"series_count"`
}

// do issues a request with an arbitrary method, mirroring harness.postJSON.
func do(t *testing.T, h *harness, method, path string, body, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, rd)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	decodeBody(t, resp.Body, out)
	return resp.StatusCode
}

func TestProfileCRUDAndSeriesAssignment(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)

	// --- create ---------------------------------------------------------------
	in := map[string]any{
		"name":              "Trusted",
		"resolution_order":  []string{"1080p", "720p"},
		"preferred_source":  "web",
		"sub_pref":          "softsub",
		"prefer_dual_audio": true,
		"codec_pref":        "h265",
		"hard_excludes":     []string{"hardsub"},
		"min_score":         100,
		"groups": []profileGroupJSON{
			{Name: "FirstChoice"},
			{Name: "SecondChoice"},
			{Name: "BadRipCo", Blocked: true},
		},
	}
	var created profileJSON
	if code := do(t, h, "POST", "/api/v1/profiles", in, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", code)
	}
	if created.ID == 0 || created.Name != "Trusted" || created.MinScore != 100 {
		t.Fatalf("created = %+v, want echoed fields with an id", created)
	}
	wantGroups := []profileGroupJSON{
		{Name: "FirstChoice"}, {Name: "SecondChoice"}, {Name: "BadRipCo", Blocked: true},
	}
	if fmt.Sprint(created.Groups) != fmt.Sprint(wantGroups) {
		t.Errorf("groups = %+v, want %+v in rank order", created.Groups, wantGroups)
	}

	// --- list: seeded Default plus the new profile ----------------------------
	var list struct {
		Profiles []profileJSON `json:"profiles"`
	}
	if code := do(t, h, "GET", "/api/v1/profiles", nil, &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if len(list.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2 (Default + Trusted)", len(list.Profiles))
	}
	if !list.Profiles[0].IsDefault {
		t.Errorf("first listed profile should be the default")
	}

	// --- update: rename and reorder; a mid-list blocked row reads back last ---
	in["name"] = "Trusted v2"
	in["groups"] = []profileGroupJSON{
		{Name: "SecondChoice"},
		{Name: "BadRipCo", Blocked: true},
		{Name: "FirstChoice"},
	}
	var updated profileJSON
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/profiles/%d", created.ID), in, &updated); code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", code)
	}
	if updated.Name != "Trusted v2" || updated.Groups[0].Name != "SecondChoice" {
		t.Errorf("updated = %+v, want renamed with SecondChoice first", updated)
	}
	wantUpdated := []profileGroupJSON{
		{Name: "SecondChoice"}, {Name: "FirstChoice"}, {Name: "BadRipCo", Blocked: true},
	}
	if fmt.Sprint(updated.Groups) != fmt.Sprint(wantUpdated) {
		t.Errorf("groups = %+v, want blocked rows sorted last", updated.Groups)
	}

	// --- assign to a series ---------------------------------------------------
	var assigned struct {
		SeriesID  int64 `json:"series_id"`
		ProfileID int64 `json:"profile_id"`
	}
	code := do(t, h, "PUT", fmt.Sprintf("/api/v1/series/%d/profile", seriesID),
		map[string]any{"profile_id": created.ID}, &assigned)
	if code != http.StatusOK {
		t.Fatalf("assign status = %d, want 200", code)
	}
	var detail struct {
		QualityProfileID int64 `json:"quality_profile_id"`
	}
	if code := do(t, h, "GET", fmt.Sprintf("/api/v1/series/%d", seriesID), nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.QualityProfileID != created.ID {
		t.Errorf("series quality_profile_id = %d, want %d", detail.QualityProfileID, created.ID)
	}

	// --- delete while in use: refused with the conflict explained -------------
	var errOut struct {
		Detail string `json:"detail"`
	}
	if code := do(t, h, "DELETE", fmt.Sprintf("/api/v1/profiles/%d", created.ID), nil, &errOut); code != http.StatusConflict {
		t.Fatalf("in-use delete status = %d, want 409", code)
	}
	if errOut.Detail == "" {
		t.Error("in-use delete should explain the conflict")
	}

	// --- delete with a migration target: series move, profile goes ------------
	if code := do(t, h, "DELETE", fmt.Sprintf("/api/v1/profiles/%d?reassign_to=1", created.ID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("reassigning delete status = %d, want 204", code)
	}
	series, err := h.store.Q.GetSeries(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("get series: %v", err)
	}
	if series.QualityProfileID != 1 {
		t.Errorf("series profile after delete = %d, want default 1", series.QualityProfileID)
	}
	if code := do(t, h, "GET", "/api/v1/profiles", nil, &list); code != http.StatusOK || len(list.Profiles) != 1 {
		t.Fatalf("profiles after delete = %d (status %d), want just Default", len(list.Profiles), code)
	}
}

func TestDeleteDefaultProfileRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code := do(t, h, "DELETE", "/api/v1/profiles/1", nil, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("delete default status = %d, want 422", code)
	}
}

func TestAssignUnknownProfileRejected(t *testing.T) {
	h := newHarness(t, nil, nil)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	code := do(t, h, "PUT", fmt.Sprintf("/api/v1/series/%d/profile", seriesID),
		map[string]any{"profile_id": 999}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("assign unknown profile status = %d, want 422", code)
	}
}

func TestCreateProfileValidation(t *testing.T) {
	h := newHarness(t, nil, nil)

	// An axis value outside the parser's vocabulary is rejected.
	code := do(t, h, "POST", "/api/v1/profiles",
		map[string]any{"name": "Bad", "preferred_source": "vhs"}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad source status = %d, want 422", code)
	}

	// A whitespace-only name passes minLength but trims to nothing.
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "   "}, nil); c != http.StatusUnprocessableEntity {
		t.Fatalf("blank name status = %d, want 422", c)
	}

	// Duplicate names conflict rather than 500.
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Dup"}, nil); c != http.StatusCreated {
		t.Fatalf("first create = %d, want 201", c)
	}
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Dup"}, nil); c != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", c)
	}
}

func TestCreateProfileRejectsCaseVariantName(t *testing.T) {
	h := newHarness(t, nil, nil)

	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Dup"}, nil); c != http.StatusCreated {
		t.Fatalf("first create = %d, want 201", c)
	}
	for _, name := range []string{"dup", "  DUP  ", "default"} {
		if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": name}, nil); c != http.StatusConflict {
			t.Errorf("create %q = %d, want 409", name, c)
		}
	}

	var list struct {
		Profiles []profileJSON `json:"profiles"`
	}
	if c := do(t, h, "GET", "/api/v1/profiles", nil, &list); c != http.StatusOK {
		t.Fatalf("list = %d, want 200", c)
	}
	if len(list.Profiles) != 2 {
		t.Fatalf("profiles = %d, want the seeded Default plus Dup", len(list.Profiles))
	}
}

func TestUpdateProfileRejectsNameTakenByAnother(t *testing.T) {
	h := newHarness(t, nil, nil)

	var alpha, beta profileJSON
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Alpha"}, &alpha); c != http.StatusCreated {
		t.Fatalf("create Alpha = %d, want 201", c)
	}
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Beta"}, &beta); c != http.StatusCreated {
		t.Fatalf("create Beta = %d, want 201", c)
	}

	path := fmt.Sprintf("/api/v1/profiles/%d", beta.ID)
	for _, name := range []string{"alpha", "Alpha"} {
		if c := do(t, h, "PUT", path, map[string]any{"name": name}, nil); c != http.StatusConflict {
			t.Errorf("rename Beta to %q = %d, want 409", name, c)
		}
	}

	// A refused rename must have written nothing.
	var list struct {
		Profiles []profileJSON `json:"profiles"`
	}
	if c := do(t, h, "GET", "/api/v1/profiles", nil, &list); c != http.StatusOK {
		t.Fatalf("list = %d, want 200", c)
	}
	stored := ""
	for _, p := range list.Profiles {
		if p.ID == beta.ID {
			stored = p.Name
		}
	}
	if stored != "Beta" {
		t.Errorf("profile %d is stored as %q, want Beta", beta.ID, stored)
	}
}

func TestUpdateProfileKeepsItsOwnName(t *testing.T) {
	h := newHarness(t, nil, nil)

	var created, updated profileJSON
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Alpha"}, &created); c != http.StatusCreated {
		t.Fatalf("create = %d, want 201", c)
	}
	path := fmt.Sprintf("/api/v1/profiles/%d", created.ID)
	c := do(t, h, "PUT", path, map[string]any{"name": "Alpha", "min_score": 50}, &updated)
	if c != http.StatusOK {
		t.Fatalf("update keeping its own name = %d, want 200", c)
	}
	if updated.MinScore != 50 {
		t.Errorf("min_score = %d, want 50", updated.MinScore)
	}
}

func TestUpdateProfileChangesOnlyItsOwnNameCase(t *testing.T) {
	h := newHarness(t, nil, nil)

	var created, updated profileJSON
	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Alpha"}, &created); c != http.StatusCreated {
		t.Fatalf("create = %d, want 201", c)
	}
	path := fmt.Sprintf("/api/v1/profiles/%d", created.ID)
	if c := do(t, h, "PUT", path, map[string]any{"name": "ALPHA"}, &updated); c != http.StatusOK {
		t.Fatalf("recasing its own name = %d, want 200", c)
	}
	if updated.Name != "ALPHA" {
		t.Errorf("name = %q, want ALPHA", updated.Name)
	}
}

// An unknown id outranks a name clash, and a bad body outranks both.
func TestUpdateUnknownProfileIsNotFound(t *testing.T) {
	h := newHarness(t, nil, nil)

	if c := do(t, h, "POST", "/api/v1/profiles", map[string]any{"name": "Alpha"}, nil); c != http.StatusCreated {
		t.Fatalf("create = %d, want 201", c)
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"taken name", map[string]any{"name": "Alpha"}, http.StatusNotFound},
		{"free name", map[string]any{"name": "Gamma"}, http.StatusNotFound},
		{"bad axis", map[string]any{"name": "Alpha", "preferred_source": "vhs"}, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		if c := do(t, h, "PUT", "/api/v1/profiles/9999", tc.body, nil); c != tc.want {
			t.Errorf("update unknown id with %s = %d, want %d", tc.name, c, tc.want)
		}
	}
}

// A pre-#90 install can hold Anime and anime, which nothing dedupes; the newer
// row must stay savable rather than answering 409 to its own name.
func TestUpdateProfileWithPreExistingCaseVariantName(t *testing.T) {
	h := newHarness(t, nil, nil)
	ctx := context.Background()

	seed := func(name string) int64 {
		t.Helper()
		p, err := h.store.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
			Name: name, ResolutionOrder: `[]`, HardExcludes: `[]`,
		})
		if err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
		return p.ID
	}
	seed("Anime")
	second := fmt.Sprintf("/api/v1/profiles/%d", seed("anime"))

	var got profileJSON
	if c := do(t, h, "PUT", second, map[string]any{"name": "anime", "min_score": 25}, &got); c != http.StatusOK {
		t.Fatalf("saving the second row under its own name = %d, want 200", c)
	}
	if got.Name != "anime" || got.MinScore != 25 {
		t.Errorf("profile = %q/%d, want anime/25", got.Name, got.MinScore)
	}
	if c := do(t, h, "PUT", second, map[string]any{"name": "ANIME"}, &got); c != http.StatusOK {
		t.Errorf("recasing its own name = %d, want 200", c)
	}

	// Renaming it onto the other row's exact name is still refused, by the
	// binary unique constraint rather than by the pre-check.
	if c := do(t, h, "PUT", second, map[string]any{"name": "Anime"}, nil); c != http.StatusConflict {
		t.Errorf("collapsing the pair = %d, want 409", c)
	}
}
