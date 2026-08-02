package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/version"
)

type healthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok"`
		Version string `json:"version"`
	}
}

type jobStatusDTO struct {
	Name           string  `json:"name"`
	IntervalMs     int64   `json:"interval_ms"`
	Running        bool    `json:"running"`
	LastRun        string  `json:"last_run,omitempty" doc:"RFC3339 UTC; absent until the job has run"`
	LastDurationMs float64 `json:"last_duration_ms" doc:"Fractional: a sub-millisecond sweep would otherwise always report 0"`
	LastError      string  `json:"last_error,omitempty"`
	NextRun        string  `json:"next_run,omitempty" doc:"RFC3339 UTC; absent while the runner is not running"`
}

type listJobsOutput struct {
	Body struct {
		Jobs []jobStatusDTO `json:"jobs"`
	}
}

func registerSystemRoutes(api huma.API, deps routeDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/v1/health",
		Summary:     "Health check (public, no API key required)",
		Tags:        []string{"system"},
		// Empty (non-nil) security overrides the global requirement → public.
		Security: []map[string][]string{},
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		out.Body.Version = version.Version
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-jobs",
		Method:      http.MethodGet,
		Path:        "/api/v1/system/jobs",
		Summary:     "Status of the runner-managed background jobs",
		Tags:        []string{"system"},
	}, func(_ context.Context, _ *struct{}) (*listJobsOutput, error) {
		var statuses []jobs.JobStatus
		if deps.jobs != nil {
			statuses = deps.jobs.Status()
		}
		out := &listJobsOutput{}
		out.Body.Jobs = make([]jobStatusDTO, 0, len(statuses))
		for _, s := range statuses {
			out.Body.Jobs = append(out.Body.Jobs, toJobStatusDTO(s))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "run-job",
		Method:        http.MethodPost,
		Path:          "/api/v1/system/jobs/{name}/run",
		Summary:       "Run a background job now",
		Description:   "Queues the job on its own goroutine and returns immediately. A trigger during a run queues one follow-up run; further triggers coalesce into that pending one.",
		Tags:          []string{"system"},
		DefaultStatus: http.StatusAccepted,
	}, func(_ context.Context, in *struct {
		Name string `path:"name" doc:"The runner's job name, as reported by list-jobs"`
	}) (*struct{}, error) {
		if deps.jobs == nil {
			return nil, huma.Error404NotFound("unknown job")
		}
		if err := deps.jobs.Trigger(in.Name); err != nil {
			if errors.Is(err, jobs.ErrUnknownJob) {
				return nil, huma.Error404NotFound("unknown job")
			}
			return nil, err
		}
		return nil, nil
	})
}

func toJobStatusDTO(s jobs.JobStatus) jobStatusDTO {
	dto := jobStatusDTO{
		Name:           s.Name,
		IntervalMs:     s.Interval.Milliseconds(),
		Running:        s.Running,
		LastDurationMs: math.Round(float64(s.LastDuration)/float64(time.Microsecond)) / 1000,
	}
	if !s.LastRun.IsZero() {
		dto.LastRun = s.LastRun.UTC().Format(time.RFC3339)
	}
	if !s.NextRun.IsZero() {
		dto.NextRun = s.NextRun.UTC().Format(time.RFC3339)
	}
	if s.LastError != nil {
		dto.LastError = s.LastError.Error()
	}
	return dto
}
