package acquire

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/parser"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// QueueCursor is a keyset position in a wanted-queue listing: the ordering pair
// (air date, id) of the last row read. It is an exclusive upper bound, so
// resuming from it starts at the row after.
type QueueCursor struct {
	AirsAt string
	ID     int64
}

// QueueCursorTop is ahead of every row in the listing's order: an air date above
// every stored one, and an id below every stored one for the ascending tie-break.
// One query then serves the first page and every later one alike, and both
// listings order the same way, so both start here.
func QueueCursorTop() QueueCursor { return QueueCursor{AirsAt: "~", ID: 0} }

// scanBatches bounds how far one request reads past rows that already meet their
// cutoff. Membership is decided in Go, so a page is filled by scanning; without
// a cap a library where nearly everything is at cutoff would turn one request
// into a full-table walk. Hitting it returns a short page with a cursor, which
// is correct, just not full.
const scanBatches = 20

// CutoffUnmetParams selects a page of held items scoring below their cutoff.
type CutoffUnmetParams struct {
	Limit              int
	Cursor             QueueCursor
	IncludeUnmonitored bool
}

// CutoffUnmetItem is one held item whose release scores below its profile's
// cutoff, with the numbers that say so.
type CutoffUnmetItem struct {
	ID               int64
	SeriesID         int64
	SeriesTitle      string
	Monitored        bool
	Number           int
	Name             string
	AirsAt           string
	HeldReleaseTitle string
	Score            int
	CutoffScore      int
	ProfileName      string
	Grab             db.Grab
	HasGrab          bool
}

// CutoffUnmetPage is one page of that listing; a zero NextCursor is the end.
type CutoffUnmetPage struct {
	Items      []CutoffUnmetItem
	NextCursor QueueCursor
}

// CutoffUnmet lists held items whose release scores below the cutoff of the
// profile their series is on (#97's semantics, #150's second tab). Membership is
// re-derived from the stored release name under the current profile rather than
// recorded, so editing a profile moves the list without a write anywhere.
func (s *Service) CutoffUnmet(ctx context.Context, p CutoffUnmetParams) (CutoffUnmetPage, error) {
	if p.Limit <= 0 {
		return CutoffUnmetPage{}, nil
	}
	cursor := p.Cursor
	if cursor == (QueueCursor{}) {
		cursor = QueueCursorTop()
	}
	unmonitored := int64(0)
	if p.IncludeUnmonitored {
		unmonitored = 1
	}

	profiles := map[int64]domain.QualityProfile{}
	out := CutoffUnmetPage{Items: make([]CutoffUnmetItem, 0, p.Limit)}
	for range scanBatches {
		rows, err := s.store.Q.ListCutoffUnmetPage(ctx, db.ListCutoffUnmetPageParams{
			Column1:  unmonitored,
			AirsAt:   sql.NullString{String: cursor.AirsAt, Valid: true},
			AirsAt_2: sql.NullString{String: cursor.AirsAt, Valid: true},
			ID:       cursor.ID,
			Limit:    int64(p.Limit),
		})
		if err != nil {
			return CutoffUnmetPage{}, fmt.Errorf("list cutoff-unmet items: %w", err)
		}
		for _, r := range rows {
			cursor = QueueCursor{AirsAt: r.AirsAt.String, ID: r.ID}
			profile, ok := profiles[r.ProfileID]
			if !ok {
				if profile, err = s.profileByID(ctx, r.ProfileID); err != nil {
					return CutoffUnmetPage{}, err
				}
				profiles[r.ProfileID] = profile
			}
			score, _ := decide.Score(parser.Parse(r.HeldReleaseTitle), indexer.Release{}, profile)
			if score >= profile.CutoffScore {
				continue
			}
			out.Items = append(out.Items, cutoffItem(r, score, profile.CutoffScore))
			if len(out.Items) == p.Limit {
				out.NextCursor = cursor
				return out, nil
			}
		}
		// A short batch is the end of the listing, so there is nothing to resume.
		if len(rows) < p.Limit {
			return out, nil
		}
	}
	out.NextCursor = cursor
	return out, nil
}

func cutoffItem(r db.ListCutoffUnmetPageRow, score, cutoff int) CutoffUnmetItem {
	return CutoffUnmetItem{
		ID:               r.ID,
		SeriesID:         r.SeriesID,
		SeriesTitle:      r.SeriesTitle,
		Monitored:        r.SeriesMonitored == 1,
		Number:           int(r.Number.Int64),
		Name:             r.Title.String,
		AirsAt:           r.AirsAt.String,
		HeldReleaseTitle: r.HeldReleaseTitle,
		Score:            score,
		CutoffScore:      cutoff,
		ProfileName:      r.ProfileName,
		Grab: db.Grab{
			Status:       r.GrabStatus.String,
			ReleaseTitle: r.GrabReleaseTitle.String,
			LastError:    r.GrabLastError,
		},
		HasGrab: r.GrabStatus.Valid,
	}
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
