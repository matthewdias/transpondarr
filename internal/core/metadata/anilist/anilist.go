// Package anilist implements metadata.Provider against AniList's public GraphQL
// API (https://graphql.anilist.co). It is net/http + encoding/json over two
// queries, which don't justify a GraphQL codegen dependency. Rate limiting
// (golang.org/x/time/rate) and 429 backoff live here; read-through caching is
// layered on by metadata.Cached.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

const (
	defaultEndpoint = "https://graphql.anilist.co"
	minInterval     = 2 * time.Second   // ~30 req/min ceiling
	defaultBackoff  = 10 * time.Second  // 429 fallback when Retry-After is absent
	maxBackoff      = 120 * time.Second // cap on any single backoff wait
	maxAttempts     = 3
	searchPerPage   = 10
	maxRespBytes    = 8 << 20 // 8 MiB cap on a decoded response body
)

// Client is an AniList metadata provider. One instance is shared process-wide:
// the limiter it carries is the only thing keeping concurrent callers inside the
// budget, so a second client would silently double the request rate.
type Client struct {
	http     *http.Client
	limiter  *rate.Limiter
	endpoint string
	log      *slog.Logger

	// mu guards retryAt, the shared 429 backoff deadline: one caller's 429 must
	// hold back every caller, or concurrency makes a rate-limited window worse.
	mu      sync.Mutex
	retryAt time.Time
}

// New constructs an AniList client with sane defaults.
func New(log *slog.Logger) *Client {
	return &Client{
		http:     &http.Client{Timeout: 20 * time.Second},
		limiter:  rate.NewLimiter(rate.Every(minInterval), 1), // burst 1 ≈ spacing
		endpoint: defaultEndpoint,
		log:      log,
	}
}

func (c *Client) Name() string { return "anilist" }

// --- GraphQL wire types -----------------------------------------------------

type mediaTitle struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

type media struct {
	ID         int64      `json:"id"`
	Title      mediaTitle `json:"title"`
	Format     string     `json:"format"`
	Episodes   *int       `json:"episodes"`
	Status     string     `json:"status"`
	SeasonYear *int       `json:"seasonYear"`
	CoverImage struct {
		Large string `json:"large"`
	} `json:"coverImage"`
}

func (m media) titles() metadata.Titles {
	return metadata.Titles{Romaji: m.Title.Romaji, English: m.Title.English, Native: m.Title.Native}
}

func (m media) episodes() int {
	if m.Episodes == nil {
		return 0
	}
	return *m.Episodes
}

// --- Provider methods -------------------------------------------------------

const searchQuery = `
query ($search: String!, $perPage: Int!) {
  Page(page: 1, perPage: $perPage) {
    media(search: $search, type: ANIME, sort: SEARCH_MATCH) {
      id
      title { romaji english native }
      format
      episodes
      status
      seasonYear
      coverImage { large }
    }
  }
}`

// Search returns AniList's best matches for a free-text term.
func (c *Client) Search(ctx context.Context, term string) ([]metadata.Candidate, error) {
	var data struct {
		Page struct {
			Media []media `json:"media"`
		} `json:"Page"`
	}
	vars := map[string]any{"search": term, "perPage": searchPerPage}
	if err := c.do(ctx, searchQuery, vars, &data); err != nil {
		return nil, err
	}

	out := make([]metadata.Candidate, 0, len(data.Page.Media))
	for _, m := range data.Page.Media {
		year := 0
		if m.SeasonYear != nil {
			year = *m.SeasonYear
		}
		out = append(out, metadata.Candidate{
			ProviderID: m.ID,
			Titles:     m.titles(),
			Format:     m.Format,
			Episodes:   m.episodes(),
			Status:     m.Status,
			Year:       year,
			CoverURL:   m.CoverImage.Large,
		})
	}
	return out, nil
}

const titleQuery = `
query ($id: Int!) {
  Media(id: $id, type: ANIME) {
    id
    title { romaji english native }
    format
    episodes
    status
  }
}`

