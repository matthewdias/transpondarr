package anilist

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The series page shows cover art from the cached snapshot, so GetTitle must
// request and map it.
func TestGetTitleMapsCover(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"Media":{
			"id": 9,
			"title": {"romaji": "Sample Show"},
			"format": "TV",
			"episodes": 2,
			"status": "RELEASING",
			"coverImage": {"large": "https://img.example/9.png"}
		}}}`)
	}))
	defer srv.Close()

	meta, _, err := stubClient(srv.URL).GetTitle(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if meta.CoverURL != "https://img.example/9.png" {
		t.Errorf("CoverURL = %q, want the cover mapped", meta.CoverURL)
	}
	if !strings.Contains(query, "coverImage") {
		t.Error("title query does not request coverImage")
	}
}
