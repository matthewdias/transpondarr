package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/browse"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

// The provider enum carries the same runtime-vs-spec coupling as candidateDTO's.
type seasonEntryDTO struct {
	Provider     string     `json:"provider" enum:"anilist" doc:"Metadata provider whose id space provider_id is numbered in"`
	ProviderID   int64      `json:"provider_id"`
	Romaji       string     `json:"romaji,omitempty"`
	English      string     `json:"english,omitempty"`
	Native       string     `json:"native,omitempty"`
	Format       string     `json:"format,omitempty"`
	Status       string     `json:"status,omitempty"`
	Description  string     `json:"description,omitempty" doc:"Provider-formatted HTML synopsis"`
	Episodes     int        `json:"episodes"`
	Genres       []string   `json:"genres"`
	AverageScore int        `json:"average_score"`
	Studio       string     `json:"studio,omitempty"`
	CoverURL     string     `json:"cover_url,omitempty"`
	NextEpisode  int        `json:"next_episode,omitempty"`
	NextAirsAt   *time.Time `json:"next_airs_at,omitempty"`
	Tracked      bool       `json:"tracked"`
	TitleID      int64      `json:"title_id,omitempty" doc:"Local title id when tracked"`
}

type browseSeasonInput struct {
	Season string `query:"season" enum:"winter,spring,summer,fall" doc:"Season to chart; defaults to the current one"`
	Year   int    `query:"year" minimum:"1940" maximum:"2100" doc:"Season year; defaults to the current one"`
}

type browseSeasonOutput struct {
	Body struct {
		Season  string           `json:"season" enum:"winter,spring,summer,fall"`
		Year    int              `json:"year"`
		Entries []seasonEntryDTO `json:"entries"`
	}
}

func registerBrowseRoutes(api huma.API, deps routeDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "browse-season",
		Method:      http.MethodGet,
		Path:        "/api/v1/browse/season",
		Summary:     "Chart a broadcast season, served from the per-season cache",
		Tags:        []string{"browse"},
	}, func(ctx context.Context, in *browseSeasonInput) (*browseSeasonOutput, error) {
		season, year := browse.CurrentSeason(time.Now())
		if in.Season != "" {
			season = metadata.Season(strings.ToUpper(in.Season))
		}
		if in.Year != 0 {
			year = in.Year
		}

		entries, err := deps.browse.Chart(ctx, season, year)
		if err != nil {
			return nil, huma.Error502BadGateway("seasonal browse failed", err)
		}

		provider := deps.browse.ProviderName()
		out := &browseSeasonOutput{}
		out.Body.Season = strings.ToLower(string(season))
		out.Body.Year = year
		out.Body.Entries = make([]seasonEntryDTO, 0, len(entries))
		for _, e := range entries {
			genres := e.Genres
			// The schema promises a non-nullable array; a nil slice would marshal
			// as null and break that contract.
			if genres == nil {
				genres = []string{}
			}
			dto := seasonEntryDTO{
				Provider:     provider,
				ProviderID:   e.ProviderID,
				Romaji:       e.Titles.Romaji,
				English:      e.Titles.English,
				Native:       e.Titles.Native,
				Format:       e.Format,
				Status:       e.Status,
				Description:  e.Description,
				Episodes:     e.Episodes,
				Genres:       genres,
				AverageScore: e.AverageScore,
				Studio:       e.Studio,
				CoverURL:     e.CoverURL,
				Tracked:      e.Tracked,
				TitleID:      e.SeriesID,
			}
			if e.NextAiring != nil {
				dto.NextEpisode = e.NextAiring.Number
				airsAt := e.NextAiring.AirsAt
				dto.NextAirsAt = &airsAt
			}
			out.Body.Entries = append(out.Body.Entries, dto)
		}
		return out, nil
	})
}
