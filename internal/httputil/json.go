package httputil

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

// WriteError renders the standard error envelope. It fills requestId from the
// request context (set by RequestLogger) so a client's error report can be tied
// back to the matching server log line; the field is omitted when no id is set.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	WriteJSON(w, status, ErrorResponse{Code: code, Message: message, RequestID: RequestID(r.Context())})
}
