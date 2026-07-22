package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

type releaseDTO struct {
	Title       string `json:"title"`
	DownloadURL string `json:"download_url"`
	InfoHash    string `json:"infohash,omitempty"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	Indexer     string `json:"indexer"`
}

type searchIndexerInput struct {
	Term string `query:"term" required:"true" minLength:"1" doc:"Free-text release search term"`
}

type searchIndexerOutput struct {
	Body struct {
		Results []releaseDTO `json:"results"`
	}
}

func registerIndexerRoutes(api huma.API, deps routeDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "search-indexer",
		Method:      http.MethodGet,
		Path:        "/api/v1/indexer/search",
		Summary:     "Search the configured indexer for releases",
		Tags:        []string{"indexer"},
	}, func(ctx context.Context, in *searchIndexerInput) (*searchIndexerOutput, error) {
		idx := deps.clients.Indexer()
		if idx == nil {
			return nil, huma.Error503ServiceUnavailable(
				"no indexer configured (set it in Settings, or TRANSPONDARR_TORZNAB_URL/_APIKEY)")
		}
		releases, err := idx.Search(ctx, indexer.Query{Term: in.Term})
		if err != nil {
			return nil, huma.Error502BadGateway("indexer search failed", err)
		}
		out := &searchIndexerOutput{}
		out.Body.Results = make([]releaseDTO, 0, len(releases))
		for _, rel := range releases {
			out.Body.Results = append(out.Body.Results, releaseDTO{
				Title:       rel.Title,
				DownloadURL: rel.DownloadURL,
				InfoHash:    rel.InfoHash,
				Size:        rel.Size,
				Seeders:     rel.Seeders,
				Indexer:     rel.Indexer,
			})
		}
		return out, nil
	})
}
