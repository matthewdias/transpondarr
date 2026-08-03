package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/catalog"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type seriesDTO struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Format    string `json:"format"`
	Monitored bool   `json:"monitored"`
	Total     int    `json:"total"`
	Have      int    `json:"have"`
}

type listSeriesOutput struct {
	Body struct {
		Series []seriesDTO `json:"series"`
	}
}

type wantedItemDTO struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Name   string `json:"name,omitempty"`
	Have   bool   `json:"have"`
}

type seriesDetailDTO struct {
	ID        int64           `json:"id"`
	AniListID int64           `json:"anilist_id"`
	Title     string          `json:"title"`
	Format    string          `json:"format"`
	Monitored bool            `json:"monitored"`
	Items     []wantedItemDTO `json:"items"`
}

type addSeriesInput struct {
	Body struct {
		AniListID int64 `json:"anilist_id" required:"true" doc:"AniList media id to add"`
		Monitored *bool `json:"monitored,omitempty" doc:"Whether to monitor for downloads (default true)"`
	}
}

type addSeriesOutput struct {
	Body seriesDetailDTO
}

// detailItemDTO is one wanted item with its derived acquisition state, so the UI
// can render each episode row (have / downloading / deferred / wanted) without a
// second call.
type detailItemDTO struct {
	ID           int64  `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name,omitempty"`
	Have         bool   `json:"have"`
	Status       string `json:"status" enum:"have,downloading,stuck,deferred,wanted" doc:"Derived acquisition state"`
	ReleaseTitle string `json:"release_title,omitempty"`
	ImportError  string `json:"import_error,omitempty" doc:"Why the last import attempt failed (status stuck)"`
	AirsAt       string `json:"airs_at,omitempty" format:"date-time" doc:"Broadcast time (RFC 3339, Japanese broadcast clock); absent when the provider publishes none"`
}

type seriesDetailReadDTO struct {
	ID               int64           `json:"id"`
	AniListID        int64           `json:"anilist_id,omitempty"`
	Title            string          `json:"title"`
	English          string          `json:"english,omitempty"`
	Native           string          `json:"native,omitempty"`
	Format           string          `json:"format"`
	Status           string          `json:"status,omitempty" doc:"Provider status (e.g. RELEASING, FINISHED)"`
	CoverURL         string          `json:"cover_url,omitempty"`
	Monitored        bool            `json:"monitored"`
	QualityProfileID int64           `json:"quality_profile_id"`
	PinnedGroup      string          `json:"pinned_group,omitempty" doc:"Release group pinned above profile scoring; absent when none"`
	PinDelayHours    *int            `json:"pin_delay_hours,omitempty" doc:"Per-series override of how long the sweep waits for the pinned group; absent means the global default"`
	Items            []detailItemDTO `json:"items"`
}

type getSeriesInput struct {
	ID int64 `path:"id" doc:"Series id"`
}

type getSeriesOutput struct {
	Body seriesDetailReadDTO
}

type setMonitoredInput struct {
	ID   int64 `path:"id" doc:"Series id"`
	Body struct {
		Monitored bool `json:"monitored" doc:"Whether to monitor the series for downloads"`
	}
}

type setMonitoredOutput struct {
	Body struct {
		ID        int64 `json:"id"`
		Monitored bool  `json:"monitored"`
	}
}

type setPinnedGroupInput struct {
	ID   int64 `path:"id" doc:"Series id"`
	Body struct {
		Group string `json:"group" maxLength:"100" doc:"Release group to pin above profile scoring; empty clears the pin"`
		// maximum mirrors acquire.MaxPinDelayHours, which a struct tag cannot
		// reference: past it the duration multiply wraps and the wait vanishes.
		DelayHours *int `json:"delay_hours,omitempty" minimum:"0" maximum:"8760" doc:"Hours the scheduled sweep waits for this group before taking another (max 8760); omit to use the global default"`
	}
}

type setPinnedGroupOutput struct {
	Body struct {
		SeriesID    int64  `json:"series_id"`
		PinnedGroup string `json:"pinned_group,omitempty"`
	}
}

type deleteSeriesInput struct {
	ID              int64 `path:"id" doc:"Series id"`
	RemoveDownloads bool  `query:"remove_downloads" doc:"Also remove the series' torrents (and their data) from the download client; otherwise they are left seeding"`
}

