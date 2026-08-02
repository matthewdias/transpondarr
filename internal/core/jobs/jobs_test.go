package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// statusByName fails the test if the job was never registered.
func statusByName(t *testing.T, r *Runner, name string) JobStatus {
	t.Helper()
	for _, s := range r.Status() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no status for job %q", name)
	return JobStatus{}
}

// advance moves the bubble's clock on and then settles it. The sleep is what
// lets virtual time pass at all: a pending Wait takes priority over advancing
// it, so Wait alone would return with a job still mid-run inside its own sleep.
func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

// start launches r on a cancellable context and returns both halves.
func start(r *Runner) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	return cancel, r.Start(ctx)
}

// syncWriter is a goroutine-safe log sink: the test reads it while a job writes.
type syncWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestRunExecutesAJobImmediatelyWhenRunAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int64
		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			runs.Add(1)
			return nil
		}})

		cancel, done := start(r)
		synctest.Wait()
		if got := runs.Load(); got != 1 {
			t.Fatalf("ran %d times before the first tick, want 1", got)
		}
		cancel()
		<-done
	})
}

// The acceptance criterion that makes closing the store safe: done must not
// close while a job is still executing.
func TestDoneWaitsForInFlightJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		r := New(discardLogger())
		r.Add(Job{Name: "slow", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			close(entered)
			<-release
			return nil
		}})

		cancel, done := start(r)
		<-entered
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("done closed while a job was still in flight")
		default:
		}

		close(release)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("done did not close once the in-flight job finished")
		}
	})
}

// A panicking job must not take down the process, its siblings, or — the part
// that is easy to get wrong — its own loop.
func TestPanickingJobLeavesOtherJobsAndItsOwnLoopAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var panicky, healthy atomic.Int64
		r := New(discardLogger())
		r.Add(Job{Name: "panicky", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			panicky.Add(1)
			panic("boom")
		}})
		r.Add(Job{Name: "healthy", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			healthy.Add(1)
			return nil
		}})

		cancel, done := start(r)
		synctest.Wait()
		if err := r.Trigger("panicky"); err != nil {
			t.Fatalf("trigger panicky: %v", err)
		}
		if err := r.Trigger("healthy"); err != nil {
			t.Fatalf("trigger healthy: %v", err)
		}
		synctest.Wait()

		if got := panicky.Load(); got != 2 {
			t.Errorf("panicking job ran %d times, want 2: its own loop must survive the panic", got)
		}
		if got := healthy.Load(); got != 2 {
			t.Errorf("healthy job ran %d times, want 2: a sibling's panic must not stop it", got)
		}
		if err := statusByName(t, r, "panicky").LastError; err == nil || !strings.Contains(err.Error(), "panic") {
			t.Errorf("LastError = %v, want the panic recorded", err)
		}

		cancel()
		<-done
	})
}

func TestFirstRunWaitsOneIntervalWhenNotRunAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int64
		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, Run: func(context.Context) error {
			runs.Add(1)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		time.Sleep(time.Hour - time.Minute)
		synctest.Wait()
		if got := runs.Load(); got != 0 {
			t.Fatalf("ran %d times after 59m, want 0: the first run waits a full interval", got)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if got := runs.Load(); got != 1 {
			t.Fatalf("ran %d times after the interval elapsed, want 1", got)
		}
	})
}

// The schedule anchors at start, so a run's own duration must not push the
// following runs later — the drift a naive "finished + interval" would cause.
func TestNextRunTracksTheSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Hour
		const runTime = 10 * time.Minute
		begin := time.Now()

		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: interval, RunAtStart: true, Run: func(context.Context) error {
			time.Sleep(runTime)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		advance(runTime + time.Minute)
		if got, want := statusByName(t, r, "a").NextRun, begin.Add(interval); !got.Equal(want) {
			t.Errorf("NextRun after run 1 = %v, want %v", got, want)
		}

		advance(interval)
		if got, want := statusByName(t, r, "a").NextRun, begin.Add(2*interval); !got.Equal(want) {
			t.Errorf("NextRun after run 2 = %v, want %v: the schedule drifted by the run's duration", got, want)
		}
	})
}

