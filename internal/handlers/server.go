// Package handlers implements the generated StrictServerInterface from
// internal/api. Most methods are still stubs (returning nil) pending their
// service-layer implementations; the compiler, via the var _ assertion below,
// is the source of truth if generated names drift from these signatures.
package handlers

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	ecv4 "github.com/mdhender/ecv4"
	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

var _ api.StrictServerInterface = (*Server)(nil)

// Server carries the dependencies the handlers need: the store for persistence
// and the token service for issuing and verifying JWTs.
type Server struct {
	store  *store.Store
	tokens *auth.TokenService
}

func NewServer(st *store.Store, tokens *auth.TokenService) *Server {
	return &Server{store: st, tokens: tokens}
}

func (s *Server) GetHealth(ctx context.Context, request api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	return api.GetHealth200JSONResponse{Status: "ok", Version: ecv4.Version().Short()}, nil
}

func (s *Server) GetVersion(ctx context.Context, request api.GetVersionRequestObject) (api.GetVersionResponseObject, error) {
	schemaVersion, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetVersion200JSONResponse{
		Application: ecv4.Version().String(),
		Database:    api.DatabaseVersion{SchemaVersion: schemaVersion},
	}, nil
}

func (s *Server) Login(ctx context.Context, request api.LoginRequestObject) (api.LoginResponseObject, error) {
	if request.Body == nil {
		return loginUnauthorized("missing credentials"), nil
	}

	// Usernames are account emails, stored lower-cased.
	email := strings.ToLower(strings.TrimSpace(request.Body.Username))
	account, hashedSecret, err := s.store.Credentials(ctx, email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Same message for unknown user, wrong password, and inactive account
		// so the response does not reveal which accounts exist.
		return loginUnauthorized("invalid username or password"), nil
	case err != nil:
		return nil, err
	case !account.IsActive:
		return loginUnauthorized("invalid username or password"), nil
	}

	if bcrypt.CompareHashAndPassword([]byte(hashedSecret), []byte(request.Body.Password)) != nil {
		return loginUnauthorized("invalid username or password"), nil
	}

	roles := roleStrings(accountRoles(account))
	accessToken, _, err := s.tokens.IssueAccess(account.ID, account.Email, roles)
	if err != nil {
		return nil, err
	}
	refreshToken, _, err := s.tokens.IssueRefresh(account.ID)
	if err != nil {
		return nil, err
	}

	return api.Login200JSONResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        api.Bearer,
		ExpiresInSeconds: int(s.tokens.AccessTTL().Seconds()),
	}, nil
}

func (s *Server) RefreshToken(ctx context.Context, request api.RefreshTokenRequestObject) (api.RefreshTokenResponseObject, error) {
	// TODO: validate refresh token and issue replacement tokens.
	return nil, nil
}

func (s *Server) Logout(ctx context.Context, request api.LogoutRequestObject) (api.LogoutResponseObject, error) {
	// TODO: revoke refresh token/session family.
	return nil, nil
}

func (s *Server) GetMe(ctx context.Context, request api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	// The bearer-auth middleware puts verified claims in the context; their
	// absence means the request reached here unauthenticated.
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return unauthorized("missing credentials"), nil
	}

	// Read fresh account state rather than trusting the token: an account may
	// have been deactivated or removed since the token was issued.
	account, err := s.store.AccountByID(ctx, claims.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return unauthorized("account no longer exists"), nil
	case err != nil:
		return nil, err
	case !account.IsActive:
		return unauthorized("account is not active"), nil
	}

	return api.GetMe200JSONResponse{
		User: api.User{
			Id:       account.ID,
			Username: account.Email,
			Roles:    accountRoles(account),
		},
	}, nil
}

// accountRoles maps an account to its global roles. The accounts table carries
// only the admin flag; game-scoped GM/player roles live in game_account_role
// and surface on game endpoints, not in the global user context.
func accountRoles(account store.Account) []api.Role {
	if account.IsAdmin {
		return []api.Role{api.Admin}
	}
	return []api.Role{api.Player}
}

// roleStrings converts API roles to the plain strings embedded in a token.
func roleStrings(roles []api.Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

// loginUnauthorized builds the 401 response for Login.
func loginUnauthorized(message string) api.Login401JSONResponse {
	return api.Login401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

// unauthorized builds the 401 response for GetMe with a machine-readable code.
func unauthorized(message string) api.GetMe401JSONResponse {
	return api.GetMe401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

func (s *Server) ListGames(ctx context.Context, request api.ListGamesRequestObject) (api.ListGamesResponseObject, error) {
	// TODO: list games visible to the authenticated user.
	return nil, nil
}

func (s *Server) CreateGame(ctx context.Context, request api.CreateGameRequestObject) (api.CreateGameResponseObject, error) {
	// TODO: require GM/admin and create a game.
	return nil, nil
}

func (s *Server) GetGame(ctx context.Context, request api.GetGameRequestObject) (api.GetGameResponseObject, error) {
	// TODO: enforce object-level authorization and return game.
	return nil, nil
}

func (s *Server) ListTurns(ctx context.Context, request api.ListTurnsRequestObject) (api.ListTurnsResponseObject, error) {
	// TODO: enforce access to game and return turns.
	return nil, nil
}

func (s *Server) GetTurn(ctx context.Context, request api.GetTurnRequestObject) (api.GetTurnResponseObject, error) {
	// TODO: enforce access to game/turn and return turn.
	return nil, nil
}

func (s *Server) ValidateOrders(ctx context.Context, request api.ValidateOrdersRequestObject) (api.ValidateOrdersResponseObject, error) {
	// TODO: parse and validate orders without creating a submission.
	return nil, nil
}

func (s *Server) SubmitOrders(ctx context.Context, request api.SubmitOrdersRequestObject) (api.SubmitOrdersResponseObject, error) {
	// TODO: validate, authorize faction ownership, and create a submission.
	return nil, nil
}

func (s *Server) GetOrderSubmission(ctx context.Context, request api.GetOrderSubmissionRequestObject) (api.GetOrderSubmissionResponseObject, error) {
	// TODO: enforce access to submission and return it.
	return nil, nil
}
