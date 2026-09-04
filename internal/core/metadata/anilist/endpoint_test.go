package anilist

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestEndpointOptionOverridesTheDefault(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"Media":{"id":9,"title":{"romaji":"Placeholder"},"format":"TV","status":"FINISHED","episodes":12}}}`)
	}))
	defer srv.Close()

	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)), WithEndpoint(srv.URL))
	c.limiter = rate.NewLimiter(rate.Inf, 1)

	if _, _, err := c.GetTitle(context.Background(), 9); err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if hits != 1 {
		t.Errorf("stub saw %d requests, want 1 — the option did not redirect the client", hits)
	}
}

func TestNewKeepsTheDefaultEndpointWithoutTheOption(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c.endpoint != defaultEndpoint {
		t.Errorf("endpoint = %q, want the AniList default", c.endpoint)
	}
}

func TestEmptyEndpointOptionIsIgnored(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)), WithEndpoint(""))
	if c.endpoint != defaultEndpoint {
		t.Errorf("endpoint = %q, want an empty override to leave the default alone", c.endpoint)
	}
}
