package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/notify"
)

func TestSendPostsTheDocumentedContract(t *testing.T) {
	var raw map[string]json.RawMessage
	var ua, ct string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		ua = r.Header.Get("User-Agent")
		ct = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind:         notify.KindImported,
		SeriesTitle:  "Placeholder Saga",
		ItemNumber:   5,
		ReleaseTitle: "[Group] Placeholder Saga - 05 (1080p)",
		Path:         "/library/Placeholder Saga/Season 01/Placeholder Saga - S01E05.mkv",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if ua != "transpondarr" {
		t.Errorf("user-agent = %q, want transpondarr", ua)
	}

	// The shape is a contract users script against: all keys always present.
	for _, key := range []string{"application", "event", "series_title", "item_number", "release_title", "error", "path", "timestamp"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("key %q missing from payload %v", key, raw)
		}
	}

	var got struct {
		Application  string `json:"application"`
		Event        string `json:"event"`
		SeriesTitle  string `json:"series_title"`
		ItemNumber   int    `json:"item_number"`
		ReleaseTitle string `json:"release_title"`
		Error        string `json:"error"`
		Path         string `json:"path"`
		Timestamp    string `json:"timestamp"`
	}
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Application != "transpondarr" || got.Event != "imported" {
		t.Errorf("application/event = %q/%q", got.Application, got.Event)
	}
	if got.SeriesTitle != "Placeholder Saga" || got.ItemNumber != 5 {
		t.Errorf("series/item = %q/%d", got.SeriesTitle, got.ItemNumber)
	}
	if got.Error != "" {
		t.Errorf("error = %q, want empty-but-present", got.Error)
	}
	if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
		t.Errorf("timestamp %q is not RFC3339: %v", got.Timestamp, err)
	}
}

func TestSendReportsNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	err := New(ts.URL).Send(context.Background(), notify.Event{Kind: notify.KindTest})
	if err == nil {
		t.Fatal("want an error on a 500")
	}
	if !strings.Contains(err.Error(), "webhook:") || !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should carry the adapter name and status", err)
	}
}
