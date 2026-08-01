// Package blocklist is the pipeline's failure memory: which release of a series
// already failed, so the sweep stops re-deriving the same doomed ranking (#118).
//
// Scope is deliberately per-series — a group whose encodes are broken everywhere
// is the quality profile's BlockedGroups, not this. The expiry escalates rather
// than blocking permanently on the first failure because the importer's failure
// paths fire for environmental reasons (a full disk, a restarted client, a ratio
// rule removing torrents) as often as release-specific ones, and those fail many
// grabs at once; only a repeat failure of the same release separates a dead
// release from a bad day.
package blocklist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// ErrNotFound reports an entry that is not this series', so the HTTP layer can
// 404 without the store leaking upwards.
var ErrNotFound = errors.New("blocklist: entry not found")

// The escalation ladder. A third failure blocks permanently.
const (
	firstBlock  = 24 * time.Hour
	secondBlock = 7 * 24 * time.Hour
)

// blockDuration is how long the nth failure of one release blocks it; zero means
// permanently.
func blockDuration(failures int) time.Duration {
	switch {
	case failures <= 1:
		return firstBlock
	case failures == 2:
		return secondBlock
	default:
		return 0
	}
}

// Service records and reads blocklist entries. One instance serves the whole
// daemon: the breaker's view of client health is only right if every failure
// path passes through it.
type Service struct {
	store   *store.Store
	log     *slog.Logger
	now     func() time.Time
	breaker breaker
}

// New builds the service. A nil logger is tolerated, as elsewhere in core.
func New(st *store.Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: st, log: log, now: time.Now}
}

// Record blocks a release for this series, escalating if it has failed before,
// and reports whether it recorded anything. itemIDs are the wanted items the
// release covered — the breaker's evidence, and the reason a caller passes them
// even though the entry is per series.
//
// A false return is the breaker: too many distinct items have failed too
// recently for this to be the release's fault, so nothing is written. A caller
// must also skip whatever it would do to act on a fresh failure — the evidence
// that justified it is what the breaker just rejected.
//
// The two results are independent, and a caller should honour both: a true with
// an error means the entry was written but its expiry may still be the first
// rung rather than the one the ladder owed it. Acting on the failure is then
// still correct; only the block is shorter than it should be, and the next
// failure re-derives it.
func (s *Service) Record(ctx context.Context, seriesID int64, itemIDs []int64, infoHash, releaseTitle, reason string) (bool, error) {
	normalized := decide.NormalizeReleaseTitle(releaseTitle)
	if normalized == "" {
		return false, fmt.Errorf("blocklist: refusing to record an empty release title for series %d", seriesID)
	}
	now := s.now()
	ref := releaseRef{seriesID: seriesID, normalized: normalized}
	if !s.breaker.observe(ref, itemIDs, now) {
		st := s.breaker.state(now)
		s.log.Warn("blocklist: too many items failing at once to blame the releases; not remembering this one",
			"series", seriesID, "release", releaseTitle, "items_failed", st.Items, "window", st.Window)
		return false, nil
	}

	entry, err := s.store.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
		SeriesID:        seriesID,
		InfoHash:        infoHash,
		ReleaseTitle:    releaseTitle,
		NormalizedTitle: normalized,
		Reason:          reason,
		BlockedUntil:    expiry(blockDuration(1), now),
	})
	if err != nil {
		return false, fmt.Errorf("record blocklist entry for series %d: %w", seriesID, err)
	}
	// The upsert reports the resulting count only after writing, so a repeat
	// failure needs a second write to move up the ladder; a first one is already
	// at firstBlock.
	if entry.Failures <= 1 {
		return true, nil
	}
	if err := s.store.Q.SetBlocklistExpiry(ctx, db.SetBlocklistExpiryParams{
		BlockedUntil: expiry(blockDuration(int(entry.Failures)), now), ID: entry.ID,
	}); err != nil {
		return true, fmt.Errorf("set blocklist expiry for entry %d: %w", entry.ID, err)
	}
	return true, nil
}

