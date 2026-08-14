package acquire

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// maxFeedMarkIDs bounds what one mark remembers. A page is ~100 entries, so this
// only ever trips on a feed that publishes no dates at all and pages far deeper.
const maxFeedMarkIDs = 500

// feedGapAiredSlack widens the recovery window back past the mark: a rip is
// published after its episode airs, so an item that aired shortly before the
// mark can still have fallen through the gap.
const feedGapAiredSlack = time.Hour

// feedMark is what a poll remembers about the last one: the newest publish time
// it saw and the entries carrying it. Sonarr stores the same pair per indexer
// (LastRssSyncReleaseInfo) — the timestamp answers "how far did we get", and the
// ids settle the ties it cannot, since a batch of releases shares a second.
type feedMark struct {
	Latest time.Time `json:"latest,omitempty"`
	IDs    []string  `json:"ids,omitempty"`
}

// feedMarkKey namespaces the mark by indexer name. There is one Torznab endpoint
// today, but a second source must not inherit the first's seen set (#128).
func feedMarkKey(indexerName string) string { return "feed.seen." + indexerName }

// PollFeedOnce takes the indexer's newest releases and grabs what any monitored
// title wants, and is what the job runner calls. It is only a cheaper trigger
// than the sweep: eligibility is the sweep's, because both drive grabPass over a
// Match built the same way. The clients and the kill switch are read per run, so
// a Settings edit takes effect on the next tick — except on a hand-triggered run,
// which passes the kill switch as explicit intent (#122).
//
// An indexer with no recent feed is a supported configuration, not a failure:
// the scheduled sweep already covers those title, just less promptly.
func (s *Service) PollFeedOnce(ctx context.Context) error {
	// Gated jobs are mirrored in the UI's AUTOMATION_GATED list (jobs.tsx).
	if !s.cfg.AutomationEnabled() && !jobs.ManualRun(ctx) {
		return nil
	}
	idx := s.clients.Indexer()
	if idx == nil || s.clients.Download() == nil {
		return nil
	}
	feed, ok := idx.(indexer.RecentFeed)
	if !ok {
		s.log.Debug("indexer publishes no recent feed; the scheduled sweep covers it",
			"indexer", idx.Name())
		return nil
	}

	entries, err := feed.Recent(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndexerSearch, err)
	}
	if len(entries) == 0 {
		return nil
	}

	mark, err := s.loadFeedMark(ctx, idx.Name())
	if err != nil {
		return err
	}
	fresh := unseenEntries(entries, mark)
	// Recognising nothing means the mark scrolled off the page: the feed moved
	// further than one page between polls, so whatever aired in between is the
	// sweep's to find — and with a feed configured the sweep no longer aims at
	// the airing window, leaving its backoff ladder to reach it up to a day
	// later. The poll knows when coverage was lost, so it recovers (#140).
	// A gap means every entry is fresh, so the quiet-feed return below is never
	// this path.
	gap := !mark.Latest.IsZero() && len(fresh) == len(entries)
	// The whole point of the mark: a quiet feed costs one request and nothing else.
	if len(fresh) == 0 {
		return s.saveFeedMark(ctx, idx.Name(), advanceFeedMark(mark, nextFeedMark(entries)))
	}

	releases := make([]indexer.Release, 0, len(fresh))
	for _, e := range fresh {
		releases = append(releases, e.Release)
	}
	// Recovery runs after the page is processed, so anything this poll just
	// grabbed has settled its item and drops out of the reset set.
	polled := s.pollTitle(ctx, releases)
	var recovered error
	if gap {
		recovered = s.recoverFeedGap(ctx, idx.Name(), mark.Latest, len(entries))
	}
	// The mark advances even when a title failed: those entries were seen, and
	// re-processing the page would not fix whatever broke.
	return errors.Join(
		polled, recovered,
		s.saveFeedMark(ctx, idx.Name(), advanceFeedMark(mark, nextFeedMark(entries))),
	)
}

// recoverFeedGap puts the sweep back on the title whose broadcast happened
// while the feed was scrolling past us. The set is bounded to one sweep pass'
// worth of title and ordered furthest-postponed first: the gap fires routinely
// on a busy aggregating indexer, so resetting everything would queue more
// searches than the sweep can spend. A failed reset still lets the mark advance
// — the sweep's ladder remains the fallback it already was.
func (s *Service) recoverFeedGap(ctx context.Context, indexerName string, since time.Time, page int) error {
	now := time.Now()
	stale, err := s.store.Q.ListBackedOffTitlesWantedInWindow(ctx,
		db.ListBackedOffTitlesWantedInWindowParams{
			NextSearchAt: sql.NullString{String: store.FormatTimestamp(now), Valid: true},
			AirsAt:       sql.NullString{String: store.FormatTimestamp(since.Add(-feedGapAiredSlack)), Valid: true},
			AirsAt_2:     sql.NullString{String: store.FormatTimestamp(now), Valid: true},
			Limit:        titlesPerPass,
		})
	if err != nil {
		return fmt.Errorf("list series that aired inside a feed gap: %w", err)
	}

	var errs []error
	reset := 0
	for _, title := range stale {
		if ctx.Err() != nil {
			break
		}
		if err := s.store.Q.ResetTitleSearchState(ctx, title.ID); err != nil {
			errs = append(errs, fmt.Errorf("reset series %d after a feed gap: %w", title.ID, err))
			continue
		}
		reset++
	}
	if reset > 0 {
		s.log.Warn("the recent feed moved more than one page between polls; series that aired inside the gap go back to the front of the sweep",
			"indexer", indexerName, "since", since, "poll_shorter_than", page, "reset", reset)
	} else {
		s.log.Warn("the recent feed moved more than one page between polls; nothing qualified for a reset, so anything missed waits for the sweep's backoff",
			"indexer", indexerName, "since", since, "poll_shorter_than", page)
	}
	return errors.Join(errs...)
}