type grabEventDTO struct {
	ID           int64  `json:"id"`
	ItemNumber   int    `json:"item_number"`
	ReleaseTitle string `json:"release_title"`
	InfoHash     string `json:"infohash"`
	Status       string `json:"status" enum:"grabbed,imported,import_deferred,failed"`
	Detail       string `json:"detail,omitempty" doc:"Why a failed event failed"`
	CreatedAt    string `json:"created_at"`
}

type seriesGrabsInput struct {
	ID int64 `path:"id" doc:"Series id"`
}

type seriesGrabsOutput struct {
	Body struct {
		Series string         `json:"series"`
		Events []grabEventDTO `json:"events"`
	}
}

// seriesHandler owns the dependencies shared by the series endpoints — the
// read/CRUD handlers here and the acquisition handlers in
// series_acquisition_routes.go. Bundling them on a receiver lets both files hang
// handlers off the same type and share helpers like requireSeries without
// threading deps through every call.
type seriesHandler struct {
	store   *store.Store
	catalog *catalog.Service
	clients *clients.Registry
	acquire *acquire.Service
}

func newSeriesHandler(deps routeDeps) *seriesHandler {
	return &seriesHandler{
		store:   deps.store,
		catalog: deps.catalog,
		clients: deps.clients,
		acquire: deps.acquire,
	}
}

// requireSeries loads a series or returns the appropriate huma status error (404
// when absent, 500 otherwise), so handlers can `return nil, err` on failure.
func (h *seriesHandler) requireSeries(ctx context.Context, id int64) (db.Series, error) {
	series, err := h.store.Q.GetSeries(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Series{}, huma.Error404NotFound("series not found")
	}
	if err != nil {
		return db.Series{}, huma.Error500InternalServerError("failed to load series", err)
	}
	return series, nil
}

// registerSeriesRoutes wires the series read/CRUD endpoints: listing, adding,
// fetching detail with derived acquisition state, monitoring toggle, and grab
// history. The search/grab acquisition endpoints live in
// series_acquisition_routes.go as methods on the same seriesHandler.
func registerSeriesRoutes(api huma.API, deps routeDeps) {
	h := newSeriesHandler(deps)

	huma.Register(api, huma.Operation{
		OperationID: "list-series",
		Method:      http.MethodGet,
		Path:        "/api/v1/series",
		Summary:     "List monitored series",
		Tags:        []string{"series"},
	}, h.listSeries)

	huma.Register(api, huma.Operation{
		OperationID:   "add-series",
		Method:        http.MethodPost,
		Path:          "/api/v1/series",
		Summary:       "Add a series by AniList id (expands its wanted items)",
		Tags:          []string{"series"},
		DefaultStatus: http.StatusCreated,
	}, h.addSeries)

	huma.Register(api, huma.Operation{
		OperationID: "get-series",
		Method:      http.MethodGet,
		Path:        "/api/v1/series/{id}",
		Summary:     "Get a series with its wanted items and their acquisition state",
		Tags:        []string{"series"},
	}, h.getSeries)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-series",
		Method:        http.MethodDelete,
		Path:          "/api/v1/series/{id}",
		Summary:       "Delete a series and everything tracked for it; library files are never touched",
		Tags:          []string{"series"},
		DefaultStatus: http.StatusNoContent,
	}, h.deleteSeries)

	huma.Register(api, huma.Operation{
		OperationID: "set-series-monitored",
		Method:      http.MethodPatch,
		Path:        "/api/v1/series/{id}",
		Summary:     "Update whether a series is monitored",
		Tags:        []string{"series"},
	}, h.setMonitored)

	huma.Register(api, huma.Operation{
		OperationID: "set-series-pinned-group",
		Method:      http.MethodPut,
		Path:        "/api/v1/series/{id}/pinned-group",
		Summary:     "Pin a release group for a series (an absolute tier above profile scoring)",
		Tags:        []string{"series"},
	}, h.setPinnedGroup)

	huma.Register(api, huma.Operation{
		OperationID: "list-series-grabs",
		Method:      http.MethodGet,
		Path:        "/api/v1/series/{id}/grabs",
		Summary:     "List grab/import history for a series",
		Tags:        []string{"series"},
	}, h.listGrabs)
}

func (h *seriesHandler) listSeries(ctx context.Context, _ *struct{}) (*listSeriesOutput, error) {
	rows, err := h.store.Q.ListSeriesWithProgress(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list series", err)
	}
	out := &listSeriesOutput{}
	out.Body.Series = make([]seriesDTO, 0, len(rows))
	for _, s := range rows {
		out.Body.Series = append(out.Body.Series, seriesDTO{
			ID:        s.ID,
			Title:     s.Title,
			Format:    s.Format,
			Monitored: s.Monitored == 1,
			Total:     int(s.TotalItems),
			Have:      int(s.HaveItems),
		})
	}
	return out, nil
}

