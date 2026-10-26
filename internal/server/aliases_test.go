package server_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

type aliasesEcho struct {
	TitleID int64    `json:"title_id"`
	Aliases []string `json:"aliases"`
}

type aliasesDetail struct {
	Aliases *[]string `json:"aliases"`
}

func TestSetTitleAliases_RoundTripsThroughDetail(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Starlight Knight (2026)", 3)
	detailPath := fmt.Sprintf("/api/v1/titles/%d", titleID)
	path := detailPath + "/aliases"

	var fresh aliasesDetail
	if code := do(t, h, "GET", detailPath, nil, &fresh); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if fresh.Aliases == nil || len(*fresh.Aliases) != 0 {
		t.Errorf("a new title's aliases = %v, want present and empty", fresh.Aliases)
	}

	var set aliasesEcho
	if code := do(t, h, "PUT", path, map[string]any{"aliases": []string{" Hoshi  no Kishi 2026 "}}, &set); code != http.StatusOK {
		t.Fatalf("set status = %d, want 200", code)
	}
	want := []string{"Hoshi no Kishi 2026"}
	if set.TitleID != titleID || !slices.Equal(set.Aliases, want) {
		t.Errorf("echo = %+v, want the title id and %q as stored", set, want)
	}
	var detail aliasesDetail
	if code := do(t, h, "GET", detailPath, nil, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", code)
	}
	if detail.Aliases == nil || !slices.Equal(*detail.Aliases, want) {
		t.Errorf("detail aliases = %v, want %q", detail.Aliases, want)
	}

	if code := do(t, h, "PUT", path, map[string]any{"aliases": []string{}}, nil); code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200", code)
	}
	var cleared aliasesDetail
	do(t, h, "GET", detailPath, nil, &cleared)
	if cleared.Aliases == nil || len(*cleared.Aliases) != 0 {
		t.Errorf("aliases after clearing = %v, want present and empty", cleared.Aliases)
	}
}

func TestSetTitleAliases_422s(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Starlight Knight (2026)", 3)
	path := fmt.Sprintf("/api/v1/titles/%d/aliases", titleID)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"omitted list", map[string]any{}, "aliases"},
		{"blank alias", map[string]any{"aliases": []string{"  "}}, "blank"},
		{"no Latin letter or digit", map[string]any{"aliases": []string{"魔法騎士"}}, "Latin letters or digits"},
		{"six aliases", map[string]any{"aliases": []string{"A one", "A two", "A three", "A four", "A five", "A six"}}, ""},
	}
	for _, tc := range cases {
		var problem struct {
			Detail string `json:"detail"`
			Errors []struct {
				Message  string `json:"message"`
				Location string `json:"location"`
			} `json:"errors"`
		}
		code := do(t, h, "PUT", path, tc.body, &problem)
		if code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", tc.name, code)
			continue
		}
		parts := []string{problem.Detail}
		for _, e := range problem.Errors {
			parts = append(parts, e.Location, e.Message)
		}
		if text := strings.Join(parts, " "); !strings.Contains(text, tc.want) {
			t.Errorf("%s: problem %q doesn't mention %q", tc.name, text, tc.want)
		}
	}
}

func TestSetTitleAliases_UnknownTitle404(t *testing.T) {
	h := newHarness(t, nil, nil)
	code := do(t, h, "PUT", "/api/v1/titles/999/aliases", map[string]any{"aliases": []string{"Hoshi no Kishi 2026"}}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

// The issue's manual-grab criterion: refused as unmatched before the alias, taken
// after it, with nothing else changed.
func TestGrab_ReleaseUnderAliasReturns201(t *testing.T) {
	const url = "magnet:?xt=urn:btih:00000000000000000000000000000000000a1a03"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[Grp] Hoshi no Kishi 2026 - 03 (1080p) [ABCD1234].mkv", DownloadURL: url, Seeders: 10},
	}}
	h := newHarness(t, idx, &coretest.FakeDownload{})
	titleID := seedTitle(t, h.store, "Starlight Knight (2026)", 12)
	grab := fmt.Sprintf("/api/v1/titles/%d/grab", titleID)

	if code := h.postJSON(t, grab, map[string]any{"download_url": url}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("grab before the alias = %d, want 422", code)
	}
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/titles/%d/aliases", titleID),
		map[string]any{"aliases": []string{"Hoshi no Kishi 2026"}}, nil); code != http.StatusOK {
		t.Fatalf("set aliases = %d, want 200", code)
	}
	if code := h.postJSON(t, grab, map[string]any{"download_url": url}, nil); code != http.StatusCreated {
		t.Fatalf("grab after the alias = %d, want 201", code)
	}
}
