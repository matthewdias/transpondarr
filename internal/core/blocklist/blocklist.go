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

// Service records and reads blocklist entries.
type Service struct {
	store *store.Store
	log   *slog.Logger
}

// New builds the service. A nil logger is tolerated, as elsewhere in core.
func New(st *store.Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: st, log: log}
}

// Record blocks a release for this series, escalating if it has failed before.
// The expiry is computed from the resulting failure count, so the write is one
// round trip and two concurrent records cannot both read "1".
func (s *Service) Record(ctx context.Context, seriesID int64, infoHash, releaseTitle, reason string) error {
	normalized := decide.NormalizeReleaseTitle(releaseTitle)
	if normalized == "" {
		return fmt.Errorf("blocklist: refusing to record an empty release title for series %d", seriesID)
	}
	entry, err := s.store.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
		SeriesID:        seriesID,
		InfoHash:        infoHash,
		ReleaseTitle:    releaseTitle,
		NormalizedTitle: normalized,
		Reason:          reason,
		BlockedUntil:    expiry(blockDuration(1), time.Now()),
	})
	if err != nil {
		return fmt.Errorf("record blocklist entry for series %d: %w", seriesID, err)
	}
	// The upsert cannot know the resulting count before it writes, so the ladder
	// is applied in a second write once the row reports what it became.
	want := expiry(blockDuration(int(entry.Failures)), time.Now())
	if want == entry.BlockedUntil {
		return nil
	}
	if err := s.store.Q.SetBlocklistExpiry(ctx, db.SetBlocklistExpiryParams{
		BlockedUntil: want, ID: entry.ID,
	}); err != nil {
		return fmt.Errorf("set blocklist expiry for entry %d: %w", entry.ID, err)
	}
	return nil
}

// Active lists the entries blocking a release right now.
func (s *Service) Active(ctx context.Context, seriesID int64) ([]db.ReleaseBlocklist, error) {
	return s.store.Q.ListActiveBlocklist(ctx, db.ListActiveBlocklistParams{
		SeriesID:     seriesID,
		BlockedUntil: sql.NullString{String: store.FormatTimestamp(time.Now()), Valid: true},
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

// expiry renders a block duration as a stored timestamp; a zero duration is
// permanent, which the schema spells NULL.
func expiry(d time.Duration, now time.Time) sql.NullString {
	if d <= 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: store.FormatTimestamp(now.Add(d)), Valid: true}
}
