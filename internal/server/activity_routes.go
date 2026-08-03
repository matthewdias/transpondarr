package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// queueItemDTO is one in-flight grab with the live client-reported state
// alongside the derived status the rest of the UI speaks.
type queueItemDTO struct {
	ID           int64    `json:"id"`
	SeriesID     int64    `json:"series_id"`
	SeriesTitle  string   `json:"series_title"`
	ItemNumber   int      `json:"item_number"`
	ReleaseTitle string   `json:"release_title"`
	InfoHash     string   `json:"infohash"`
	Status       string   `json:"status" enum:"downloading,stuck,deferred"`
	ImportError  string   `json:"import_error,omitempty" doc:"Why the completed download cannot import (stuck rows)"`
	ClientState  string   `json:"client_state,omitempty" enum:"downloading,complete,stalled,checking,paused,error,unknown" doc:"Live torrent state; absent when the client is unreachable"`
	Progress     *float64 `json:"progress,omitempty" minimum:"0" maximum:"1"`
	CreatedAt    string   `json:"created_at"`
}

type activityQueueOutput struct {
	Body struct {
		Items    []queueItemDTO `json:"items"`
		ClientOk bool           `json:"client_ok" doc:"False when the download client is missing or did not answer; rows then carry grab state only"`
	}
}

// activityEventDTO is one settled or initiating moment in a grab's lifecycle.
type activityEventDTO struct {
	ID           int64  `json:"id"`
	SeriesID     int64  `json:"series_id"`
	SeriesTitle  string `json:"series_title"`
	ItemNumber   int    `json:"item_number"`
	ReleaseTitle string `json:"release_title"`
	InfoHash     string `json:"infohash"`
	Status       string `json:"status" enum:"grabbed,imported,import_deferred,failed"`
	Detail       string `json:"detail,omitempty" doc:"Why a failed event failed"`
	CreatedAt    string `json:"created_at"`
}

type activityHistoryInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Events per page"`
	Cursor string `query:"cursor" doc:"Opaque cursor from the previous page's next_cursor"`
}

type activityHistoryOutput struct {
	Body struct {
		Events     []activityEventDTO `json:"events"`
		NextCursor string             `json:"next_cursor,omitempty" doc:"Absent on the last page"`
	}
}

// historyCursor encodes a keyset position as base64("created_at|id").
func historyCursor(createdAt string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt + "|" + strconv.FormatInt(id, 10)))
}

func decodeHistoryCursor(cursor string) (createdAt string, id int64, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, err
	}
	at, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return "", 0, fmt.Errorf("cursor missing separator")
	}
	id, err = strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "", 0, err
	}
	return at, id, nil
}

func registerActivityRoutes(api huma.API, deps routeDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "get-activity-queue",
		Method:      http.MethodGet,
		Path:        "/api/v1/activity/queue",
		Summary:     "Every in-flight grab across the library, with live client state",
		Tags:        []string{"activity"},
	}, func(ctx context.Context, _ *struct{}) (*activityQueueOutput, error) {
		rows, err := deps.store.Q.ListOpenGrabs(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list open grabs", err)
		}

		// Client trouble degrades to grab state, never a 5xx: the queue's job is
		// to answer even when the client cannot.
		byHash := map[string]download.Status{}
		clientOk := false
		if dl := deps.clients.Download(); dl != nil {
			clientOk = true
			if len(rows) > 0 {
				hashes := make([]string, 0, len(rows))
				seen := map[string]bool{}
				for _, g := range rows {
					h := strings.ToLower(g.InfoHash)
					if !seen[h] {
						seen[h] = true
						hashes = append(hashes, h)
					}
				}
				statuses, err := dl.Status(ctx, hashes...)
				if err != nil {
					clientOk = false
				}
				for _, s := range statuses {
					byHash[strings.ToLower(s.Hash)] = s
				}
			}
		}

		out := &activityQueueOutput{}
		out.Body.ClientOk = clientOk
		out.Body.Items = make([]queueItemDTO, 0, len(rows))
		for _, g := range rows {
			state := deriveItemState(false, db.Grab{
				ID:           g.ID,
				WantedItemID: g.WantedItemID,
				InfoHash:     g.InfoHash,
				ReleaseTitle: g.ReleaseTitle,
				Status:       g.Status,
				MissingSince: g.MissingSince,
				LastError:    g.LastError,
				CreatedAt:    g.CreatedAt,
			}, true)
			item := queueItemDTO{
				ID:           g.ID,
				SeriesID:     g.SeriesID,
				SeriesTitle:  g.SeriesTitle,
				ItemNumber:   int(g.ItemNumber.Int64),
				ReleaseTitle: g.ReleaseTitle,
				InfoHash:     g.InfoHash,
				Status:       state.Status,
				ImportError:  state.ImportError,
				CreatedAt:    g.CreatedAt,
			}
			if s, ok := byHash[strings.ToLower(g.InfoHash)]; ok {
				item.ClientState = string(s.State)
				p := s.Progress
				item.Progress = &p
			}
			out.Body.Items = append(out.Body.Items, item)
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-activity-history",
		Method:      http.MethodGet,
		Path:        "/api/v1/activity/history",
		Summary:     "Recent grab and import events across the library, newest first",
		Tags:        []string{"activity"},
	}, func(ctx context.Context, in *activityHistoryInput) (*activityHistoryOutput, error) {
		limit := in.Limit

		// Fetch one past the page to learn whether a next page exists.
		var rows []db.ListGrabEventsPageRow
		if in.Cursor == "" {
			var err error
			rows, err = deps.store.Q.ListGrabEventsPage(ctx, int64(limit)+1)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to list history", err)
			}
		} else {
			at, id, err := decodeHistoryCursor(in.Cursor)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid cursor")
			}
			before, err := deps.store.Q.ListGrabEventsPageBefore(ctx, db.ListGrabEventsPageBeforeParams{
				CreatedAt: at, CreatedAt_2: at, ID: id, Limit: int64(limit) + 1,
			})
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to list history", err)
			}
			rows = make([]db.ListGrabEventsPageRow, len(before))
			for i, r := range before {
				rows[i] = db.ListGrabEventsPageRow(r)
			}
		}

		out := &activityHistoryOutput{}
		out.Body.Events = make([]activityEventDTO, 0, min(len(rows), limit))
		for i, r := range rows {
			if i == limit {
				last := out.Body.Events[len(out.Body.Events)-1]
				out.Body.NextCursor = historyCursor(last.CreatedAt, last.ID)
				break
			}
			out.Body.Events = append(out.Body.Events, activityEventDTO{
				ID:           r.ID,
				SeriesID:     r.SeriesID,
				SeriesTitle:  r.SeriesTitle,
				ItemNumber:   int(r.ItemNumber),
				ReleaseTitle: r.ReleaseTitle,
				InfoHash:     r.InfoHash,
				Status:       r.Event,
				Detail:       r.Detail,
				CreatedAt:    r.CreatedAt,
			})
		}
		return out, nil
	})
}
