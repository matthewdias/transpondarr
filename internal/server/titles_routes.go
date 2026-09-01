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

// titleDTO carries two denominators deliberately: tracked is what the title is
// actually pursuing (monitored and broadcast), total is every item it has. total
// keeps its old meaning because narrowing it in place would be a silent break
// for API clients.
type titleDTO struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Format         string `json:"format"`
	Year           int    `json:"year,omitempty" doc:"Release year; absent when the provider publishes none"`
	Monitored      bool   `json:"monitored"`
	Total          int    `json:"total"`
	Tracked        int    `json:"tracked" doc:"Items this title is pursuing: monitored and already broadcast"`
	MonitoredItems int    `json:"monitored_items" doc:"Monitored items whether or not they have aired, so a zero tracked count can name its cause"`
	InLibrary      int    `json:"in_library" doc:"Held items inside the tracked set, so progress can never exceed it"`
	// The state is per item and this row is per title, so it is published only
	// where format guarantees the two are the same thing (#208).
	ItemStatus  string `json:"item_status,omitempty" enum:"in_library,downloading,stuck,deferred,wanted" doc:"Derived acquisition state of the sole item; present for a movie only"`
	ImportError string `json:"import_error,omitempty" doc:"Why the last import attempt failed (item_status stuck)"`
}

type listTitlesOutput struct {
	Body struct {
		Titles []titleDTO `json:"titles"`
	}
}

type wantedItemDTO struct {
	ID        int64  `json:"id"`
	Number    int    `json:"number"`
	Name      string `json:"name,omitempty"`
	InLibrary bool   `json:"in_library"`
}

type titleDetailDTO struct {
	ID         int64           `json:"id"`
	Provider   string          `json:"provider"`
	ProviderID int64           `json:"provider_id"`
	Title      string          `json:"title"`
	Format     string          `json:"format"`
	Year       int             `json:"year,omitempty" doc:"Release year; absent when the provider publishes none"`
	Monitored  bool            `json:"monitored"`
	Items      []wantedItemDTO `json:"items"`
}

// provider is required rather than defaulted: a default hides which id space the
// caller meant, which is the bug class the pair exists to prevent.
type addTitleInput struct {
	Body struct {
		Provider   string `json:"provider" required:"true" enum:"anilist" doc:"Metadata provider whose id space provider_id is numbered in"`
		ProviderID int64  `json:"provider_id" required:"true" minimum:"1" doc:"The provider's id for the title to add"`
		Monitored  *bool  `json:"monitored,omitempty" doc:"Whether to monitor for downloads (default true)"`
		// The default must stay "all": an omitted field has to keep meaning
		// today's behaviour for a client that never learned about the choice.
		MonitorItems string `json:"monitor_items,omitempty" enum:"all,future" default:"all" doc:"Which items to monitor, now and as the title grows: all, or only those that have not yet aired"`
		// Carried in the add so it is one atomic write; omitted (0) takes the
		// default profile, which stays right as that default changes.
		QualityProfileID int64 `json:"quality_profile_id,omitempty" minimum:"1" doc:"Quality profile to assign; omitted takes the default profile"`
	}
}

type addTitleOutput struct {
	Body titleDetailDTO
}

// detailItemDTO is one wanted item with its derived acquisition state, so the UI
// can render each episode row (in_library / downloading / deferred / wanted)
// without a second call.
type detailItemDTO struct {
	ID           int64  `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name,omitempty"`
	InLibrary    bool   `json:"in_library"`
	Monitored    bool   `json:"monitored" doc:"Whether automation pursues this item; candidacy and possession stay separate fields"`
	Status       string `json:"status" enum:"in_library,downloading,stuck,deferred,wanted" doc:"Derived acquisition state"`
	ReleaseTitle string `json:"release_title,omitempty"`
	ImportError  string `json:"import_error,omitempty" doc:"Why the last import attempt failed (status stuck)"`
	AirsAt       string `json:"airs_at,omitempty" format:"date-time" doc:"Broadcast time (RFC 3339, Japanese broadcast clock); absent when the provider publishes none"`
}