// A long job would otherwise report a next run in the past for its whole
// duration, since the schedule was only published once the run finished.
func TestNextRunPointsForwardWhileAJobRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Hour
		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: interval, RunAtStart: true, Run: func(context.Context) error {
			time.Sleep(30 * time.Minute)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		// Wait settles with the job durably blocked inside its own sleep — mid-run.
		synctest.Wait()
		st := statusByName(t, r, "a")
		if !st.Running {
			t.Fatal("expected the job to still be running")
		}
		if !st.NextRun.After(time.Now()) {
			t.Errorf("NextRun = %v while running at %v, want a future time", st.NextRun, time.Now())
		}
	})
}

func TestAnOverrunReschedulesFromNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Minute
		const runTime = 3 * time.Minute
		begin := time.Now()

		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: interval, RunAtStart: true, Run: func(context.Context) error {
			time.Sleep(runTime)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		advance(runTime + 30*time.Second)
		if got, want := statusByName(t, r, "a").NextRun, begin.Add(runTime+interval); !got.Equal(want) {
			t.Errorf("NextRun = %v, want %v: a run longer than its interval must re-anchor, not fire back-to-back", got, want)
		}
	})
}

func TestStatusReportsLastRunDurationAndError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errBoom := errors.New("boom")
		const runTime = 5 * time.Millisecond
		begin := time.Now()

		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			time.Sleep(runTime)
			return errBoom
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()
		advance(runTime + time.Millisecond)

		st := statusByName(t, r, "a")
		if !st.LastRun.Equal(begin) {
			t.Errorf("LastRun = %v, want %v", st.LastRun, begin)
		}
		if st.LastDuration != runTime {
			t.Errorf("LastDuration = %v, want %v", st.LastDuration, runTime)
		}
		if !errors.Is(st.LastError, errBoom) {
			t.Errorf("LastError = %v, want %v", st.LastError, errBoom)
		}
		if st.Running {
			t.Error("Running is true after the run returned")
		}
	})
}

// A settled state must never carry a stale error (issue #37's invariant, which
// the importer learned the hard way).
func TestASuccessfulRunClearsThePreviousError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var failing atomic.Bool
		failing.Store(true)

		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			if failing.Load() {
				return errors.New("boom")
			}
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		synctest.Wait()
		if statusByName(t, r, "a").LastError == nil {
			t.Fatal("the failing run recorded no error")
		}

		failing.Store(false)
		if err := r.Trigger("a"); err != nil {
			t.Fatalf("trigger: %v", err)
		}
		synctest.Wait()
		if err := statusByName(t, r, "a").LastError; err != nil {
			t.Errorf("LastError = %v, want nil: a successful run must clear the previous failure", err)
		}
	})
}

func TestStatusReportsANeverRunJob(t *testing.T) {
	r := New(discardLogger())
	r.Add(Job{Name: "a", Interval: 90 * time.Second, Run: func(context.Context) error { return nil }})

	st := statusByName(t, r, "a")
	if !st.LastRun.IsZero() {
		t.Errorf("LastRun = %v, want the zero time", st.LastRun)
	}
	if !st.NextRun.IsZero() {
		t.Errorf("NextRun = %v, want the zero time before Run", st.NextRun)
	}
	if st.LastError != nil || st.LastDuration != 0 || st.Running {
		t.Errorf("status = %+v, want an untouched job", st)
	}
	if st.Interval != 90*time.Second {
		t.Errorf("Interval = %v, want 90s", st.Interval)
	}
}

// Real clock on purpose: a 1ms interval keeps the loop writing job state while
// Status reads it. Only meaningful under -race.
func TestStatusIsSafeWhileJobsRun(t *testing.T) {
	r := New(discardLogger())
	r.Add(Job{Name: "a", Interval: time.Millisecond, RunAtStart: true, Run: func(context.Context) error {
		return nil
	}})

	cancel, done := start(r)
	for range 2000 {
		_ = r.Status()
	}
	cancel()
	<-done
}

func TestTriggerRunsTheJobBeforeItsInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int64
		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, Run: func(context.Context) error {
			runs.Add(1)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		synctest.Wait()
		if got := runs.Load(); got != 0 {
			t.Fatalf("ran %d times before any trigger, want 0", got)
		}
		if err := r.Trigger("a"); err != nil {
			t.Fatalf("trigger: %v", err)
		}
		synctest.Wait()
		if got := runs.Load(); got != 1 {
			t.Fatalf("ran %d times after the trigger, want 1", got)
		}
	})
}

