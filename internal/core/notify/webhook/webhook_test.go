package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
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
		Title:        "Placeholder Saga",
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
	for _, key := range []string{"application", "event", "title", "item_number", "release_title", "error", "path", "timestamp"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("key %q missing from payload %v", key, raw)
		}
	}

	var got struct {
		Application  string `json:"application"`
		Event        string `json:"event"`
		Title        string `json:"title"`
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
	if got.Title != "Placeholder Saga" || got.ItemNumber != 5 {
		t.Errorf("title/item = %q/%d", got.Title, got.ItemNumber)
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

// items is part of the contract: the raw numbers, always an array, so a script
// reads a multi-episode import without parsing a label.
func TestSendSerializesItemsAsAnArray(t *testing.T) {
	var raw map[string]json.RawMessage
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	if err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind: notify.KindImported, Title: "Placeholder Saga", Items: []int{1, 2, 3},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := string(raw["items"]); got != "[1,2,3]" {
		t.Errorf("items = %s, want [1,2,3]", got)
	}

	// A single-item event still carries the key, as an empty array not null.
	if err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind: notify.KindImported, Title: "Placeholder Saga", ItemNumber: 5,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := string(raw["items"]); got != "[]" {
		t.Errorf("items = %s, want an empty array", got)
	}
}

// The payload is machine-facing and item_number: 1 is correct for a movie — it
// is item-scoped, with one item numbered 1. The rendering decision that hides
// the number from humans must not reach the wire, so a movie's body is
// byte-identical to the same event carrying no item kind at all.
func TestMovieEventLeavesTheWireContractUntouched(t *testing.T) {
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	ev := notify.Event{
		Kind:         notify.KindImported,
		Title:        "Placeholder Film",
		ItemNumber:   1,
		ReleaseTitle: "Placeholder.Film.2019.1080p-SynthGroup",
		Path:         "/library/Placeholder Film (2019)/Placeholder Film (2019).mkv",
	}
	n := New(ts.URL)
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatalf("send without a kind: %v", err)
	}
	ev.ItemKind = domain.KindMovie
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatalf("send as a movie: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("captured %d bodies, want 2", len(bodies))
	}
	// The timestamp is send time, so compare everything else.
	strip := func(s string) string { return timestampValue.ReplaceAllString(s, `"timestamp":""`) }
	if strip(bodies[0]) != strip(bodies[1]) {
		t.Errorf("a movie's payload changed:\n before %s\n  after %s", bodies[0], bodies[1])
	}
	if !strings.Contains(bodies[1], `"item_number":1`) {
		t.Errorf("payload = %s, want item_number 1 kept on the wire", bodies[1])
	}
}

var timestampValue = regexp.MustCompile(`"timestamp":"[^"]*"`)