type titleDetailReadDTO struct {
	ID               int64           `json:"id"`
	Provider         string          `json:"provider,omitempty" doc:"Metadata provider this title is keyed on; absent when untracked"`
	ProviderID       int64           `json:"provider_id,omitempty"`
	Title            string          `json:"title"`
	English          string          `json:"english,omitempty"`
	Native           string          `json:"native,omitempty"`
	Format           string          `json:"format"`
	Year             int             `json:"year,omitempty" doc:"Release year; absent when the provider publishes none"`
	Status           string          `json:"status,omitempty" doc:"Provider status (e.g. RELEASING, FINISHED)"`
	CoverURL         string          `json:"cover_url,omitempty"`
	Monitored        bool            `json:"monitored"`
	QualityProfileID int64           `json:"quality_profile_id"`
	PinnedGroup      string          `json:"pinned_group,omitempty" doc:"Release group pinned above profile scoring; absent when none"`
	PinDelayHours    *int            `json:"pin_delay_hours,omitempty" doc:"Per-title override of how long the sweep waits for the pinned group; absent means the global default"`
	Items            []detailItemDTO `json:"items"`
}

type getTitleInput struct {
	ID int64 `path:"id" doc:"Title id"`
}

type getTitleOutput struct {
	Body titleDetailReadDTO
}

type setMonitoredInput struct {
	ID   int64 `path:"id" doc:"Title id"`
	Body struct {
		Monitored bool `json:"monitored" doc:"Whether to monitor the title for downloads"`
	}
}

type setMonitoredOutput struct {
	Body struct {
		ID        int64 `json:"id"`
		Monitored bool  `json:"monitored"`
	}
}

type setPinnedGroupInput struct {
	ID   int64 `path:"id" doc:"Title id"`
	Body struct {
		Group string `json:"group" maxLength:"100" doc:"Release group to pin above profile scoring; empty clears the pin"`
		// maximum mirrors acquire.MaxPinDelayHours, which a struct tag cannot
		// reference: past it the duration multiply wraps and the wait vanishes.
		DelayHours *int `json:"delay_hours,omitempty" minimum:"0" maximum:"8760" doc:"Hours the scheduled sweep waits for this group before taking another (max 8760); omit to use the global default"`
	}
}

type setPinnedGroupOutput struct {
	Body struct {
		TitleID     int64  `json:"title_id"`
		PinnedGroup string `json:"pinned_group,omitempty"`
	}
}

// count is required with no default, per #227: an omitted field must not be
// able to choose a value, least of all the numbering bound decide reads.
type setItemCountInput struct {
	ID   int64 `path:"id" doc:"Title id"`
	Body struct {
		Count int `json:"count" required:"true" minimum:"1" maximum:"5000" doc:"How many items the title has; creates items 1..count"`
	}
}

type setItemCountOutput struct {
	Body struct {
		Created int `json:"created"`
	}
}

