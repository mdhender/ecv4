package httputil

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder wraps an http.ResponseWriter to capture the status code and the
// number of body bytes written, so RequestLogger can report them after the
// handler returns. status defaults to 200 to match net/http's behavior when a
// handler writes a body without an explicit WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (rec *statusRecorder) WriteHeader(status int) {
	if !rec.wroteHeader {
		rec.status = status
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	rec.wroteHeader = true
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// RequestLogger assigns each request a correlation id, records the response
// status, and logs one line per request. It honors an inbound X-Request-Id
// header (else mints one), stores the id on the request context for the error
// writers, and echoes it back on the response header. 5xx responses are logged
// at Error level; everything else at Info.
func RequestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		if id != "" {
			w.Header().Set(RequestIDHeader, id)
			r = r.WithContext(withRequestID(r.Context(), id))
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		logger.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.String("duration", time.Since(start).String()),
			slog.Int("bytes", rec.bytes),
			slog.String("requestId", id),
		)
	})
}
