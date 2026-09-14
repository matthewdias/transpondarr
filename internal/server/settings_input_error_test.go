package server

import (
	"errors"
	"fmt"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/settings"
)

// An error settingsInputDetail has no wording for keeps its own text, so a check
// added to the settings service later still reaches the user.
func TestUnmappedSettingsErrorPassesThrough(t *testing.T) {
	unmapped := fmt.Errorf("%w: a destination this layer has no wording for", settings.ErrSecretRequired)
	model, ok := errors.AsType[*huma.ErrorModel](settingsError(unmapped, huma.Error502BadGateway, "unused"))
	if !ok {
		t.Fatal("settingsError didn't return a huma.ErrorModel")
	}
	if model.Status != 422 || model.Detail != unmapped.Error() || len(model.Errors) != 0 {
		t.Errorf("got %d %q %v, want 422 with the error's own text and no errors[]", model.Status, model.Detail, model.Errors)
	}

	if model, ok = errors.AsType[*huma.ErrorModel](settingsInputError(errors.New("a new check"), "the fallback")); !ok {
		t.Fatal("settingsInputError didn't return a huma.ErrorModel")
	}
	if model.Status != 422 || model.Detail != "the fallback" || len(model.Errors) != 0 {
		t.Errorf("got %d %q %v, want 422 with the fallback and no errors[]", model.Status, model.Detail, model.Errors)
	}
}
