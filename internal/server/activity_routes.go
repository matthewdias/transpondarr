package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// queueItemDTO is one in-flight grab with the live client-reported state
// alongside the derived status the rest of the UI speaks.
type queueItemDTO struct {
	ID           int64    `json:"id"`
	TitleID      int64    `json:"title_id"`
	Title        string   `json:"title"`
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
	TitleID      int64  `json:"title_id"`
	Title        string `json:"title"`
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

// payloadFileDTO is one video file in a deferred grab's payload, with what its
// name parsed to — the evidence a human needs to say which episode it is.
type payloadFileDTO struct {
	Path            string `json:"path" doc:"Payload-relative path; the identity an assignment names"`
	EpisodeStart    int    `json:"episode_start"`
	EpisodeEnd      int    `json:"episode_end"`
	AbsoluteEpisode int    `json:"absolute_episode"`
	Batch           bool   `json:"batch"`
	Version         int    `json:"version"`
	Repack          bool   `json:"repack"`
	SuggestedItem   int    `json:"suggested_item" doc:"What an automatic re-map would claim; 0 when nothing"`
}

// payloadArchiveDTO is one archive set in the payload. Nothing can place it in
// the library, so it is listed beside the files and never among them.
type payloadArchiveDTO struct {
	Path  string `json:"path" doc:"Payload-relative path of the volume to extract"`
	Parts int    `json:"parts" doc:"Volumes the set spans"`
}

// payloadItemDTO is one grab row sharing the release being fixed.
type payloadItemDTO struct {
	GrabID     int64  `json:"grab_id"`
	ItemNumber int    `json:"item_number"`
	Status     string `json:"status" enum:"grabbed,imported,import_deferred,failed"`
}

type queuePayloadInput struct {
	ID int64 `path:"id" doc:"Grab id from the activity queue"`
}

type queuePayloadOutput struct {
	Body struct {
		ReleaseTitle string              `json:"release_title"`
		InfoHash     string              `json:"infohash"`
		Items        []payloadItemDTO    `json:"items"`
		Files        []payloadFileDTO    `json:"files"`
		Archives     []payloadArchiveDTO `json:"archives"`
	}
}

// retryAssignmentDTO is a human answering "this file is episode N".
type retryAssignmentDTO struct {
	File       string `json:"file" doc:"Payload-relative path from the payload listing"`
	ItemNumber int    `json:"item_number" minimum:"1"`
}

type retryImportInput struct {
	ID   int64 `path:"id" doc:"Grab id from the activity queue"`
	Body struct {
		Assignments []retryAssignmentDTO `json:"assignments,omitempty" doc:"Empty re-runs the automatic mapping"`
	}
}

type retryResultDTO struct {
	ItemNumber int    `json:"item_number"`
	Outcome    string `json:"outcome" enum:"imported,import_deferred,failed,unchanged"`
	Detail     string `json:"detail,omitempty"`
}

type retryImportOutput struct {
	Body struct {
		Results []retryResultDTO `json:"results"`
	}
}

// unmatchedItemDTO is one torrent in Transpondarr's category that no grab row
// references. Every field is live client state; there is no row to read from.
type unmatchedItemDTO struct {
	InfoHash    string  `json:"infohash"`
	Name        string  `json:"name"`
	ClientState string  `json:"client_state" enum:"downloading,complete,stalled,checking,paused,error,unknown"`
	Progress    float64 `json:"progress" minimum:"0" maximum:"1"`
	SavePath    string  `json:"save_path,omitempty"`
	Size        int64   `json:"size" doc:"Payload size in bytes"`
	AddedAt     string  `json:"added_at,omitempty" doc:"RFC3339; absent when the client reports no add time"`
}

type activityUnmatchedOutput struct {
	Body struct {
		Items    []unmatchedItemDTO `json:"items"`
		ClientOk bool               `json:"client_ok" doc:"False when the download client is missing or did not answer; nothing can be listed without it"`
		Scoped   bool               `json:"scoped" doc:"False when no download category is configured, which leaves our torrents indistinguishable from the user's; the list is then always empty"`
	}
}

type removeUnmatchedInput struct {
	Hash       string `path:"hash" doc:"Info hash from the unmatched listing"`
	DeleteData bool   `query:"delete_data" default:"true" doc:"Also delete the downloaded payload from disk"`
}

// activityHandler groups the queue, history, import-fix and unmatched-download
// routes, which share the store and the one importer the scan runs on.
type activityHandler struct {
	deps routeDeps
}

func registerActivityRoutes(api huma.API, deps routeDeps) {
	h := &activityHandler{deps: deps}

	huma.Register(api, huma.Operation{
		OperationID: "get-queue-item-payload",
		Method:      http.MethodGet,
		Path:        "/api/v1/activity/queue/{id}/payload",
		Summary:     "What a deferred grab's payload holds, so an import can be fixed by hand",
		Tags:        []string{"activity"},
	}, h.getPayload)

	huma.Register(api, huma.Operation{
		OperationID: "retry-queue-item-import",
		Method:      http.MethodPost,
		Path:        "/api/v1/activity/queue/{id}/retry-import",
		Summary:     "Re-run a deferred grab's import, optionally naming which file is which episode",
		Tags:        []string{"activity"},
	}, h.retryImport)

	huma.Register(api, huma.Operation{
		OperationID: "list-unmatched-downloads",
		Method:      http.MethodGet,
		Path:        "/api/v1/activity/unmatched",
		Summary:     "Torrents in Transpondarr's category that no grab row references",
		Tags:        []string{"activity"},
	}, h.listUnmatched)

	huma.Register(api, huma.Operation{
		OperationID:   "remove-unmatched-download",
		Method:        http.MethodDelete,
		Path:          "/api/v1/activity/unmatched/{hash}",
		Summary:       "Remove one unmatched download from the client, optionally with its data",
		Tags:          []string{"activity"},
		DefaultStatus: http.StatusNoContent,
	}, h.removeUnmatched)

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
				TitleID:      g.TitleID,
				Title:        g.TitleName,
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
			at, id, err := decodeKeysetCursor(in.Cursor)
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
				out.Body.NextCursor = keysetCursor(last.CreatedAt, last.ID)
				break
			}
			out.Body.Events = append(out.Body.Events, activityEventDTO{
				ID:           r.ID,
				TitleID:      r.SeriesID,
				Title:        r.TitleName,
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

// downloadCategory is the safety boundary: only torrents carrying it are ours.
// settings is nil only on the OpenAPI-dump path, where no handler runs.
// downloadCategory is empty only when settings are absent: the settings layer
// substitutes the default for a blank one, so the value is never normalized here
// — it is compared verbatim against what the same value wrote on the add.
func (h *activityHandler) downloadCategory() string {
	if h.deps.settings == nil {
		return ""
	}
	return h.deps.settings.DownloadCategory()
}

// referencedHashes spans every grab status, settled ones included: unmatched
// means nothing points at the torrent, not that nothing useful does.
func (h *activityHandler) referencedHashes(ctx context.Context) (map[string]bool, error) {
	rows, err := h.deps.store.Q.ListGrabInfoHashes(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(rows))
	for _, hash := range rows {
		seen[strings.ToLower(hash)] = true
	}
	return seen, nil
}

// pickUnmatched keeps the torrents in our category that nothing references. A
// blank category cannot tell ours from the user's, so it keeps nothing. It
// re-checks the category even when the client already filtered: that filter is
// an optional capability, so the fallback path arrives unfiltered.
func pickUnmatched(statuses []download.Status, referenced map[string]bool, category string) []download.Status {
	if category == "" {
		return nil
	}
	out := make([]download.Status, 0, len(statuses))
	for _, s := range statuses {
		if s.Category != category || referenced[strings.ToLower(s.Hash)] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// listUnmatched surfaces the downloads nothing is waiting on. Removal stays the
// user's: a deferred payload is exactly what a human was about to fix by hand.
func (h *activityHandler) listUnmatched(ctx context.Context, _ *struct{}) (*activityUnmatchedOutput, error) {
	out := &activityUnmatchedOutput{}
	out.Body.Items = []unmatchedItemDTO{}
	category := h.downloadCategory()
	dl := h.deps.clients.Download()
	// Set before the scoped return: an unscoped listing asks the client nothing,
	// so reporting a healthy client as unreachable would simply be false.
	out.Body.ClientOk = dl != nil
	out.Body.Scoped = category != ""
	if !out.Body.Scoped || dl == nil {
		return out, nil
	}

	statuses, err := download.StatusInCategory(ctx, dl, category)
	if err != nil {
		out.Body.ClientOk = false
		return out, nil
	}
	referenced, err := h.referencedHashes(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list grab hashes", err)
	}
	for _, s := range pickUnmatched(statuses, referenced, category) {
		item := unmatchedItemDTO{
			InfoHash:    strings.ToLower(s.Hash),
			Name:        s.Name,
			ClientState: string(s.State),
			Progress:    s.Progress,
			SavePath:    s.SavePath,
			Size:        s.Size,
		}
		if !s.AddedAt.IsZero() {
			item.AddedAt = s.AddedAt.UTC().Format(time.RFC3339)
		}
		out.Body.Items = append(out.Body.Items, item)
	}
	return out, nil
}

// removeUnmatched re-derives the set rather than trusting the listing: a scan or
// a grab can adopt the hash between the render and the click.
func (h *activityHandler) removeUnmatched(ctx context.Context, in *removeUnmatchedInput) (*struct{}, error) {
	category := h.downloadCategory()
	if category == "" {
		return nil, huma.Error503ServiceUnavailable("no download category is configured, so Transpondarr's own downloads cannot be told apart")
	}
	dl := h.deps.clients.Download()
	if dl == nil {
		return nil, acquireHTTPError(acquire.ErrNoDownloadClient)
	}
	statuses, err := download.StatusInCategory(ctx, dl, category)
	if err != nil {
		return nil, huma.Error502BadGateway("failed to read the download client", err)
	}
	referenced, err := h.referencedHashes(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list grab hashes", err)
	}

	hash := strings.ToLower(in.Hash)
	ours := false
	for _, s := range statuses {
		if strings.ToLower(s.Hash) == hash && s.Category == category {
			ours = true
			break
		}
	}
	if !ours {
		return nil, huma.Error404NotFound("no download in Transpondarr's category has that hash")
	}
	if referenced[hash] {
		return nil, huma.Error409Conflict("that download is referenced by a grab again; refresh the queue")
	}
	if err := dl.Remove(ctx, []string{hash}, in.DeleteData); err != nil {
		return nil, huma.Error502BadGateway("failed to remove the download from the client", err)
	}
	return nil, nil
}

// getPayload lists a deferred grab's payload for the import-fix dialog.
func (h *activityHandler) getPayload(ctx context.Context, in *queuePayloadInput) (*queuePayloadOutput, error) {
	if h.deps.importer == nil {
		return nil, huma.Error503ServiceUnavailable("the importer is not available")
	}
	info, err := h.deps.importer.ListPayload(ctx, in.ID)
	if err != nil {
		return nil, importerError(err)
	}

	out := &queuePayloadOutput{}
	out.Body.ReleaseTitle = info.ReleaseTitle
	out.Body.InfoHash = info.InfoHash
	out.Body.Items = make([]payloadItemDTO, 0, len(info.Items))
	for _, it := range info.Items {
		out.Body.Items = append(out.Body.Items, payloadItemDTO{
			GrabID: it.GrabID, ItemNumber: it.ItemNumber, Status: it.Status,
		})
	}
	out.Body.Files = make([]payloadFileDTO, 0, len(info.Files))
	for _, f := range info.Files {
		out.Body.Files = append(out.Body.Files, payloadFileDTO{
			Path:            f.Path,
			EpisodeStart:    f.EpisodeStart,
			EpisodeEnd:      f.EpisodeEnd,
			AbsoluteEpisode: f.AbsoluteEpisode,
			Batch:           f.Batch,
			Version:         f.Version,
			Repack:          f.Repack,
			SuggestedItem:   f.SuggestedItem,
		})
	}
	out.Body.Archives = make([]payloadArchiveDTO, 0, len(info.Archives))
	for _, a := range info.Archives {
		out.Body.Archives = append(out.Body.Archives, payloadArchiveDTO{Path: a.Path, Parts: a.Parts})
	}
	return out, nil
}

// retryImport re-runs a deferred release's import. It is the only way a deferral
// reopens: the scan never re-walks bytes it already settled.
func (h *activityHandler) retryImport(ctx context.Context, in *retryImportInput) (*retryImportOutput, error) {
	if h.deps.importer == nil {
		return nil, huma.Error503ServiceUnavailable("the importer is not available")
	}
	assignments := make(map[string]int, len(in.Body.Assignments))
	for _, a := range in.Body.Assignments {
		if _, dup := assignments[a.File]; dup {
			return nil, huma.Error422UnprocessableEntity("file " + a.File + " was assigned twice")
		}
		assignments[a.File] = a.ItemNumber
	}

	results, err := h.deps.importer.RetryImport(ctx, in.ID, assignments)
	if err != nil {
		return nil, importerError(err)
	}
	out := &retryImportOutput{}
	out.Body.Results = make([]retryResultDTO, 0, len(results))
	for _, r := range results {
		out.Body.Results = append(out.Body.Results, retryResultDTO{
			ItemNumber: r.ItemNumber, Outcome: r.Outcome, Detail: r.Detail,
		})
	}
	return out, nil
}

// importerError maps the importer's sentinels to status codes. A payload that is
// gone and a row that is not deferred are both 409: the request was well-formed,
// the world moved.
func importerError(err error) error {
	switch {
	case errors.Is(err, importer.ErrGrabNotFound):
		return huma.Error404NotFound("no such grab")
	case errors.Is(err, importer.ErrNotDeferred):
		return huma.Error409Conflict("this grab is not awaiting an import fix")
	case errors.Is(err, importer.ErrPayloadGone):
		return huma.Error409Conflict("the payload is no longer available: " + err.Error())
	case errors.Is(err, importer.ErrNoClient):
		return huma.Error503ServiceUnavailable("no download client or library is configured")
	case errors.Is(err, importer.ErrBadAssignment):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return huma.Error500InternalServerError("import retry failed", err)
}
