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
	maxErrBytes     = 2 << 10 // an error message is not a response body, so maxRespBytes is no bound here
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
	ID          int64      `json:"id"`
	Title       mediaTitle `json:"title"`
	Format      string     `json:"format"`
	Description string     `json:"description"`
	Episodes    *int       `json:"episodes"`
	Status      string     `json:"status"`
	SeasonYear  *int       `json:"seasonYear"`
	StartDate   struct {
		Year  *int `json:"year"`
		Month *int `json:"month"`
		Day   *int `json:"day"`
	} `json:"startDate"`
	CoverImage struct {
		Large string `json:"large"`
	} `json:"coverImage"`
	Genres       []string `json:"genres"`
	AverageScore *int     `json:"averageScore"`
	Studios      struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"studios"`
	NextAiringEpisode *struct {
		Episode  int   `json:"episode"`
		AiringAt int64 `json:"airingAt"`
	} `json:"nextAiringEpisode"`
	AiringSchedule struct {
		Nodes []struct {
			Episode int `json:"episode"`
		} `json:"nodes"`
	} `json:"airingSchedule"`
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

// year prefers startDate over seasonYear: AniList assigns a season later than a
// year becomes known, and its WINTER bucket spans December, so seasonYear can
// name the year after the premiere that release names carry.
func (m media) year() int {
	if m.StartDate.Year != nil {
		return *m.StartDate.Year
	}
	if m.SeasonYear != nil {
		return *m.SeasonYear
	}
	return 0
}

// premiere fixes the instant that reads as startDate's calendar day. Noon UTC
// rather than JST midnight: startDate carries no clock to preserve, and a day
// named at midnight anywhere lands a cell early for half the world. It is off by
// a day east of UTC+11, which needs the viewer's zone the server does not have.
func (m media) premiere() time.Time {
	if m.StartDate.Year == nil || m.StartDate.Month == nil || m.StartDate.Day == nil {
		return time.Time{}
	}
	return time.Date(*m.StartDate.Year, time.Month(*m.StartDate.Month), *m.StartDate.Day, 12, 0, 0, 0, time.UTC)
}

// highestItem is the last episode number the title is known to reach. A published
// count wins outright: a schedule can carry an entry past the announced end.
func (m media) highestItem() int {
	// Format is the discriminator and the count never is: three shorts released
	// as one film carry episodes: 3 and are still one acquirable item.
	if mapFormat(m.Format) == domain.FormatMovie {
		return 1
	}
	if n := m.episodes(); n > 0 {
		return n
	}
	n := 0
	for _, node := range m.AiringSchedule.Nodes {
		n = max(n, node.Episode)
	}
	if m.NextAiringEpisode != nil {
		n = max(n, m.NextAiringEpisode.Episode)
	}
	return n
}

// nextItem is the next scheduled broadcast's number, already selected by
// titleQuery for highestItem, so reading it costs no extra request.
func nextItem(m media) int {
	if m.NextAiringEpisode == nil {
		return 0
	}
	return m.NextAiringEpisode.Episode
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
      startDate { year }
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
		out = append(out, metadata.Candidate{
			ProviderID: m.ID,
			Titles:     m.titles(),
			Format:     m.Format,
			Episodes:   m.episodes(),
			Status:     m.Status,
			Year:       m.year(),
			CoverURL:   m.CoverImage.Large,
		})
	}
	return out, nil
}

// airingSchedule is a field on Media, not a root query, so one page of it rides
// along here for no extra request.
const titleQuery = `
query ($id: Int!, $perPage: Int!) {
  Media(id: $id, type: ANIME) {
    id
    title { romaji english native }
    format
    episodes
    status
    seasonYear
    startDate { year month day }
    coverImage { large }
    nextAiringEpisode { episode }
    airingSchedule(page: 1, perPage: $perPage) {
      nodes { episode }
    }
  }
}`

// GetTitle resolves one title and expands its items (1..N, absolute numbering,
// no per-episode names), N coming from highestItem. Filling to that number
// rather than transcribing the schedule is what creates an episode AniList lists
// no entry for, having shared a broadcast slot.
func (c *Client) GetTitle(ctx context.Context, id int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	var data struct {
		Media media `json:"Media"`
	}
	vars := map[string]any{"id": id, "perPage": schedulePerPage}
	if err := c.do(ctx, titleQuery, vars, &data); err != nil {
		return metadata.TitleMeta{}, nil, err
	}
	m := data.Media

	meta := metadata.TitleMeta{
		ProviderID: m.ID,
		Titles:     m.titles(),
		Format:     mapFormat(m.Format),
		Episodes:   m.episodes(),
		Status:     m.Status,
		CoverURL:   m.CoverImage.Large,
		Year:       m.year(),
		Premiere:   m.premiere(),
		NextItem:   nextItem(m),
	}

	highest := m.highestItem()
	items := make([]metadata.ItemMeta, 0, highest)
	for n := 1; n <= highest; n++ {
		items = append(items, metadata.ItemMeta{Number: n})
	}
	return meta, items, nil
}

// mapFormat translates an AniList MediaFormat string to a domain.Format,
// defaulting to TV for the title-shaped formats v1 targets.
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

// envelope is AniList's GraphQL response shape. A failing status carries it too,
// which is where the provider says what went wrong.
type envelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// message is the provider's own explanation, or "" when the envelope carries none.
func (e envelope) message() string {
	if len(e.Errors) == 0 {
		return ""
	}
	return strings.TrimSpace(e.Errors[0].Message)
}

// decode reads a GraphQL envelope, surfacing transport and GraphQL errors.
func decode(resp *http.Response, out any) error {
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBytes))
		detail := strings.TrimSpace(string(b))
		// A downed API often answers with a proxy's HTML page, which has no
		// message to extract — its truncated self beats an empty string.
		var env envelope
		if err := json.Unmarshal(b, &env); err == nil && env.message() != "" {
			detail = env.message()
		}
		return fmt.Errorf("anilist: status %d: %s", resp.StatusCode, detail)
	}
	var env envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBytes)).Decode(&env); err != nil {
		return fmt.Errorf("anilist: decode envelope: %w", err)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("anilist: %s", env.message())
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
