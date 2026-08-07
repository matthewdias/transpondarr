package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// sweepJobName is the runner's name for the scheduled search, which is what a
// queued search triggers rather than issuing indexer requests itself.
const sweepJobName = "wanted-search"

// lastPassDTO dates the one stored reason a row can carry (#181). It is emitted
// only when the pass tier won: everything else is computed at request time, and
// an "as of" on a fresh answer would be a lie about how it was reached.
type lastPassDTO struct {
	ReleaseTitle string `json:"release_title,omitempty" doc:"The release the pass acted on or turned down; absent when nothing matched"`
	Source       string `json:"source" enum:"sweep,feed" doc:"Which entry point decided; only a search ever reports no_match"`
	At           string `json:"at" format:"date-time" doc:"When the pass decided this"`
	HeldUntil    string `json:"held_until,omitempty" format:"date-time" doc:"When a pinned-group hold expires (reason pin_held)"`
}

// missingItemDTO is one item still worth acquiring. It carries no derived status
// because the listing's predicate admits only wanted ones -- an in-flight grab
// is Activity's row, not this page's. Its reason covers only what varies row to
// row; the series' story lives on the group and the page's on global_reason.
type missingItemDTO struct {
	ID           int64        `json:"id"`
	Number       int          `json:"number"`
	Name         string       `json:"name,omitempty"`
	Monitored    bool         `json:"monitored" doc:"False rows appear only under ?unmonitored=true, so the click that hid one can be undone"`
	AirsAt       string       `json:"airs_at,omitempty" doc:"Broadcast time (RFC 3339 UTC); absent when the provider publishes no schedule"`
	Reason       string       `json:"reason,omitempty" enum:"unmonitored,unaired,grab_failed,no_match,declined,pin_held,would_grab,add_failed" doc:"This item's own story; absent when the group and page tell it all"`
	ReasonDetail string       `json:"reason_detail,omitempty" doc:"Why the last grab failed, or why the pass turned a release down"`
	LastPass     *lastPassDTO `json:"last_pass,omitempty" doc:"Present only when the reason is the last pass's answer, which is dated because it can go stale"`
}

// missingGroupDTO is one series' missing items. Grouping is the page's shape
// because the bulk action is per series: a search queues a series, never a row.
type missingGroupDTO struct {
	SeriesID        int64            `json:"series_id"`
	SeriesTitle     string           `json:"series_title"`
	Monitored       bool             `json:"monitored"`
	Reason          string           `json:"reason" enum:"unmonitored,blocklisted,never_searched,search_backoff,search_due" doc:"The series' standing in the sweep queue, derived from stored state at request time"`
	BlockedReleases int              `json:"blocked_releases,omitempty" doc:"Releases this series is currently refusing (reason blocklisted)"`
	NextSearchAt    string           `json:"next_search_at,omitempty" doc:"When the sweep next reaches this series (reason search_backoff)"`
	Missing         int              `json:"missing" doc:"Missing items in the whole group; may exceed len(items), which is capped"`
	Items           []missingItemDTO `json:"items"`
}

// cutoffItemDTO is one held item whose release scores below its profile's
// cutoff, with the numbers behind that claim. The cutoff itself lives on the
// group, being the profile's rather than any one item's. There is no import
// error here: a held item derives to in_library/downloading and never to stuck,
// so a failing upgrade's reason is the Activity queue's to show, alongside the
// deferred imports this listing also leaves to it.
type cutoffItemDTO struct {
	ID          int64          `json:"id"`
	Number      int            `json:"number"`
	Name        string         `json:"name,omitempty"`
	Monitored   bool           `json:"monitored" doc:"False rows appear only under ?unmonitored=true"`
	AirsAt      string         `json:"airs_at,omitempty" format:"date-time"`
	Status      string         `json:"status" enum:"in_library,downloading" doc:"Derived acquisition state; downloading while an upgrade is in flight"`
	HeldRelease string         `json:"held_release" doc:"What the library holds, and what the score below rates"`
	Score       int            `json:"score"`
	UnmetGoals  []scorePartDTO `json:"unmet_goals,omitempty" doc:"Profile axes the held release scores below its best on, each with the points still available"`
}

// cutoffGroupDTO is one series' sub-cutoff items, the listing's pagination unit.
type cutoffGroupDTO struct {
	SeriesID    int64           `json:"series_id"`
	SeriesTitle string          `json:"series_title"`
	Monitored   bool            `json:"monitored"`
	ProfileName string          `json:"profile_name"`
	CutoffScore int             `json:"cutoff_score"`
	Below       int             `json:"below" doc:"Items below the cutoff in the whole series; may exceed len(items), which is capped"`
	Items       []cutoffItemDTO `json:"items"`
}

