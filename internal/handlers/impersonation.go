package handlers

import (
	"context"
	"errors"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/store"
)

// CreateImpersonation mints a short-lived, non-refreshable access token that
// bears a target account's identity so an admin can reproduce that player's
// exact view and permissions. The right to impersonate is checked here against
// the real admin (requireAdmin); the minted token then carries the target as its
// subject and the admin as an audit-only actor claim, so all downstream
// authorization uses the target's (effective) identity and never admin.
//
// The target must be an active, non-admin account other than the caller.
// Impersonating yourself, another admin, or an inactive account is a 409 — none
// would faithfully reproduce a playable account, and impersonating an admin
// would hand out god-mode.
func (s *Server) CreateImpersonation(ctx context.Context, request api.CreateImpersonationRequestObject) (api.CreateImpersonationResponseObject, error) {
	admin, authErr, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	} else if authErr != nil {
		status, code, message := authErr.response()
		if status == http.StatusForbidden {
			return api.CreateImpersonation403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse{
				Code: code, Message: message,
			}}, nil
		}
		return api.CreateImpersonation401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
			Code: code, Message: message,
		}}, nil
	}

	if request.Body == nil {
		return impersonationBadRequest("request body is required"), nil
	}
	if request.Body.AccountId <= 0 {
		return impersonationBadRequest("accountId is required"), nil
	}

	// Self is off-limits and needs no lookup: it is the caller, who is an admin.
	if request.Body.AccountId == admin.ID {
		return impersonationConflict("cannot impersonate your own account"), nil
	}

	target, err := s.store.AccountByID(ctx, request.Body.AccountId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return api.CreateImpersonation404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
			Code: "not_found", Message: "account not found",
		}}, nil
	case err != nil:
		return nil, err
	}

	switch {
	case target.IsAdmin:
		return impersonationConflict("cannot impersonate an admin account"), nil
	case !target.IsActive:
		return impersonationConflict("cannot impersonate an inactive account"), nil
	}

	// The token carries the target's identity and roles; god-mode is off because
	// the target is not an admin. admin.ID rides along as the audited actor.
	roles := roleStrings(accountRoles(target))
	token, _, err := s.tokens.IssueImpersonation(target.ID, target.Email, roles, admin.ID)
	if err != nil {
		return nil, err
	}

	return api.CreateImpersonation200JSONResponse{
		Token:            token,
		TokenType:        api.ImpersonationResponseTokenTypeBearer,
		ExpiresInSeconds: int(s.tokens.ImpersonationTTL().Seconds()),
		Subject: api.ImpersonationSubject{
			AccountId: target.ID,
			Email:     openapi_types.Email(target.Email),
		},
		Actor: api.ImpersonationActor{AccountId: admin.ID},
	}, nil
}

func impersonationBadRequest(message string) api.CreateImpersonation400JSONResponse {
	return api.CreateImpersonation400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}

func impersonationConflict(message string) api.CreateImpersonation409JSONResponse {
	return api.CreateImpersonation409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse{
		Code: "conflict", Message: message,
	}}
}
