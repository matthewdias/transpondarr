// Package browse serves seasonal discovery charts from a per-season cache. A
// chart is a ~50-title query result refreshed by a job on the runner, never on
// a page view — a view reads the cache even when stale, and only a season
// nobody has ever cached pays for a live fetch, once.
package browse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seasonsPerPass bounds how much of the request budget one refresh pass can
// spend: a season costs one paged query, and the budget is shared with the
// metadata and airing jobs.
const seasonsPerPass = 2

const (
	activeSeasonTTL = 6 * time.Hour       // current/upcoming: scores, statuses and next airings still move
	pastSeasonTTL   = 30 * 24 * time.Hour // settled charts drift slowly
)

// Service reads and refreshes the per-season chart cache.
type Service struct {
	store    *store.Store
	provider metadata.Provider
	log      *slog.Logger
}

// New builds a Service over the shared provider. Sharing matters: the provider
// carries the rate limiter, so a private instance would double the request rate.
func New(st *store.Store, provider metadata.Provider, log *slog.Logger) *Service {
	return &Service{store: st, provider: provider, log: log}
}

// ProviderName is the id space every chart entry is numbered in, so callers can
// pair it with a ProviderID without hardcoding one.
func (s *Service) ProviderName() string { return s.provider.Name() }

// Season returns a season's chart, from cache whatever its age — staleness is
// the refresh job's problem, not the page view's. Only a never-cached season is
// fetched live. A provider that cannot browse yields an empty chart, not an
// error.
func (s *Service) Season(ctx context.Context, season metadata.Season, year int) ([]metadata.SeasonEntry, error) {
	row, err := s.store.Q.GetSeasonCache(ctx, db.GetSeasonCacheParams{
		Provider: s.provider.Name(),
		Season:   string(season),
		Year:     int64(year),
	})
	if err == nil {
		var entries []metadata.SeasonEntry
		// A malformed row degrades to a re-fetch rather than a hard failure.
		if json.Unmarshal([]byte(row.Raw), &entries) == nil {
			return entries, nil
		}
	}

	prov, ok := s.provider.(metadata.BrowseProvider)
	if !ok {
		return nil, nil
	}
	entries, err := prov.BrowseSeason(ctx, season, year)
	if err != nil {
		return nil, fmt.Errorf("browse %s %d: %w", season, year, err)
	}
	// Best-effort: the view still serves what it fetched.
	if err := s.put(ctx, season, year, entries); err != nil {
		s.log.Warn("season cache write failed", "season", season, "year", year, "err", err)
	}
	return entries, nil
}

// Entry is one chart row as the discovery page sees it: the provider snapshot
// plus this library's view of the same title.
type Entry struct {
	metadata.SeasonEntry
	SeriesID int64
	Tracked  bool
}

