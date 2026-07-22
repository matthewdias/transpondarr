package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/version"
)

type healthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok"`
		Version string `json:"version"`
	}
}

func registerSystemRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/v1/health",
		Summary:     "Health check (public, no API key required)",
		Tags:        []string{"system"},
		// Empty (non-nil) security overrides the global requirement → public.
		Security: []map[string][]string{},
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		out.Body.Version = version.Version
		return out, nil
	})
}
