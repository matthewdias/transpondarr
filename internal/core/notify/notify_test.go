package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects the events a route received, safely across the dispatcher's
// per-route goroutines.
type recorder struct {
	name string
	err  error

	mu        sync.Mutex
	events    []Event
	ctxErrs   []error // ctx.Err() observed inside Send, before the dispatcher's cancel
	deadlines []bool  // whether the send ctx carried a deadline
	got       chan struct{}
}

func newRecorder(name string, err error) *recorder {
	return &recorder{name: name, err: err, got: make(chan struct{}, 16)}
}

func (r *recorder) Name() string { return r.name }

func (r *recorder) Send(ctx context.Context, ev Event) error {
	_, hasDeadline := ctx.Deadline()
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
	r.deadlines = append(r.deadlines, hasDeadline)
	r.mu.Unlock()
	r.got <- struct{}{}
	return r.err
}

func (r *recorder) received(t *testing.T) Event {
	t.Helper()
	select {
	case <-r.got:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a send")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[len(r.events)-1]
}

func allKinds() map[Kind]bool {
	return map[Kind]bool{
		KindGrabbed: true, KindImported: true, KindImportStuck: true,
		KindGrabFailed: true, KindTitleAdded: true,
	}
}

func TestDispatchFansOutOnlyToKindEnabledRoutes(t *testing.T) {
	on := newRecorder("on", nil)
	off := newRecorder("off", nil)
	d := NewDispatcher(slog.New(slog.DiscardHandler),
		Route{Notifier: on, Kinds: allKinds()},
		Route{Notifier: off, Kinds: map[Kind]bool{KindGrabbed: true}},
	)

	d.Dispatch(context.Background(), Event{Kind: KindImported, Title: "Placeholder Saga"})

	if ev := on.received(t); ev.Kind != KindImported || ev.Title != "Placeholder Saga" {
		t.Fatalf("enabled route got %+v, want the imported event", ev)
	}
	// The disabled route must stay silent; the enabled one already ran, so a
	// short grace is enough to catch a stray goroutine.
	select {
	case <-off.got:
		t.Fatal("route received an event for a kind it is not enabled for")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFailingRouteLogsAndDoesNotAffectOthers(t *testing.T) {
	var buf strings.Builder
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, nil))

	bad := newRecorder("bad", errors.New("boom"))
	good := newRecorder("good", nil)
	d := NewDispatcher(log,
		Route{Notifier: bad, Kinds: allKinds()},
		Route{Notifier: good, Kinds: allKinds()},
	)

	d.Dispatch(context.Background(), Event{Kind: KindGrabFailed})

	bad.received(t)
	good.received(t)
	// The warn is written after Send returns; poll briefly for it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		s := buf.String()
		mu.Unlock()
		if strings.Contains(s, "send failed") && strings.Contains(s, "bad") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no warn logged for the failing route; log: %q", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// blocker blocks Send until released, to prove Dispatch never waits on a route.
type blocker struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blocker) Name() string { return "blocker" }

func (b *blocker) Send(context.Context, Event) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}

func TestDispatchReturnsWhileASendBlocks(t *testing.T) {
	b := &blocker{entered: make(chan struct{}, 1), release: make(chan struct{})}
	d := NewDispatcher(slog.New(slog.DiscardHandler), Route{Notifier: b, Kinds: allKinds()})

	done := make(chan struct{})
	go func() {
		d.Dispatch(context.Background(), Event{Kind: KindImported})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch blocked on a slow send")
	}
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the blocked route was never sent to")
	}
	close(b.release)
}

func TestCallerCancellationDoesNotCancelASend(t *testing.T) {
	r := newRecorder("survivor", nil)
	d := NewDispatcher(slog.New(slog.DiscardHandler), Route{Notifier: r, Kinds: allKinds()})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a request-scoped ctx already gone by dispatch time
	d.Dispatch(ctx, Event{Kind: KindTitleAdded})

	r.received(t)
	r.mu.Lock()
	ctxErr := r.ctxErrs[len(r.ctxErrs)-1]
	hasDeadline := r.deadlines[len(r.deadlines)-1]
	r.mu.Unlock()
	if ctxErr != nil {
		t.Fatalf("send ctx already cancelled: %v", ctxErr)
	}
	if !hasDeadline {
		t.Fatal("send ctx has no timeout; a hung notifier would leak its goroutine forever")
	}
}
