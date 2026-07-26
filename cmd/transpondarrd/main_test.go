package main

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/jobs"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStragglingNamesTheImporterUntilItFinishes(t *testing.T) {
	importerDone := make(chan struct{})
	runner := jobs.New(discardLogger())

	if got, want := straggling(importerDone, runner), []string{"importer"}; !slices.Equal(got, want) {
		t.Errorf("straggling = %v, want %v", got, want)
	}

	close(importerDone)
	if got := straggling(importerDone, runner); len(got) != 0 {
		t.Errorf("straggling = %v, want nothing once the importer has drained", got)
	}
}

// The whole point of the warning is naming the worker to blame, so a job still
// inside its Run must appear.
func TestStragglingNamesARunningJob(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})

	runner := jobs.New(discardLogger())
	runner.Add(jobs.Job{Name: "slow", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
		close(entered)
		<-release
		return nil
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobsDone := runner.Start(ctx)
	<-entered

	importerDone := make(chan struct{})
	if got, want := straggling(importerDone, runner), []string{"importer", "slow"}; !slices.Equal(got, want) {
		t.Errorf("straggling = %v, want %v", got, want)
	}

	close(importerDone)
	if got, want := straggling(importerDone, runner), []string{"slow"}; !slices.Equal(got, want) {
		t.Errorf("straggling = %v, want %v", got, want)
	}

	close(release)
	cancel()
	<-jobsDone
	if got := straggling(importerDone, runner); len(got) != 0 {
		t.Errorf("straggling = %v, want nothing once every job has returned", got)
	}
}
