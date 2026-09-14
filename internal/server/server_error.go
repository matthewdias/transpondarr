package server

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// logServerErrors is a Huma transformer: every 5xx is logged here once, and a
// 500's cause stays in the log, because it is internal and gives the user nothing to act on.
func logServerErrors(logger *slog.Logger) huma.Transformer {
	return func(ctx huma.Context, _ string, v any) (any, error) {
		model, ok := v.(*huma.ErrorModel)
		if !ok || model.Status < http.StatusInternalServerError {
			return v, nil
		}
		causes := make([]string, 0, len(model.Errors))
		for _, e := range model.Errors {
			causes = append(causes, e.Message)
		}
		logger.Error("server error",
			"method", ctx.Method(), "path", ctx.URL().Path,
			"status", model.Status, "detail", model.Detail, "causes", causes)
		if model.Status == http.StatusInternalServerError {
			model.Errors = nil
		}
		return model, nil
	}
}
