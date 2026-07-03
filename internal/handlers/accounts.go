package handlers

import (
	"context"
	"errors"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// adminAuthError explains why admin authorization failed: forbidden is true for
// an authenticated caller who is not an active admin (403) and false for a
// caller with no verified claims at all (401). Callers map it to their
// operation's typed response.
type adminAuthError struct {
	forbidden bool
	message   string
}

// forbiddenBody and unauthorizedBody build the shared error envelopes an admin
// handler embeds in its operation-specific 403/401 response. The generated
// response types are per-operation, so each route still names its own
// api.XxxNNNJSONResponse wrapper, but the code/message strings and the
// forbidden-vs-unauthorized decision live here so every admin handler maps an
// auth failure the same way (branch on authErr.forbidden). See the admin
// handlers in accounts.go, server.go, impersonation.go, and games.go.
func (e *adminAuthError) forbiddenBody() api.ForbiddenJSONResponse {
	return api.ForbiddenJSONResponse{Code: "forbidden", Message: e.message}
}

func (e *adminAuthError) unauthorizedBody() api.UnauthorizedJSONResponse {
	return api.UnauthorizedJSONResponse{Code: "unauthorized", Message: e.message}
}

// requireAdmin resolves the caller from the context claims and re-reads fresh
// account state (like GetMe), requiring an active admin rather than trusting the
// token's roles. It returns the admin account on success. On an authorization
// failure it returns a non-nil *adminAuthError; a real store error is returned
// as the final error for the handler to surface as a 500.
func (s *Server) requireAdmin(ctx context.Context) (store.Account, *adminAuthError, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return store.Account{}, &adminAuthError{message: "missing credentials"}, nil
	}

	account, err := s.store.AccountByID(ctx, claims.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// The token's account has since been removed, deactivated, or was never
		// an admin: all are indistinguishable to the caller as "not allowed".
		return store.Account{}, &adminAuthError{forbidden: true, message: "admin privileges required"}, nil
	case err != nil:
		return store.Account{}, nil, err
	case !account.IsActive || !account.IsAdmin:
		return store.Account{}, &adminAuthError{forbidden: true, message: "admin privileges required"}, nil
	}
	return account, nil, nil
}

// apiAccount maps a store account to its API representation, including derived
// roles.
func apiAccount(a store.Account) api.Account {
	return api.Account{
		Id:       a.ID,
		Email:    openapi_types.Email(a.Email),
		IsActive: a.IsActive,
		IsAdmin:  a.IsAdmin,
		Roles:    accountRoles(a),
	}
}

func (s *Server) GetAccount(ctx context.Context, request api.GetAccountRequestObject) (api.GetAccountResponseObject, error) {
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.GetAccount403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.GetAccount401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	account, err := s.store.AccountByID(ctx, request.AccountId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return api.GetAccount404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
				Code: "not_found", Message: "account not found",
			}}, nil
		}
		return nil, err
	}
	return api.GetAccount200JSONResponse{Account: apiAccount(account)}, nil
}

func (s *Server) ListAccounts(ctx context.Context, request api.ListAccountsRequestObject) (api.ListAccountsResponseObject, error) {
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.ListAccounts403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.ListAccounts401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]api.Account, len(accounts))
	for i, a := range accounts {
		out[i] = apiAccount(a)
	}
	return api.ListAccounts200JSONResponse{Accounts: out}, nil
}

func (s *Server) CreateAccount(ctx context.Context, request api.CreateAccountRequestObject) (api.CreateAccountResponseObject, error) {
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.CreateAccount403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.CreateAccount401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	if request.Body == nil {
		return createAccountBadRequest("request body is required"), nil
	}

	// Emails are stored lower-cased; normalize new ones the same way.
	email := strings.ToLower(strings.TrimSpace(string(request.Body.Email)))
	if email == "" {
		return createAccountBadRequest("email is required"), nil
	}

	// Use the caller's secret when supplied; otherwise generate one and echo the
	// plaintext back once in the response (only its hash is stored).
	var generatedSecret *string
	secret := ""
	if request.Body.Secret != nil {
		secret = *request.Body.Secret
	} else {
		gen, err := auth.GenerateSecret(nil)
		if err != nil {
			return nil, err
		}
		secret = gen
		generatedSecret = &gen
	}

	hashedSecret, err := auth.HashSecret(secret)
	if err != nil {
		if errors.Is(err, auth.ErrSecretTooShort) {
			return createAccountBadRequest(err.Error()), nil
		}
		return nil, err
	}

	// isActive and isAdmin default to false when omitted: a created account is
	// inactive until explicitly activated.
	isActive := request.Body.IsActive != nil && *request.Body.IsActive
	isAdmin := request.Body.IsAdmin != nil && *request.Body.IsAdmin

	id, err := s.store.CreateAccount(ctx, email, isAdmin, isActive, hashedSecret)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return api.CreateAccount409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse{
				Code: "conflict", Message: "an account with that email already exists",
			}}, nil
		}
		return nil, err
	}

	return api.CreateAccount201JSONResponse{
		Account:         apiAccount(store.Account{ID: id, Email: email, IsAdmin: isAdmin, IsActive: isActive}),
		GeneratedSecret: generatedSecret,
	}, nil
}

func (s *Server) UpdateAccount(ctx context.Context, request api.UpdateAccountRequestObject) (api.UpdateAccountResponseObject, error) {
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.UpdateAccount403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.UpdateAccount401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	if request.Body == nil {
		return updateAccountBadRequest("request body is required"), nil
	}

	upd := store.AccountUpdate{
		IsActive: request.Body.IsActive,
		IsAdmin:  request.Body.IsAdmin,
	}
	if request.Body.Secret != nil {
		hashedSecret, err := auth.HashSecret(*request.Body.Secret)
		if err != nil {
			if errors.Is(err, auth.ErrSecretTooShort) {
				return updateAccountBadRequest(err.Error()), nil
			}
			return nil, err
		}
		upd.HashedSecret = &hashedSecret
	}

	// Require at least one field: the JSON analog of the CLI's tri-state, and
	// what keeps the store from rejecting a no-op update as an error.
	if upd.IsActive == nil && upd.IsAdmin == nil && upd.HashedSecret == nil {
		return updateAccountBadRequest("at least one field must be provided"), nil
	}

	if err := s.store.UpdateAccountByID(ctx, request.AccountId, upd); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return api.UpdateAccount404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
				Code: "not_found", Message: "account not found",
			}}, nil
		}
		return nil, err
	}

	// Return fresh state so the response reflects the applied update.
	account, err := s.store.AccountByID(ctx, request.AccountId)
	if err != nil {
		return nil, err
	}
	return api.UpdateAccount200JSONResponse{Account: apiAccount(account)}, nil
}

func createAccountBadRequest(message string) api.CreateAccount400JSONResponse {
	return api.CreateAccount400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}

func updateAccountBadRequest(message string) api.UpdateAccount400JSONResponse {
	return api.UpdateAccount400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}
