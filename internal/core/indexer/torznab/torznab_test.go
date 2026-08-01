package torznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// A realistic Torznab feed: namespaced torznab:attr elements, an enclosure, and
// a mix of magnet and .torrent download URLs across the two items.
const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <title>Prowlarr</title>
    <item>
      <title>[ExampleSubs] Placeholder Saga - 01 (1080p)</title>
      <guid>abc123</guid>
      <pubDate>Sat, 01 Aug 2026 12:30:00 +0000</pubDate>
      <link>http://prowlarr:9696/download/aaa.torrent</link>
      <size>1500000000</size>
      <enclosure url="http://prowlarr:9696/download/aaa.torrent" length="1500000000" type="application/x-bittorrent"/>
      <torznab:attr name="seeders" value="42"/>
      <torznab:attr name="peers" value="50"/>
      <torznab:attr name="infohash" value="0123456789ABCDEF0123456789ABCDEF01234567"/>
      <torznab:attr name="size" value="1500000000"/>
    </item>
    <item>
      <title>[SampleRaws] Placeholder Saga - 02 (720p)</title>
      <guid>def456</guid>
      <link>magnet:?xt=urn:btih:deadbeef&amp;dn=placeholder02</link>
      <torznab:attr name="seeders" value="7"/>
      <torznab:attr name="magneturl" value="magnet:?xt=urn:btih:deadbeef&amp;dn=placeholder02"/>
      <torznab:attr name="size" value="700000000"/>
    </item>
  </channel>
</rss>`

func TestParseFeed(t *testing.T) {
	got, err := parseFeed([]byte(sampleFeed), "prowlarr")
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d releases, want 2", len(got))
	}

	first := got[0]
	if first.Title != "[ExampleSubs] Placeholder Saga - 01 (1080p)" {
		t.Errorf("title = %q", first.Title)
	}
	if first.DownloadURL != "http://prowlarr:9696/download/aaa.torrent" {
		t.Errorf("download url = %q, want the enclosure/torrent url", first.DownloadURL)
	}
	// infohash attr must be lowercased.
	if first.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("infohash = %q, want lowercased", first.InfoHash)
	}
	if first.Size != 1500000000 {
		t.Errorf("size = %d, want 1500000000", first.Size)
	}
	if first.Seeders != 42 {
		t.Errorf("seeders = %d, want 42", first.Seeders)
	}
	if first.Indexer != "prowlarr" {
		t.Errorf("indexer = %q, want prowlarr", first.Indexer)
	}

	// Second item has no enclosure — download URL falls back to the magnet link.
	second := got[1]
	if second.DownloadURL != "magnet:?xt=urn:btih:deadbeef&dn=placeholder02" {
		t.Errorf("download url = %q, want the magnet link", second.DownloadURL)
	}
	if second.Size != 700000000 {
		t.Errorf("size = %d, want 700000000 (from attr)", second.Size)
	}
	if second.Seeders != 7 {
		t.Errorf("seeders = %d, want 7", second.Seeders)
	}
}

func TestParseFeedError(t *testing.T) {
	const errDoc = `<?xml version="1.0" encoding="UTF-8"?>
