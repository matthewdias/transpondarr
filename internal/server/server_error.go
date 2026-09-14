package server

import (
	"encoding/json"
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

// writeProblem is the problem+json body Huma sends, for the handlers outside Huma.
func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}

// writeServerError is logServerErrors for the handlers outside Huma.
func writeServerError(w http.ResponseWriter, req *http.Request, logger *slog.Logger, detail string, err error) {
	logger.Error("server error",
		"method", req.Method, "path", req.URL.Path,
		"status", http.StatusInternalServerError, "detail", detail, "causes", []string{err.Error()})
	writeProblem(w, http.StatusInternalServerError, detail)
}
