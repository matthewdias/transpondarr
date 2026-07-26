// Package metadata defines the pluggable metadata-provider interface. A Provider
// answers "what is this title and what items should exist" — the ground truth
// the acquisition pipeline works toward. It never identifies releases; that is
// the parser's job (identity-by-construction).
package metadata

import (
	"context"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

type Titles struct {
	Romaji  string `json:"romaji,omitempty"`
	English string `json:"english,omitempty"`
	Native  string `json:"native,omitempty"`
}

func (t Titles) Preferred() string {
	switch {
	case t.Romaji != "":
		return t.Romaji
	case t.English != "":
		return t.English
	default:
		return t.Native
	}
}

type Candidate struct {
	ProviderID int64
	Titles     Titles
	Format     string // provider-native format string (e.g. "TV", "OVA")
	Episodes   int    // 0 when unknown
	Status     string // provider-native status (e.g. "RELEASING", "FINISHED")
	Year       int
	CoverURL   string
}

type TitleMeta struct {
	ProviderID int64
	Titles     Titles
	Format     domain.Format // provider-native format mapped to the domain vocabulary by the adapter
	Episodes   int           // 0 when the count is not yet known (e.g. still releasing)
	Status     string
}

type ItemMeta struct {
	Number int
	Name   string
}

type Provider interface {
	Name() string
	Search(ctx context.Context, term string) ([]Candidate, error)
	GetTitle(ctx context.Context, id int64) (TitleMeta, []ItemMeta, error)
}

// Airing is one item's scheduled broadcast. AirsAt is the provider's own clock —
// AniList publishes the Japanese broadcast time, which is the right one to hang
// fansub delay windows off.
type Airing struct {
	Number int
	AirsAt time.Time
}

// AiringProvider is an optional Provider capability: providers that publish a
// broadcast schedule implement it. It stays off Provider because paging a full
// schedule costs many requests, and GetTitle is on the request path.
type AiringProvider interface {
	// GetSchedule returns a title's known broadcast times. notYetAired limits the
	// fetch to the upcoming tail — aired times never change, so a resync pays for
	// history once.
	GetSchedule(ctx context.Context, id int64, notYetAired bool) ([]Airing, error)
}

type CachedTitle struct {
	Title TitleMeta  `json:"title"`
	Items []ItemMeta `json:"items"`
}

type Cache interface {
	Get(ctx context.Context, provider string, id int64) (snap CachedTitle, fetchedAt time.Time, ok bool, err error)
	Put(ctx context.Context, provider string, id int64, snap CachedTitle) error
}

// Cached wraps a provider in a read-through title cache. The wrapper carries
// AiringProvider only when inner implements it, so asserting the capability on a
// composed provider still answers for the adapter underneath.
func Cached(inner Provider, cache Cache) Provider {
	c := &cached{inner: inner, cache: cache}
	if airing, ok := inner.(AiringProvider); ok {
		return &cachedAiring{cached: c, airing: airing}
	}
	return c
}

type cached struct {
	inner Provider
	cache Cache
}

type cachedAiring struct {
	*cached
	airing AiringProvider
}

// GetSchedule passes through uncached: schedules are persisted per item by the
// airing sync, not held in the title snapshot this cache stores.
func (c *cachedAiring) GetSchedule(ctx context.Context, id int64, notYetAired bool) ([]Airing, error) {
	return c.airing.GetSchedule(ctx, id, notYetAired)
}

func (c *cached) Name() string { return c.inner.Name() }

func (c *cached) Search(ctx context.Context, term string) ([]Candidate, error) {
	return c.inner.Search(ctx, term)
}

func (c *cached) GetTitle(ctx context.Context, id int64) (TitleMeta, []ItemMeta, error) {
	snap, fetchedAt, ok, cacheErr := c.cache.Get(ctx, c.inner.Name(), id)
	if cacheErr == nil && ok && fresh(snap.Title.Status, len(snap.Items), fetchedAt) {
		return snap.Title, snap.Items, nil
	}

	meta, items, err := c.inner.GetTitle(ctx, id)
	if err != nil {
		// The provider failed (typically a 429 — the exact rate-limit
		// pressure this cache exists to absorb). If we still hold a stale snapshot,
		// serve it rather than failing: a slightly out-of-date title beats none.
		if cacheErr == nil && ok {
			return snap.Title, snap.Items, nil
		}
		return TitleMeta{}, nil, err
	}
	// Best-effort: a cache write failure must not fail the fetch.
	_ = c.cache.Put(ctx, c.inner.Name(), id, CachedTitle{Title: meta, Items: items})
	return meta, items, nil
}

const shortTTL = 6 * time.Hour

// fresh reports whether a cached snapshot is still within its status-aware TTL. An
// empty snapshot (itemCount 0) always uses the short TTL: a FINISHED title whose
// episode count came back unknown/null would otherwise be trusted as "zero
// episodes" for weeks instead of being re-checked soon.
func fresh(status string, itemCount int, fetchedAt time.Time) bool {
	ttl := TTLFor(status)
	if itemCount == 0 {
		ttl = shortTTL
	}
	return time.Since(fetchedAt) < ttl
}

// TTLFor keeps finished titles cached far longer than releasing ones (whose
// episode count and status are still moving). Exported so background refreshes
// pace themselves by the same status-aware policy instead of inventing a second.
func TTLFor(status string) time.Duration {
	switch status {
	case "FINISHED", "CANCELLED":
		return 30 * 24 * time.Hour
	default: // RELEASING, NOT_YET_RELEASED, HIATUS, or unknown
		return shortTTL
	}
}
