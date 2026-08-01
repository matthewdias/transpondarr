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

type seriesBlocklistInput struct {
	ID int64 `path:"id" doc:"Series id"`
}

type seriesBlocklistOutput struct {
	Body struct {
		Series  string              `json:"series"`
		Entries []blocklistEntryDTO `json:"entries"`
	}
}

type clearBlocklistEntryInput struct {
	ID      int64 `path:"id" doc:"Series id"`
	EntryID int64 `path:"entryId" doc:"Blocklist entry id"`
}

type clearSeriesBlocklistInput struct {
	ID      int64 `path:"id" doc:"Series id"`
	Expired bool  `query:"expired" doc:"Clear only the entries whose block has lapsed, keeping what still blocks"`
}

type clearedOutput struct {
	Body struct {
		Cleared int64 `json:"cleared" doc:"How many entries were forgotten"`
	}
}

// blocklistHandler owns the per-series blocklist endpoints. Separate from grab
// history because the two outlive each other.
type blocklistHandler struct {
	store     *store.Store
	blocklist *blocklist.Service
}

func registerBlocklistRoutes(api huma.API, deps routeDeps) {
	h := &blocklistHandler{store: deps.store, blocklist: deps.blocklist}

	huma.Register(api, huma.Operation{
		OperationID: "list-series-blocklist",
		Method:      http.MethodGet,
		Path:        "/api/v1/series/{id}/blocklist",
		Summary:     "List releases remembered as failed for a series",
		Tags:        []string{"series"},
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "clear-series-blocklist",
		Method:      http.MethodDelete,
		Path:        "/api/v1/series/{id}/blocklist",
		Summary:     "Unblock every remembered release for a series",
		Tags:        []string{"series"},
	}, h.clearSeries)

	huma.Register(api, huma.Operation{
		OperationID:   "clear-series-blocklist-entry",
		Method:        http.MethodDelete,
		Path:          "/api/v1/series/{id}/blocklist/{entryId}",
		Summary:       "Unblock a release, making it eligible again",
		Tags:          []string{"series"},
		DefaultStatus: http.StatusNoContent,
	}, h.clear)
}

func (h *blocklistHandler) list(ctx context.Context, in *seriesBlocklistInput) (*seriesBlocklistOutput, error) {
	series, err := h.store.Q.GetSeries(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("series not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load series", err)
	}
	rows, err := h.blocklist.List(ctx, series.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load blocklist", err)
	}

	now := time.Now().UTC()
	out := &seriesBlocklistOutput{}
	out.Body.Series = series.Title
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

// clearSeries is the bulk unblock. It 404s on an unknown series rather than
// reporting zero cleared, so a stale series id is not read as "nothing to do".
func (h *blocklistHandler) clearSeries(ctx context.Context, in *clearSeriesBlocklistInput) (*clearedOutput, error) {
	series, err := h.store.Q.GetSeries(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("series not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load series", err)
	}

	cleared := h.blocklist.ClearSeries
	if in.Expired {
		cleared = h.blocklist.ClearExpired
	}
	n, err := cleared(ctx, series.ID)
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
