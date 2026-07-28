package anilist

import (
	"context"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

const (
	browsePerPage = 50
	// maxBrowsePages bounds one season at ~500 titles, comfortably past a real
	// season. It exists so a provider that never clears hasNextPage cannot page
	// forever against a 30 req/min budget.
	maxBrowsePages = 10
)

const browseQuery = `
query ($season: MediaSeason!, $seasonYear: Int!, $page: Int!, $perPage: Int!) {
  Page(page: $page, perPage: $perPage) {
    pageInfo { hasNextPage }
    media(season: $season, seasonYear: $seasonYear, type: ANIME, sort: POPULARITY_DESC) {
      id
      title { romaji english native }
      description
      format
      episodes
      status
      genres
      averageScore
      coverImage { large }
      studios(isMain: true) { nodes { name } }
      nextAiringEpisode { episode airingAt }
    }
  }
}`

// BrowseSeason pages a full seasonal chart, most popular first.
func (c *Client) BrowseSeason(ctx context.Context, season metadata.Season, year int) ([]metadata.SeasonEntry, error) {
	var out []metadata.SeasonEntry
	for page := 1; ; page++ {
		var data struct {
			Page struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Media []media `json:"media"`
			} `json:"Page"`
		}
		vars := map[string]any{"season": string(season), "seasonYear": year, "page": page, "perPage": browsePerPage}
		if err := c.do(ctx, browseQuery, vars, &data); err != nil {
			return nil, err
		}

		for _, m := range data.Page.Media {
			out = append(out, m.seasonEntry())
		}
		if !data.Page.PageInfo.HasNextPage {
			break
		}
		if page == maxBrowsePages {
			c.log.Warn("season chart truncated at the page cap", "season", season, "year", year, "pages", maxBrowsePages)
			break
		}
	}
	return out, nil
}

func (m media) seasonEntry() metadata.SeasonEntry {
	e := metadata.SeasonEntry{
		ProviderID:  m.ID,
		Titles:      m.titles(),
		Format:      m.Format,
		Status:      m.Status,
		Description: m.Description,
		Episodes:    m.episodes(),
		Genres:      m.Genres,
		CoverURL:    m.CoverImage.Large,
	}
	if m.AverageScore != nil {
		e.AverageScore = *m.AverageScore
	}
	if len(m.Studios.Nodes) > 0 {
		e.Studio = m.Studios.Nodes[0].Name
	}
	if n := m.NextAiringEpisode; n != nil && n.Episode > 0 && n.AiringAt > 0 {
		e.NextAiring = &metadata.Airing{Number: n.Episode, AirsAt: time.Unix(n.AiringAt, 0).UTC()}
	}
	return e
}

var _ metadata.BrowseProvider = (*Client)(nil)