func (h *seriesHandler) addSeries(ctx context.Context, in *addSeriesInput) (*addSeriesOutput, error) {
	monitored := true
	if in.Body.Monitored != nil {
		monitored = *in.Body.Monitored
	}

	title, err := h.catalog.AddSeries(ctx, in.Body.AniListID, monitored)
	if errors.Is(err, catalog.ErrAlreadyExists) {
		return nil, huma.Error409Conflict("series already exists")
	}
	if err != nil {
		return nil, huma.Error502BadGateway("failed to add series", err)
	}
	// Dispatch is async, so this adds no request latency.
	if d := h.clients.Notify(); d != nil {
		d.Dispatch(ctx, notify.Event{Kind: notify.KindSeriesAdded, SeriesTitle: title.Name})
	}

	out := &addSeriesOutput{}
	out.Body = seriesDetailDTO{
		ID:        title.ID,
		AniListID: title.AniListID,
		Title:     title.Name,
		Format:    string(title.Format),
		Monitored: title.Monitored,
		Items:     make([]wantedItemDTO, 0, len(title.Items)),
	}
	for _, it := range title.Items {
		out.Body.Items = append(out.Body.Items, wantedItemDTO{
			ID:     it.ID,
			Number: it.Number,
			Name:   it.Name,
			Have:   it.Have,
		})
	}
	return out, nil
}