// GetTitle resolves one title and expands its known episode count into items
// (1..N, absolute numbering, no per-episode names). When the count is not yet
// known (a releasing series with a null count), it returns zero items — the
// title is still created, and a later refresh/airing-schedule pass fills it in.
func (c *Client) GetTitle(ctx context.Context, id int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	var data struct {
		Media media `json:"Media"`
	}
	if err := c.do(ctx, titleQuery, map[string]any{"id": id}, &data); err != nil {
		return metadata.TitleMeta{}, nil, err
	}
	m := data.Media

	meta := metadata.TitleMeta{
		ProviderID: m.ID,
		Titles:     m.titles(),
		Format:     mapFormat(m.Format),
		Episodes:   m.episodes(),
		Status:     m.Status,
	}

	items := make([]metadata.ItemMeta, 0, meta.Episodes)
	for n := 1; n <= meta.Episodes; n++ {
		items = append(items, metadata.ItemMeta{Number: n})
	}
	return meta, items, nil
}

// mapFormat translates an AniList MediaFormat string to a domain.Format,
// defaulting to TV for the series-shaped formats v1 targets.
func mapFormat(anilistFormat string) domain.Format {
	switch anilistFormat {
	case "OVA":
		return domain.FormatOVA
	case "ONA":
		return domain.FormatONA
	case "SPECIAL":
		return domain.FormatSpecial
	case "MOVIE":
		return domain.FormatMovie
	default: // TV, TV_SHORT, MUSIC, or unknown
		return domain.FormatTV
	}
}

// --- transport --------------------------------------------------------------

// do executes one GraphQL request, decoding the `data` field into out. It waits
// on the rate limiter before each attempt and retries on HTTP 429, honouring
// Retry-After.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("anilist: marshal request: %w", err)
	}

	for attempt := 1; ; attempt++ {
		if err := c.awaitBackoff(ctx); err != nil {
			return err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("anilist: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("anilist: request: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			_ = resp.Body.Close()
			// Published even on the final attempt: the window is rate limited
			// regardless of whether this caller has retries left.
			c.extendBackoff(time.Now().Add(wait))
			if attempt >= maxAttempts {
				return fmt.Errorf("anilist: rate limited after %d attempts", attempt)
			}
			continue
		}

		err = decode(resp, out)
		_ = resp.Body.Close()
		return err
	}
}

// extendBackoff moves the shared deadline out, never in — concurrent 429s keep
// the furthest wait any of them was told.
func (c *Client) extendBackoff(until time.Time) {
	c.mu.Lock()
	if until.After(c.retryAt) {
		c.retryAt = until
	}
	c.mu.Unlock()
}

// awaitBackoff sleeps until the shared deadline, re-checking in case another
// caller's 429 extended it mid-sleep.
func (c *Client) awaitBackoff(ctx context.Context) error {
	for {
		c.mu.Lock()
		until := c.retryAt
		c.mu.Unlock()
		d := time.Until(until)
		if d <= 0 {
			return nil
		}
		if err := sleep(ctx, d); err != nil {
			return err
		}
	}
}

// decode reads a GraphQL envelope, surfacing transport and GraphQL errors.
func decode(resp *http.Response, out any) error {
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("anilist: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBytes)).Decode(&env); err != nil {
		return fmt.Errorf("anilist: decode envelope: %w", err)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("anilist: %s", env.Errors[0].Message)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("anilist: decode data: %w", err)
	}
	return nil
}

// retryAfter parses a Retry-After header (delta-seconds), clamping to a sane
// range and falling back when absent or unparseable.
func retryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return defaultBackoff
	}
	secs, err := strconv.Atoi(h)
	if err != nil || secs < 0 {
		return defaultBackoff
	}
	d := time.Duration(secs) * time.Second
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// sleep waits for d, honouring context cancellation.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var _ metadata.Provider = (*Client)(nil)
