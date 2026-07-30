package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type candidateReleaseDTO struct {
	Title        string `json:"title"`
	DownloadURL  string `json:"download_url"`
	InfoHash     string `json:"infohash,omitempty"`
	Size         int64  `json:"size"`
	Seeders      int    `json:"seeders"`
	ReleaseGroup string `json:"release_group,omitempty"`
	Resolution   string `json:"resolution,omitempty"`
	DualAudio    bool   `json:"dual_audio"`
	Matched      bool   `json:"matched"`
	Items        []int  `json:"items,omitempty"`
	Reason       string `json:"reason"`

	Score            int            `json:"score" doc:"Profile score; ranking is by this, seeders only break ties"`
	ScoreParts       []scorePartDTO `json:"score_parts,omitempty" doc:"Per-axis contributions summing to score"`
	Eligible         bool           `json:"eligible"`
	IneligibleReason string         `json:"ineligible_reason,omitempty" doc:"Why the profile refuses this release; empty when eligible"`
	Pinned           bool           `json:"pinned" doc:"Release group is the series' pinned group; ranks above profile score when eligible"`
}

type scorePartDTO struct {
	Label  string `json:"label"`
	Points int    `json:"points"`
}

type searchSeriesInput struct {
	ID int64 `path:"id" doc:"Series id to find releases for"`
}

type searchSeriesOutput struct {
	Body struct {
		Series  string                `json:"series"`
		Term    string                `json:"term"`
		Results []candidateReleaseDTO `json:"results"`
	}
}

type grabSeriesInput struct {
	ID   int64 `path:"id" doc:"Series id"`
	Body struct {
		DownloadURL string `json:"download_url" required:"true" doc:"download_url of a matched release from the series search"`
		Paused      bool   `json:"paused,omitempty" doc:"Add the torrent stopped (no data transfer) — useful for testing the grab flow"`
	}
}

type grabSeriesOutput struct {
	Body struct {
		InfoHash         string `json:"infohash"`
		Outcome          string `json:"outcome" example:"success"`
		Release          string `json:"release"`
		Items            []int  `json:"items"`
		IneligibleReason string `json:"ineligible_reason,omitempty" doc:"Set when the grabbed release falls outside the series' quality profile — informational, the grab still succeeds"`
	}
}

// registerSeriesAcquisitionRoutes wires the series acquisition endpoints: the
// read-only release search (match against wanted items) and the grab that hands
// a chosen release to the download client and records it. The handlers are
// methods on seriesHandler (defined in series_routes.go), so they share one
// acquire.Service with the scheduled sweep.
func registerSeriesAcquisitionRoutes(api huma.API, deps routeDeps) {
	h := newSeriesHandler(deps)

	huma.Register(api, huma.Operation{
		OperationID: "search-series-releases",
		Method:      http.MethodGet,
		Path:        "/api/v1/series/{id}/search",
		Summary:     "Find and match indexer releases against a series' wanted items (read-only; does not grab)",
		Tags:        []string{"series"},
	}, h.searchReleases)

	huma.Register(api, huma.Operation{
		OperationID:   "grab-series-release",
		Method:        http.MethodPost,
		Path:          "/api/v1/series/{id}/grab",
		Summary:       "Grab a chosen release: add it to the download client and record it against the wanted items it covers",
		Tags:          []string{"series"},
		DefaultStatus: http.StatusCreated,
	}, h.grabRelease)
}

func (h *seriesHandler) searchReleases(ctx context.Context, in *searchSeriesInput) (*searchSeriesOutput, error) {
	m, err := h.acquire.MatchSeries(ctx, in.ID)
	if err != nil {
		return nil, acquireHTTPError(err)
	}
	out := &searchSeriesOutput{}
	out.Body.Series = m.Series.Title
	out.Body.Term = m.Term
	out.Body.Results = make([]candidateReleaseDTO, 0, len(m.Candidates))
	for _, c := range m.Candidates {
		parts := make([]scorePartDTO, 0, len(c.ScoreParts))
		for _, p := range c.ScoreParts {
			parts = append(parts, scorePartDTO{Label: p.Label, Points: p.Points})
		}
		out.Body.Results = append(out.Body.Results, candidateReleaseDTO{
			Title:            c.Release.Title,
			DownloadURL:      c.Release.DownloadURL,
			InfoHash:         c.Release.InfoHash,
			Size:             c.Release.Size,
			Seeders:          c.Release.Seeders,
			ReleaseGroup:     c.Release.ReleaseGroup,
			Resolution:       c.Release.Resolution,
			DualAudio:        c.Release.DualAudio,
			Matched:          c.Matched,
			Items:            c.Items,
			Reason:           c.Reason,
			Score:            c.Score,
			ScoreParts:       parts,
			Eligible:         c.Eligible,
			IneligibleReason: c.IneligibleReason,
			Pinned:           c.Pinned,
		})
	}
	return out, nil
}

func (h *seriesHandler) grabRelease(ctx context.Context, in *grabSeriesInput) (*grabSeriesOutput, error) {
	dl := h.clients.Download()
	if dl == nil {
		return nil, huma.Error503ServiceUnavailable(
			"no download client configured (set it in Settings, or TRANSPONDARR_QBIT_URL/_USER/_PASSWORD)")
	}

	m, err := h.acquire.MatchSeries(ctx, in.ID)
	if err != nil {
		return nil, acquireHTTPError(err)
	}
	items := m.Items

	// Re-run the match and locate the chosen release by URL, so we grab exactly
	// what the decider says it covers rather than trusting client-supplied item
	// numbers.
	var chosen *decide.Candidate
	for i := range m.Candidates {
		if m.Candidates[i].Release.DownloadURL == in.Body.DownloadURL {
			chosen = &m.Candidates[i]
			break
		}
	}
	if chosen == nil {
		return nil, huma.Error404NotFound("release not found in current search results")
	}
	if !chosen.Matched || len(chosen.Items) == 0 {
		return nil, huma.Error422UnprocessableEntity("release does not match any wanted item: " + chosen.Reason)
	}

	// Hand off to the download client. The category is best-effort decoration.
	res, err := dl.Add(ctx, download.AddOptions{
		URL:      in.Body.DownloadURL,
		Category: h.settings.DownloadCategory(),
		Paused:   in.Body.Paused,
	})
	if err != nil {
		return nil, huma.Error502BadGateway("download client add failed", err)
	}

	// Record a grab per covered wanted item (identity keyed on the info hash).
	// have stays false — only a successful import marks an item as had.
	itemID := make(map[int]int64, len(items))
	for _, it := range items {
		itemID[it.Number] = it.ID
	}
	grabbed := make([]int, 0, len(chosen.Items))
	for _, n := range chosen.Items {
		id, ok := itemID[n]
		if !ok {
			continue
		}
		if _, gerr := h.store.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: id,
			InfoHash:     res.Hash,
			ReleaseTitle: chosen.Release.Title,
			Status:       "grabbed",
		}); gerr != nil {
			return nil, huma.Error500InternalServerError("failed to record grab", gerr)
		}
		grabbed = append(grabbed, n)
	}

	out := &grabSeriesOutput{}
	out.Body.InfoHash = res.Hash
	out.Body.Outcome = string(res.Outcome)
	out.Body.Release = chosen.Release.Title
	out.Body.Items = grabbed
	out.Body.IneligibleReason = chosen.IneligibleReason
	return out, nil
}
