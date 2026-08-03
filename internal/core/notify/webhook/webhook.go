// Package webhook delivers notify events as a JSON POST users script against.
//
// The payload is a stable contract — every key is always present:
//
//	{
//	  "application": "transpondarr",
//	  "event": "grabbed" | "imported" | "import_stuck" | "grab_failed" | "series_added" | "test",
//	  "series_title": "…",
//	  "item_number": 0,
//	  "release_title": "…",
//	  "error": "…",
//	  "path": "…",
//	  "timestamp": "RFC 3339"
//	}
//
// Requests are sent with Content-Type: application/json and User-Agent:
// transpondarr; any 2xx response counts as delivered.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/notify"
)

// Notifier posts events to one webhook URL.
type Notifier struct {
	url  string
	http *http.Client
}

var _ notify.Notifier = (*Notifier)(nil)

// New builds a notifier for the given URL.
func New(url string) *Notifier {
	return &Notifier{url: url, http: &http.Client{Timeout: 30 * time.Second}}
}

// Name identifies the adapter in logs.
func (n *Notifier) Name() string { return "webhook" }

// payload is the wire shape; no omitempty anywhere, so the contract is stable.
type payload struct {
	Application  string `json:"application"`
	Event        string `json:"event"`
	SeriesTitle  string `json:"series_title"`
	ItemNumber   int    `json:"item_number"`
	ReleaseTitle string `json:"release_title"`
	Error        string `json:"error"`
	Path         string `json:"path"`
	Timestamp    string `json:"timestamp"`
}

// Send posts ev using the package's documented JSON contract.
func (n *Notifier) Send(ctx context.Context, ev notify.Event) error {
	body, err := json.Marshal(payload{
		Application:  "transpondarr",
		Event:        string(ev.Kind),
		SeriesTitle:  ev.SeriesTitle,
		ItemNumber:   ev.ItemNumber,
		ReleaseTitle: ev.ReleaseTitle,
		Error:        ev.Error,
		Path:         ev.Path,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("webhook: encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "transpondarr")
	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook: unexpected status %d: %s", resp.StatusCode, detail)
	}
	return nil
}
