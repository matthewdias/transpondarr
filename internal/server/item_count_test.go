package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

type setItemCountResponse struct {
	Created int `json:"created"`
}

func itemCountPath(titleID int64) string {
	return fmt.Sprintf("/api/v1/titles/%d/items", titleID)
}

func TestSetItemCountEndpointMaterializesItems(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 0)

	var out setItemCountResponse
	code := h.postJSON(t, itemCountPath(titleID), map[string]any{"count": 12}, &out)
	if code != http.StatusCreated {
		t.Fatalf("POST items = %d, want 201", code)
	}
	if out.Created != 12 {
		t.Errorf("created = %d, want 12", out.Created)
	}

	var detail titleDetailDTO
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d", titleID), &detail); code != http.StatusOK {
		t.Fatalf("GET title = %d, want 200", code)
	}
	if len(detail.Items) != 12 {
		t.Errorf("detail lists %d items, want 12", len(detail.Items))
	}
}

func TestSetItemCountEndpointRefusesATitleThatAlreadyHasItems(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 3)

	var out setItemCountResponse
	if code := h.postJSON(t, itemCountPath(titleID), map[string]any{"count": 12}, &out); code != http.StatusConflict {
		t.Fatalf("POST items on a populated title = %d, want 409", code)
	}
}

func TestSetItemCountEndpointRefusesAnUnknownTitle(t *testing.T) {
	h := newHarness(t, nil, nil)

	var out setItemCountResponse
	if code := h.postJSON(t, itemCountPath(999), map[string]any{"count": 12}, &out); code != http.StatusNotFound {
		t.Fatalf("POST items on an unknown title = %d, want 404", code)
	}
}

// Required with no default, per #227: an omitted count must not be able to
// choose a value, and a zero one must not create an itemless title's nothing.
func TestSetItemCountEndpointRejectsAnAbsentOrZeroCount(t *testing.T) {
	h := newHarness(t, nil, nil)
	titleID := seedTitle(t, h.store, "Placeholder Saga", 0)

	for name, body := range map[string]map[string]any{
		"absent": {},
		"zero":   {"count": 0},
	} {
		var out setItemCountResponse
		if code := h.postJSON(t, itemCountPath(titleID), body, &out); code != http.StatusUnprocessableEntity {
			t.Errorf("POST items with %s count = %d, want 422", name, code)
		}
	}
}
