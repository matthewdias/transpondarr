package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// The provider enum is filled at runtime from ProviderName(), so it must widen
// in step with what is configured or the server serves responses its own spec
// rejects. It earns the tag by giving the frontend a narrow union type.
type candidateDTO struct {
	Provider   string `json:"provider" enum:"anilist" doc:"Metadata provider whose id space provider_id is numbered in"`
	ProviderID int64  `json:"provider_id"`
	Romaji     string `json:"romaji,omitempty"`
	English    string `json:"english,omitempty"`
	Native     string `json:"native,omitempty"`
	Format     string `json:"format,omitempty"`
	Episodes   int    `json:"episodes"`
	Status     string `json:"status,omitempty"`
	Year       int    `json:"year,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
	NextItem   int    `json:"next_item,omitempty" doc:"Number of the next scheduled broadcast; omitted when nothing is scheduled"`
}

type searchMetadataInput struct {
	Term string `query:"term" required:"true" minLength:"1" doc:"Title to search AniList for"`
}

type searchMetadataOutput struct {
	Body struct {
		Results []candidateDTO `json:"results"`
	}
}

func registerMetadataRoutes(api huma.API, deps routeDeps) {
	svc := deps.catalog
	huma.Register(api, huma.Operation{
		OperationID: "search-metadata",
		Method:      http.MethodGet,
		Path:        "/api/v1/metadata/search",
		Summary:     "Search AniList for titles to add",
		Tags:        []string{"metadata"},
	}, func(ctx context.Context, in *searchMetadataInput) (*searchMetadataOutput, error) {
		cands, err := svc.Search(ctx, in.Term)
		if err != nil {
			return nil, huma.Error502BadGateway("metadata search failed", err)
		}
		provider := svc.ProviderName()
		out := &searchMetadataOutput{}
		out.Body.Results = make([]candidateDTO, 0, len(cands))
		for _, c := range cands {
			out.Body.Results = append(out.Body.Results, candidateDTO{
				Provider:   provider,
				ProviderID: c.ProviderID,
				Romaji:     c.Titles.Romaji,
				English:    c.Titles.English,
				Native:     c.Titles.Native,
				Format:     c.Format,
				Episodes:   c.Episodes,
				Status:     c.Status,
				Year:       c.Year,
				CoverURL:   c.CoverURL,
				NextItem:   c.NextItem,
			})
		}
		return out, nil
	})
}
