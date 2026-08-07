// Package catalog is the entry point to Transpondarr's tracked-title collection.
// It ties a metadata.Provider to the store: searching for titles and adding one
// (the first write path in the app), which materializes a Title plus its
// WantedItems by identity-by-construction.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

var (
	ErrAlreadyExists = errors.New("catalog: series already exists")
	// A row keyed on a provider nothing can read would be unrefreshable and
	// unsearchable, so the pair is rejected at the door rather than persisted.
	ErrUnknownProvider = errors.New("catalog: unknown metadata provider")
)

type Service struct {
	store    *store.Store
	provider metadata.Provider
}

func NewService(st *store.Store, provider metadata.Provider) *Service {
	return &Service{store: st, provider: provider}
}

func (s *Service) Search(ctx context.Context, term string) ([]metadata.Candidate, error) {
	return s.provider.Search(ctx, term)
}

// ProviderName is the id space every candidate this service returns is numbered
// in, so callers can pair it with a ProviderID without hardcoding one.
func (s *Service) ProviderName() string { return s.provider.Name() }

// TitleVariants returns the accepted display-name variants (romaji/english/
// native) for a title, used by the decide layer to filter releases that use a
// different one of a series' names. It is cache-backed via the provider.
func (s *Service) TitleVariants(ctx context.Context, providerID int64) ([]string, error) {
	meta, _, err := s.provider.GetTitle(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return dedupeNonEmpty(meta.Titles.Romaji, meta.Titles.English, meta.Titles.Native), nil
}

// CachedTitleVariants is TitleVariants answered only from the metadata cache;
// ok=false (an unwrapped provider, or no snapshot) costs no provider request.
func (s *Service) CachedTitleVariants(ctx context.Context, providerID int64) ([]string, bool, error) {
	reader, ok := s.provider.(metadata.CachedTitleReader)
	if !ok {
		return nil, false, nil
	}
	meta, _, hit, err := reader.TitleFromCache(ctx, providerID)
	if err != nil || !hit {
		return nil, false, err
	}
	return dedupeNonEmpty(meta.Titles.Romaji, meta.Titles.English, meta.Titles.Native), true, nil
}

func dedupeNonEmpty(vals ...string) []string {
	seen := make(map[string]bool, len(vals))
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// AddSeries fetches a title's metadata by provider identity and persists it
// together with its expanded WantedItems in a single transaction. It is
// idempotent-ish: a title already tracked returns ErrAlreadyExists rather than a
// duplicate. Deduping is per id space — the same title known to two providers is
// two rows until the cross-reference layer (#189) can relate them.
func (s *Service) AddSeries(ctx context.Context, provider string, providerID int64, monitored bool) (domain.Title, error) {
	if provider != s.provider.Name() {
		return domain.Title{}, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}

	identity := db.GetSeriesByProviderIDParams{
		Provider:   sql.NullString{String: provider, Valid: true},
		ProviderID: sql.NullInt64{Int64: providerID, Valid: true},
	}
	if _, err := s.store.Q.GetSeriesByProviderID(ctx, identity); err == nil {
		return domain.Title{}, ErrAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.Title{}, fmt.Errorf("check existing series: %w", err)
	}

	meta, items, err := s.provider.GetTitle(ctx, providerID)
	if err != nil {
		return domain.Title{}, fmt.Errorf("fetch metadata: %w", err)
	}

	format := meta.Format
	name := meta.Titles.Preferred()

	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.Title{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)

	srow, err := q.CreateSeries(ctx, db.CreateSeriesParams{
		Provider:   identity.Provider,
		ProviderID: identity.ProviderID,
		Title:      name,
		Format:     string(format),
		Monitored:  boolToInt(monitored),
	})
	if err != nil {
		return domain.Title{}, fmt.Errorf("create series: %w", err)
	}

	title := domain.Title{
		ID:         srow.ID,
		Provider:   provider,
		ProviderID: providerID,
		Name:       name,
		Format:     format,
		Monitored:  monitored,
	}
	for _, it := range items {
		wrow, err := q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID:  srow.ID,
			Kind:      string(domain.KindEpisode),
			Number:    sql.NullInt64{Int64: int64(it.Number), Valid: true},
			Title:     nullString(it.Name),
			InLibrary: 0,
		})
		if err != nil {
			return domain.Title{}, fmt.Errorf("create wanted item %d: %w", it.Number, err)
		}
		title.Items = append(title.Items, domain.WantedItem{
			ID:     wrow.ID,
			Kind:   domain.KindEpisode,
			Number: it.Number,
			Name:   it.Name,
		})
	}

	if err := tx.Commit(); err != nil {
		return domain.Title{}, fmt.Errorf("commit: %w", err)
	}
	return title, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
