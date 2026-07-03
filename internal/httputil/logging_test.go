package httputil

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdhender/ecv4/internal/api"
)

// capturingHandler is a slog.Handler that keeps every record it receives so a
// test can assert on the attributes RequestLogger emitted.
type capturingHandler struct {
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func attrs(r slog.Record) map[string]slog.Value {
	m := make(map[string]slog.Value, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value
		return true
	})
	return m
}

func TestRequestLoggerMintsIDAndLogsStatus(t *testing.T) {
	cap := &capturingHandler{}
	logger := slog.New(cap)

	handler := RequestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A handler must be able to see the same id the middleware echoes.
		if RequestID(r.Context()) == "" {
			t.Error("request id missing from context inside handler")
		}
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	id := rec.Header().Get(RequestIDHeader)
	if id == "" {
		t.Fatal("expected a minted X-Request-Id response header")
	}

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(cap.records))
	}
	rec0 := cap.records[0]
	if rec0.Level != slog.LevelInfo {
		t.Errorf("expected Info level for 418, got %v", rec0.Level)
	}
	a := attrs(rec0)
	if got := a["status"].Int64(); got != http.StatusTeapot {
		t.Errorf("logged status = %d, want %d", got, http.StatusTeapot)
	}
	if got := a["requestId"].String(); got != id {
		t.Errorf("logged requestId = %q, want %q (the echoed header)", got, id)
	}
	if got := a["method"].String(); got != http.MethodGet {
		t.Errorf("logged method = %q, want GET", got)
	}
}

func TestRequestLoggerHonorsInboundID(t *testing.T) {
	cap := &capturingHandler{}
	handler := RequestLogger(slog.New(cap), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/thing", nil)
	req.Header.Set(RequestIDHeader, "client-supplied-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(RequestIDHeader); got != "client-supplied-id" {
		t.Errorf("echoed request id = %q, want the inbound one", got)
	}
	if got := attrs(cap.records[0])["requestId"].String(); got != "client-supplied-id" {
		t.Errorf("logged requestId = %q, want the inbound one", got)
	}
}

func TestRequestLoggerElevates5xx(t *testing.T) {
	cap := &capturingHandler{}
	handler := RequestLogger(slog.New(cap), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))

	if cap.records[0].Level != slog.LevelError {
		t.Errorf("expected Error level for 500, got %v", cap.records[0].Level)
	}
}

func TestRequestLoggerDefaultsStatusToOK(t *testing.T) {
	cap := &capturingHandler{}
	handler := RequestLogger(slog.New(cap), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write a body without an explicit WriteHeader; net/http implies 200.
		_, _ = w.Write([]byte("ok"))
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	a := attrs(cap.records[0])
	if got := a["status"].Int64(); got != http.StatusOK {
		t.Errorf("logged status = %d, want 200", got)
	}
	if got := a["bytes"].Int64(); got != 2 {
		t.Errorf("logged bytes = %d, want 2", got)
	}
}

func TestWriteErrorCarriesRequestID(t *testing.T) {
	cap := &capturingHandler{}
	handler := RequestLogger(slog.New(cap), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusBadRequest, "bad_request", "nope")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var body api.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.RequestId == nil || *body.RequestId == "" {
		t.Error("error body requestId is empty")
	}
	if body.RequestId == nil || *body.RequestId != rec.Header().Get(RequestIDHeader) {
		t.Errorf("body requestId %v does not match header %q", body.RequestId, rec.Header().Get(RequestIDHeader))
	}
}

func TestWriteErrorOmitsRequestIDWithoutMiddleware(t *testing.T) {
	// A request that never passed through RequestLogger has no id; the field must
	// be omitted rather than emitted empty.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	WriteError(rec, req, http.StatusInternalServerError, "internal", "boom")

	if got := rec.Body.String(); got == "" || contains(got, "requestId") {
		t.Errorf("expected body without requestId, got %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestRequestLoggerTagsImpersonation checks that when a downstream handler
// records an impersonation (via SetImpersonation), the request log line carries
// the actor and subject — the minimum audit surface for a "pose-as" request.
func TestRequestLoggerTagsImpersonation(t *testing.T) {
	cap := &capturingHandler{}
	handler := RequestLogger(slog.New(cap), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetImpersonation(r.Context(), 7, 42) // admin 7 acting as account 42
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/me", nil))

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(cap.records))
	}
	a := attrs(cap.records[0])
	if got := a["actor"].Int64(); got != 7 {
		t.Errorf("logged actor = %d, want 7", got)
	}
	if got := a["subject"].Int64(); got != 42 {
		t.Errorf("logged subject = %d, want 42", got)
	}
}

// TestRequestLoggerOmitsImpersonationWhenAbsent guards against tagging ordinary
// requests: no SetImpersonation call means no actor/subject attributes.
func TestRequestLoggerOmitsImpersonationWhenAbsent(t *testing.T) {
	cap := &capturingHandler{}
	handler := RequestLogger(slog.New(cap), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/me", nil))

	a := attrs(cap.records[0])
	if _, ok := a["actor"]; ok {
		t.Error("ordinary request log carries an actor attribute")
	}
	if _, ok := a["subject"]; ok {
		t.Error("ordinary request log carries a subject attribute")
	}
}
