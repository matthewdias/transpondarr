package ntfy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/notify"
)

type captured struct {
	path    string
	headers http.Header
	body    string
}

func capture(t *testing.T) (*httptest.Server, *captured) {
	t.Helper()
	var got captured
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		got = captured{path: r.URL.Path, headers: r.Header.Clone(), body: string(body)}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, &got
}

func TestSendPostsToServerSlashTopic(t *testing.T) {
	ts, got := capture(t)
	if err := New(ts.URL, "transpondarr-events", "").Send(context.Background(), notify.Event{
		Kind: notify.KindImported, SeriesTitle: "Placeholder Saga", ItemNumber: 5,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.path != "/transpondarr-events" {
		t.Errorf("path = %q, want /transpondarr-events", got.path)
	}
	if p := got.headers.Get("Priority"); p != "default" {
		t.Errorf("priority = %q, want default", p)
	}
	if tags := got.headers.Get("Tags"); tags != "white_check_mark" {
		t.Errorf("tags = %q, want white_check_mark", tags)
	}
	if title := got.headers.Get("Title"); title != "Import succeeded" {
		t.Errorf("title = %q, want Import succeeded", title)
	}
	if !strings.Contains(got.body, "Placeholder Saga") {
		t.Errorf("body %q should carry the series", got.body)
	}
	if got.headers.Get("Authorization") != "" {
		t.Error("authorization header sent with no token configured")
	}
}

func TestSendMarksStuckHighPriority(t *testing.T) {
	ts, got := capture(t)
	if err := New(ts.URL, "topic", "").Send(context.Background(), notify.Event{
		Kind: notify.KindImportStuck, SeriesTitle: "Placeholder Saga", Error: "source not accessible",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if p := got.headers.Get("Priority"); p != "high" {
		t.Errorf("priority = %q, want high", p)
	}
	if tags := got.headers.Get("Tags"); tags != "warning" {
		t.Errorf("tags = %q, want warning", tags)
	}
	if !strings.Contains(got.body, "source not accessible") {
		t.Errorf("body %q should carry the error", got.body)
	}
}

func TestSendBearsTokenOnlyWhenSet(t *testing.T) {
	ts, got := capture(t)
	if err := New(ts.URL, "topic", "tk_secret").Send(context.Background(), notify.Event{Kind: notify.KindTest}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if auth := got.headers.Get("Authorization"); auth != "Bearer tk_secret" {
		t.Errorf("authorization = %q, want Bearer tk_secret", auth)
	}
}

func TestSendReportsNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(ts.Close)
	err := New(ts.URL, "topic", "").Send(context.Background(), notify.Event{Kind: notify.KindTest})
	if err == nil {
		t.Fatal("want an error on a 403")
	}
	if !strings.Contains(err.Error(), "ntfy:") || !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q should carry the adapter name and status", err)
	}
}
