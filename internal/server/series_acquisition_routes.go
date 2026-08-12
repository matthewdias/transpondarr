package server

import (
	"context"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/decide"
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
	Pinned           bool           `json:"pinned" doc:"Release group is the title's pinned group; ranks above profile score when eligible"`

	UpgradeItems   []int               `json:"upgrade_items,omitempty" doc:"Covered items already in the library that this release may replace"`
	UpgradeBlocked []upgradeBlockedDTO `json:"upgrade_blocked,omitempty" doc:"Covered items automation would not replace, and why; a manual grab is not gated by it"`
}

// upgradeBlockedDTO is one held item the upgrade policy refused (#97).
type upgradeBlockedDTO struct {
	Item   int    `json:"item"`
	Reason string `json:"reason"`
}

type scorePartDTO struct {
	Label  string `json:"label"`
	Points int    `json:"points"`
}

type searchTitleInput struct {
	ID int64 `path:"id" doc:"Title id to find releases for"`
}

type searchTitleOutput struct {
	Body struct {
		Title   string                `json:"title"`
		Term    string                `json:"term"`
		Results []candidateReleaseDTO `json:"results"`
	}
}

type grabTitleInput struct {
	ID   int64 `path:"id" doc:"Title id"`
	Body struct {
		DownloadURL string `json:"download_url" required:"true" doc:"download_url of a matched release from the title search"`
		Paused      bool   `json:"paused,omitempty" doc:"Add the torrent stopped (no data transfer) — useful for testing the grab flow"`
	}
}

type grabTitleOutput struct {
	Body struct {
		InfoHash         string `json:"infohash"`
		Outcome          string `json:"outcome" example:"success"`
		Release          string `json:"release"`
		Items            []int  `json:"items"`
		IneligibleReason string `json:"ineligible_reason,omitempty" doc:"Set when the grabbed release falls outside the title's quality profile — informational, the grab still succeeds"`
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
		OperationID: "search-title-releases",
		Method:      http.MethodGet,
		Path:        "/api/v1/titles/{id}/search",
		Summary:     "Find and match indexer releases against a title's wanted items (read-only; does not grab)",
		Tags:        []string{"titles"},
	}, h.searchReleases)

	huma.Register(api, huma.Operation{
		OperationID:   "grab-title-release",
		Method:        http.MethodPost,
		Path:          "/api/v1/titles/{id}/grab",
		Summary:       "Grab a chosen release: add it to the download client and record it against the wanted items it covers",
		Tags:          []string{"titles"},
		DefaultStatus: http.StatusCreated,
	}, h.grabRelease)
}

func (h *seriesHandler) searchReleases(ctx context.Context, in *searchTitleInput) (*searchTitleOutput, error) {
	m, err := h.acquire.MatchSeries(ctx, in.ID)
	if err != nil {
		return nil, acquireHTTPError(err)
	}
	out := &searchTitleOutput{}
	out.Body.Title = m.Series.Title
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
			UpgradeItems:     c.UpgradeItems,
			UpgradeBlocked:   upgradeBlockedDTOs(c.UpgradeBlocked),
		})
	}
	return out, nil
}

// upgradeBlockedDTOs renders the refusals in item order, since a map has none.
func upgradeBlockedDTOs(blocked map[int]string) []upgradeBlockedDTO {
	if len(blocked) == 0 {
		return nil
	}
	out := make([]upgradeBlockedDTO, 0, len(blocked))
	for item, reason := range blocked {
		out = append(out, upgradeBlockedDTO{Item: item, Reason: reason})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Item < out[b].Item })
	return out
}

func (h *seriesHandler) grabRelease(ctx context.Context, in *grabTitleInput) (*grabTitleOutput, error) {
	if h.clients.Download() == nil {
		return nil, acquireHTTPError(acquire.ErrNoDownloadClient)
	}

	m, err := h.acquire.MatchSeries(ctx, in.ID)
	if err != nil {
		return nil, acquireHTTPError(err)
	}

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

	// Eligibility is reported, never enforced, on a manual grab (PR #57).
	res, err := h.acquire.Grab(ctx, in.ID, *chosen, m.Items, in.Body.Paused)
	if err != nil {
		return nil, acquireHTTPError(err)
	}

	out := &grabTitleOutput{}
	out.Body.InfoHash = res.InfoHash
	out.Body.Outcome = string(res.Outcome)
	out.Body.Release = chosen.Release.Title
	out.Body.Items = res.Items
	out.Body.IneligibleReason = chosen.IneligibleReason
	return out, nil
}