// Chart is Season enriched with the tracked-series overlay: an entry already in
// the library is marked as such, and once the airing job has synced it, local
// wanted_items.airs_at replaces the snapshot's countdown — same AniList fact,
// fresher schedule. A never-synced series has no local truth yet, so its
// snapshot stands.
func (s *Service) Chart(ctx context.Context, season metadata.Season, year int) ([]Entry, error) {
	entries, err := s.Season(ctx, season, year)
	if err != nil {
		return nil, err
	}
	// Scoped to this provider's id space: a chart entry's id only means anything
	// against a series numbered in the same one.
	rows, err := s.store.Q.ListTrackedNextAiring(ctx, db.ListTrackedNextAiringParams{
		AirsAt:   sql.NullString{String: store.FormatTimestamp(time.Now()), Valid: true},
		Provider: sql.NullString{String: s.provider.Name(), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list tracked next airing: %w", err)
	}
	tracked := make(map[int64]db.ListTrackedNextAiringRow, len(rows))
	for _, row := range rows {
		tracked[row.ProviderID.Int64] = row
	}

	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		entry := Entry{SeasonEntry: e}
		if row, ok := tracked[e.ProviderID]; ok {
			entry.Tracked = true
			entry.SeriesID = row.ID
			if row.AiringSyncedAt.Valid {
				entry.NextAiring = localAiring(row)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// localAiring converts an overlay row's next scheduled item; nil when nothing
// is upcoming or the stored timestamp is unreadable.
func localAiring(row db.ListTrackedNextAiringRow) *metadata.Airing {
	if !row.NextAirsAt.Valid {
		return nil
	}
	airsAt, err := store.ParseTimestamp(row.NextAirsAt.String)
	if err != nil {
		return nil
	}
	return &metadata.Airing{Number: int(row.NextNumber.Int64), AirsAt: airsAt}
}

// RefreshOnce re-fetches every season due a refresh, and is what the job runner
// calls. One season's error never costs the rest their refresh.
func (s *Service) RefreshOnce(ctx context.Context) error {
	prov, ok := s.provider.(metadata.BrowseProvider)
	if !ok {
		return nil
	}
	due, err := s.due(ctx)
	if err != nil {
		return fmt.Errorf("list cached seasons: %w", err)
	}

	var errs []error
	for _, ref := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, err := prov.BrowseSeason(ctx, ref.season, ref.year)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %d: fetch season: %w", ref.season, ref.year, err))
			continue
		}
		if err := s.put(ctx, ref.season, ref.year, entries); err != nil {
			errs = append(errs, fmt.Errorf("%s %d: store season: %w", ref.season, ref.year, err))
			continue
		}
		s.log.Debug("season chart refreshed", "season", ref.season, "year", ref.year, "entries", len(entries))
	}
	return errors.Join(errs...)
}

type seasonRef struct {
	season metadata.Season
	year   int
}

// due lists the seasons worth spending budget on this pass: the current season
// when missing or stale (always first — it is the acceptance-critical one),
// then any cached season past its TTL.
func (s *Service) due(ctx context.Context) ([]seasonRef, error) {
	rows, err := s.store.Q.ListSeasonCache(ctx, s.provider.Name())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	curSeason, curYear := CurrentSeason(now)

	var due []seasonRef
	haveCurrent := false
	for _, row := range rows {
		ref := seasonRef{metadata.Season(row.Season), int(row.Year)}
		fetchedAt, parseErr := store.ParseTimestamp(row.FetchedAt)
		stale := parseErr != nil || now.Sub(fetchedAt) >= ttl(ref, curSeason, curYear)
		if ref.season == curSeason && ref.year == curYear {
			haveCurrent = true
			if stale {
				due = append([]seasonRef{ref}, due...)
			}
			continue
		}
		if stale {
			due = append(due, ref)
		}
	}
	if !haveCurrent {
		due = append([]seasonRef{{curSeason, curYear}}, due...)
	}
	if len(due) > seasonsPerPass {
		due = due[:seasonsPerPass]
	}
	return due, nil
}

func (s *Service) put(ctx context.Context, season metadata.Season, year int, entries []metadata.SeasonEntry) error {
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return s.store.Q.UpsertSeasonCache(ctx, db.UpsertSeasonCacheParams{
		Provider: s.provider.Name(),
		Season:   string(season),
		Year:     int64(year),
		Raw:      string(raw),
	})
}

// ttl paces a season by where it sits relative to now: a past season's chart is
// effectively settled, while the current and upcoming ones still move.
func ttl(ref seasonRef, curSeason metadata.Season, curYear int) time.Duration {
	if ref.year < curYear || (ref.year == curYear && seasonIndex(ref.season) < seasonIndex(curSeason)) {
		return pastSeasonTTL
	}
	return activeSeasonTTL
}

func seasonIndex(s metadata.Season) int {
	switch s {
	case metadata.SeasonWinter:
		return 0
	case metadata.SeasonSpring:
		return 1
	case metadata.SeasonSummer:
		return 2
	default:
		return 3
	}
}

// CurrentSeason maps a moment to the season convention AniList uses:
// Jan-Mar WINTER, Apr-Jun SPRING, Jul-Sep SUMMER, Oct-Dec FALL.
func CurrentSeason(now time.Time) (metadata.Season, int) {
	switch m := now.Month(); {
	case m <= time.March:
		return metadata.SeasonWinter, now.Year()
	case m <= time.June:
		return metadata.SeasonSpring, now.Year()
	case m <= time.September:
		return metadata.SeasonSummer, now.Year()
	default:
		return metadata.SeasonFall, now.Year()
	}
}