type wantedPageInput struct {
	Limit       int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Series groups per page on both tabs, and the scan batch size on cutoff-unmet; a page may close below it once it lists about 200 items"`
	Cursor      string `query:"cursor" doc:"Opaque cursor from the previous page's next_cursor"`
	Unmonitored bool   `query:"unmonitored" doc:"Include items from unmonitored series"`
	Unaired     bool   `query:"unaired" doc:"Include items whose broadcast is still ahead; the Calendar owns the forward-looking view"`
}

type missingOutput struct {
	Body struct {
		GlobalReason string            `json:"global_reason,omitempty" enum:"no_indexer,automation_off,notify_only" doc:"What stops any search running at all; absent when nothing does"`
		Groups       []missingGroupDTO `json:"groups"`
		NextCursor   string            `json:"next_cursor,omitempty" doc:"Absent on the last page"`
	}
}

type cutoffOutput struct {
	Body struct {
		Groups     []cutoffGroupDTO `json:"groups"`
		NextCursor string           `json:"next_cursor,omitempty" doc:"Absent on the last page"`
	}
}

type queueSearchInput struct {
	Body struct {
		SeriesIDs []int64 `json:"series_ids" required:"true" maxItems:"500" doc:"Series to put back at the front of the sweep queue; an explicit empty array means the whole library, and omitting the field is rejected so a mis-serialized request cannot reset everything by accident"`
	}
}

type queueSearchOutput struct {
	Body struct {
		SeriesQueued int    `json:"series_queued" doc:"Series whose search cadence was reset; -1 when the whole library was"`
		Automation   string `json:"automation" enum:"off,notify_only,on" doc:"notify_only rehearses: the run happens, nothing reaches the download client"`
		RunTriggered bool   `json:"run_triggered" doc:"False when no runner is attached; the reset stands and the next scheduled pass picks it up"`
	}
}

// setItemsMonitoredInput is one state-setter for both call sites; a single
// toggle is a one-element array. The client chunks against maxItems.
type setItemsMonitoredInput struct {
	Body struct {
		ItemIDs   []int64 `json:"item_ids" required:"true" maxItems:"1000" doc:"Wanted items to set; ids that no longer exist are skipped"`
		Monitored bool    `json:"monitored" doc:"Whether automation should pursue these items"`
	}
}

type setItemsMonitoredOutput struct {
	Body struct {
		Updated      int `json:"updated" doc:"Items actually changed; below len(item_ids) when some were deleted"`
		SeriesQueued int `json:"series_queued" doc:"Distinct series put back at the front of the sweep queue; always 0 when unmonitoring"`
	}
}

// wantedHandler groups the cross-series wanted queue: the two listings and the
// cadence reset behind their bulk actions.
type wantedHandler struct {
	deps routeDeps
	now  func() time.Time
}

func registerWantedRoutes(api huma.API, deps routeDeps) {
	h := &wantedHandler{deps: deps, now: time.Now}

	huma.Register(api, huma.Operation{
		OperationID: "list-wanted-missing",
		Method:      http.MethodGet,
		Path:        "/api/v1/wanted/missing",
		Summary:     "Every item across the library still worth acquiring, newest broadcast first, with why it is still missing",
		Tags:        []string{"wanted"},
	}, h.listMissing)

	huma.Register(api, huma.Operation{
		OperationID: "list-wanted-cutoff-unmet",
		Method:      http.MethodGet,
		Path:        "/api/v1/wanted/cutoff-unmet",
		Summary:     "Held items whose release scores below the cutoff of an upgrading quality profile",
		Tags:        []string{"wanted"},
	}, h.listCutoffUnmet)

	huma.Register(api, huma.Operation{
		OperationID:   "queue-wanted-search",
		Method:        http.MethodPost,
		Path:          "/api/v1/wanted/search",
		Summary:       "Put series back at the front of the sweep queue and run the sweep now",
		Description:   "Queues work rather than searching: the sweep's per-pass limit is the indexer budget the search design protects, so a library-wide reset drains over several passes.",
		Tags:          []string{"wanted"},
		DefaultStatus: http.StatusAccepted,
	}, h.queueSearch)

	huma.Register(api, huma.Operation{
		OperationID: "set-wanted-items-monitored",
		Method:      http.MethodPatch,
		Path:        "/api/v1/wanted/items",
		Summary:     "Set whether automation pursues these wanted items",
		Description: "Monitoring gates search and grab, never a manual action: a manual search or grab on an unmonitored item still works, and a pack grabbed for its monitored neighbours still imports the file.",
		Tags:        []string{"wanted"},
	}, h.setItemsMonitored)
}

