package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/jobs"
)

// jobStatusDTO mirrors the job-status endpoint's per-job shape.
type jobStatusDTO struct {
	Name           string `json:"name"`
	IntervalMs     int64  `json:"interval_ms"`
	Running        bool   `json:"running"`
	LastRun        string `json:"last_run"`
	LastDurationMs int64  `json:"last_duration_ms"`
	LastError      string `json:"last_error"`
	NextRun        string `json:"next_run"`
}

type jobsResponse struct {
	Jobs []jobStatusDTO `json:"jobs"`
}

func TestListJobsReportsRegisteredJobs(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.jobs.Add(jobs.Job{Name: "session-cleanup", Interval: 24 * time.Hour, Run: func(context.Context) error {
		return nil
	}})

	var out jobsResponse
	if code := h.get(t, "/api/v1/system/jobs", &out); code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("jobs = %+v, want exactly one", out.Jobs)
	}
	j := out.Jobs[0]
	if j.Name != "session-cleanup" {
		t.Errorf("name = %q, want session-cleanup", j.Name)
	}
	if j.IntervalMs != (24 * time.Hour).Milliseconds() {
		t.Errorf("interval_ms = %d, want %d", j.IntervalMs, (24 * time.Hour).Milliseconds())
	}
	if j.LastRun != "" {
		t.Errorf("last_run = %q, want absent for a job that has never run", j.LastRun)
	}
	if j.Running {
		t.Error("running = true for a runner that was never started")
	}
}

// The issue's acceptance criterion: the endpoint reports each job's last run,
// duration and last error.
func TestListJobsReportsLastRunDurationAndError(t *testing.T) {
	h := newHarness(t, nil, nil)
	ran := make(chan struct{}, 1)
	h.jobs.Add(jobs.Job{Name: "flaky", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
		ran <- struct{}{}
		return errors.New("boom")
	}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.jobs.Run(ctx)
	}()
	<-ran
	// Forcing a second run proves the first one's bookkeeping is committed.
	if err := h.jobs.Trigger("flaky"); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	<-ran
	cancel()
	<-done

	var out jobsResponse
	if code := h.get(t, "/api/v1/system/jobs", &out); code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("jobs = %+v, want exactly one", out.Jobs)
	}
	j := out.Jobs[0]
	if _, err := time.Parse(time.RFC3339, j.LastRun); err != nil {
		t.Errorf("last_run = %q, want an RFC3339 timestamp: %v", j.LastRun, err)
	}
	if j.LastDurationMs < 0 {
		t.Errorf("last_duration_ms = %d, want a non-negative duration", j.LastDurationMs)
	}
	if j.LastError != "boom" {
		t.Errorf("last_error = %q, want boom", j.LastError)
	}
	if j.NextRun == "" {
		t.Error("next_run is absent for a scheduled job")
	}
}

func TestListJobsReturnsAnEmptyArray(t *testing.T) {
	h := newHarness(t, nil, nil)

	var out jobsResponse
	if code := h.get(t, "/api/v1/system/jobs", &out); code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if out.Jobs == nil {
		t.Fatal("jobs decoded as nil, want an empty array")
	}
	if len(out.Jobs) != 0 {
		t.Fatalf("jobs = %+v, want empty", out.Jobs)
	}
}
