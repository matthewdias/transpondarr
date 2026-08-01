package server

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type calendarItemDTO struct {
	ID          int64  `json:"id"`
	SeriesID    int64  `json:"series_id"`
	SeriesTitle string `json:"series_title"`
	Monitored   bool   `json:"monitored"`
	Number      int    `json:"number"`
	Name        string `json:"name,omitempty"`
	AirsAt      string `json:"airs_at" format:"date-time" doc:"Broadcast time (RFC 3339 UTC)"`
	Status      string `json:"status" enum:"have,downloading,stuck,deferred,wanted" doc:"Derived acquisition state"`
	ImportError string `json:"import_error,omitempty" doc:"Why the last import attempt failed (status stuck)"`
}

type unscheduledSeriesDTO struct {
	SeriesID int64  `json:"series_id"`
	Title    string `json:"title"`
}

type calendarInput struct {
	Start       time.Time `query:"start" required:"true" doc:"Range start, inclusive (RFC 3339)"`
	End         time.Time `query:"end" required:"true" doc:"Range end, exclusive (RFC 3339)"`
	Unmonitored bool      `query:"unmonitored" doc:"Include unmonitored series"`
}

type calendarOutput struct {
	Body struct {
		Items       []calendarItemDTO      `json:"items"`
		Unscheduled []unscheduledSeriesDTO `json:"unscheduled" doc:"Monitored series missing episodes with no schedule data"`
	}
}

func registerCalendarRoutes(api huma.API, deps routeDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "get-calendar",
		Method:      http.MethodGet,
		Path:        "/api/v1/calendar",
		Summary:     "Wanted items airing in a range, with derived acquisition state",
		Tags:        []string{"calendar"},
	}, func(ctx context.Context, in *calendarInput) (*calendarOutput, error) {
		if !in.End.After(in.Start) {
			return nil, huma.Error422UnprocessableEntity("end must be after start")
		}

		rows, err := deps.store.Q.ListCalendarItems(ctx, db.ListCalendarItemsParams{
			AirsAt:   sql.NullString{String: store.FormatTimestamp(in.Start), Valid: true},
			AirsAt_2: sql.NullString{String: store.FormatTimestamp(in.End), Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to load calendar items", err)
		}

		out := &calendarOutput{}
		out.Body.Items = make([]calendarItemDTO, 0, len(rows))
		for _, r := range rows {
			monitored := r.SeriesMonitored == 1
			if !monitored && !in.Unmonitored {
				continue
			}
			state := deriveItemState(r.Have == 1, db.Grab{
				Status:       r.GrabStatus.String,
				ReleaseTitle: r.GrabReleaseTitle.String,
				LastError:    r.GrabLastError,
			}, r.GrabStatus.Valid)
			out.Body.Items = append(out.Body.Items, calendarItemDTO{
				ID:          r.ID,
				SeriesID:    r.SeriesID,
				SeriesTitle: r.SeriesTitle,
				Monitored:   monitored,
				Number:      int(r.Number.Int64),
				Name:        r.Title.String,
				AirsAt:      storedTimeRFC3339(r.AirsAt),
				Status:      state.Status,
				ImportError: state.ImportError,
			})
		}

		unscheduled, err := deps.store.Q.ListUnscheduledSeries(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to load unscheduled series", err)
		}
		out.Body.Unscheduled = make([]unscheduledSeriesDTO, 0, len(unscheduled))
		for _, s := range unscheduled {
			out.Body.Unscheduled = append(out.Body.Unscheduled, unscheduledSeriesDTO{
				SeriesID: s.ID,
				Title:    s.Title,
			})
		}
		return out, nil
	})
}