<error code="100" description="Incorrect user credentials"/>`
	_, err := parseFeed([]byte(errDoc), "prowlarr")
	if err == nil {
		t.Fatal("expected an error for an <error> document")
	}
	if want := "Incorrect user credentials"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

func TestParseFeedEmpty(t *testing.T) {
	const empty = `<rss version="2.0"><channel><title>Empty</title></channel></rss>`
	got, err := parseFeed([]byte(empty), "prowlarr")
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

func TestSizeFallsBackToEnclosureLength(t *testing.T) {
	// No size attr, but an enclosure length is present.
	const feed = `<rss><channel><item>
      <title>x</title>
      <link>http://x/y.torrent</link>
      <enclosure url="http://x/y.torrent" length="12345"/>
    </item></channel></rss>`
	got, err := parseFeed([]byte(feed), "t")
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if got[0].Size != 12345 {
		t.Errorf("size = %d, want 12345 from enclosure length", got[0].Size)
	}
}

func TestSearchURL(t *testing.T) {
	i := New("prowlarr", "http://prowlarr:9696/1/api", "SECRETKEY")
	raw, err := i.searchURL(indexer.Query{Term: "placeholder saga 01"})
	if err != nil {
		t.Fatalf("searchURL: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	q := u.Query()
	if q.Get("t") != "search" {
		t.Errorf("t = %q, want search", q.Get("t"))
	}
	if q.Get("q") != "placeholder saga 01" {
		t.Errorf("q = %q, want 'placeholder saga 01'", q.Get("q"))
	}
	if q.Get("apikey") != "SECRETKEY" {
		t.Errorf("apikey = %q", q.Get("apikey"))
	}
	if q.Get("limit") == "" {
		t.Error("limit not set")
	}
}

// ensureAPIPath must make Prowlarr- and Jackett-shaped feed URLs both resolve to
// a "/api" endpoint, without doubling it or leaving a trailing slash.
func TestEnsureAPIPath(t *testing.T) {
	cases := map[string]string{
		// Prowlarr: already ends in /api — left as-is.
		"http://prowlarr:9696/1/api": "/1/api",
		// Jackett: ends in /torznab/ — /api appended.
		"http://localhost:9117/api/v2.0/indexers/all/results/torznab/": "/api/v2.0/indexers/all/results/torznab/api",
		"http://localhost:9117/api/v2.0/indexers/all/results/torznab":  "/api/v2.0/indexers/all/results/torznab/api",
		"http://prowlarr:9696/1/api/":                                  "/1/api",
		"http://indexer.example.com":                                   "/api",
		"http://indexer.example.com/":                                  "/api",
	}
	for raw, wantPath := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		ensureAPIPath(u)
		if u.Path != wantPath {
			t.Errorf("ensureAPIPath(%q) path = %q, want %q", raw, u.Path, wantPath)
		}
	}
}

// A Jackett feed URL (no /api) must produce a request URL with /api appended and
// the query intact.
func TestSearchURLJackettAppendsAPI(t *testing.T) {
	i := New("jackett", "http://localhost:9117/api/v2.0/indexers/all/results/torznab/", "KEY")
	raw, err := i.searchURL(indexer.Query{Term: "x"})
	if err != nil {
		t.Fatalf("searchURL: %v", err)
	}
	u, _ := url.Parse(raw)
	if want := "/api/v2.0/indexers/all/results/torznab/api"; u.Path != want {
		t.Errorf("path = %q, want %q", u.Path, want)
	}
	if u.Query().Get("apikey") != "KEY" || u.Query().Get("t") != "search" {
		t.Errorf("query lost: %q", u.RawQuery)
	}
}

// searchURL must preserve params already present in the base URL (Prowlarr bakes
// per-indexer settings into the feed URL).
func TestSearchURLPreservesBaseParams(t *testing.T) {
	i := New("prowlarr", "http://prowlarr:9696/api?extra=keep", "")
	raw, err := i.searchURL(indexer.Query{Term: "x"})
	if err != nil {
		t.Fatalf("searchURL: %v", err)
	}
	u, _ := url.Parse(raw)
	if u.Query().Get("extra") != "keep" {
		t.Errorf("base param 'extra' lost: %q", raw)
	}
	if u.Query().Get("apikey") != "" {
		t.Error("apikey should be absent when not configured")
	}
}

// Search drives the full HTTP path against a stub server.
func TestSearchHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "search" || r.URL.Query().Get("q") != "placeholder" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()

	i := New("prowlarr", srv.URL, "k")
	got, err := i.Search(context.Background(), indexer.Query{Term: "placeholder"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d releases, want 2", len(got))
	}
}

// The recent feed is a search with no term (#101), which is what Sonarr's own
// RSS sync issues. Everything else about the request is unchanged.
func TestRecentHTTPUsesAnEmptyTerm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("t") != "search" || q.Get("q") != "" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()

	got, err := New("prowlarr", srv.URL, "k").Recent(context.Background())
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].GUID != "abc123" {
		t.Errorf("guid = %q, want abc123", got[0].GUID)
	}
	if got[0].Release.Title != "[ExampleSubs] Placeholder Saga - 01 (1080p)" {
		t.Errorf("release not carried through: %q", got[0].Release.Title)
	}
	if got[0].Published.IsZero() {
		t.Error("published not parsed from pubDate")
	}
}

// A feed that omits pubDate or guid is a supported shape, not an error: the
// caller falls back to other identity and to the seen set.
func TestParseEntriesToleratesMissingFeedMetadata(t *testing.T) {
	const feed = `<rss><channel><item>
      <title>[ExampleSubs] Placeholder Saga - 05 (1080p)</title>
      <link>magnet:?xt=urn:btih:abc</link>
    </item></channel></rss>`
	got, err := parseEntries([]byte(feed), "t")
	if err != nil {
		t.Fatalf("parseEntries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].GUID != "" || !got[0].Published.IsZero() {
		t.Errorf("expected zero feed metadata, got guid=%q published=%v", got[0].GUID, got[0].Published)
	}
	if got[0].Release.DownloadURL != "magnet:?xt=urn:btih:abc" {
		t.Errorf("release not parsed: %+v", got[0].Release)
	}
}

// pubDate is RFC822/1123 in practice, but indexers vary the timezone spelling
// and drop seconds. Sonarr needs a regex to strip named zones for the same
// reason; a handful of stdlib layouts covers it here.
func TestParsePubDate(t *testing.T) {
	want := time.Date(2026, 8, 1, 12, 30, 0, 0, time.UTC)
	for _, raw := range []string{
		"Sat, 01 Aug 2026 12:30:00 +0000",
		"Sat, 01 Aug 2026 12:30:00 GMT",
		"Sat, 01 Aug 2026 12:30:00 UTC",
		"01 Aug 26 12:30 +0000",
		"2026-08-01T12:30:00Z",
	} {
		got, ok := parsePubDate(raw)
		if !ok {
			t.Errorf("parsePubDate(%q) failed", raw)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parsePubDate(%q) = %s, want %s", raw, got, want)
		}
	}
	if _, ok := parsePubDate("not a date"); ok {
		t.Error("parsePubDate accepted nonsense")
	}
}

func TestSearchHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	i := New("prowlarr", srv.URL, "k")
	if _, err := i.Search(context.Background(), indexer.Query{Term: "x"}); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}
