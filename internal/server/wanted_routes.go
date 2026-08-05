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

// missingItemsPerGroup caps what one group lists. A back-catalog add can put
// hundreds of items in one series; the header's count is the progress display,
// and listing them all past this point adds rows without adding information.
const missingItemsPerGroup = 50

// missingItemDTO is one item still worth acquiring. It carries no derived status
// because the listing's predicate admits only wanted ones -- an in-flight grab
// is Activity's row, not this page's. Its reason covers only what varies row to
// row; the series' story lives on the group and the page's on global_reason.
type missingItemDTO struct {
	ID           int64  `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name,omitempty"`
	AirsAt       string `json:"airs_at,omitempty" doc:"Broadcast time (RFC 3339 UTC); absent when the provider publishes no schedule"`
	Reason       string `json:"reason,omitempty" enum:"unaired,grab_failed" doc:"This item's own story; absent when the group and page tell it all"`
	ReasonDetail string `json:"reason_detail,omitempty" doc:"Why the last grab failed (reason grab_failed)"`
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
// cutoff, with the numbers behind that claim.
type cutoffItemDTO struct {
	ID           int64          `json:"id"`
	SeriesID     int64          `json:"series_id"`
	SeriesTitle  string         `json:"series_title"`
	Monitored    bool           `json:"monitored"`
	Number       int            `json:"number"`
	Name         string         `json:"name,omitempty"`
	AirsAt       string         `json:"airs_at,omitempty" format:"date-time"`
	Status       string         `json:"status" enum:"have,downloading,stuck,deferred,wanted" doc:"Derived acquisition state; downloading while an upgrade is in flight"`
	HeldRelease  string         `json:"held_release" doc:"What the library holds, and what the score below rates"`
	Score        int            `json:"score"`
	CutoffScore  int            `json:"cutoff_score"`
	UnmetGoals   []scorePartDTO `json:"unmet_goals,omitempty" doc:"Profile axes the held release scores below its best on, each with the points still available"`
	ProfileName  string         `json:"profile_name"`
	UpgradeError string         `json:"upgrade_error,omitempty" doc:"Why the last upgrade attempt failed"`
}

type wantedPageInput struct {
	Limit       int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Page size: series groups on missing, items on cutoff-unmet"`
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
		Items      []cutoffItemDTO `json:"items"`
		NextCursor string          `json:"next_cursor,omitempty" doc:"Absent on the last page"`
	}
}

type queueSearchInput struct {
	Body struct {
		SeriesIDs []int64 `json:"series_ids,omitempty" doc:"Series to put back at the front of the sweep queue; empty means the whole library"`
	}
}

type queueSearchOutput struct {
	Body struct {
		SeriesQueued int    `json:"series_queued" doc:"Series whose search cadence was reset; -1 when the whole library was"`
		Automation   string `json:"automation" enum:"off,notify_only,on" doc:"notify_only rehearses: the run happens, nothing reaches the download client"`
		RunTriggered bool   `json:"run_triggered" doc:"False when no runner is attached; the reset stands and the next scheduled pass picks it up"`
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
}

func (h *wantedHandler) listMissing(ctx context.Context, in *wantedPageInput) (*missingOutput, error) {
	cursor, err := pageCursor(in.Cursor)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid cursor")
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
		AirsAt_2: sql.NullString{String: cursor.AirsAt, Valid: true},
		AirsAt_3: sql.NullString{String: cursor.AirsAt, Valid: true},
		ID:       cursor.ID,
		Limit:    int64(in.Limit) + 1,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list missing series", err)
	}
	if len(seriesRows) > in.Limit {
		last := seriesRows[in.Limit-1]
		out.Body.NextCursor = keysetCursor(aggregateString(last.LatestMissingAir), last.ID)
		seriesRows = seriesRows[:in.Limit]
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
		Column2:   boolParam(in.Unaired),
		AirsAt:    nowStored,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list missing items", err)
	}
	itemsBySeries := make(map[int64][]missingItemDTO, len(seriesRows))
	for _, r := range itemRows {
		if len(itemsBySeries[r.SeriesID]) == missingItemsPerGroup {
			continue
		}
		item := missingItemDTO{
			ID:     r.ID,
			Number: int(r.Number.Int64),
			Name:   r.Title.String,
			AirsAt: storedTimeRFC3339(r.AirsAt),
			Reason: itemReason(itemFacts{
				AirsAt: storedTime(r.AirsAt), GrabFailed: r.GrabStatus.Valid,
			}, now),
		}
		if item.Reason == reasonGrabFailed {
			item.ReasonDetail = r.GrabLastError.String
		}
		itemsBySeries[r.SeriesID] = append(itemsBySeries[r.SeriesID], item)
	}

	blocked, err := h.blockedCounts(ctx, nowStored)
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
	out.Body.Items = make([]cutoffItemDTO, 0, len(page.Items))
	for _, it := range page.Items {
		state := deriveItemState(true, it.Grab, it.HasGrab)
		out.Body.Items = append(out.Body.Items, cutoffItemDTO{
			ID:           it.ID,
			SeriesID:     it.SeriesID,
			SeriesTitle:  it.SeriesTitle,
			Monitored:    it.Monitored,
			Number:       it.Number,
			Name:         it.Name,
			AirsAt:       storedTimeRFC3339(sql.NullString{String: it.AirsAt, Valid: it.AirsAt != ""}),
			Status:       state.Status,
			HeldRelease:  it.HeldReleaseTitle,
			Score:        it.Score,
			CutoffScore:  it.CutoffScore,
			UnmetGoals:   scorePartDTOs(it.UnmetGoals),
			ProfileName:  it.ProfileName,
			UpgradeError: state.ImportError,
		})
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
	} else {
		for _, id := range in.Body.SeriesIDs {
			if _, err := h.deps.store.Q.GetSeries(ctx, id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, huma.Error404NotFound("no such series")
				}
				return nil, huma.Error500InternalServerError("failed to load series", err)
			}
			if err := h.deps.store.Q.ResetSeriesSearchState(ctx, id); err != nil {
				return nil, huma.Error500InternalServerError("failed to reset the search queue", err)
			}
		}
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

// blockedCounts is how many releases each series currently refuses, keyed for
// the per-row lookup the reason column does.
func (h *wantedHandler) blockedCounts(ctx context.Context, now sql.NullString) (map[int64]int64, error) {
	rows, err := h.deps.store.Q.ListActiveBlocklistCounts(ctx, now)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load blocklist counts", err)
	}
	counts := make(map[int64]int64, len(rows))
	for _, r := range rows {
		counts[r.SeriesID] = r.Entries
	}
	return counts, nil
}

// pageCursor decodes a page cursor, an empty one meaning the top of the listing.
func pageCursor(encoded string) (acquire.QueueCursor, error) {
	if encoded == "" {
		return acquire.QueueCursorTop(), nil
	}
	at, id, err := decodeKeysetCursor(encoded)
	if err != nil {
		return acquire.QueueCursor{}, err
	}
	return acquire.QueueCursor{AirsAt: at, ID: id}, nil
}

func encodeCursor(c acquire.QueueCursor) string { return keysetCursor(c.AirsAt, c.ID) }

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
