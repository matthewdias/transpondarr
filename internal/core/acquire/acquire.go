// Package acquire owns search, decide, and grab, shared by the manual HTTP
// routes, the scheduled sweep and the feed poll so all three drive exactly one
// matcher.
//
// The two automatic entry points divide by what they can see. The feed poll
// (#101) owns releases published while we are watching: one request lists an
// indexer's newest releases, so it is flat in library size and cheap enough to
// run on a short tick. The sweep (#100) owns what already existed before we
// looked — back-catalog, anything that scrolled off the feed page, a series
// added after an entry was seen — and owns everything when no feed is
// configured, at one search per series.
//
// Cadence follows that division: with a feed configured the sweep stands down
// from the airing window and sleeps on its backoff. Grab scope deliberately does
// not. A sweep search that turns up a current release still takes it, because
// the feed's dedupe is one-shot — an entry seen before its series or item
// existed never comes around again, and the sweep is the only thing that rescues
// it.
//
// Two automatic grabs never take the same item, by two mechanisms rather than
// one. An in-process claim over wanted-item ids stops them overlapping, and a
// re-read of grab state under that claim stops the later one acting on a list it
// loaded before the other had finished — the gap the claim alone leaves, since a
// pass reads its items, goes out on the network for seconds, and grabs after.
//
// A manual grab is outside both, deliberately: it takes its claim
// unconditionally and never re-checks, because explicit user intent is never
// refused (PR #57). So a manual grab racing automation, or another manual grab,
// still duplicates exactly as double-clicking Grab always has — and converges on
// the download client's info-hash dedupe.
package acquire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/notify"
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

// ClientSource supplies the current indexer, download client, and notification
// dispatcher, read per use so a runtime settings change takes effect without a
// restart.
type ClientSource interface {
	Indexer() indexer.Indexer
	Download() download.Client
	Notify() *notify.Dispatcher
}

// TitleSource supplies a title's accepted name variants (satisfied by
// *catalog.Service).
type TitleSource interface {
	TitleVariants(ctx context.Context, providerID int64) ([]string, error)
}

// CachedTitleSource is an optional TitleSource capability: variants answered from
// the local metadata cache, spending no provider request (satisfied by
// *catalog.Service).
type CachedTitleSource interface {
	CachedTitleVariants(ctx context.Context, providerID int64) ([]string, bool, error)
}

// Recorder remembers a release automation could not use, so it is not re-ranked
// first next pass. Satisfied by *blocklist.Service, which must be the instance
// the importer holds: false means its breaker refused to blame the release.
type Recorder interface {
	Record(ctx context.Context, seriesID int64, itemIDs []int64, infoHash, releaseTitle, reason string) (bool, error)
}

// Config is the runtime configuration acquire reads (satisfied by
// *settings.Service). Every value is read per use, so a Settings edit applies to
// the next sweep rather than the next restart.
type Config interface {
	DownloadCategory() string
	// AutomationEnabled gates whether unattended work runs at all; NotifyOnly
	// makes a run that does happen rehearse (#116): evaluate, notify, never grab.
	AutomationEnabled() bool
	NotifyOnly() bool
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

// passItem is where an entry point states candidacy for itself: the stored item,
// whose Have is the library's answer, beside the pass's own grabbable. This is
// the one place the two may sit together — deriving either from the other
// anywhere else is exactly the conflation this type exists to end (#97).
type passItem struct {
	domain.WantedItem
	grabbable bool
	heldTitle string
}

// matchItems is the matcher's view of a pass: numbering basis plus candidacy.
func matchItems(items []passItem) []decide.Item {
	out := make([]decide.Item, 0, len(items))
	for _, it := range items {
		out = append(out, decide.Item{Number: it.Number, Grabbable: it.grabbable, HeldTitle: it.heldTitle})
	}
	return out
}

// wantedItems drops the pass's candidacy, leaving what a grab resolves ids from.
func wantedItems(items []passItem) []domain.WantedItem {
	out := make([]domain.WantedItem, 0, len(items))
	for _, it := range items {
		out = append(out, it.WantedItem)
	}
	return out
}

// Service runs search/decide/grab against the store and the live clients.
type Service struct {
	store     *store.Store
	clients   ClientSource
	titles    TitleSource
	cfg       Config
	log       *slog.Logger
	blocklist Recorder
	claims    *claims
}

// New builds the service. A nil logger is tolerated so a caller that only needs
// the service to exist (the OpenAPI spec dump builds its routes with empty deps)
// cannot turn a missing logger into a panic inside a sweep; a nil recorder just
// means nothing is remembered.
func New(st *store.Store, clients ClientSource, titles TitleSource, cfg Config, log *slog.Logger, blocklist Recorder) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: st, clients: clients, titles: titles, cfg: cfg, log: log,
		blocklist: blocklist, claims: newClaims()}
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