type deleteTitleInput struct {
	ID              int64 `path:"id" doc:"Title id"`
	RemoveDownloads bool  `query:"remove_downloads" doc:"Also remove the title's torrents (and their data) from the download client; otherwise they are left seeding"`
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

type titleGrabsInput struct {
	ID int64 `path:"id" doc:"Title id"`
}

type titleGrabsOutput struct {
	Body struct {
		Title  string         `json:"title"`
		Events []grabEventDTO `json:"events"`
	}
}

// titleHandler owns the dependencies shared by the title endpoints — the
// read/CRUD handlers here and the acquisition handlers in
// titles_acquisition_routes.go. Bundling them on a receiver lets both files hang
// handlers off the same type and share helpers like requireTitle without
// threading deps through every call.
type titleHandler struct {
	store   *store.Store
	catalog *catalog.Service
	clients *clients.Registry
	acquire *acquire.Service
}

func newTitleHandler(deps routeDeps) *titleHandler {
	return &titleHandler{
		store:   deps.store,
		catalog: deps.catalog,
		clients: deps.clients,
		acquire: deps.acquire,
	}
}

// requireTitle loads a title or returns the appropriate huma status error (404
// when absent, 500 otherwise), so handlers can `return nil, err` on failure.
func (h *titleHandler) requireTitle(ctx context.Context, id int64) (db.Series, error) {
	title, err := h.store.Q.GetTitle(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Series{}, huma.Error404NotFound("series not found")
	}
	if err != nil {
		return db.Series{}, huma.Error500InternalServerError("failed to load series", err)
	}
	return title, nil
}

// registerTitleRoutes wires the title read/CRUD endpoints: listing, adding,
// fetching detail with derived acquisition state, monitoring toggle, and grab
// history. The search/grab acquisition endpoints live in
// titles_acquisition_routes.go as methods on the same titleHandler.
func registerTitleRoutes(api huma.API, deps routeDeps) {
	h := newTitleHandler(deps)

	huma.Register(api, huma.Operation{
		OperationID: "list-titles",
		Method:      http.MethodGet,
		Path:        "/api/v1/titles",
		Summary:     "List monitored titles",
		Tags:        []string{"titles"},
	}, h.listTitles)

	huma.Register(api, huma.Operation{
		OperationID:   "add-title",
		Method:        http.MethodPost,
		Path:          "/api/v1/titles",
		Summary:       "Add a title by AniList id (expands its wanted items)",
		Tags:          []string{"titles"},
		DefaultStatus: http.StatusCreated,
	}, h.addTitle)

	huma.Register(api, huma.Operation{
		OperationID: "get-title",
		Method:      http.MethodGet,
		Path:        "/api/v1/titles/{id}",
		Summary:     "Get a title with its wanted items and their acquisition state",
		Tags:        []string{"titles"},
	}, h.getTitle)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-title",
		Method:        http.MethodDelete,
		Path:          "/api/v1/titles/{id}",
		Summary:       "Delete a title and everything tracked for it; library files are never touched",
		Tags:          []string{"titles"},
		DefaultStatus: http.StatusNoContent,
	}, h.deleteTitle)

	huma.Register(api, huma.Operation{
		OperationID: "set-title-monitored",
		Method:      http.MethodPatch,
		Path:        "/api/v1/titles/{id}",
		Summary:     "Update whether a title is monitored",
		Tags:        []string{"titles"},
	}, h.setMonitored)

	huma.Register(api, huma.Operation{
		OperationID: "set-title-pinned-group",
		Method:      http.MethodPut,
		Path:        "/api/v1/titles/{id}/pinned-group",
		Summary:     "Pin a release group for a title (an absolute tier above profile scoring)",
		Tags:        []string{"titles"},
	}, h.setPinnedGroup)

	huma.Register(api, huma.Operation{
		OperationID:   "set-title-item-count",
		Method:        http.MethodPost,
		Path:          "/api/v1/titles/{id}/items",
		Summary:       "Create items 1..count for a title the provider published no episode count for",
		Tags:          []string{"titles"},
		DefaultStatus: http.StatusCreated,
	}, h.setItemCount)

	huma.Register(api, huma.Operation{
		OperationID: "list-title-grabs",
		Method:      http.MethodGet,
		Path:        "/api/v1/titles/{id}/grabs",
		Summary:     "List grab/import history for a title",
		Tags:        []string{"titles"},
	}, h.listGrabs)
}

