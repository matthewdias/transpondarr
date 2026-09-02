package server_test

import (
	"net/http"
	"testing"
)

// A blank secret aimed at a host the secret was not saved for is refused (#259),
// and the refusal is the caller's to fix by sending it — so it must surface as a
// 422 rather than as the 502 a genuine connection failure gets, or as the 500 the
// save path would otherwise report for an ordinary bad request.
func TestSecretRefusalIsAClientError(t *testing.T) {
	h := newHarness(t, nil, nil)

	seed := map[string]any{
		"url": "http://qb.saved:8080", "user": "admin", "password": "hunter2",
		"category": "anime", "stall_hours": 6,
	}
	if code := do(t, h, http.MethodPut, "/api/v1/settings/download", seed, nil); code != http.StatusOK {
		t.Fatalf("seed = %d, want 200", code)
	}

	elsewhere := map[string]any{
		"url": "http://qb.attacker:8080", "user": "admin",
		"category": "anime", "stall_hours": 6,
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/settings/download/test"},
		{http.MethodPut, "/api/v1/settings/download"},
	} {
		if code := do(t, h, tc.method, tc.path, elsewhere, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("%s %s with a blank password for a new host = %d, want 422", tc.method, tc.path, code)
		}
	}
}
