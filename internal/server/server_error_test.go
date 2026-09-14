package server_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a log sink safe for the server's handler goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type problemBody struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func getProblem(t *testing.T, url string) problemBody {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var p problemBody
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if p.Status != resp.StatusCode {
		t.Fatalf("body status %d, response status %d", p.Status, resp.StatusCode)
	}
	return p
}

func TestServerErrorIsLoggedAndHidesItsCause(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarnessWithLogger(t, nil, nil, testProvider(), slog.New(slog.NewTextHandler(logs, nil)))
	// A closed database makes the title list fail the way a broken store does.
	_ = h.store.DB.Close()

	p := getProblem(t, h.ts.URL+"/api/v1/titles")
	if p.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", p.Status)
	}
	if len(p.Errors) != 0 {
		t.Errorf("a 500 sent its cause to the client: %+v", p.Errors)
	}
	if p.Detail == "" {
		t.Error("a 500 has no detail")
	}

	out := logs.String()
	for _, want := range []string{"level=ERROR", "GET", "/api/v1/titles", "database is closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %q:\n%s", want, out)
		}
	}
}

func TestBadGatewayKeepsItsUpstreamCause(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarnessWithLogger(t, nil, nil, testProvider(), slog.New(slog.NewTextHandler(logs, nil)))

	p := getProblem(t, h.ts.URL+"/api/v1/metadata/search?term=x")
	if p.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", p.Status)
	}
	if len(p.Errors) == 0 || !strings.Contains(p.Errors[0].Message, "unexpected metadata call") {
		t.Errorf("a 502 lost its upstream cause: %+v", p.Errors)
	}

	out := logs.String()
	for _, want := range []string{"level=ERROR", "/api/v1/metadata/search", "unexpected metadata call"} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %q:\n%s", want, out)
		}
	}
}

func TestClientErrorIsNotLogged(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarnessWithLogger(t, nil, nil, testProvider(), slog.New(slog.NewTextHandler(logs, nil)))

	p := getProblem(t, h.ts.URL+"/api/v1/titles/999999")
	if p.Status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", p.Status)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a 404 was logged as a server error:\n%s", logs.String())
	}
}