func (h *wantedHandler) listMissing(ctx context.Context, in *wantedPageInput) (*missingOutput, error) {
	cursor, err := pageCursor(in.Cursor)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid cursor")
	}
	if cursor == (acquire.QueueCursor{}) {
		cursor = acquire.QueueCursorTop()
	}
	now := h.now().UTC()
	nowStored := sql.NullString{String: store.FormatTimestamp(now), Valid: true}

	out := &missingOutput{}
	out.Body.GlobalReason = globalReason(h.deps.clients.Indexer() != nil,
		h.deps.settings.Snapshot().Automation.Mode)

	// One past the page, to learn whether a next page exists.
	seriesRows, err := h.deps.store.Q.ListMissingSeriesPage(ctx, db.ListMissingSeriesPageParams{
		Column1:  boolParam(in.Unmonitored),
		Column2:  boolParam(in.Unaired),
		AirsAt:   nowStored,
		AirsAt_2: sql.NullString{String: cursor.Key, Valid: true},
		AirsAt_3: sql.NullString{String: cursor.Key, Valid: true},
		ID:       cursor.ID,
		Limit:    int64(in.Limit) + 1,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list missing series", err)
	}
	hasMore := len(seriesRows) > in.Limit
	if hasMore {
		seriesRows = seriesRows[:in.Limit]
	}
	// The page's weight is rows, not groups, so it also closes on an item
	// budget. The aggregate already says what each group will list, so the
	// budget is applied before any items are fetched; the first group always
	// ships, however large its cap.
	itemSum := 0
	for i, s := range seriesRows {
		shown := min(int(s.Missing), acquire.ItemsPerGroup)
		if i > 0 && itemSum+shown > acquire.PageItemBudget {
			seriesRows = seriesRows[:i]
			hasMore = true
			break
		}
		itemSum += shown
	}
	if hasMore {
		last := seriesRows[len(seriesRows)-1]
		out.Body.NextCursor = keysetCursor(aggregateString(last.LatestMissingAir), last.ID)
	}
	out.Body.Groups = make([]missingGroupDTO, 0, len(seriesRows))
	if len(seriesRows) == 0 {
		return out, nil
	}

	ids := make([]int64, 0, len(seriesRows))
	for _, s := range seriesRows {
		ids = append(ids, s.ID)
	}
	itemRows, err := h.deps.store.Q.ListMissingItemsBySeries(ctx, db.ListMissingItemsBySeriesParams{
		SeriesIds: ids,
		Column2:   boolParam(in.Unmonitored),
		Column3:   boolParam(in.Unaired),
		AirsAt:    nowStored,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list missing items", err)
	}
	itemsBySeries := make(map[int64][]missingItemDTO, len(seriesRows))
	for _, r := range itemRows {
		if len(itemsBySeries[r.SeriesID]) == acquire.ItemsPerGroup {
			continue
		}
		facts := itemFacts{
			Monitored:  r.Monitored == 1,
			AirsAt:     storedTime(r.AirsAt),
			GrabFailed: r.GrabStatus.Valid,
			GrabbedAt:  storedTime(r.GrabCreatedAt),
			Pass: passFacts{
				Outcome:    r.PassOutcome.String,
				Release:    r.PassReleaseTitle.String,
				Detail:     r.PassDetail.String,
				Source:     r.PassSource.String,
				RecordedAt: storedTime(r.PassRecordedAt),
			},
		}
		reason, fromPass := itemReason(facts, now)
		item := missingItemDTO{
			ID:        r.ID,
			Number:    int(r.Number.Int64),
			Name:      r.Title.String,
			Monitored: facts.Monitored,
			AirsAt:    storedTimeRFC3339(r.AirsAt),
			Reason:    reason,
		}
		switch {
		case fromPass:
			item.ReasonDetail = facts.Pass.Detail
			item.LastPass = &lastPassDTO{
				ReleaseTitle: facts.Pass.Release,
				Source:       facts.Pass.Source,
				At:           storedTimeRFC3339(r.PassRecordedAt),
				HeldUntil:    storedTimeRFC3339(r.PassHeldUntil),
			}
		case reason == reasonGrabFailed:
			item.ReasonDetail = r.GrabLastError.String
		}
		itemsBySeries[r.SeriesID] = append(itemsBySeries[r.SeriesID], item)
	}

	blocked, err := h.blockedCounts(ctx, ids, nowStored)
	if err != nil {
		return nil, err
	}
	for _, s := range seriesRows {
		items := itemsBySeries[s.ID]
		if len(items) == 0 {
			// The two queries are not one transaction; a grab settling between
			// them empties a group, and an empty group is a lie about a count.
			continue
		}
		facts := seriesFacts{
			Monitored:       s.Monitored == 1,
			BlockedReleases: int(blocked[s.ID]),
			LastSearchedAt:  storedTime(s.LastSearchedAt),
			NextSearchAt:    storedTime(s.NextSearchAt),
		}
		group := missingGroupDTO{
			SeriesID:    s.ID,
			SeriesTitle: s.Title,
			Monitored:   facts.Monitored,
			Reason:      seriesReason(facts, now),
			Missing:     int(s.Missing),
			Items:       items,
		}
		switch group.Reason {
		case reasonBlocklisted:
			group.BlockedReleases = facts.BlockedReleases
		case reasonSearchBackoff:
			group.NextSearchAt = storedTimeRFC3339(s.NextSearchAt)
		}
		out.Body.Groups = append(out.Body.Groups, group)
	}
	return out, nil
}

// aggregateString reads a text aggregate sqlc could only type as interface{}.
func aggregateString(v any) string {
	s, _ := v.(string)
	return s
}

func scorePartDTOs(parts []decide.ScorePart) []scorePartDTO {
	if len(parts) == 0 {
		return nil
	}
	out := make([]scorePartDTO, 0, len(parts))
	for _, p := range parts {
		out = append(out, scorePartDTO{Label: p.Label, Points: p.Points})
	}
	return out
}

func (h *wantedHandler) listCutoffUnmet(ctx context.Context, in *wantedPageInput) (*cutoffOutput, error) {
	if h.deps.acquire == nil {
		return nil, huma.Error503ServiceUnavailable("the acquisition service is not available")
	}
	// The zero cursor is this listing's natural top: it ascends by title, so
	// every row is past ("", 0) already.
	cursor, err := pageCursor(in.Cursor)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid cursor")
	}
	page, err := h.deps.acquire.CutoffUnmet(ctx, acquire.CutoffUnmetParams{
		Limit:              in.Limit,
		Cursor:             cursor,
		IncludeUnmonitored: in.Unmonitored,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list cutoff-unmet items", err)
	}

	out := &cutoffOutput{}
	out.Body.Groups = make([]cutoffGroupDTO, 0, len(page.Groups))
	for _, g := range page.Groups {
		group := cutoffGroupDTO{
			SeriesID:    g.SeriesID,
			SeriesTitle: g.SeriesTitle,
			Monitored:   g.Monitored,
			ProfileName: g.ProfileName,
			CutoffScore: g.CutoffScore,
			Below:       g.Below,
			Items:       make([]cutoffItemDTO, 0, len(g.Items)),
		}
		for _, it := range g.Items {
			state := deriveItemState(true, it.Grab, it.HasGrab)
			group.Items = append(group.Items, cutoffItemDTO{
				ID:          it.ID,
				Number:      it.Number,
				Name:        it.Name,
				Monitored:   it.Monitored,
				AirsAt:      storedTimeRFC3339(sql.NullString{String: it.AirsAt, Valid: it.AirsAt != ""}),
				Status:      state.Status,
				HeldRelease: it.HeldReleaseTitle,
				Score:       it.Score,
				UnmetGoals:  scorePartDTOs(it.UnmetGoals),
			})
		}
		out.Body.Groups = append(out.Body.Groups, group)
	}
	if page.NextCursor != (acquire.QueueCursor{}) {
		out.Body.NextCursor = encodeCursor(page.NextCursor)
	}
	return out, nil
}

// queueSearch resets the sweep cadence and triggers a run. It never issues an
// indexer request itself: seriesPerPass is the budget the whole search design
// protects, so "search all" on a large library is a queue, not a burst.
func (h *wantedHandler) queueSearch(ctx context.Context, in *queueSearchInput) (*queueSearchOutput, error) {
	out := &queueSearchOutput{}
	out.Body.Automation = string(h.deps.settings.Snapshot().Automation.Mode)

	if len(in.Body.SeriesIDs) == 0 {
		if err := h.deps.store.Q.ResetAllSeriesSearchState(ctx); err != nil {
			return nil, huma.Error500InternalServerError("failed to reset the search queue", err)
		}
		out.Body.SeriesQueued = -1
	} else if err := h.resetSelected(ctx, in.Body.SeriesIDs); err != nil {
		return nil, err
	} else {
		out.Body.SeriesQueued = len(in.Body.SeriesIDs)
	}

	if h.deps.jobs != nil {
		if err := h.deps.jobs.Trigger(sweepJobName); err != nil && !errors.Is(err, jobs.ErrUnknownJob) {
			return nil, huma.Error500InternalServerError("failed to trigger the sweep", err)
		} else if err == nil {
			out.Body.RunTriggered = true
		}
	}
	return out, nil
}

// setItemsMonitored applies the flag to a whole selection in one transaction.
// An unknown id is skipped rather than 404ing the batch, deliberately, unlike
// resetSelected beside it (#188).
func (h *wantedHandler) setItemsMonitored(ctx context.Context, in *setItemsMonitoredInput) (*setItemsMonitoredOutput, error) {
	out := &setItemsMonitoredOutput{}
	if len(in.Body.ItemIDs) == 0 {
		return out, nil
	}

	tx, err := h.deps.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update item monitoring", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := h.deps.store.Q.WithTx(tx)

	// Only where something will actually move: the update reports a row count,
	// not which series it touched.
	var seriesIDs []int64
	if in.Body.Monitored {
		seriesIDs, err = qtx.ListSeriesIDsForUnmonitoredItems(ctx, in.Body.ItemIDs)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to load the items", err)
		}
	}
	updated, err := qtx.SetWantedItemsMonitored(ctx, db.SetWantedItemsMonitoredParams{
		Monitored: boolToInt(in.Body.Monitored),
		Ids:       in.Body.ItemIDs,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update item monitoring", err)
	}
	for _, id := range seriesIDs {
		if err := qtx.ResetSeriesSearchState(ctx, id); err != nil {
			return nil, huma.Error500InternalServerError("failed to reset the search cadence", err)
		}
	}
	out.Body.SeriesQueued = len(seriesIDs)
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("failed to update item monitoring", err)
	}
	out.Body.Updated = int(updated)
	return out, nil
}