// The marker a manually triggered run carries, which is how the automation
// kill switch tells an operator's request apart from the schedule.
func TestOnlyATriggeredRunIsMarkedManual(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seen := make(chan bool, 4)
		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, RunAtStart: true, Run: func(ctx context.Context) error {
			seen <- ManualRun(ctx)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		synctest.Wait()
		if <-seen {
			t.Error("a scheduled run reported ManualRun(ctx) = true")
		}

		if err := r.Trigger("a"); err != nil {
			t.Fatalf("trigger: %v", err)
		}
		synctest.Wait()
		if !<-seen {
			t.Error("a triggered run reported ManualRun(ctx) = false")
		}

		// The mark is per run: the interval firing after a trigger is scheduled work.
		advance(time.Hour)
		if <-seen {
			t.Error("the run after a trigger inherited its manual mark")
		}
	})
}

func TestManualRunIsFalseWithoutTheMarker(t *testing.T) {
	if ManualRun(context.Background()) {
		t.Error("ManualRun on a plain context = true, want false")
	}
	if !ManualRun(WithManualRun(context.Background())) {
		t.Error("ManualRun on a marked context = false, want true")
	}
}

func TestTriggerUnknownJobIsAnError(t *testing.T) {
	err := New(discardLogger()).Trigger("nope")
	if !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("err = %v, want ErrUnknownJob", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v, want the job name in the message", err)
	}
}

func TestTriggerDoesNotRunAJobConcurrentlyWithItself(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var inFlight atomic.Int64
		var overlapped atomic.Bool
		release := make(chan struct{})

		r := New(discardLogger())
		r.Add(Job{Name: "a", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			if inFlight.Add(1) > 1 {
				overlapped.Store(true)
			}
			<-release
			inFlight.Add(-1)
			return nil
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()

		synctest.Wait()
		for range 2 {
			if err := r.Trigger("a"); err != nil {
				t.Fatalf("trigger: %v", err)
			}
		}
		synctest.Wait()

		close(release)
		synctest.Wait()
		if overlapped.Load() {
			t.Error("a triggered run started while the job was already running")
		}
	})
}

// The sweep is the only thing bounding some tables, so a silent failure would
// reproduce issue #4.
func TestRunLogsAFailingJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &syncWriter{}
		r := New(slog.New(slog.NewTextHandler(w, nil)))
		r.Add(Job{Name: "session-cleanup", Interval: time.Hour, RunAtStart: true, Run: func(context.Context) error {
			return errors.New("sql: database is closed")
		}})

		cancel, done := start(r)
		defer func() { cancel(); <-done }()
		synctest.Wait()

		out := w.String()
		if !strings.Contains(out, "job failed") || !strings.Contains(out, "session-cleanup") {
			t.Errorf("log = %q, want a warning naming the failing job", out)
		}
	})
}

func TestCancellationIsNotLoggedAsAFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &syncWriter{}
		r := New(slog.New(slog.NewTextHandler(w, nil)))
		ctx, cancel := context.WithCancel(context.Background())
		r.Add(Job{Name: "a", Interval: time.Hour, RunAtStart: true, Run: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		}})

		<-r.Start(ctx)

		if out := w.String(); strings.Contains(out, "job failed") {
			t.Errorf("log = %q, want cancellation not reported as a failure", out)
		}
	})
}

func TestAddRejectsDuplicateNames(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a duplicate job name did not panic")
		}
	}()
	r := New(discardLogger())
	j := Job{Name: "a", Interval: time.Hour, Run: func(context.Context) error { return nil }}
	r.Add(j)
	r.Add(j)
}

func TestAddRejectsNonPositiveInterval(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a non-positive interval did not panic")
		}
	}()
	New(discardLogger()).Add(Job{Name: "a", Run: func(context.Context) error { return nil }})
}

// Start closes registration synchronously, so this panics deterministically
// rather than racing the launch.
func TestAddAfterStartPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Add after Start did not panic")
		}
	}()
	r := New(discardLogger())
	r.Start(context.Background())
	r.Add(Job{Name: "a", Interval: time.Hour, Run: func(context.Context) error { return nil }})
}

func TestStartTwicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a second Start did not panic")
		}
	}()
	r := New(discardLogger())
	r.Start(context.Background())
	r.Start(context.Background())
}

// Pins the documented edge that a runner with no jobs is done immediately.
func TestStartWithNoJobsIsDoneImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		select {
		case <-New(discardLogger()).Start(ctx):
		case <-time.After(time.Minute):
			t.Fatal("an empty runner never signalled done")
		}
	})
}
