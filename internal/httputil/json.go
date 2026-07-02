package httputil

import (
	"encoding/json"
	"net/http"

	"github.com/mdhender/ecv4/internal/api"
)

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// WriteError renders the standard error envelope. It marshals the generated
// api.ErrorResponse so the wire shape has a single source of truth: a change to
// the error schema in api/openapi.yaml regenerates that type and this path
// follows it, rather than silently diverging from a hand-written duplicate.
//
// It fills requestId from the request context (set by RequestLogger) so a
// client's error report can be tied back to the matching server log line; the
// field (a nil *string) is omitted when no id is set.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	body := api.ErrorResponse{Code: code, Message: message}
	if id := RequestID(r.Context()); id != "" {
		body.RequestId = &id
	}
	WriteJSON(w, status, body)
}
