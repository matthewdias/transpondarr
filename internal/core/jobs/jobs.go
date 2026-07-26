// Package jobs runs named background work on a fixed interval, in memory. It
// gives the daemon one lifecycle and one status surface for periodic work
// instead of a bare `go` per feature: jobs are registered by name before Start,
// each gets its own goroutine, a panic is contained to the single run that
// caused it, and Start's channel closes only once every in-flight run has
// returned — which is what lets the caller close the store knowing nothing is
// still writing.
//
// It is deliberately not a cron library and not a queue: intervals only, no
// schedule expressions, nothing persisted, and no retry or backoff beyond the
// next tick. The runner also never cancels a job itself — ctx is the only
// shutdown signal a job gets, so work that must finish (an import past its
// point of no return) still can.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// ErrUnknownJob is returned by Trigger for a name that was never registered.
var ErrUnknownJob = errors.New("unknown job")

// Job is a unit of periodic work. Run must honour ctx — cancellation is the
// only shutdown signal it gets.
type Job struct {
	Name       string
	Interval   time.Duration
	RunAtStart bool
	Run        func(ctx context.Context) error
}

// JobStatus is a snapshot of one job's history; LastRun is zero until it has run.
type JobStatus struct {
	Name         string
	Interval     time.Duration
	Running      bool
	LastRun      time.Time
	LastDuration time.Duration
	LastError    error
	NextRun      time.Time
}

type job struct {
	spec    Job
	trigger chan struct{}

	running bool
	lastRun time.Time
	lastDur time.Duration
	lastErr error
	nextRun time.Time
}

// Runner owns the registered jobs and their goroutines.
type Runner struct {
	log *slog.Logger

	// mu guards the registry and every job's mutable state, and is never held
	// across a job's Run — so a blocked job cannot wedge Status.
	mu      sync.Mutex
	jobs    []*job
	byName  map[string]*job
	started bool
}

// New returns an empty runner.
func New(log *slog.Logger) *Runner {
	return &Runner{log: log, byName: make(map[string]*job)}
}

// Add registers j before Start. A late Add, a duplicate name, or a non-positive
// interval is a wiring bug, so each panics rather than silently dropping work.
func (r *Runner) Add(j Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		panic("jobs: Add after Start")
	}
	if j.Interval <= 0 {
		panic("jobs: non-positive interval for job " + j.Name)
	}
	if _, dup := r.byName[j.Name]; dup {
		panic("jobs: duplicate job name " + j.Name)
	}
	n := &job{spec: j, trigger: make(chan struct{}, 1)}
	r.jobs = append(r.jobs, n)
	r.byName[j.Name] = n
}

// Start launches every registered job and returns a channel closed once ctx is
// cancelled and every in-flight run has returned — the signal that nothing is
// still writing and the store is safe to close. It owns the goroutines so that
// registration closes synchronously here: an Add afterwards always panics
// instead of racing the launch. With no jobs registered the channel closes
// immediately, so it is not a "block until shutdown" primitive.
func (r *Runner) Start(ctx context.Context) <-chan struct{} {
	r.mu.Lock()
	if r.started {
		panic("jobs: Start called twice")
	}
	r.started = true
	js := r.jobs
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, j := range js {
			wg.Go(func() {
				r.log.Info("job started", "job", j.spec.Name, "interval", j.spec.Interval)
				r.loop(ctx, j)
			})
		}
		wg.Wait()
	}()
	return done
}

func (r *Runner) loop(ctx context.Context, j *job) {
	next := time.Now()
	if !j.spec.RunAtStart {
		next = next.Add(j.spec.Interval)
	}
	r.schedule(j, next)

	t := time.NewTimer(time.Until(next))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-j.trigger:
		}
		if ctx.Err() != nil {
			return
		}

		// Anchor on the schedule rather than on when the run finishes, so a run's
		// own duration never pushes later runs out. Published before the run so
		// NextRun points forward while a long job is still working.
		next = next.Add(j.spec.Interval)
		r.schedule(j, next)

		r.runOnce(ctx, j)

		if now := time.Now(); next.Before(now) {
			next = now.Add(j.spec.Interval)
			r.schedule(j, next)
		}
		// Safe without draining t.C: Go 1.23+ timers cannot deliver a stale tick
		// after Reset, which a trigger-driven early wake would otherwise leave behind.
		t.Reset(time.Until(next))
	}
}

func (r *Runner) schedule(j *job, at time.Time) {
	r.mu.Lock()
	j.nextRun = at
	r.mu.Unlock()
}

func (r *Runner) runOnce(ctx context.Context, j *job) {
	r.mu.Lock()
	j.running = true
	r.mu.Unlock()

	start := time.Now()
	err := r.call(ctx, j)
	elapsed := time.Since(start)

	r.mu.Lock()
	j.running, j.lastRun, j.lastDur, j.lastErr = false, start, elapsed, err
	r.mu.Unlock()

	// Cancellation is not a failure — the rule the importer and session sweep follow.
	if err != nil && ctx.Err() == nil {
		r.log.Warn("job failed", "job", j.spec.Name, "err", err)
	}
}

// call contains a panic to the single run that caused it: recovering here rather
// than in loop is what lets the job's own loop reschedule instead of dying.
func (r *Runner) call(ctx context.Context, j *job) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
			r.log.Error("job panicked", "job", j.spec.Name, "panic", v, "stack", string(debug.Stack()))
		}
	}()
	return j.spec.Run(ctx)
}

// Status snapshots every job in registration order. Safe to call while jobs run.
func (r *Runner) Status() []JobStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]JobStatus, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, JobStatus{
			Name:         j.spec.Name,
			Interval:     j.spec.Interval,
			Running:      j.running,
			LastRun:      j.lastRun,
			LastDuration: j.lastDur,
			LastError:    j.lastErr,
			NextRun:      j.nextRun,
		})
	}
	return out
}

// Trigger asks a job to run now, returning as soon as the request is queued. The
// run happens on the job's own goroutine, so a job never runs concurrently with
// itself and a pending trigger is coalesced; a manual run re-anchors the interval.
func (r *Runner) Trigger(name string) error {
	r.mu.Lock()
	j, ok := r.byName[name]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownJob, name)
	}
	select {
	case j.trigger <- struct{}{}:
	default:
	}
	return nil
}
