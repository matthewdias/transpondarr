// Package discord delivers notify events to a Discord webhook as one embed.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/notify"
)

// Notifier posts events to one Discord webhook URL.
type Notifier struct {
	url  string
	http *http.Client
}

var _ notify.Notifier = (*Notifier)(nil)

// New builds a notifier for the given webhook URL.
func New(webhookURL string) *Notifier {
	return &Notifier{url: webhookURL, http: &http.Client{Timeout: 30 * time.Second}}
}

// Name identifies the adapter in logs.
func (n *Notifier) Name() string { return "discord" }

type field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// maxFieldLen is Discord's per-field-value cap; exceeding it 400s the whole embed.
const maxFieldLen = 1024

func capValue(v string) string {
	r := []rune(v)
	if len(r) <= maxFieldLen {
		return v
	}
	return string(r[:maxFieldLen-1]) + "…"
}

type embed struct {
	Title  string  `json:"title"`
	Color  int     `json:"color"`
	Fields []field `json:"fields,omitempty"`
}

// look maps each kind to its embed title and color.
func look(k notify.Kind) (string, int) {
	switch k {
	case notify.KindGrabbed:
		return "Release grabbed", 0x3498DB
	case notify.KindImported:
		return "Import succeeded", 0x2ECC71
	case notify.KindImportStuck:
		return "Import stuck", 0xE67E22
	case notify.KindGrabFailed:
		return "Grab failed", 0xE74C3C
	case notify.KindSeriesAdded:
		return "Series added", 0x3498DB
	default:
		return "Test notification", 0x5865F2
	}
}

// Send posts ev as an embed; any 2xx (Discord returns 204) is success.
func (n *Notifier) Send(ctx context.Context, ev notify.Event) error {
	title, color := look(ev.Kind)
	e := embed{Title: title, Color: color}
	if ev.SeriesTitle != "" {
		e.Fields = append(e.Fields, field{Name: "Series", Value: capValue(ev.SeriesTitle)})
	}
	if ev.ItemNumber > 0 {
		e.Fields = append(e.Fields, field{Name: "Episode", Value: strconv.Itoa(ev.ItemNumber)})
	}
	if ev.ReleaseTitle != "" {
		e.Fields = append(e.Fields, field{Name: "Release", Value: capValue(ev.ReleaseTitle)})
	}
	if ev.Error != "" {
		e.Fields = append(e.Fields, field{Name: "Error", Value: capValue(ev.Error)})
	}
	if ev.Path != "" {
		e.Fields = append(e.Fields, field{Name: "Path", Value: capValue(ev.Path)})
	}

	body, err := json.Marshal(map[string]any{"embeds": []embed{e}})
	if err != nil {
		return fmt.Errorf("discord: encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("discord: unexpected status %d: %s", resp.StatusCode, detail)
	}
	return nil
}
