package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// ListMySessions returns the caller's active sessions (un-revoked, un-expired
// refresh-token families). It is self-service: the object scope is the caller's
// own account, taken from the verified token rather than any request field.
func (s *Server) ListMySessions(ctx context.Context, request api.ListMySessionsRequestObject) (api.ListMySessionsResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return listSessionsUnauthorized(msg), nil
	}

	// Filter on the token service's clock so "expired" matches issuance and
	// verification and stays deterministic under WithClock.
	sessions, err := s.store.SessionsForAccount(ctx, account.ID, s.tokens.Now().Unix())
	if err != nil {
		return nil, err
	}

	out := make([]api.Session, len(sessions))
	for i, ss := range sessions {
		out[i] = api.Session{
			FamilyId:  ss.FamilyID,
			IssuedAt:  time.Unix(ss.IssuedAt, 0).UTC(),
			ExpiresAt: time.Unix(ss.ExpiresAt, 0).UTC(),
		}
	}
	return api.ListMySessions200JSONResponse{Sessions: out}, nil
}

// RevokeMySession revokes one of the caller's sessions by family id. The store
// scopes the revoke to the caller's account, so a family that is unknown or owned
// by another account is reported as 404 rather than revealing its existence.
func (s *Server) RevokeMySession(ctx context.Context, request api.RevokeMySessionRequestObject) (api.RevokeMySessionResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return revokeSessionUnauthorized(msg), nil
	}

	switch err := s.store.RevokeFamilyForAccount(ctx, request.FamilyId, account.ID); {
	case errors.Is(err, store.ErrNotFound):
		return api.RevokeMySession404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
			Code: "not_found", Message: "session not found",
		}}, nil
	case err != nil:
		return nil, err
	}
	return api.RevokeMySession204Response{}, nil
}

// authenticatedAccount resolves the caller's fresh account from the verified
// claims in ctx, re-reading store state rather than trusting the token: an account
// may have been deactivated or removed since it was issued. It returns exactly one
// of three outcomes: a real store error (the account is zero and msg empty — the
// caller maps this to a 500); an authentication failure (msg holds a reason and
// err is nil — the caller maps this to its operation's 401); or success (a live
// account with msg empty and err nil).
func (s *Server) authenticatedAccount(ctx context.Context) (account store.Account, msg string, err error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return store.Account{}, "missing credentials", nil
	}
	account, err = s.store.AccountByID(ctx, claims.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Account{}, "account no longer exists", nil
	case err != nil:
		return store.Account{}, "", err
	case !account.IsActive:
		return store.Account{}, "account is not active", nil
	}
	return account, "", nil
}

// listSessionsUnauthorized builds the 401 response for ListMySessions.
func listSessionsUnauthorized(message string) api.ListMySessions401JSONResponse {
	return api.ListMySessions401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

// revokeSessionUnauthorized builds the 401 response for RevokeMySession.
func revokeSessionUnauthorized(message string) api.RevokeMySession401JSONResponse {
	return api.RevokeMySession401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}