// BreakerState reports whether failure memory is currently being suppressed.
func (s *Service) BreakerState() BreakerState {
	return s.breaker.state(s.now())
}

// Active lists the entries blocking a release right now.
func (s *Service) Active(ctx context.Context, seriesID int64) ([]db.ReleaseBlocklist, error) {
	return s.store.Q.ListActiveBlocklist(ctx, db.ListActiveBlocklistParams{
		SeriesID:     seriesID,
		BlockedUntil: sql.NullString{String: store.FormatTimestamp(s.now()), Valid: true},
	})
}

// List returns every entry for a series, expired ones included: an expired entry
// still carries the failure count, and the UI shows it as history.
func (s *Service) List(ctx context.Context, seriesID int64) ([]db.ReleaseBlocklist, error) {
	return s.store.Q.ListBlocklistBySeries(ctx, seriesID)
}

// Clear unblocks one entry, scoped to its series.
func (s *Service) Clear(ctx context.Context, seriesID, entryID int64) error {
	rows, err := s.store.Q.DeleteBlocklistEntry(ctx, db.DeleteBlocklistEntryParams{
		ID: entryID, SeriesID: seriesID,
	})
	if err != nil {
		return fmt.Errorf("delete blocklist entry %d: %w", entryID, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearSeries forgets every remembered release for one series, and reports how
// many it forgot. Every user-initiated clear discards failure counts, the
// single-entry Clear included: unblocking says the block was wrong, and a wrong
// block's place on the ladder is wrong with it.
func (s *Service) ClearSeries(ctx context.Context, seriesID int64) (int64, error) {
	rows, err := s.store.Q.DeleteBlocklistBySeries(ctx, seriesID)
	if err != nil {
		return 0, fmt.Errorf("clear blocklist for series %d: %w", seriesID, err)
	}
	return rows, nil
}

// ClearAll forgets every remembered release across the library and closes the
// breaker: this is the recovery action for an environmental fault, which does
// not respect series boundaries, and the operator who fixed the fault should
// not then have to wait out a window.
func (s *Service) ClearAll(ctx context.Context) (int64, error) {
	rows, err := s.store.Q.DeleteAllBlocklist(ctx)
	if err != nil {
		return 0, fmt.Errorf("clear the blocklist: %w", err)
	}
	s.breaker.reset()
	return rows, nil
}

// Summary counts what is being skipped right now, and across how many series.
func (s *Service) Summary(ctx context.Context) (db.CountActiveBlocklistRow, error) {
	row, err := s.store.Q.CountActiveBlocklist(ctx,
		sql.NullString{String: store.FormatTimestamp(s.now()), Valid: true})
	if err != nil {
		return db.CountActiveBlocklistRow{}, fmt.Errorf("count the blocklist: %w", err)
	}
	return row, nil
}

// ClearExpired forgets only the lapsed entries, leaving what still blocks. It
// discards their failure counts, so a release cleared this way starts the
// escalation ladder over — which is the point: the counts that survive an
// environmental fault are the ones worth discarding.
func (s *Service) ClearExpired(ctx context.Context, seriesID int64) (int64, error) {
	rows, err := s.store.Q.DeleteExpiredBlocklistBySeries(ctx, db.DeleteExpiredBlocklistBySeriesParams{
		SeriesID:     seriesID,
		BlockedUntil: sql.NullString{String: store.FormatTimestamp(s.now()), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("clear expired blocklist for series %d: %w", seriesID, err)
	}
	return rows, nil
}

// expiry renders a block duration as a stored timestamp; a zero duration is
// permanent, which the schema spells NULL.
func expiry(d time.Duration, now time.Time) sql.NullString {
	if d <= 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: store.FormatTimestamp(now.Add(d)), Valid: true}
}
