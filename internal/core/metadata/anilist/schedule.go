package anilist

import (
	"context"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
)

const (
	schedulePerPage = 50 // AniList's per-page ceiling for this connection
	// maxSchedulePages bounds one call at ~3000 episodes, comfortably past the
	// longest running series. It exists so a provider that never clears
	// hasNextPage cannot page forever against a 30 req/min budget.
	maxSchedulePages = 60
)

const scheduleQuery = `
query ($id: Int!, $page: Int!, $perPage: Int!, $notYetAired: Boolean) {
  Media(id: $id, type: ANIME) {
    airingSchedule(page: $page, perPage: $perPage, notYetAired: $notYetAired) {
      pageInfo { hasNextPage }
      nodes { episode airingAt }
    }
  }
}`

// GetSchedule pages a title's broadcast schedule. notYetAired fetches only the
// upcoming tail (1-2 pages) — the asymmetry that makes full history affordable:
// aired times are immutable, so only a never-synced title pays for all of them.
func (c *Client) GetSchedule(ctx context.Context, id int64, notYetAired bool) ([]metadata.Airing, error) {
	var out []metadata.Airing
	for page := 1; page <= maxSchedulePages; page++ {
		var data struct {
			Media struct {
				AiringSchedule struct {
					PageInfo struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []struct {
						Episode  int   `json:"episode"`
						AiringAt int64 `json:"airingAt"`
					} `json:"nodes"`
				} `json:"airingSchedule"`
			} `json:"Media"`
		}
		vars := map[string]any{"id": id, "page": page, "perPage": schedulePerPage, "notYetAired": notYetAired}
		if err := c.do(ctx, scheduleQuery, vars, &data); err != nil {
			return nil, err
		}

		sched := data.Media.AiringSchedule
		for _, n := range sched.Nodes {
			// A node without a usable episode number or timestamp carries nothing a
			// consumer could key on, so drop it rather than write a zero air date.
			if n.Episode <= 0 || n.AiringAt <= 0 {
				continue
			}
			out = append(out, metadata.Airing{Number: n.Episode, AirsAt: time.Unix(n.AiringAt, 0).UTC()})
		}
		if !sched.PageInfo.HasNextPage {
			break
		}
	}
	return out, nil
}

var _ metadata.AiringProvider = (*Client)(nil)
