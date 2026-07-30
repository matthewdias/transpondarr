package anilist

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const titleResponse = `{"data":{"Media":{"id":1,"title":{"romaji":"Synthetic Show"},"format":"TV","episodes":1,"status":"FINISHED"}}}`

// A 429 observed by one caller must hold back every caller: the second GetTitle
// here never sees a 429 itself, yet must not hit the server before the backoff
// deadline the first caller's 429 established.
func TestBackoffIsSharedAcrossCallers(t *testing.T) {
	var mu sync.Mutex
	var requests int
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		n := requests
		times = append(times, time.Now())
		mu.Unlock()
		if n <= maxAttempts {
			// Only the final attempt's Retry-After forces a wait, so caller A
			// exhausts its attempts immediately and returns with a backoff pending.
			retry := "0"
			if n == maxAttempts {
				retry = "1"
			}
			w.Header().Set("Retry-After", retry)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, titleResponse)
	}))
	defer srv.Close()

	c := stubClient(srv.URL)
	if _, _, err := c.GetTitle(context.Background(), 1); err == nil {
		t.Fatal("caller A: want rate-limit error, got nil")
	}
	if _, _, err := c.GetTitle(context.Background(), 1); err != nil {
		t.Fatalf("caller B: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if requests != maxAttempts+1 {
		t.Fatalf("server saw %d requests, want %d", requests, maxAttempts+1)
	}
	gap := times[maxAttempts].Sub(times[maxAttempts-1])
	if gap < 900*time.Millisecond {
		t.Errorf("caller B hit the server %v after caller A's 429, want >= ~1s (shared backoff)", gap)
	}
}

// An expired backoff must not delay anyone.
func TestBackoffInThePastDoesNotDelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, titleResponse)
	}))
	defer srv.Close()

	c := stubClient(srv.URL)
	start := time.Now()
	if _, _, err := c.GetTitle(context.Background(), 1); err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("clean request took %v, want no backoff wait", elapsed)
	}
}

// Concurrent callers rate-limited in the same window sleep on one shared
// deadline rather than stacking independent backoffs.
func TestConcurrentCallersShareOneBackoffWindow(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	// Barrier: hold both first attempts until each caller has arrived, so the two
	// 429s always land in one window. Without it a late-scheduled caller lets the
	// other absorb both 429s serially, retrying into the second one — which times
	// at ~2s and never exercises sharing at all.
	bothArrived := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		n := len(times)
		if n == 2 {
			close(bothArrived)
		}
		mu.Unlock()
		if n <= 2 {
			select {
			case <-bothArrived:
			case <-time.After(5 * time.Second):
				panic("only one caller reached the server; the barrier never released")
			}
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, titleResponse)
	}))
	defer srv.Close()

	c := stubClient(srv.URL)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			_, _, errs[i] = c.GetTitle(context.Background(), 1)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 4 {
		t.Fatalf("server saw %d requests, want 4 (both callers 429 once, then retry once)", len(times))
	}
	// Measured from the second 429 rather than from spawn: how long the callers
	// took to arrive is scheduler noise, and folding it in is what made this test
	// flaky under a loaded CI runner. Stacked backoffs put the last retry ~2s
	// after the window opened; one shared deadline puts both retries at ~1s.
	if spread := times[3].Sub(times[1]); spread > 1800*time.Millisecond {
		t.Errorf("last retry landed %v after the window opened, want a single shared ~1s backoff", spread)
	}
}