// resetSelected puts the named series back at the front of the sweep queue in
// one transaction: a partial reset would leave half the selection queued behind
// a 500, with nothing telling the caller which half.
func (h *wantedHandler) resetSelected(ctx context.Context, ids []int64) error {
	found, err := h.deps.store.Q.CountSeriesByIDs(ctx, ids)
	if err != nil {
		return huma.Error500InternalServerError("failed to load series", err)
	}
	if int(found) != len(ids) {
		return huma.Error404NotFound("no such series")
	}
	tx, err := h.deps.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return huma.Error500InternalServerError("failed to reset the search queue", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := h.deps.store.Q.WithTx(tx)
	for _, id := range ids {
		if err := qtx.ResetSeriesSearchState(ctx, id); err != nil {
			return huma.Error500InternalServerError("failed to reset the search queue", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return huma.Error500InternalServerError("failed to reset the search queue", err)
	}
	return nil
}

// blockedCounts is how many releases each series currently refuses, keyed for
// the per-row lookup the reason column does. Scoped to the page's series.
func (h *wantedHandler) blockedCounts(ctx context.Context, ids []int64, now sql.NullString) (map[int64]int64, error) {
	rows, err := h.deps.store.Q.ListActiveBlocklistCounts(ctx, db.ListActiveBlocklistCountsParams{
		SeriesIds: ids, BlockedUntil: now,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load blocklist counts", err)
	}
	counts := make(map[int64]int64, len(rows))
	for _, r := range rows {
		counts[r.SeriesID] = r.Entries
	}
	return counts, nil
}

// pageCursor decodes a page cursor; an empty one returns the zero cursor, and
// each listing supplies its own top -- Missing descends from QueueCursorTop,
// Cutoff Unmet ascends so its top is the zero cursor itself.
func pageCursor(encoded string) (acquire.QueueCursor, error) {
	if encoded == "" {
		return acquire.QueueCursor{}, nil
	}
	at, id, err := decodeKeysetCursor(encoded)
	if err != nil {
		return acquire.QueueCursor{}, err
	}
	return acquire.QueueCursor{Key: at, ID: id}, nil
}

func encodeCursor(c acquire.QueueCursor) string { return keysetCursor(c.Key, c.ID) }

// boolParam renders a filter flag for a SQL "? = 1 OR ..." predicate.
func boolParam(on bool) int64 {
	if on {
		return 1
	}
	return 0
}

// storedTime parses a stored timestamp, an unset or unparseable one reading as
// the zero time -- which the reason derivation takes as "unknown", never as a
// date in the past or future.
func storedTime(stored sql.NullString) time.Time {
	if !stored.Valid {
		return time.Time{}
	}
	t, err := store.ParseTimestamp(stored.String)
	if err != nil {
		return time.Time{}
	}
	return t
}
