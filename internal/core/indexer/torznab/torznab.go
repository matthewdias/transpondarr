// Package torznab implements the Indexer interface against a Torznab-compatible
// endpoint (e.g. Prowlarr or Jackett).
package torznab

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

const (
	defaultLimit = 100
	maxBodyBytes = 8 << 20 // cap on a single feed response (8 MiB)
	httpTimeout  = 30 * time.Second
)

type Indexer struct {
	name    string
	baseURL string
	apiKey  string
	http    *http.Client
}

// New constructs a Torznab indexer. baseURL is the Torznab feed URL as copied
// from Prowlarr or Jackett; the "/api" operation path is appended automatically
// when absent (Prowlarr's URL already ends in /api, Jackett's ends in /torznab/),
// matching the conventional Torznab default API Path.
func New(name, baseURL, apiKey string) *Indexer {
	return &Indexer{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: httpTimeout},
	}
}

func (i *Indexer) Name() string { return i.name }

var _ indexer.Indexer = (*Indexer)(nil)

// Search runs a free-text Torznab search and maps the feed to releases.
func (i *Indexer) Search(ctx context.Context, q indexer.Query) ([]indexer.Release, error) {
	reqURL, err := i.searchURL(q)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("torznab: build request: %w", err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")

	resp, err := i.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torznab: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("torznab: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torznab: status %d: %s", resp.StatusCode, snippet(body))
	}
	return parseFeed(body, i.name)
}

// searchURL builds the t=search request URL, preserving any query parameters
// already baked into baseURL (Prowlarr embeds per-indexer params there).
func (i *Indexer) searchURL(q indexer.Query) (string, error) {
	u, err := url.Parse(i.baseURL)
	if err != nil {
		return "", fmt.Errorf("torznab: bad base url %q: %w", i.baseURL, err)
	}
	ensureAPIPath(u)
	params := u.Query()
	params.Set("t", "search")
	params.Set("q", q.Term)
	params.Set("limit", strconv.Itoa(defaultLimit))
	if i.apiKey != "" {
		params.Set("apikey", i.apiKey)
	}
	u.RawQuery = params.Encode()
	return u.String(), nil
}

// ensureAPIPath appends the Torznab "/api" operation segment unless the path
// already ends with it, and drops any trailing slash. This lets a Prowlarr feed
// URL (…/1/api) and a Jackett feed URL (…/results/torznab/) both work as copied.
func ensureAPIPath(u *url.URL) {
	trimmed := strings.TrimRight(u.Path, "/")
	segments := strings.Split(trimmed, "/")
	if last := segments[len(segments)-1]; strings.EqualFold(last, "api") {
		u.Path = trimmed
		return
	}
	u.Path = trimmed + "/api"
}

// --- wire types -------------------------------------------------------------

type rss struct {
	Channel struct {
		Items []item `xml:"item"`
	} `xml:"channel"`
}

type item struct {
	Title     string    `xml:"title"`
	Link      string    `xml:"link"`
	Size      int64     `xml:"size"`
	Enclosure enclosure `xml:"enclosure"`
	// Attrs matches <torznab:attr>; the unqualified tag matches the local name
	// regardless of the (torznab) namespace prefix.
	Attrs []torznabAttr `xml:"attr"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
}

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// torznabError models the <error code=.. description=..> failure root. Because
// its XMLName pins the root to "error", unmarshalling a normal <rss> feed into
// it fails — which is exactly how parseFeed tells the two apart.
type torznabError struct {
	XMLName     xml.Name `xml:"error"`
	Code        string   `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// --- parsing ----------------------------------------------------------------

func parseFeed(body []byte, indexerName string) ([]indexer.Release, error) {
	if apiErr, ok := decodeError(body); ok {
		desc := apiErr.Description
		if desc == "" {
			desc = "unknown error"
		}
		return nil, fmt.Errorf("torznab: api error %s: %s", apiErr.Code, desc)
	}

	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("torznab: parse feed: %w", err)
	}

	releases := make([]indexer.Release, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		releases = append(releases, it.toRelease(indexerName))
	}
	return releases, nil
}

// decodeError reports whether body is a Torznab <error> document.
func decodeError(body []byte) (torznabError, bool) {
	var e torznabError
	if err := xml.Unmarshal(body, &e); err == nil {
		return e, true
	}
	return torznabError{}, false
}

func (it item) toRelease(indexerName string) indexer.Release {
	attrs := it.attrMap()
	return indexer.Release{
		Title:       strings.TrimSpace(it.Title),
		DownloadURL: firstNonEmpty(it.Enclosure.URL, it.Link, attrs["magneturl"]),
		InfoHash:    strings.ToLower(attrs["infohash"]),
		Size:        it.size(attrs),
		Seeders:     atoiSafe(attrs["seeders"]),
		Indexer:     indexerName,
	}
}

// attrMap flattens the torznab:attr list into a name->value map (names lowered).
func (it item) attrMap() map[string]string {
	m := make(map[string]string, len(it.Attrs))
	for _, a := range it.Attrs {
		if a.Name != "" {
			m[strings.ToLower(a.Name)] = a.Value
		}
	}
	return m
}

// size resolves the release size, preferring the explicit torznab attr, then the
// enclosure length, then a bare <size> element.
func (it item) size(attrs map[string]string) int64 {
	if v := atoi64(attrs["size"]); v > 0 {
		return v
	}
	if it.Enclosure.Length > 0 {
		return it.Enclosure.Length
	}
	return it.Size
}

// --- helpers ----------------------------------------------------------------

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
