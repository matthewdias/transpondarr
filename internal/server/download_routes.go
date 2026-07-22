package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type testDownloadOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
		Client string `json:"client" example:"qbittorrent"`
	}
}

func registerDownloadRoutes(api huma.API, deps routeDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "test-download-client",
		Method:      http.MethodPost,
		Path:        "/api/v1/download/test",
		Summary:     "Test connectivity to the configured download client",
		Tags:        []string{"download"},
	}, func(ctx context.Context, _ *struct{}) (*testDownloadOutput, error) {
		dl := deps.clients.Download()
		if dl == nil {
			return nil, huma.Error503ServiceUnavailable(
				"no download client configured (set it in Settings, or TRANSPONDARR_QBIT_URL/_USER/_PASSWORD)")
		}
		if err := dl.Test(ctx); err != nil {
			return nil, huma.Error502BadGateway("download client test failed", err)
		}
		out := &testDownloadOutput{}
		out.Body.Status = "ok"
		out.Body.Client = dl.Name()
		return out, nil
	})
}
