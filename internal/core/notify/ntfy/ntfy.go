// Package ntfy delivers notify events to an ntfy topic as plain-text pushes.
package ntfy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/notify"
)

// Notifier posts events to {server}/{topic}, optionally bearer-authenticated.
type Notifier struct {
	server string
	topic  string
	token  string
	http   *http.Client
}

var _ notify.Notifier = (*Notifier)(nil)

// New builds a notifier for the given server, topic and optional access token.
func New(server, topic, token string) *Notifier {
	return &Notifier{server: server, topic: topic, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// Name identifies the adapter in logs.
func (n *Notifier) Name() string { return "ntfy" }

// look maps each kind to its push title, priority, and tag.
func look(k notify.Kind) (title, priority, tags string) {
	switch k {
	case notify.KindGrabbed:
		return "Release grabbed", "default", "inbox_tray"
	case notify.KindImported:
		return "Import succeeded", "default", "white_check_mark"
	case notify.KindImportStuck:
		return "Import stuck", "high", "warning"
	case notify.KindGrabFailed:
		return "Grab failed", "high", "x"
	case notify.KindSeriesAdded:
		return "Series added", "default", "new"
	default:
		return "Test notification", "default", "information_source"
	}
}

// body flattens the event's detail into the push's plain-text message.
func body(ev notify.Event) string {
	var lines []string
	if ev.SeriesTitle != "" {
		lines = append(lines, ev.SeriesTitle)
	}
	if ev.ItemNumber > 0 {
		lines = append(lines, "Episode "+strconv.Itoa(ev.ItemNumber))
	}
	if ev.ReleaseTitle != "" {
		lines = append(lines, ev.ReleaseTitle)
	}
	if ev.Error != "" {
		lines = append(lines, ev.Error)
	}
	if ev.Path != "" {
		lines = append(lines, ev.Path)
	}
	if len(lines) == 0 {
		lines = append(lines, "Transpondarr notification")
	}
	return strings.Join(lines, "\n")
}

// Send posts ev as a plain-text message with Title/Priority/Tags headers.
func (n *Notifier) Send(ctx context.Context, ev notify.Event) error {
	url := strings.TrimSuffix(n.server, "/") + "/" + n.topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body(ev)))
	if err != nil {
		return fmt.Errorf("ntfy: build request: %w", err)
	}
	title, priority, tags := look(ev.Kind)
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tags)
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: unexpected status %d: %s", resp.StatusCode, detail)
	}
	return nil
}
