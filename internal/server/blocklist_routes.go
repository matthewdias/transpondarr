package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// blocklistEntryDTO is one remembered failed release. blocked_until is absent
// when the block is permanent, which is why active is reported separately.
type blocklistEntryDTO struct {
	ID           int64  `json:"id"`
	ReleaseTitle string `json:"release_title"`
	InfoHash     string `json:"infohash,omitempty"`
	Reason       string `json:"reason"`
	Failures     int    `json:"failures" doc:"How many times this release has failed; the third failure blocks it permanently"`
	BlockedUntil string `json:"blocked_until,omitempty" format:"date-time" doc:"When the block lapses; absent means permanent"`
	Active       bool   `json:"active" doc:"Whether the block applies right now"`
	CreatedAt    string `json:"created_at"`
}

type titleBlocklistInput struct {
	ID int64 `path:"id" doc:"Title id"`
}

type titleBlocklistOutput struct {
	Body struct {
		Title   string              `json:"title"`
		Entries []blocklistEntryDTO `json:"entries"`
	}
}

type clearBlocklistEntryInput struct {
	ID      int64 `path:"id" doc:"Title id"`
	EntryID int64 `path:"entryId" doc:"Blocklist entry id"`
}

type clearTitleBlocklistInput struct {
	ID      int64 `path:"id" doc:"Title id"`
	Expired bool  `query:"expired" doc:"Clear only the entries whose block has lapsed, keeping what still blocks"`
}

type clearedOutput struct {
	Body struct {
		Cleared int64 `json:"cleared" doc:"How many entries were forgotten"`
	}
}

// breakerDTO reports whether failure memory is suppressed right now; open means
// too many items failed inside the window for the releases to be the cause.
type breakerDTO struct {
	Open      bool   `json:"open"`
	Items     int    `json:"items" doc:"Distinct wanted items that failed inside the window, counting one item per release so a batch cannot inflate it"`
	Threshold int    `json:"threshold" doc:"How many distinct items opens the breaker"`
	WindowMin int    `json:"window_minutes"`
	Since     string `json:"since,omitempty" format:"date-time" doc:"When the breaker opened; absent while closed"`
}

type blocklistSummaryOutput struct {
	Body struct {
		Blocked int        `json:"blocked" doc:"Releases being skipped right now"`
		Titles  int        `json:"titles" doc:"How many titles they span"`
		Breaker breakerDTO `json:"breaker"`
	}
}

// blocklistHandler owns the per-title blocklist endpoints. Separate from grab
// history because the two outlive each other.
type blocklistHandler struct {
	store     *store.Store
	blocklist *blocklist.Service
}

func registerBlocklistRoutes(api huma.API, deps routeDeps) {
	h := &blocklistHandler{store: deps.store, blocklist: deps.blocklist}

	huma.Register(api, huma.Operation{
		OperationID: "get-blocklist-summary",
		Method:      http.MethodGet,
		Path:        "/api/v1/blocklist",
		Summary:     "How much of the library is being skipped, and whether memory is suppressed",
		Tags:        []string{"system"},
	}, h.summary)

	huma.Register(api, huma.Operation{
		OperationID: "clear-blocklist",
		Method:      http.MethodDelete,
		Path:        "/api/v1/blocklist",
		Summary:     "Forget every remembered release across the library",
		Tags:        []string{"system"},
	}, h.clearAll)

	huma.Register(api, huma.Operation{
		OperationID: "list-title-blocklist",
		Method:      http.MethodGet,
		Path:        "/api/v1/titles/{id}/blocklist",
		Summary:     "List releases remembered as failed for a title",
		Tags:        []string{"titles"},
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "clear-title-blocklist",
		Method:      http.MethodDelete,
		Path:        "/api/v1/titles/{id}/blocklist",
		Summary:     "Unblock every remembered release for a title",
		Tags:        []string{"titles"},
	}, h.clearTitle)

	huma.Register(api, huma.Operation{
		OperationID:   "clear-title-blocklist-entry",
		Method:        http.MethodDelete,
		Path:          "/api/v1/titles/{id}/blocklist/{entryId}",
		Summary:       "Unblock a release, making it eligible again",
		Tags:          []string{"titles"},
		DefaultStatus: http.StatusNoContent,
	}, h.clear)
}