// pollTitle matches one already-fetched page against every title with
// something wanted. This is the inverse of the sweep's lookup, so it is title ×
// entry rather than one search per title — deliberately unoptimised, because a
// page is ~100 entries and the due query already drops any title with nothing
// left to grab. One title' failure never costs the rest their pass.
func (s *Service) pollTitle(ctx context.Context, releases []indexer.Release) error {
	now := time.Now()
	due, err := s.store.Q.ListTitlesWithWantedItems(ctx,
		sql.NullString{String: store.FormatTimestamp(now), Valid: true})
	if err != nil {
		return fmt.Errorf("list series with wanted items: %w", err)
	}

	var errs []error
	for _, title := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.pollOneTitle(ctx, title, releases, now); err != nil {
			errs = append(errs, fmt.Errorf("series %d: %w", title.ID, err))
		}
	}
	return errors.Join(errs...)
}

// pollOneTitle runs the shared decision path over the feed page. It writes no
// search cadence: nothing was searched, and a grab settles its item, so the
// sweep's due query drops the title on its own.
func (s *Service) pollOneTitle(ctx context.Context, title db.Series, releases []indexer.Release, now time.Time) error {
	sweep, err := s.loadSweepItems(ctx, title.ID, now)
	if err != nil {
		return err
	}
	m, err := s.evaluate(ctx, title, passItems(sweep), s.cachedVariants(ctx, title), "", releases)
	if err != nil {
		return err
	}
	_, _, err = s.grabPass(ctx, title, m, sweep, now, sourceFeed)
	return err
}

// feedEntryID is an entry's identity for deduping: its GUID, or the fields a feed
// publishing none still carries. Sonarr keys the same check on the download URL
// rather than the GUID, because Torznab GUIDs are not dependable across
// implementations.
func feedEntryID(e indexer.FeedEntry) string {
	for _, v := range []string{e.GUID, e.Release.InfoHash, e.Release.DownloadURL} {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return decide.NormalizeReleaseTitle(e.Release.Title)
}

// unseenEntries narrows a page to what the last poll did not already process. An
// entry older than the mark is skipped even when its id is unfamiliar, which is
// what stops a truncated id set from re-processing history.
func unseenEntries(entries []indexer.FeedEntry, mark feedMark) []indexer.FeedEntry {
	seen := make(map[string]bool, len(mark.IDs))
	for _, id := range mark.IDs {
		seen[id] = true
	}
	out := make([]indexer.FeedEntry, 0, len(entries))
	for _, e := range entries {
		if seen[feedEntryID(e)] {
			continue
		}
		if !mark.Latest.IsZero() && !e.Published.IsZero() && e.Published.Before(mark.Latest) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// nextFeedMark is the newest publish time on the page plus the ids that need
// remembering: the entries sharing that instant, and any the feed dated not at
// all, for which the id set is the only dedupe available.
func nextFeedMark(entries []indexer.FeedEntry) feedMark {
	var mark feedMark
	for _, e := range entries {
		if e.Published.After(mark.Latest) {
			mark.Latest = e.Published
		}
	}
	for _, e := range entries {
		if e.Published.IsZero() || e.Published.Equal(mark.Latest) {
			mark.IDs = append(mark.IDs, feedEntryID(e))
		}
	}
	if len(mark.IDs) > maxFeedMarkIDs {
		mark.IDs = mark.IDs[:maxFeedMarkIDs]
	}
	return mark
}

// advanceFeedMark keeps the furthest point the poll has reached. An indexer that
// transiently serves an older page must not rewind the window, which would
// re-process everything published since — so the older page's ids are remembered
// alongside the mark rather than replacing it.
func advanceFeedMark(prev, next feedMark) feedMark {
	if prev.Latest.IsZero() || !next.Latest.Before(prev.Latest) {
		return next
	}
	merged := feedMark{Latest: prev.Latest, IDs: append(append([]string{}, next.IDs...), prev.IDs...)}
	if len(merged.IDs) > maxFeedMarkIDs {
		merged.IDs = merged.IDs[:maxFeedMarkIDs]
	}
	return merged
}

func (s *Service) loadFeedMark(ctx context.Context, indexerName string) (feedMark, error) {
	v, err := s.store.Q.GetSetting(ctx, feedMarkKey(indexerName))
	if errors.Is(err, sql.ErrNoRows) {
		return feedMark{}, nil
	}
	if err != nil {
		return feedMark{}, fmt.Errorf("read feed mark for %s: %w", indexerName, err)
	}
	var mark feedMark
	if err := json.Unmarshal([]byte(v), &mark); err != nil {
		// One re-processed page, not a dead feed — Sonarr's equivalent field stops
		// RSS sync working entirely when its JSON goes bad.
		s.log.Warn("feed mark unreadable; treating the next page as new",
			"indexer", indexerName, "err", err)
		return feedMark{}, nil
	}
	return mark, nil
}

func (s *Service) saveFeedMark(ctx context.Context, indexerName string, mark feedMark) error {
	blob, err := json.Marshal(mark)
	if err != nil {
		return fmt.Errorf("encode feed mark for %s: %w", indexerName, err)
	}
	if err := s.store.Q.UpsertSetting(ctx, db.UpsertSettingParams{
		Key: feedMarkKey(indexerName), Value: string(blob),
	}); err != nil {
		return fmt.Errorf("persist feed mark for %s: %w", indexerName, err)
	}
	return nil
}
