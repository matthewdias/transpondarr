package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// logServerErrors is a Huma transformer that logs every 5xx once. A 500's cause
// is removed from the response, since it is internal and nothing a user can act on.
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

// storeError is the 500 for a failed store read or write; its cause goes only to the log.
func storeError(action string, errs ...error) huma.StatusError {
	return huma.Error500InternalServerError("Couldn't "+action+". The server log has the cause.", errs...)
}

// Details more than one route returns.
const (
	staleCursorDetail      = "This results page is out of date. Reload the page."
	titleGoneDetail        = "This title is no longer in your library."
	profileGoneDetail      = "That quality profile no longer exists. Choose another."
	profileNameTakenDetail = "A quality profile with that name already exists. Choose another name."
	noIndexerDetail        = "No indexer is set up. Add one in Settings > Indexer."
	noDownloadClientDetail = "No download client is set up. Add one in Settings > Download client."
)
