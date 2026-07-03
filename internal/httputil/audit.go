package httputil

import "context"

// ImpersonatedSubjectHeader names the response header set on any response to a
// request made with an impersonation token. Its value is the impersonated
// (effective) account id, so a caller — including plain curl — can always see
// that a response reflects an admin acting as another account, not the admin's
// own view.
const ImpersonatedSubjectHeader = "Impersonated-Subject"

type impersonationKey struct{}

// impersonation is a mutable record of an impersonated request. RequestLogger
// installs an empty one on the context before the handler chain runs; the
// authenticator fills it in via SetImpersonation once it has verified the token.
// It must be a pointer so a value written deep in the chain is visible to the
// logger afterward, which the plain context.WithValue mechanism cannot do (the
// logger holds the parent context, not the children the chain derives).
type impersonation struct {
	actorID   int64
	subjectID int64
}

// withImpersonationHolder returns a context carrying a fresh, empty
// impersonation record along with a pointer to it for the caller (RequestLogger)
// to read after the handler returns.
func withImpersonationHolder(ctx context.Context) (context.Context, *impersonation) {
	holder := &impersonation{}
	return context.WithValue(ctx, impersonationKey{}, holder), holder
}

// SetImpersonation records that the current request acts as subjectID on behalf
// of admin actorID, so RequestLogger can attribute the request to both. It is a
// no-op when no holder was installed (a request that did not pass through
// RequestLogger), so callers may invoke it unconditionally.
func SetImpersonation(ctx context.Context, actorID, subjectID int64) {
	if holder, ok := ctx.Value(impersonationKey{}).(*impersonation); ok {
		holder.actorID = actorID
		holder.subjectID = subjectID
	}
}
