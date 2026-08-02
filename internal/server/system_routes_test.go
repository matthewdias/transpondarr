package server

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/matthewdias/transpondarr/internal/core/jobs"
)

// The spec-dump path registers routes with zero-value deps, so a nil runner
// must answer like an unknown job rather than panic.
func TestRunJobWithoutARunnerIsNotFound(t *testing.T) {
	_, api := humatest.New(t)
	registerSystemRoutes(api, routeDeps{})
	if resp := api.Post("/api/v1/system/jobs/import-scan/run"); resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNotFound)
	}
}

// In-package so the DTO mapping can be pinned without timing: a sub-millisecond
// job is the common case (the session sweep is one DELETE), and integer
// milliseconds would report every one of them as 0.
func TestJobStatusDTOKeepsSubMillisecondDurations(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want float64
	}{
		{0, 0},
		{500 * time.Microsecond, 0.5},
		{1500 * time.Microsecond, 1.5},
		{2 * time.Second, 2000},
		{100 * time.Nanosecond, 0}, // below microsecond resolution
	} {
		if got := toJobStatusDTO(jobs.JobStatus{LastDuration: tc.in}).LastDurationMs; got != tc.want {
			t.Errorf("LastDuration %v rendered as %v ms, want %v", tc.in, got, tc.want)
		}
	}
}

// A never-run job must not render as the year 1; the field is omitted instead.
func TestJobStatusDTOOmitsUnsetTimes(t *testing.T) {
	dto := toJobStatusDTO(jobs.JobStatus{Name: "a", Interval: time.Minute})
	if dto.LastRun != "" || dto.NextRun != "" {
		t.Errorf("last_run = %q, next_run = %q, want both empty", dto.LastRun, dto.NextRun)
	}
	if dto.LastError != "" {
		t.Errorf("last_error = %q, want empty", dto.LastError)
	}
	if dto.IntervalMs != 60_000 {
		t.Errorf("interval_ms = %d, want 60000", dto.IntervalMs)
	}
}

func TestJobStatusDTOFormatsTimesAsUTCRFC3339(t *testing.T) {
	at := time.Date(2026, 7, 26, 5, 42, 21, 0, time.FixedZone("CST", -6*60*60))
	dto := toJobStatusDTO(jobs.JobStatus{LastRun: at, NextRun: at.Add(24 * time.Hour), LastError: errors.New("boom")})

	if dto.LastRun != "2026-07-26T11:42:21Z" {
		t.Errorf("last_run = %q, want the UTC instant", dto.LastRun)
	}
	if dto.NextRun != "2026-07-27T11:42:21Z" {
		t.Errorf("next_run = %q, want the UTC instant", dto.NextRun)
	}
	if dto.LastError != "boom" {
		t.Errorf("last_error = %q, want boom", dto.LastError)
	}
}