// loadItems reads a series and every wanted item belonging to it. Nothing
// withholds an item from a manual pass: an item we hold is offered as an
// upgrade, since profiles inform manual actions and gate only automation
// (PR #57). A held item with no recorded release matches as a plain one.
func (s *Service) loadItems(ctx context.Context, id int64) (db.Series, []passItem, error) {
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
	items := make([]passItem, 0, len(rows))
	for _, r := range rows {
		it := passItem{
			WantedItem: domain.WantedItem{
				ID:     r.ID,
				Kind:   domain.WantedKind(r.Kind),
				Number: int(r.Number.Int64),
				Have:   r.Have == 1,
			},
			grabbable: true,
		}
		if it.Have {
			it.heldTitle = r.HeldReleaseTitle
		}
		items = append(items, it)
	}
	return series, items, nil
}

// match takes the item list explicitly so a caller can scope what is worth
// grabbing — the sweep marks in-flight and unaired items non-candidates — while
// reusing one matcher.
func (s *Service) match(ctx context.Context, idx indexer.Indexer, series db.Series, items []passItem) (Match, error) {
	variants := s.variants(ctx, series)
	releases, term, err := s.search(ctx, idx, variants)
	if err != nil {
		return Match{}, err
	}
	return s.evaluate(ctx, series, items, variants, term, releases)
}

// variants are the names the matcher will accept for a series (romaji/english/
// native), so a release using a different one of them still matches.
// Best-effort: fall back to the stored title if the metadata lookup fails.
func (s *Service) variants(ctx context.Context, series db.Series) []string {
	variants := []string{series.Title}
	if series.AnilistID.Valid {
		if v, err := s.titles.TitleVariants(ctx, series.AnilistID.Int64); err == nil {
			variants = append(variants, v...)
		}
	}
	return variants
}

// cachedVariants is variants for the unbounded feed path (#139). A missing
// capability or snapshot degrades to the stored title — never to the fetching
// path, which would silently reintroduce the unbounded provider spend.
func (s *Service) cachedVariants(ctx context.Context, series db.Series) []string {
	variants := []string{series.Title}
	if !series.AnilistID.Valid {
		return variants
	}
	if src, ok := s.titles.(CachedTitleSource); ok {
		v, hit, err := src.CachedTitleVariants(ctx, series.AnilistID.Int64)
		switch {
		case err != nil:
			s.log.Debug("cached title variants unreadable; matching on the stored title alone",
				"series", series.ID, "err", err)
		case hit:
			variants = append(variants, v...)
		}
	}
	return variants
}

// search asks the indexer for one series, reporting the term that answered.
// Sanitized title first, then each variant as a zero-result fallback (#107): a
// romaji term can be unsearchable even sanitized, and one extra request is cheap
// next to reporting nothing.
func (s *Service) search(ctx context.Context, idx indexer.Indexer, variants []string) ([]indexer.Release, string, error) {
	var releases []indexer.Release
	term := ""
	for _, t := range decide.SearchTerms(variants) {
		if term == "" {
			term = t
		}
		var err error
		releases, err = idx.Search(ctx, indexer.Query{Term: t})
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrIndexerSearch, err)
		}
		if len(releases) > 0 {
			term = t
			break
		}
	}
	return releases, term, nil
}

// evaluate ranks already-fetched releases against a series. It is split from the
// search so the feed poll (#101) drives exactly this decision layer — profile,
// blocklist, eligibility — over one page it fetched once for every series.
func (s *Service) evaluate(ctx context.Context, series db.Series, items []passItem, variants []string, term string, releases []indexer.Release) (Match, error) {
	profile, err := s.profile(ctx, series)
	if err != nil {
		return Match{}, err
	}
	blocked, err := s.blocked(ctx, series.ID)
	if err != nil {
		return Match{}, err
	}

	return Match{
		Series: series,
		Term:   term,
		Items:  wantedItems(items),
		Candidates: decide.Match(matchItems(items), variants, releases, profile,
			decide.MatchOpts{PinnedGroup: series.PinnedGroup.String, Blocked: blocked}),
	}, nil
}

// blocked loads the series' active release blocklist. Read off the store rather
// than blocklist.Service so acquire need not depend on the package that writes it.
func (s *Service) blocked(ctx context.Context, seriesID int64) (decide.BlockedSet, error) {
	rows, err := s.store.Q.ListActiveBlocklist(ctx, db.ListActiveBlocklistParams{
		SeriesID:     seriesID,
		BlockedUntil: sql.NullString{String: store.FormatTimestamp(time.Now()), Valid: true},
	})
	if err != nil {
		return decide.BlockedSet{}, fmt.Errorf("load blocklist for series %d: %w", seriesID, err)
	}
	if len(rows) == 0 {
		return decide.BlockedSet{}, nil
	}
	set := decide.BlockedSet{
		Hashes: make(map[string]string, len(rows)),
		Titles: make(map[string]string, len(rows)),
	}
	for _, r := range rows {
		reason := "release previously failed: " + r.Reason
		if h := strings.ToLower(strings.TrimSpace(r.InfoHash)); h != "" {
			set.Hashes[h] = reason
		}
		set.Titles[r.NormalizedTitle] = reason
	}
	return set, nil
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
