// Package notify is the notification seam: one structured Event with typed
// kinds, fanned out by a Dispatcher to configured adapters (Discord, generic
// webhook, ntfy). Delivery is fire-and-forget push — a failing notifier logs and
// never blocks or fails the pipeline; retry and queueing are out of scope.
package notify

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// sendTimeout bounds one adapter's delivery so a hung endpoint cannot leak its
// goroutine forever.
const sendTimeout = 30 * time.Second

// Kind names one notification-worthy pipeline moment.
type Kind string

// The event kinds adapters render.
const (
	KindTest        Kind = "test"
	KindGrabbed     Kind = "grabbed"
	KindImported    Kind = "imported"
	KindImportStuck Kind = "import_stuck"
	KindGrabFailed  Kind = "grab_failed"
	KindSeriesAdded Kind = "series_added"
	// KindRehearsal is a notify-only pass reporting what automation would have
	// done (#116): ReleaseTitle set means "would have grabbed"; otherwise Error
	// carries why nothing would have been.
	KindRehearsal Kind = "rehearsal"
)

// Event is the one structured payload; adapters flatten it, emitters never do.
type Event struct {
	Kind         Kind
	SeriesTitle  string
	ItemNumber   int    // 0 when not item-scoped or multi-item
	Items        []int  // sorted item numbers when one release covered several; nil otherwise
	ReleaseTitle string // empty when not release-scoped
	Error        string // stuck/failed reason; on rehearsal, the outcome either way
	Path         string // library destination on imported
}

// ItemsLabel renders Items as contiguous runs ("1-3, 5"), so every adapter
// flattens a multi-item event the same way. Empty when the event is not multi-item.
func (e Event) ItemsLabel() string {
	if len(e.Items) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(e.Items); {
		j := i
		for j+1 < len(e.Items) && e.Items[j+1] == e.Items[j]+1 {
			j++
		}
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(e.Items[i]))
		if j > i {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(e.Items[j]))
		}
		i = j + 1
	}
	return b.String()
}

// DetailLabel names what Error holds for this kind, so an adapter with labelled
// fields does not file a correct rehearsal outcome under "Error".
func (k Kind) DetailLabel() string {
	if k == KindRehearsal {
		return "Outcome"
	}
	return "Error"
}

// Notifier delivers one event to one destination.
type Notifier interface {
	Name() string
	Send(ctx context.Context, ev Event) error
}

// Route pairs an adapter with the kinds it is enabled for.
type Route struct {
	Notifier Notifier
	Kinds    map[Kind]bool
}

// Dispatcher fans events out to its routes.
type Dispatcher struct {
	routes []Route
	log    *slog.Logger
}

// NewDispatcher builds a dispatcher over the given routes.
func NewDispatcher(log *slog.Logger, routes ...Route) *Dispatcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Dispatcher{routes: routes, log: log}
}

// Dispatch delivers ev to every kind-enabled route, one goroutine per route.
// It returns immediately and never reports failure — "never blocks, never fails
// the caller" is structural. WithoutCancel because a request-scoped ctx or a
// shutdown must not cancel an in-flight send.
func (d *Dispatcher) Dispatch(ctx context.Context, ev Event) {
	for _, r := range d.routes {
		if !r.Kinds[ev.Kind] {
			continue
		}
		go func(r Route) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
			defer cancel()
			if err := r.Notifier.Send(ctx, ev); err != nil {
				d.log.Warn("notify: send failed", "notifier", r.Notifier.Name(), "kind", ev.Kind, "err", err)
			}
		}(r)
	}
}
