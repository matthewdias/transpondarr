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

// scanBatches bounds how far one request reads past title whose held releases
// all meet their cutoff. Membership is decided in Go, so a page is filled by
// scanning; without a cap a library where nearly everything is at cutoff would
// turn one request into a full-table walk. Hitting it returns a short page with
// a cursor, which is correct, just not full.
//
// It bounds the response, not the work: a batch parses and scores every held
// item of the title it read, so one request can cost scanBatches x Limit
// title' worth of parsing whatever it returns. The worst case is the healthy
// steady state (everything already at cutoff), not an exotic one -- tracked
// separately rather than papered over here, since scoring is what decides
// membership and cannot be pushed into SQL.
const scanBatches = 20

// ItemsPerGroup caps what one group of either wanted listing lists; the group's
// own count (Below here, Missing on the other tab) is the whole truth either way.
const ItemsPerGroup = 50

// PageItemBudget closes a wanted-queue page early once it carries this many
// items across its groups. The group limit bounds the seasonal shape (many
// tiny groups); this bounds the back-catalog shape (few groups at their item
// cap), where a full page of capped groups would otherwise paint thousands of
// rows. A page always ships at least one group, however large its cap.
const PageItemBudget = 200

// CutoffUnmetParams selects a page of title groups holding sub-cutoff releases.
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
	Monitored        bool
	AirsAt           string
	HeldReleaseTitle string
	Score            int
	// UnmetGoals is what the profile still wants that the held release is not:
	// the axes scoring below their best, with the points each leaves unearned.
	UnmetGoals []decide.ScorePart
	Grab       db.Grab
	HasGrab    bool
}

// CutoffGroup is one title' sub-cutoff items; the cutoff itself lives here
// because it is the profile's, not any one item's.
type CutoffGroup struct {
	TitleID     int64
	TitleName   string
	Format      string
	Monitored   bool
	ProfileName string
	CutoffScore int
	Below       int // items below the cutoff in the whole title; Items is capped
	Items       []CutoffUnmetItem
}

// CutoffUnmetPage is one page of groups; a zero NextCursor is the end.
type CutoffUnmetPage struct {
	Groups     []CutoffGroup
	NextCursor QueueCursor
}

// CutoffUnmet lists held items whose release scores below the cutoff of the
// profile their title is on (#97's semantics, #150's second tab), grouped by
// title so the pagination unit is the group and a title never splits across
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
	itemSum := 0
	for range scanBatches {
		title, err := s.store.Q.ListCutoffTitlesPage(ctx, db.ListCutoffTitlesPageParams{
			Column1: unmonitored,
			Column2: unmonitored,
			Title:   cursor.Key,
			Title_2: cursor.Key,
			ID:      cursor.ID,
			Limit:   int64(p.Limit),
		})
		if err != nil {
			return CutoffUnmetPage{}, fmt.Errorf("list cutoff-unmet series: %w", err)
		}
		if len(title) == 0 {
			return out, nil
		}
		ids := make([]int64, 0, len(title))
		for _, sr := range title {
			ids = append(ids, sr.ID)
		}
		items, err := s.store.Q.ListCutoffItemsByTitle(ctx, db.ListCutoffItemsByTitleParams{
			TitleIds: ids,
			Column2:  unmonitored,
		})
		if err != nil {
			return CutoffUnmetPage{}, fmt.Errorf("list held items for cutoff page: %w", err)
		}
		byTitle := map[int64][]db.ListCutoffItemsByTitleRow{}
		for _, r := range items {
			byTitle[r.SeriesID] = append(byTitle[r.SeriesID], r)
		}

		for _, sr := range title {
			// The cursor advances per title examined, not per group kept, so a
			// resume never re-scores a title whose releases all met the cutoff.
			// prev survives one iteration for the budget close below, which must
			// resume AT this title rather than after it.
			prev := cursor
			cursor = QueueCursor{Key: sr.Title, ID: sr.ID}
			profile, ok := profiles[sr.ProfileID]
			if !ok {
				if profile, err = s.profileByID(ctx, sr.ProfileID); err != nil {
					return CutoffUnmetPage{}, err
				}
				profiles[sr.ProfileID] = profile
			}
			group := CutoffGroup{
				TitleID:     sr.ID,
				TitleName:   sr.Title,
				Format:      sr.Format,
				Monitored:   sr.Monitored == 1,
				ProfileName: sr.ProfileName,
				CutoffScore: profile.CutoffScore,
			}
			for _, r := range byTitle[sr.ID] {
				parsed := parser.Parse(r.HeldReleaseTitle)
				score, _ := decide.Score(parsed, indexer.Release{}, profile)
				if score >= profile.CutoffScore {
					continue
				}
				group.Below++
				if len(group.Items) == ItemsPerGroup {
					continue
				}
				group.Items = append(group.Items, CutoffUnmetItem{
					ID:               r.ID,
					Number:           int(r.Number.Int64),
					Name:             r.Title.String,
					Monitored:        r.Monitored == 1,
					AirsAt:           r.AirsAt.String,
					HeldReleaseTitle: r.HeldReleaseTitle,
					Score:            score,
					UnmetGoals:       decide.UnmetGoals(parsed, profile),
					// The join is inner now, so every listed item has a grab.
					Grab: db.Grab{
						Status:       r.GrabStatus,
						ReleaseTitle: r.GrabReleaseTitle,
						LastError:    r.GrabLastError,
					},
					HasGrab: true,
				})
			}
			if group.Below == 0 {
				continue
			}
			// The item budget closes the page before this group when taking it
			// would overweigh the page; the first group always ships.
			if len(out.Groups) > 0 && itemSum+len(group.Items) > PageItemBudget {
				out.NextCursor = prev
				return out, nil
			}
			itemSum += len(group.Items)
			out.Groups = append(out.Groups, group)
			if len(out.Groups) == p.Limit {
				out.NextCursor = cursor
				return out, nil
			}
		}
		if len(title) < p.Limit {
			return out, nil
		}
	}
	out.NextCursor = cursor
	return out, nil
}

// profileByID loads a profile in the domain form decide scores against. The
// listing carries the profile id so a page loads each one once, however many
// title share it.
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
