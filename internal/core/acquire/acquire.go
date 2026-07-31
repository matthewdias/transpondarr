// Package acquire owns search, decide, and grab, shared by the manual HTTP
// routes and the scheduled sweep so both drive exactly one matcher.
//
// The sweep (#100) searches monitored series with something worth grabbing right
// now and backs off exponentially when it finds nothing, clamped so a weekly
// show is still searched at broadcast however many empty passes preceded it.
// Nothing resets the cadence on the airing side: schedule-created items carry an
// air date, so the aired-since-last-search reset and that clamp already cover
// them. A manual grab racing a sweep converges — one grab row per item and the
// download client's own info-hash dedupe make the exposure no worse than
// double-clicking Grab.
//
// A failed pass backs off like an empty one. Only an indexer outage is free,
// because every due series shares it; anything series-local must still yield its
// slot, or a handful of permanently failing series hold the head of the bounded
// due queue and nothing else is ever searched.
//
// Throughput is deliberately small — seriesPerPass series per tick — because a
// pass costs one indexer search per series and the indexer is the scarce,
// rate-limited resource. The backoff keeps the steady-state due set well under
// that, but demand is correlated: anime airs in weekly blocks, and the clamp
// puts every title from one block back on the queue at nearly the same moment.
// A large library can therefore take a few passes to drain the evening its shows
// air, which most-overdue-first ordering makes fair rather than fast. If that
// ever needs tuning, shorten the interval before widening the pass: the ratio is
// what sets throughput, and smaller more frequent passes smooth burst latency
// without raising peak load on the indexer as much.
//
// A series with a pinned group can make the sweep wait a while before settling
// for another group (#62). The window is measured from broadcast, so an item
// with no air date is not delayed at all: there is no interval to wait out, a
// now-anchor would restart on every process restart, and the null case is
// dominated by backfill titles where the pinned release either already exists
// and wins ranking or never will. The delay lives entirely in the sweep —
// manual search and grab are untouched.
package acquire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Sentinel errors, so the HTTP layer can map failures to status codes without
// huma leaking into core.
var (
	ErrNoIndexer        = errors.New("acquire: no indexer configured")
	ErrNoDownloadClient = errors.New("acquire: no download client configured")
	ErrSeriesNotFound   = errors.New("acquire: series not found")
	ErrIndexerSearch    = errors.New("acquire: indexer search failed")
	ErrDownloadAdd      = errors.New("acquire: download client add failed")
)

// ClientSource supplies the current indexer and download client, read per use so
// a runtime settings change takes effect without a restart.
type ClientSource interface {
	Indexer() indexer.Indexer
	Download() download.Client
}

// TitleSource supplies a title's accepted name variants (satisfied by
// *catalog.Service).
type TitleSource interface {
	TitleVariants(ctx context.Context, providerID int64) ([]string, error)
}

// Config is the runtime configuration acquire reads (satisfied by
// *settings.Service). Every value is read per use, so a value that has a writer
// applies to the next sweep rather than the next restart — true today of the
// download category. The automation values have no writer until the Settings
// toggle (#102) lands, so for now they are whatever startup parsed.
type Config interface {
	DownloadCategory() string
	AutomationEnabled() bool
	PinDelayDefault() time.Duration
}

// Match is one series' search result: the ranked candidates plus the inputs a
// caller needs to act on them.
type Match struct {
	Series     db.Series
	Term       string
	Items      []domain.WantedItem
	Candidates []decide.Candidate
}

// Service runs search/decide/grab against the store and the live clients.
type Service struct {
	store   *store.Store
	clients ClientSource
	titles  TitleSource
	cfg     Config
	log     *slog.Logger
}

// New builds the service. A nil logger is tolerated so a caller that only needs
// the service to exist (the OpenAPI spec dump builds its routes with empty deps)
// cannot turn a missing logger into a panic inside a sweep.
func New(st *store.Store, clients ClientSource, titles TitleSource, cfg Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: st, clients: clients, titles: titles, cfg: cfg, log: log}
}

// MatchSeries loads a series with every wanted item and matches indexer releases
// against it.
func (s *Service) MatchSeries(ctx context.Context, id int64) (Match, error) {
	idx := s.clients.Indexer()
	if idx == nil {
		return Match{}, ErrNoIndexer
	}
	series, items, err := s.loadItems(ctx, id)
	if err != nil {
		return Match{}, err
	}
	return s.match(ctx, idx, series, items)
}

// loadItems reads a series and every wanted item belonging to it.
func (s *Service) loadItems(ctx context.Context, id int64) (db.Series, []domain.WantedItem, error) {
	series, err := s.store.Q.GetSeries(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Series{}, nil, ErrSeriesNotFound
	}
	if err != nil {
		return db.Series{}, nil, fmt.Errorf("load series %d: %w", id, err)
	}
	rows, err := s.store.Q.ListWantedItems(ctx, series.ID)
	if err != nil {
		return db.Series{}, nil, fmt.Errorf("load wanted items for series %d: %w", id, err)
	}
	items := make([]domain.WantedItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, domain.WantedItem{
			ID:     r.ID,
			Kind:   domain.WantedKind(r.Kind),
			Number: int(r.Number.Int64),
			Have:   r.Have == 1,
		})
	}
	return series, items, nil
}

// match takes the item list explicitly so a caller can narrow what is worth
// grabbing — the sweep passes a grab-state-filtered list — while reusing one
// matcher.
func (s *Service) match(ctx context.Context, idx indexer.Indexer, series db.Series, items []domain.WantedItem) (Match, error) {
	// Title variants (romaji/english/native) let the matcher accept releases that
	// use a different one of the series' names. Best-effort: fall back to the
	// stored title if the metadata lookup fails.
	variants := []string{series.Title}
	if series.AnilistID.Valid {
		if v, verr := s.titles.TitleVariants(ctx, series.AnilistID.Int64); verr == nil {
			variants = append(variants, v...)
		}
	}

	profile, err := s.profile(ctx, series)
	if err != nil {
		return Match{}, err
	}

	// Sanitized title first, then each variant as a zero-result fallback (#107):
	// a romaji term can be unsearchable even sanitized, and one extra request is
	// cheap next to reporting nothing.
	var releases []indexer.Release
	term := ""
	for _, t := range decide.SearchTerms(variants) {
		if term == "" {
			term = t
		}
		releases, err = idx.Search(ctx, indexer.Query{Term: t})
		if err != nil {
			return Match{}, fmt.Errorf("%w: %w", ErrIndexerSearch, err)
		}
		if len(releases) > 0 {
			term = t
			break
		}
	}

	return Match{
		Series: series,
		Term:   term,
		Items:  items,
		Candidates: decide.Match(items, variants, releases, profile,
			decide.MatchOpts{PinnedGroup: series.PinnedGroup.String}),
	}, nil
}

// profile loads the series' quality profile in the domain form decide scores against.
func (s *Service) profile(ctx context.Context, series db.Series) (domain.QualityProfile, error) {
	row, err := s.store.Q.GetQualityProfile(ctx, series.QualityProfileID)
	if err != nil {
		return domain.QualityProfile{}, fmt.Errorf("load quality profile %d: %w", series.QualityProfileID, err)
	}
	groups, err := s.store.Q.ListProfileGroups(ctx, row.ID)
	if err != nil {
		return domain.QualityProfile{}, fmt.Errorf("load profile groups for profile %d: %w", row.ID, err)
	}
	return profileFromRows(row, groups)
}
