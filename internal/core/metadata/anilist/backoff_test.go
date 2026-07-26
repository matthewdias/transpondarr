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
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, titleResponse)
	}))
	defer srv.Close()

	c := stubClient(srv.URL)
	start := time.Now()
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
	// Both callers 429 at ~t0 and share the ~1s deadline; independent backoffs
	// (the old behaviour) also finish near 1s, so the assertion here is only
	// that sharing did not stack the waits into ~2s.
	if elapsed := time.Since(start); elapsed > 1800*time.Millisecond {
		t.Errorf("two callers took %v, want a single shared ~1s backoff", elapsed)
	}
}