func (h *seriesHandler) getSeries(ctx context.Context, in *getSeriesInput) (*getSeriesOutput, error) {
	series, err := h.requireSeries(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	rows, err := h.store.Q.ListWantedItems(ctx, series.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load wanted items", err)
	}

	// Index active grabs by wanted item so each row can report downloading state.
	grabRows, err := h.store.Q.ListGrabsBySeries(ctx, series.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load grabs", err)
	}
	grabByItem := make(map[int64]db.Grab, len(grabRows))
	for _, g := range grabRows {
		grabByItem[g.WantedItemID] = g
	}

	out := &getSeriesOutput{}
	out.Body = seriesDetailReadDTO{
		ID:               series.ID,
		Title:            series.Title,
		Format:           series.Format,
		Monitored:        series.Monitored == 1,
		QualityProfileID: series.QualityProfileID,
		PinnedGroup:      series.PinnedGroup.String,
		Items:            make([]detailItemDTO, 0, len(rows)),
	}
	if series.PinDelayHours.Valid {
		hours := int(series.PinDelayHours.Int64)
		out.Body.PinDelayHours = &hours
	}
	if series.AnilistID.Valid {
		out.Body.AniListID = series.AnilistID.Int64
		// Best-effort enrichment from the metadata cache (no network call): the
		// series row only stores the display title, so english/native/status come
		// from the cached AniList snapshot when present.
		if row, cerr := h.store.Q.GetCachedMetadata(ctx, db.GetCachedMetadataParams{
			Provider:   "anilist",
			ProviderID: series.AnilistID.Int64,
		}); cerr == nil {
			var snap metadata.CachedTitle
			if json.Unmarshal([]byte(row.Raw), &snap) == nil {
				out.Body.English = snap.Title.Titles.English
				out.Body.Native = snap.Title.Titles.Native
				out.Body.Status = snap.Title.Status
				out.Body.CoverURL = snap.Title.CoverURL
			}
		}
	}

	for _, r := range rows {
		g, ok := grabByItem[r.ID]
		state := deriveItemState(r.Have == 1, g, ok)
		out.Body.Items = append(out.Body.Items, detailItemDTO{
			ID:           r.ID,
			Number:       int(r.Number.Int64),
			Name:         r.Title.String,
			Have:         r.Have == 1,
			Status:       state.Status,
			ReleaseTitle: state.ReleaseTitle,
			ImportError:  state.ImportError,
			AirsAt:       storedTimeRFC3339(r.AirsAt),
		})
	}
	return out, nil
}

// deleteSeries removes a series and, via FK cascades, its wanted items, grabs,
// and blocklist memory. The client removal runs first so a refusal leaves the
// series intact and the delete retryable; delete-first would orphan torrents
// with no record and no retry path.
func (h *seriesHandler) deleteSeries(ctx context.Context, in *deleteSeriesInput) (*struct{}, error) {
	if _, err := h.requireSeries(ctx, in.ID); err != nil {
		return nil, err
	}
	if in.RemoveDownloads {
		grabs, err := h.store.Q.ListGrabsBySeries(ctx, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to load grabs", err)
		}
		// Every status but failed still has a client entry: imported torrents seed
		// and deferred payloads sit in the client; failed means errored or gone.
		seen := make(map[string]bool, len(grabs))
		hashes := make([]string, 0, len(grabs))
		for _, g := range grabs {
			hash := strings.ToLower(g.InfoHash)
			if g.Status == "failed" || hash == "" || seen[hash] {
				continue
			}
			seen[hash] = true
			hashes = append(hashes, hash)
		}
		if len(hashes) > 0 {
			dl := h.clients.Download()
			if dl == nil {
				return nil, acquireHTTPError(acquire.ErrNoDownloadClient)
			}
			if err := dl.Remove(ctx, hashes, true); err != nil {
				return nil, huma.Error502BadGateway("failed to remove downloads from the client", err)
			}
		}
	}
	rows, err := h.store.Q.DeleteSeries(ctx, in.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete series", err)
	}
	if rows == 0 {
		return nil, huma.Error404NotFound("series not found")
	}
	return nil, nil
}

func (h *seriesHandler) setMonitored(ctx context.Context, in *setMonitoredInput) (*setMonitoredOutput, error) {
	if _, err := h.requireSeries(ctx, in.ID); err != nil {
		return nil, err
	}
	if err := h.store.Q.SetSeriesMonitored(ctx, db.SetSeriesMonitoredParams{
		Monitored: boolToInt(in.Body.Monitored),
		ID:        in.ID,
	}); err != nil {
		return nil, huma.Error500InternalServerError("failed to update series", err)
	}
	// Monitoring a series again asks for it to be looked after now, not once a
	// backoff accumulated before it was paused has run down.
	if in.Body.Monitored {
		if err := h.store.Q.ResetSeriesSearchState(ctx, in.ID); err != nil {
			return nil, huma.Error500InternalServerError("failed to reset the search cadence", err)
		}
	}
	out := &setMonitoredOutput{}
	out.Body.ID = in.ID
	out.Body.Monitored = in.Body.Monitored
	return out, nil
}

func (h *seriesHandler) setPinnedGroup(ctx context.Context, in *setPinnedGroupInput) (*setPinnedGroupOutput, error) {
	group := strings.TrimSpace(in.Body.Group)
	// PUT replaces: an omitted delay falls back to the global default, and a
	// cleared group takes its delay with it.
	var delay sql.NullInt64
	if group != "" && in.Body.DelayHours != nil {
		delay = sql.NullInt64{Int64: int64(*in.Body.DelayHours), Valid: true}
	}
	rows, err := h.store.Q.SetSeriesPinnedGroup(ctx, db.SetSeriesPinnedGroupParams{
		PinnedGroup:   sql.NullString{String: group, Valid: group != ""},
		PinDelayHours: delay,
		ID:            in.ID,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update series", err)
	}
	if rows == 0 {
		return nil, huma.Error404NotFound("series not found")
	}
	// A held series' next_search_at was computed from the pin that just changed,
	// so without this a shortened wait or a new group does nothing until the old
	// window closes.
	if err := h.store.Q.ResetSeriesSearchState(ctx, in.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to reset the search cadence", err)
	}
	out := &setPinnedGroupOutput{}
	out.Body.SeriesID = in.ID
	out.Body.PinnedGroup = group
	return out, nil
}

func (h *seriesHandler) listGrabs(ctx context.Context, in *seriesGrabsInput) (*seriesGrabsOutput, error) {
	series, err := h.requireSeries(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	events, err := h.store.Q.ListSeriesGrabEvents(ctx, series.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load grab history", err)
	}

	out := &seriesGrabsOutput{}
	out.Body.Series = series.Title
	out.Body.Events = make([]grabEventDTO, 0, len(events))
	for _, e := range events {
		out.Body.Events = append(out.Body.Events, grabEventDTO{
			ID:           e.ID,
			ItemNumber:   int(e.ItemNumber),
			ReleaseTitle: e.ReleaseTitle,
			InfoHash:     e.InfoHash,
			Status:       e.Event,
			Detail:       e.Detail,
			CreatedAt:    e.CreatedAt,
		})
	}
	return out, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// storedTimeRFC3339 restores the zone the stored form drops. Emitting it raw would
// leave a browser to read a UTC instant as local time and shift the row by hours.
// An unparseable value degrades to absent, matching a value that was never set.
func storedTimeRFC3339(stored sql.NullString) string {
	if !stored.Valid {
		return ""
	}
	t, err := store.ParseTimestamp(stored.String)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
