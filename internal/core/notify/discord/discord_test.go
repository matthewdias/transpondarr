package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/notify"
)

type wireEmbed struct {
	Title  string `json:"title"`
	Color  int    `json:"color"`
	Fields []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"fields"`
}

type wirePayload struct {
	Embeds []wireEmbed `json:"embeds"`
}

// capture runs a webhook endpoint that records the last wirePayload.
func capture(t *testing.T, status int) (*httptest.Server, *wirePayload) {
	t.Helper()
	var got wirePayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode wirePayload: %v", err)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts, &got
}

func fieldValue(e wireEmbed, name string) (string, bool) {
	for _, f := range e.Fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	return "", false
}

func TestSendRendersEachKind(t *testing.T) {
	cases := []struct {
		kind  notify.Kind
		title string
		color int
	}{
		{notify.KindGrabbed, "Release grabbed", 0x3498DB},
		{notify.KindImported, "Import succeeded", 0x2ECC71},
		{notify.KindImportStuck, "Import stuck", 0xE67E22},
		{notify.KindGrabFailed, "Grab failed", 0xE74C3C},
		{notify.KindTitleAdded, "Title added", 0x3498DB},
		{notify.KindTest, "Test notification", 0x5865F2},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			ts, got := capture(t, http.StatusNoContent)
			if err := New(ts.URL).Send(context.Background(), notify.Event{Kind: tc.kind, Title: "Placeholder Saga"}); err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(got.Embeds) != 1 {
				t.Fatalf("embeds = %d, want 1", len(got.Embeds))
			}
			e := got.Embeds[0]
			if e.Title != tc.title {
				t.Errorf("title = %q, want %q", e.Title, tc.title)
			}
			if e.Color != tc.color {
				t.Errorf("color = %#x, want %#x", e.Color, tc.color)
			}
		})
	}
}

func TestSendIncludesOnlyApplicableFields(t *testing.T) {
	ts, got := capture(t, http.StatusNoContent)
	err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind:         notify.KindImportStuck,
		Title:        "Placeholder Saga",
		ItemNumber:   5,
		ReleaseTitle: "[Group] Placeholder Saga - 05 (1080p)",
		Error:        "source not accessible",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	e := got.Embeds[0]
	for name, want := range map[string]string{
		"Title":   "Placeholder Saga",
		"Episode": "5",
		"Release": "[Group] Placeholder Saga - 05 (1080p)",
		"Error":   "source not accessible",
	} {
		if v, ok := fieldValue(e, name); !ok || v != want {
			t.Errorf("field %s = %q (present=%v), want %q", name, v, ok, want)
		}
	}
	if _, ok := fieldValue(e, "Path"); ok {
		t.Error("Path field present on an event with no path")
	}
}

// A rehearsal's detail is its outcome, including a perfectly healthy one, so
// filing it under a field labelled Error would report a success as a fault.
func TestSendLabelsARehearsalOutcomeNotAnError(t *testing.T) {
	ts, got := capture(t, http.StatusNoContent)
	err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind:         notify.KindRehearsal,
		Title:        "Placeholder Saga",
		ItemNumber:   5,
		ReleaseTitle: "[Group] Placeholder Saga - 05 (1080p)",
		Error:        "would have grabbed",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	e := got.Embeds[0]
	if v, ok := fieldValue(e, "Outcome"); !ok || v != "would have grabbed" {
		t.Errorf("Outcome field = %q (present=%v), want the rehearsed outcome", v, ok)
	}
	if _, ok := fieldValue(e, "Error"); ok {
		t.Error("a rehearsal rendered its outcome under an Error field")
	}
}

func TestSendOmitsUnsetFields(t *testing.T) {
	ts, got := capture(t, http.StatusNoContent)
	if err := New(ts.URL).Send(context.Background(), notify.Event{Kind: notify.KindTitleAdded, Title: "Placeholder Saga"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	e := got.Embeds[0]
	if len(e.Fields) != 1 {
		t.Fatalf("fields = %+v, want only Series", e.Fields)
	}
	if v, _ := fieldValue(e, "Title"); v != "Placeholder Saga" {
		t.Errorf("Title = %q", v)
	}
}

// Discord rejects an embed field value over 1024 chars with a 400, which would
// lose the notification exactly when the error detail is longest.
func TestSendCapsFieldValues(t *testing.T) {
	ts, got := capture(t, http.StatusNoContent)
	long := strings.Repeat("e", 3000)
	err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind: notify.KindImportStuck, Title: "Placeholder Saga", Error: long,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	v, ok := fieldValue(got.Embeds[0], "Error")
	if !ok {
		t.Fatal("Error field missing")
	}
	if n := len([]rune(v)); n != 1024 {
		t.Fatalf("field value length = %d, want exactly the 1024 cap", n)
	}
	if !strings.HasSuffix(v, "…") {
		t.Error("capped value should end with an ellipsis")
	}
}

func TestSendReportsNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid webhook"}`))
	}))
	t.Cleanup(ts.Close)
	err := New(ts.URL).Send(context.Background(), notify.Event{Kind: notify.KindTest})
	if err == nil {
		t.Fatal("want an error on a 400")
	}
	if !strings.Contains(err.Error(), "discord:") || !strings.Contains(err.Error(), "400") {
		t.Errorf("error %q should carry the adapter name and status", err)
	}
}

// A multi-item import renders one "Episodes" field, with contiguous numbers
// folded into runs so a season pack does not print a wall of digits.
func TestSendRendersMultipleEpisodesAsOneField(t *testing.T) {
	ts, got := capture(t, http.StatusNoContent)
	if err := New(ts.URL).Send(context.Background(), notify.Event{
		Kind: notify.KindImported, Title: "Placeholder Saga",
		Items: []int{1, 2, 3, 5}, Path: "/library/Placeholder Saga/Season 01",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	v, ok := fieldValue(got.Embeds[0], "Episodes")
	if !ok || v != "1-3, 5" {
		t.Errorf("Episodes = %q (present %v), want \"1-3, 5\"", v, ok)
	}
	if _, ok := fieldValue(got.Embeds[0], "Episode"); ok {
		t.Error("a multi-item event must not also render a single Episode field")
	}
}