func (h *titleHandler) listTitles(ctx context.Context, _ *struct{}) (*listTitlesOutput, error) {
	now := sql.NullString{String: store.FormatTimestamp(time.Now()), Valid: true}
	rows, err := h.store.Q.ListTitlesWithProgress(ctx, db.ListTitlesWithProgressParams{
		AirsAt: now, AirsAt_2: now,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list series", err)
	}
	// A second pass rather than a wider aggregate: the state reads the item's
	// grab, which no GROUP BY can carry, and deriving it once here keeps the
	// counts query -- and so a series' progress column -- untouched.
	movieRows, err := h.store.Q.ListMovieItemStates(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load movie item state", err)
	}
	stateByTitle := make(map[int64]itemState, len(movieRows))
	for _, r := range movieRows {
		// First by number, which is the item the detail page renders: #208
		// guarantees one item only for a film added since it, and 00022 leaves a
		// legacy movie the several it was created with.
		if _, seen := stateByTitle[r.SeriesID]; seen {
			continue
		}
		stateByTitle[r.SeriesID] = deriveItemState(r.InLibrary == 1, db.Grab{
			Status:       r.GrabStatus.String,
			ReleaseTitle: r.GrabReleaseTitle.String,
			LastError:    r.GrabLastError,
		}, r.GrabStatus.Valid)
	}
	out := &listTitlesOutput{}
	out.Body.Titles = make([]titleDTO, 0, len(rows))
	for _, s := range rows {
		dto := titleDTO{
			ID:             s.ID,
			Title:          s.Title,
			Format:         s.Format,
			Year:           int(s.Year),
			Monitored:      s.Monitored == 1,
			Total:          int(s.TotalItems),
			Tracked:        int(s.TrackedItems),
			MonitoredItems: int(s.MonitoredItems),
			InLibrary:      int(s.InLibraryItems),
		}
		if state, ok := stateByTitle[s.ID]; ok {
			dto.ItemStatus = state.Status
			dto.ImportError = state.ImportError
		}
		out.Body.Titles = append(out.Body.Titles, dto)
	}
	return out, nil
}

func (h *titleHandler) addTitle(ctx context.Context, in *addTitleInput) (*addTitleOutput, error) {
	monitored := true
	if in.Body.Monitored != nil {
		monitored = *in.Body.Monitored
	}

	mode := catalog.MonitorAll
	if in.Body.MonitorItems != "" {
		mode = catalog.MonitorMode(in.Body.MonitorItems)
	}

	title, err := h.catalog.AddTitle(ctx, in.Body.Provider, in.Body.ProviderID, monitored, mode, in.Body.QualityProfileID)
	if errors.Is(err, catalog.ErrAlreadyExists) {
		return nil, huma.Error409Conflict("series already exists")
	}
	if errors.Is(err, catalog.ErrUnknownProfile) {
		return nil, huma.Error422UnprocessableEntity("profile does not exist")
	}
	// Unreachable while the request enum lists exactly the configured provider --
	// huma rejects anything else at validation with a 422. It fires once the enum
	// widens past what is actually wired up.
	if errors.Is(err, catalog.ErrUnknownProvider) {
		return nil, huma.Error400BadRequest("unknown metadata provider", err)
	}
	if err != nil {
		return nil, huma.Error502BadGateway("failed to add series", err)
	}
	// Dispatch is async, so this adds no request latency.
	if d := h.clients.Notify(); d != nil {
		d.Dispatch(ctx, notify.Event{Kind: notify.KindTitleAdded, Title: title.Name})
	}

	out := &addTitleOutput{}
	out.Body = titleDetailDTO{
		ID:         title.ID,
		Provider:   title.Provider,
		ProviderID: title.ProviderID,
		Title:      title.Name,
		Format:     string(title.Format),
		Year:       title.Year,
		Monitored:  title.Monitored,
		Items:      make([]wantedItemDTO, 0, len(title.Items)),
	}
	for _, it := range title.Items {
		out.Body.Items = append(out.Body.Items, wantedItemDTO{
			ID:        it.ID,
			Number:    it.Number,
			Name:      it.Name,
			InLibrary: it.InLibrary,
		})
	}
	return out, nil
}

func (h *titleHandler) getTitle(ctx context.Context, in *getTitleInput) (*getTitleOutput, error) {
	title, err := h.requireTitle(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	rows, err := h.store.Q.ListWantedItems(ctx, title.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load wanted items", err)
	}

	// Index active grabs by wanted item so each row can report downloading state.
	grabRows, err := h.store.Q.ListGrabsByTitle(ctx, title.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load grabs", err)
	}
	grabByItem := make(map[int64]db.Grab, len(grabRows))
	for _, g := range grabRows {
		grabByItem[g.WantedItemID] = g
	}

	out := &getTitleOutput{}
	out.Body = titleDetailReadDTO{
		ID:               title.ID,
		Title:            title.Title,
		Format:           title.Format,
		Year:             int(title.Year),
		Monitored:        title.Monitored == 1,
		QualityProfileID: title.QualityProfileID,
		PinnedGroup:      title.PinnedGroup.String,
		Items:            make([]detailItemDTO, 0, len(rows)),
	}
	if title.PinDelayHours.Valid {
		hours := int(title.PinDelayHours.Int64)
		out.Body.PinDelayHours = &hours
	}
	if title.ProviderID.Valid {
		out.Body.Provider = title.Provider.String
		out.Body.ProviderID = title.ProviderID.Int64
		// Best-effort enrichment from the metadata cache (no network call): the
		// title row only stores the display title, so english/native/status come
		// from the cached snapshot when present.
		if row, cerr := h.store.Q.GetCachedMetadata(ctx, db.GetCachedMetadataParams{
			Provider:   title.Provider.String,
			ProviderID: title.ProviderID.Int64,
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
		state := deriveItemState(r.InLibrary == 1, g, ok)
		out.Body.Items = append(out.Body.Items, detailItemDTO{
			ID:           r.ID,
			Number:       int(r.Number.Int64),
			Name:         r.Title.String,
			InLibrary:    r.InLibrary == 1,
			Monitored:    r.Monitored == 1,
			Status:       state.Status,
			ReleaseTitle: state.ReleaseTitle,
			ImportError:  state.ImportError,
			AirsAt:       storedTimeRFC3339(r.AirsAt),
		})
	}
	return out, nil
}

// deleteTitle removes a title and, via FK cascades, its wanted items, grabs,
// and blocklist memory. The client removal runs first so a refusal leaves the
// title intact and the delete retryable; delete-first would orphan torrents
// with no record and no retry path.
func (h *titleHandler) deleteTitle(ctx context.Context, in *deleteTitleInput) (*struct{}, error) {
	if _, err := h.requireTitle(ctx, in.ID); err != nil {
		return nil, err
	}
	if in.RemoveDownloads {
		grabs, err := h.store.Q.ListGrabsByTitle(ctx, in.ID)
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
	rows, err := h.store.Q.DeleteTitle(ctx, in.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete series", err)
	}
	if rows == 0 {
		return nil, huma.Error404NotFound("series not found")
	}
	return nil, nil
}

func (h *titleHandler) setMonitored(ctx context.Context, in *setMonitoredInput) (*setMonitoredOutput, error) {
	if _, err := h.requireTitle(ctx, in.ID); err != nil {
		return nil, err
	}
	if err := h.store.Q.SetTitleMonitored(ctx, db.SetTitleMonitoredParams{
		Monitored: boolToInt(in.Body.Monitored),
		ID:        in.ID,
	}); err != nil {
		return nil, huma.Error500InternalServerError("failed to update series", err)
	}
	// Monitoring a title again asks for it to be looked after now, not once a
	// backoff accumulated before it was paused has run down.
	if in.Body.Monitored {
		if err := h.store.Q.ResetTitleSearchState(ctx, in.ID); err != nil {
			return nil, huma.Error500InternalServerError("failed to reset the search cadence", err)
		}
	}
	out := &setMonitoredOutput{}
	out.Body.ID = in.ID
	out.Body.Monitored = in.Body.Monitored
	return out, nil
}

// Guarded to a title with no items: it bounds the escape hatch to the dead end
// it exists for, since maxItem is the bound decide uses to distrust a release's
// own numbering and raising it on a healthy title would make that guard inert.
func (h *titleHandler) setItemCount(ctx context.Context, in *setItemCountInput) (*setItemCountOutput, error) {
	created, err := h.catalog.SetItemCount(ctx, in.ID, in.Body.Count)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, huma.Error404NotFound("series not found")
	case errors.Is(err, catalog.ErrTitleHasItems):
		return nil, huma.Error409Conflict("series already has episodes")
	case err != nil:
		return nil, huma.Error500InternalServerError("failed to create the series' items", err)
	}
	out := &setItemCountOutput{}
	out.Body.Created = int(created)
	return out, nil
}

func (h *titleHandler) setPinnedGroup(ctx context.Context, in *setPinnedGroupInput) (*setPinnedGroupOutput, error) {
	group := strings.TrimSpace(in.Body.Group)
	// PUT replaces: an omitted delay falls back to the global default, and a
	// cleared group takes its delay with it.
	var delay sql.NullInt64
	if group != "" && in.Body.DelayHours != nil {
		delay = sql.NullInt64{Int64: int64(*in.Body.DelayHours), Valid: true}
	}
	rows, err := h.store.Q.SetTitlePinnedGroup(ctx, db.SetTitlePinnedGroupParams{
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
	// A held title's next_search_at was computed from the pin that just changed,
	// so without this a shortened wait or a new group does nothing until the old
	// window closes.
	if err := h.store.Q.ResetTitleSearchState(ctx, in.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to reset the search cadence", err)
	}
	out := &setPinnedGroupOutput{}
	out.Body.TitleID = in.ID
	out.Body.PinnedGroup = group
	return out, nil
}

func (h *titleHandler) listGrabs(ctx context.Context, in *titleGrabsInput) (*titleGrabsOutput, error) {
	title, err := h.requireTitle(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	events, err := h.store.Q.ListTitleGrabEvents(ctx, title.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load grab history", err)
	}

	out := &titleGrabsOutput{}
	out.Body.Title = title.Title
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
