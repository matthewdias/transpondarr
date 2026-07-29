package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/server"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// harness is a running server wired to fake clients, plus the store and registry
// it reads. The registry is exposed so a test can point the importer at the same
// clients the HTTP layer uses (it satisfies importer.ClientSource).
type harness struct {
	ts    *httptest.Server
	store *store.Store
	reg   *clients.Registry
	jobs  *jobs.Runner
	idx   *coretest.FakeIndexer
	dl    *coretest.FakeDownload
	lib   *coretest.FakeLibrary
}

// newHarness stands up server.New over a temp store with the given fake clients.
// Auth is in "local" mode, so the loopback httptest client is authorized without
// a login — leaving the pipeline (not the auth layer) as what the test exercises.
func newHarness(t *testing.T, idx *coretest.FakeIndexer, dl *coretest.FakeDownload) *harness {
	t.Helper()
	ctx := context.Background()
	st := coretest.NewStore(t)

	reg := clients.New()
	cfg := &config.Config{AuthRequired: auth.RequiredLocal}

	// settings.New populates the registry from (empty) config, so set the fakes
	// afterwards to override the nil clients it installs.
	settingsSvc, err := settings.New(ctx, st, cfg, reg)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	authSvc, err := auth.New(ctx, st, cfg)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	if idx != nil {
		reg.SetIndexer(idx)
	}
	if dl != nil {
		reg.SetDownload(dl)
	}
	lib := &coretest.FakeLibrary{}
	reg.SetLibrary(lib)

	runner := jobs.New(discardLogger())
	h := server.New(cfg, st, discardLogger(), testProvider(), reg, settingsSvc, authSvc, runner)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &harness{ts: ts, store: st, reg: reg, jobs: runner, idx: idx, dl: dl, lib: lib}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// stubProvider stands in for AniList. Every method errors: these tests seed
// series with no AniList id precisely so the provider is never reached, and a
// loud failure is what proves that still holds.
type stubProvider struct{}

func testProvider() metadata.Provider { return stubProvider{} }

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, errors.New("stub provider: unexpected metadata call")
}

func (stubProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return metadata.TitleMeta{}, nil, errors.New("stub provider: unexpected metadata call")
}

// seedSeries inserts a series (no AniList id, so the handler never calls out to
// the real metadata provider) with episodes 1..count as wanted items.
func seedSeries(t *testing.T, st *store.Store, title string, count int) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: title, Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for n := 1; n <= count; n++ {
		if _, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(n), Valid: true},
		}); err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
	}
	return s.ID
}