func (h *blocklistHandler) list(ctx context.Context, in *titleBlocklistInput) (*titleBlocklistOutput, error) {
	title, err := h.store.Q.GetTitle(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("series not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load series", err)
	}
	rows, err := h.blocklist.List(ctx, title.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load blocklist", err)
	}

	now := time.Now().UTC()
	out := &titleBlocklistOutput{}
	out.Body.Title = title.Title
	out.Body.Entries = make([]blocklistEntryDTO, 0, len(rows))
	for _, e := range rows {
		out.Body.Entries = append(out.Body.Entries, blocklistEntryDTO{
			ID:           e.ID,
			ReleaseTitle: e.ReleaseTitle,
			InfoHash:     e.InfoHash,
			Reason:       e.Reason,
			Failures:     int(e.Failures),
			BlockedUntil: storedTimeRFC3339(e.BlockedUntil),
			Active:       blocklistEntryActive(e, now),
			CreatedAt:    e.CreatedAt,
		})
	}
	return out, nil
}

func (h *blocklistHandler) summary(ctx context.Context, _ *struct{}) (*blocklistSummaryOutput, error) {
	counts, err := h.blocklist.Summary(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to summarize the blocklist", err)
	}
	b := h.blocklist.BreakerState()

	out := &blocklistSummaryOutput{}
	out.Body.Blocked = int(counts.Entries)
	out.Body.Titles = int(counts.Series)
	out.Body.Breaker = breakerDTO{
		Open:      b.Open,
		Items:     b.Items,
		Threshold: b.Threshold,
		WindowMin: int(b.Window.Minutes()),
	}
	if !b.Since.IsZero() {
		out.Body.Breaker.Since = b.Since.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// clearAll is the recovery action for an environmental fault: it forgets the
// library's memory and closes the breaker, so the next tick starts clean.
func (h *blocklistHandler) clearAll(ctx context.Context, _ *struct{}) (*clearedOutput, error) {
	n, err := h.blocklist.ClearAll(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to clear the blocklist", err)
	}
	out := &clearedOutput{}
	out.Body.Cleared = n
	return out, nil
}

// clearTitle is the bulk unblock. It 404s on an unknown title rather than
// reporting zero cleared, so a stale title id is not read as "nothing to do".
func (h *blocklistHandler) clearTitle(ctx context.Context, in *clearTitleBlocklistInput) (*clearedOutput, error) {
	title, err := h.store.Q.GetTitle(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("series not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load series", err)
	}

	cleared := h.blocklist.ClearTitle
	if in.Expired {
		cleared = h.blocklist.ClearExpired
	}
	n, err := cleared(ctx, title.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to clear blocklist", err)
	}
	out := &clearedOutput{}
	out.Body.Cleared = n
	return out, nil
}

func (h *blocklistHandler) clear(ctx context.Context, in *clearBlocklistEntryInput) (*struct{}, error) {
	err := h.blocklist.Clear(ctx, in.ID, in.EntryID)
	if errors.Is(err, blocklist.ErrNotFound) {
		return nil, huma.Error404NotFound("blocklist entry not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to clear blocklist entry", err)
	}
	return nil, nil
}

// blocklistEntryActive reports whether a stored entry blocks right now. NULL is
// permanent; an unparseable expiry blocks rather than silently reopening the loop.
func blocklistEntryActive(e db.ReleaseBlocklist, now time.Time) bool {
	if !e.BlockedUntil.Valid {
		return true
	}
	until, err := store.ParseTimestamp(e.BlockedUntil.String)
	if err != nil {
		return true
	}
	return until.After(now)
}
