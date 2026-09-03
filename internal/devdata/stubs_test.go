package devdata

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/indexer/torznab"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/metadata/anilist"
)

func torznabStub(t *testing.T) *torznab.Indexer {
	t.Helper()
	srv := httptest.NewServer(TorznabHandler(fixedNow, 1))
	t.Cleanup(srv.Close)
	return torznab.New("devseed", srv.URL, "dev", "")
}

func anilistStub(t *testing.T) *anilist.Client {
	t.Helper()
	srv := httptest.NewServer(AnilistHandler(fixedNow))
	t.Cleanup(srv.Close)
	return anilist.New(slog.New(slog.NewTextHandler(io.Discard, nil)), anilist.WithEndpoint(srv.URL+"/graphql"))
}

// The stub is only worth having if the shipped adapter can read it, so the test
// drives the real Torznab client rather than asserting on the XML.
func TestTorznabStubParsesWithTheRealAdapter(t *testing.T) {
	ix := torznabStub(t)

	got, err := ix.Search(context.Background(), indexer.Query{Term: "Placeholder Frontier"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) < 4 {
		t.Fatalf("Search returned %d releases, want the competing-candidates population the Releases tab needs", len(got))
	}
	groups := map[string]bool{}
	var sawBatch, sawV2 bool
	for _, r := range got {
		if r.InfoHash == "" {
			t.Errorf("release %q carries no info hash", r.Title)
		}
		if r.Seeders == 0 {
			t.Errorf("release %q reports no seeders", r.Title)
		}
		if !strings.Contains(strings.ToLower(r.Title), "placeholder frontier") {
			t.Errorf("release %q is not for the searched title", r.Title)
		}
		switch {
		case strings.Contains(r.Title, "01-06"):
			sawBatch = true
		case strings.Contains(r.Title, "06v2"):
			sawV2 = true
		}
		if i := strings.Index(r.Title, "]"); strings.HasPrefix(r.Title, "[") && i > 1 {
			groups[r.Title[1:i]] = true
		}
	}
	if len(groups) < 3 {
		t.Errorf("releases came from %d groups, want competing groups for the reason column to discriminate", len(groups))
	}
	if !sawBatch {
		t.Error("no batch release; the coverage tier has nothing to prefer")
	}
	if !sawV2 {
		t.Error("no v2 release; the version tie-break is unexercised")
	}
}

func TestTorznabStubAnswersAnUnknownTitleWithAnEmptyFeed(t *testing.T) {
	ix := torznabStub(t)
	got, err := ix.Search(context.Background(), indexer.Query{Term: "Nothing By This Name"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Search returned %d releases for an unseeded title, want none", len(got))
	}
}

func TestTorznabStubServesRecentFeed(t *testing.T) {
	ix := torznabStub(t)
	feed, ok := any(ix).(indexer.RecentFeed)
	if !ok {
		t.Fatal("torznab adapter no longer implements RecentFeed")
	}
	entries, err := feed.Recent(context.Background())
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(entries) < 8 {
		t.Fatalf("Recent returned %d entries, want the whole endpoint", len(entries))
	}
	var titles int
	for _, e := range entries {
		if e.Published.IsZero() {
			t.Errorf("entry %q has no publish time; the feed mark has nothing to advance on", e.Release.Title)
		}
		if e.GUID == "" {
			t.Errorf("entry %q has no GUID; the dedupe set has nothing to key on", e.Release.Title)
		}
		if strings.Contains(e.Release.Title, "Placeholder") {
			titles++
		}
	}
	if titles != len(entries) {
		t.Errorf("%d of %d entries are seeded releases, want all of them", titles, len(entries))
	}
	// Newest first is what a real endpoint does and what the mark assumes.
	for i := 1; i < len(entries); i++ {
		if entries[i].Published.After(entries[i-1].Published) {
			t.Fatalf("entry %d is newer than the one before it; the feed is not ordered newest first", i)
		}
	}
}

func TestAnilistStubAnswersSearchTitleBrowseAndSchedule(t *testing.T) {
	c := anilistStub(t)
	ctx := context.Background()

	found, err := c.Search(ctx, "Placeholder Frontier")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("Search returned nothing; a title cannot be added offline")
	}
	if found[0].CoverURL == "" || found[0].Titles.Preferred() == "" {
		t.Errorf("search row = %+v, want a name and a cover", found[0])
	}

	meta, items, err := c.GetTitle(ctx, 101)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.Episodes != 12 || meta.Year != 2026 {
		t.Errorf("GetTitle = %d episodes, year %d; want 12 and 2026", meta.Episodes, meta.Year)
	}
	if len(items) == 0 {
		t.Error("GetTitle returned no items; the in-band schedule page is empty")
	}

	// A null episode count is a normal state the stub must be able to publish.
	nullCount, _, err := c.GetTitle(ctx, 105)
	if err != nil {
		t.Fatalf("GetTitle(105): %v", err)
	}
	if nullCount.Episodes != 0 {
		t.Errorf("GetTitle(105).Episodes = %d, want 0 for a title whose count the provider does not publish", nullCount.Episodes)
	}

	entries, err := c.BrowseSeason(ctx, metadata.SeasonSpring, 2026)
	if err != nil {
		t.Fatalf("BrowseSeason: %v", err)
	}
	if len(entries) < 8 {
		t.Errorf("BrowseSeason returned %d entries, want a chart dense enough to render Discovery", len(entries))
	}

	sched, ok := any(c).(metadata.AiringProvider)
	if !ok {
		t.Fatal("anilist client no longer implements AiringProvider")
	}
	airing, err := sched.GetSchedule(ctx, 101, false)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if len(airing) == 0 {
		t.Error("schedule is empty; the airing sync has nothing to write")
	}
}

func TestAnilistStubReportsAnUnknownTitleRatherThanInventingOne(t *testing.T) {
	c := anilistStub(t)
	if _, _, err := c.GetTitle(context.Background(), 999999); err == nil {
		t.Error("GetTitle for an unseeded id returned no error; the stub invented a title")
	}
}