func (h *harness) get(t *testing.T, path string, out any) int {
	t.Helper()
	resp, err := h.ts.Client().Get(h.ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	decodeBody(t, resp.Body, out)
	return resp.StatusCode
}

func (h *harness) postJSON(t *testing.T, path string, body, out any) int {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := h.ts.Client().Post(h.ts.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	decodeBody(t, resp.Body, out)
	return resp.StatusCode
}

func decodeBody(t *testing.T, r io.Reader, out any) {
	t.Helper()
	if out == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	if err := json.NewDecoder(r).Decode(out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// candidateDTO mirrors the search endpoint's per-release result shape.
type candidateDTO struct {
	Title       string `json:"title"`
	DownloadURL string `json:"download_url"`
	Matched     bool   `json:"matched"`
	Items       []int  `json:"items"`
	Reason      string `json:"reason"`
	Pinned      bool   `json:"pinned"`
}

// TestSearchAndGrabPipeline drives the whole acquisition glue over HTTP: the
// indexer search feeds the decider, the matched release is grabbed via the
// download client, and a grab row is recorded against the covered wanted item.
func TestSearchAndGrabPipeline(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:0000000000000000000000000000000000000003"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		// Matches episode 3 of the series.
		{Title: "[ExampleSubs] Placeholder Saga S1E03 [1080p]", DownloadURL: matchURL, Seeders: 100},
		// A different show — must be filtered out by the decider.
		{Title: "[Group] Completely Different Show S1E01 [1080p]", DownloadURL: "magnet:?xt=urn:btih:ffff", Seeders: 999},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash3", Outcome: download.AddSuccess}}

	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	// --- search: the matched release is surfaced against item 3 ---------------
	var searchOut struct {
		Series  string         `json:"series"`
		Term    string         `json:"term"`
		Results []candidateDTO `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/search", seriesID), &searchOut); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}

	if len(idx.Queries) != 1 || idx.Queries[0].Term != "Placeholder Saga" {
		t.Errorf("indexer queried with %+v, want one search for %q", idx.Queries, "Placeholder Saga")
	}
	var matched *candidateDTO
	for i := range searchOut.Results {
		if searchOut.Results[i].Matched {
			matched = &searchOut.Results[i]
		}
	}
	if matched == nil {
		t.Fatalf("no matched candidate in results: %+v", searchOut.Results)
	}
	if len(matched.Items) != 1 || matched.Items[0] != 3 {
		t.Errorf("matched items = %v, want [3]", matched.Items)
	}
	if matched.DownloadURL != matchURL {
		t.Errorf("matched download_url = %q, want %q", matched.DownloadURL, matchURL)
	}

	// --- grab: the chosen release is handed to the download client ------------
	var grabOut struct {
		InfoHash string `json:"infohash"`
		Outcome  string `json:"outcome"`
		Release  string `json:"release"`
		Items    []int  `json:"items"`
	}
	code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, &grabOut)
	if code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}
	if grabOut.InfoHash != "hash3" || grabOut.Outcome != "success" {
		t.Errorf("grab out = %+v, want hash3/success", grabOut)
	}
	if len(grabOut.Items) != 1 || grabOut.Items[0] != 3 {
		t.Errorf("grabbed items = %v, want [3]", grabOut.Items)
	}

	// The download client saw exactly one Add for the chosen release, tagged with
	// the default category.
	if len(dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want 1", len(dl.Adds))
	}
	if dl.Adds[0].URL != matchURL {
		t.Errorf("Add URL = %q, want %q", dl.Adds[0].URL, matchURL)
	}
	if dl.Adds[0].Category != "transpondarr" {
		t.Errorf("Add category = %q, want default %q", dl.Adds[0].Category, "transpondarr")
	}

	// --- persistence: one grab recorded against item 3, status "grabbed" ------
	grabs, err := h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 {
		t.Fatalf("recorded %d grabs, want 1: %+v", len(grabs), grabs)
	}
	g := grabs[0]
	if g.InfoHash != "hash3" || g.Status != "grabbed" {
		t.Errorf("grab row = infohash %q status %q, want hash3/grabbed", g.InfoHash, g.Status)
	}
	// The grab must point at the item whose episode number is 3.
	items, _ := h.store.Q.ListWantedItems(context.Background(), seriesID)
	var item3ID int64
	for _, it := range items {
		if int(it.Number.Int64) == 3 {
			item3ID = it.ID
		}
	}
	if g.WantedItemID != item3ID {
		t.Errorf("grab wanted_item_id = %d, want item 3's id %d", g.WantedItemID, item3ID)
	}
}

// TestGrabUnknownReleaseIsRejected covers the guard that a grab must name a
// release from the current search: an unknown download_url is a 404, and nothing
// is handed to the download client or recorded.
func TestGrabUnknownReleaseIsRejected(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:3", Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "x", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": "magnet:?xt=urn:btih:doesnotexist"}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("grab status = %d, want 404", code)
	}
	if len(dl.Adds) != 0 {
		t.Errorf("download Add called for an unknown release")
	}
	grabs, _ := h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if len(grabs) != 0 {
		t.Errorf("recorded %d grabs for a rejected grab, want 0", len(grabs))
	}
}
