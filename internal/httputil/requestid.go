package httputil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// RequestIDHeader is the header RequestLogger reads an inbound request id from
// and echoes the effective id back on. Clients that supply their own id (for
// tracing across services) have it honored; everyone else gets a minted one.
const RequestIDHeader = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = iota

// withRequestID returns a copy of ctx carrying id, retrievable with RequestID.
func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request id RequestLogger stored on ctx, or "" if the
// request did not pass through that middleware. Error writers use it to fill the
// requestId field so a client-visible error can be tied back to a server log.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// newRequestID mints a random 128-bit id as a 32-character hex string. It mirrors
// auth.NewTokenID's approach; the id is only a correlation handle, never a secret.
// A read failure from crypto/rand yields "", which the middleware treats as "no
// id" rather than aborting the request.
func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(buf[:])
}
