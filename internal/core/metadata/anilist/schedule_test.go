package anilist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// stubClient points a Client at a stub server and removes the rate limiter, so
// paging tests run at wall-clock zero instead of one 2s spacing wait per page.
func stubClient(url string) *Client {
	c := New()
	c.endpoint = url
	c.limiter = rate.NewLimiter(rate.Inf, 1)
	return c
}

// scheduleResponse renders one airingSchedule page as AniList would.
func scheduleResponse(hasNext bool, nodes ...string) string {
	return fmt.Sprintf(`{"data":{"Media":{"airingSchedule":{"pageInfo":{"hasNextPage":%t},"nodes":[%s]}}}}`,
		hasNext, strings.Join(nodes, ","))
}

func node(episode int, airingAt int64) string {
	return fmt.Sprintf(`{"episode":%d,"airingAt":%d}`, episode, airingAt)
}

// requestVars decodes the GraphQL variables of one incoming request.
func requestVars(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var payload struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return payload.Variables
}

func TestGetSchedulePagesUntilExhausted(t *testing.T) {
	var pages []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vars := requestVars(t, r)
		page, _ := vars["page"].(float64)
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case 1:
			_, _ = io.WriteString(w, scheduleResponse(true, node(1, 1700000000), node(2, 1700604800)))
		default:
			_, _ = io.WriteString(w, scheduleResponse(false, node(3, 1701209600)))
		}
	}))
	defer srv.Close()

	got, err := stubClient(srv.URL).GetSchedule(context.Background(), 123, false)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}

	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %d airings, want %d: %+v", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i].Number != n {
			t.Errorf("airing %d: number = %d, want %d", i, got[i].Number, n)
		}
	}
	if !got[0].AirsAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("AirsAt = %v, want %v", got[0].AirsAt, time.Unix(1700000000, 0).UTC())
	}
	if len(pages) != 2 || pages[0] != 1 || pages[1] != 2 {
		t.Errorf("requested pages = %v, want [1 2]", pages)
	}
}

func TestGetScheduleNotYetAiredOnlyFetchesTail(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = requestVars(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, scheduleResponse(false, node(9, 1700000000)))
	}))
	defer srv.Close()

	if _, err := stubClient(srv.URL).GetSchedule(context.Background(), 123, true); err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if notYetAired, _ := got["notYetAired"].(bool); !notYetAired {
		t.Errorf("notYetAired variable = %v, want true", got["notYetAired"])
	}
}

// A title AniList has no schedule for is a normal outcome (its coverage thins out
// badly before ~2015), not an error.
func TestGetScheduleEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, scheduleResponse(false))
	}))
	defer srv.Close()

	got, err := stubClient(srv.URL).GetSchedule(context.Background(), 123, false)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d airings, want 0", len(got))
	}
}

// A server that always claims another page must not page forever.
func TestGetScheduleCapsPaging(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, scheduleResponse(true, node(requests, 1700000000)))
	}))
	defer srv.Close()

	if _, err := stubClient(srv.URL).GetSchedule(context.Background(), 123, false); err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if requests != maxSchedulePages {
		t.Errorf("made %d requests, want the %d-page cap", requests, maxSchedulePages)
	}
}

func TestGetScheduleErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := stubClient(srv.URL).GetSchedule(context.Background(), 123, false); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}
