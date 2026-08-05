package acquire

import (
	"context"
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/parser"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// QueueCursor is a keyset position in a wanted-queue listing: the ordering pair
// (sort key, id) of the last row read. It is an exclusive bound, so resuming
// from it starts at the row after.
type QueueCursor struct {
	Key string
	ID  int64
}

// QueueCursorTop is ahead of every row in the Missing listing's order: an air
// date above every stored one, and an id below every stored one for the
// ascending tie-break. Cutoff Unmet ascends by title instead, so its top is the
// zero cursor and it does not start here.
func QueueCursorTop() QueueCursor { return QueueCursor{Key: "~", ID: 0} }

// scanBatches bounds how far one request reads past series whose held releases
// all meet their cutoff. Membership is decided in Go, so a page is filled by
// scanning; without a cap a library where nearly everything is at cutoff would
// turn one request into a full-table walk. Hitting it returns a short page with
// a cursor, which is correct, just not full.
const scanBatches = 20

// cutoffItemsPerGroup caps what one group lists; the group's Below count is the
// whole truth either way.
const cutoffItemsPerGroup = 50

// CutoffUnmetParams selects a page of series groups holding sub-cutoff releases.
type CutoffUnmetParams struct {
	Limit              int
	Cursor             QueueCursor
	IncludeUnmonitored bool
}

// CutoffUnmetItem is one held item whose release scores below its profile's
// cutoff, with the numbers that say so.
type CutoffUnmetItem struct {
	ID               int64
	Number           int
	Name             string
	AirsAt           string
	HeldReleaseTitle string
	Score            int
	// UnmetGoals is what the profile still wants that the held release is not:
	// the axes scoring below their best, with the points each leaves unearned.
	UnmetGoals []decide.ScorePart
	Grab       db.Grab
	HasGrab    bool
}

// CutoffGroup is one series' sub-cutoff items; the cutoff itself lives here
// because it is the profile's, not any one item's.
type CutoffGroup struct {
	SeriesID    int64
	SeriesTitle string
	Monitored   bool
	ProfileName string
	CutoffScore int
	Below       int // items below the cutoff in the whole series; Items is capped
	Items       []CutoffUnmetItem
}

// CutoffUnmetPage is one page of groups; a zero NextCursor is the end.
type CutoffUnmetPage struct {
	Groups     []CutoffGroup
	NextCursor QueueCursor
}

// CutoffUnmet lists held items whose release scores below the cutoff of the
// profile their series is on (#97's semantics, #150's second tab), grouped by
// series so the pagination unit is the group and a series never splits across
// pages. Membership is re-derived from the stored release name under the
// current profile rather than recorded, so editing a profile moves the list
// without a write anywhere.
func (s *Service) CutoffUnmet(ctx context.Context, p CutoffUnmetParams) (CutoffUnmetPage, error) {
	if p.Limit <= 0 {
		return CutoffUnmetPage{}, nil
	}
	cursor := p.Cursor
	unmonitored := int64(0)
	if p.IncludeUnmonitored {
		unmonitored = 1
	}

	profiles := map[int64]domain.QualityProfile{}
	out := CutoffUnmetPage{Groups: make([]CutoffGroup, 0, p.Limit)}
	for range scanBatches {
		series, err := s.store.Q.ListCutoffSeriesPage(ctx, db.ListCutoffSeriesPageParams{
			Column1: unmonitored,
			Title:   cursor.Key,
			Title_2: cursor.Key,
			ID:      cursor.ID,
			Limit:   int64(p.Limit),
		})
		if err != nil {
			return CutoffUnmetPage{}, fmt.Errorf("list cutoff-unmet series: %w", err)
		}
		if len(series) == 0 {
			return out, nil
		}
		ids := make([]int64, 0, len(series))
		for _, sr := range series {
			ids = append(ids, sr.ID)
		}
		items, err := s.store.Q.ListCutoffItemsBySeries(ctx, ids)
		if err != nil {
			return CutoffUnmetPage{}, fmt.Errorf("list held items for cutoff page: %w", err)
		}
		bySeries := map[int64][]db.ListCutoffItemsBySeriesRow{}
		for _, r := range items {
			bySeries[r.SeriesID] = append(bySeries[r.SeriesID], r)
		}

		for _, sr := range series {
			// The cursor advances per series examined, not per group kept, so a
			// resume never re-scores a series whose releases all met the cutoff.
			cursor = QueueCursor{Key: sr.Title, ID: sr.ID}
			profile, ok := profiles[sr.ProfileID]
			if !ok {
				if profile, err = s.profileByID(ctx, sr.ProfileID); err != nil {
					return CutoffUnmetPage{}, err
				}
				profiles[sr.ProfileID] = profile
			}
			group := CutoffGroup{
				SeriesID:    sr.ID,
				SeriesTitle: sr.Title,
				Monitored:   sr.Monitored == 1,
				ProfileName: sr.ProfileName,
				CutoffScore: profile.CutoffScore,
			}
			for _, r := range bySeries[sr.ID] {
				parsed := parser.Parse(r.HeldReleaseTitle)
				score, _ := decide.Score(parsed, indexer.Release{}, profile)
				if score >= profile.CutoffScore {
					continue
				}
				group.Below++
				if len(group.Items) == cutoffItemsPerGroup {
					continue
				}
				group.Items = append(group.Items, CutoffUnmetItem{
					ID:               r.ID,
					Number:           int(r.Number.Int64),
					Name:             r.Title.String,
					AirsAt:           r.AirsAt.String,
					HeldReleaseTitle: r.HeldReleaseTitle,
					Score:            score,
					UnmetGoals:       decide.UnmetGoals(parsed, profile),
					Grab: db.Grab{
						Status:       r.GrabStatus.String,
						ReleaseTitle: r.GrabReleaseTitle.String,
						LastError:    r.GrabLastError,
					},
					HasGrab: r.GrabStatus.Valid,
				})
			}
			if group.Below == 0 {
				continue
			}
			out.Groups = append(out.Groups, group)
			if len(out.Groups) == p.Limit {
				out.NextCursor = cursor
				return out, nil
			}
		}
		if len(series) < p.Limit {
			return out, nil
		}
	}
	out.NextCursor = cursor
	return out, nil
}

// profileByID loads a profile in the domain form decide scores against. The
// listing carries the profile id so a page loads each one once, however many
// series share it.
func (s *Service) profileByID(ctx context.Context, id int64) (domain.QualityProfile, error) {
	row, err := s.store.Q.GetQualityProfile(ctx, id)
	if err != nil {
		return domain.QualityProfile{}, fmt.Errorf("load quality profile %d: %w", id, err)
	}
	groups, err := s.store.Q.ListProfileGroups(ctx, row.ID)
	if err != nil {
		return domain.QualityProfile{}, fmt.Errorf("load profile groups for profile %d: %w", row.ID, err)
	}
	return profileFromRows(row, groups)
}
