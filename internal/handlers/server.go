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

// errNotImplemented is returned by the out-of-scope game handlers that have no
// service-layer implementation yet. The strict response error handler (see
// NewHTTPHandler) maps it to a 501 with the standard error envelope, so hitting
// one of these routes yields an honest "not implemented" instead of a
// misleading empty 200.
var errNotImplemented = errors.New("not implemented")

// dummyHash is a bcrypt hash of a fixed string, computed once at startup with
// the same cost real secrets use. Login compares the presented password against
// it on the unknown-email and inactive-account paths so every rejection performs
// one bcrypt comparison and costs roughly the same. Without it, those paths skip
// bcrypt and return in microseconds while a real, active account pays tens of
// milliseconds, letting a caller enumerate accounts by response timing. The
// comparison never matches; its result is discarded.
var dummyHash = mustDummyHash()

func mustDummyHash() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("no account ever uses this password"), bcrypt.DefaultCost)
	if err != nil {
		panic("handlers: precompute dummy bcrypt hash: " + err.Error())
	}
	return h
}

// Server carries the dependencies the handlers need: the store for persistence
// and the token service for issuing and verifying JWTs. shutdown, when non-nil,
// enables the development-only POST /admin/shutdown route and triggers the
// server's graceful shutdown; it is nil (route disabled) unless WithShutdown is
// passed.
type Server struct {
	store    *store.Store
	tokens   *auth.TokenService
	shutdown func()
}

// Option customizes a Server.
type Option func(*Server)

// WithShutdown enables the development-only POST /admin/shutdown route and wires
// it to trigger, which starts the server's graceful shutdown. Without it the
// route responds 404, as if it did not exist — so it is gated to callers that
// deliberately started the server in development mode.
func WithShutdown(trigger func()) Option {
	return func(s *Server) { s.shutdown = trigger }
}

func NewServer(st *store.Store, tokens *auth.TokenService, opts ...Option) *Server {
	s := &Server{store: st, tokens: tokens}
	for _, opt := range opts {
		opt(s)
	}
	return s
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
		// so the response does not reveal which accounts exist. Run a throwaway
		// bcrypt comparison so this path costs the same as a real login; without
		// it the missing-account path returns far faster and timing reveals which
		// emails exist. The result is discarded.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(request.Body.Password))
		return loginUnauthorized("invalid username or password"), nil
	case err != nil:
		return nil, err
	case !account.IsActive:
		// Inactive accounts also skip the real comparison, so equalize their cost
		// the same way. See the ErrNotFound case above.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(request.Body.Password))
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
	// A login starts a fresh session family; its refresh token is persisted so
	// it can later be rotated and revoked.
	family, err := auth.NewTokenID()
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.issueRefresh(ctx, account.ID, family)
	if err != nil {
		return nil, err
	}

	return api.Login200JSONResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        api.AuthTokensTokenTypeBearer,
		ExpiresInSeconds: int(s.tokens.AccessTTL().Seconds()),
	}, nil
}

// issueRefresh mints a refresh token in family (generating a fresh jti),
// persists it as un-revoked, and returns the signed token. Login and
// RefreshToken share it: login passes a new family, rotation reuses the
// presented token's family.
func (s *Server) issueRefresh(ctx context.Context, accountID int64, family string) (string, error) {
	jti, err := auth.NewTokenID()
	if err != nil {
		return "", err
	}
	token, exp, err := s.tokens.IssueRefresh(accountID, jti, family)
	if err != nil {
		return "", err
	}
	// exp - refreshTTL is the issue time (IssueRefresh computes exp = now +
	// refreshTTL from the same clock), so this stays correct under WithClock.
	issuedAt := exp.Add(-s.tokens.RefreshTTL())
	if err := s.store.CreateRefreshToken(ctx, jti, family, accountID, issuedAt.Unix(), exp.Unix()); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Server) RefreshToken(ctx context.Context, request api.RefreshTokenRequestObject) (api.RefreshTokenResponseObject, error) {
	if request.Body == nil || request.Body.RefreshToken == "" {
		return refreshUnauthorized("missing refresh token"), nil
	}

	// A valid signature with the refresh audience and unexpired claims; garbage,
	// expired, tampered, or access-audience tokens all fail here.
	claims, err := s.tokens.VerifyRefresh(request.Body.RefreshToken)
	if err != nil {
		return refreshUnauthorized("invalid refresh token"), nil
	}

	rec, err := s.store.RefreshTokenByJTI(ctx, claims.JTI)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// A validly signed token we hold no record of: treat it as unusable.
		return refreshUnauthorized("invalid refresh token"), nil
	case err != nil:
		return nil, err
	}

	if rec.Revoked {
		// The token verified but was already rotated away or logged out.
		// Presenting it again is the reuse/theft signal: revoke the whole family
		// so a stolen token cannot be traded for fresh sessions.
		if err := s.store.RevokeFamily(ctx, rec.FamilyID); err != nil {
			return nil, err
		}
		return refreshUnauthorized("invalid refresh token"), nil
	}

	// Re-read fresh account state rather than trusting the token: never rotate a
	// token for an account that has since been removed or deactivated.
	account, err := s.store.AccountByID(ctx, claims.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return refreshUnauthorized("invalid refresh token"), nil
	case err != nil:
		return nil, err
	case !account.IsActive:
		return refreshUnauthorized("invalid refresh token"), nil
	}

	// Rotate: mint a new access + refresh token in the SAME family, persist the
	// new refresh row, then revoke the old jti last so a mid-rotation failure
	// leaves the presented token still usable.
	roles := roleStrings(accountRoles(account))
	accessToken, _, err := s.tokens.IssueAccess(account.ID, account.Email, roles)
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.issueRefresh(ctx, account.ID, rec.FamilyID)
	if err != nil {
		return nil, err
	}
	if err := s.store.RevokeRefreshToken(ctx, claims.JTI); err != nil {
		return nil, err
	}

	return api.RefreshToken200JSONResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        api.AuthTokensTokenTypeBearer,
		ExpiresInSeconds: int(s.tokens.AccessTTL().Seconds()),
	}, nil
}

func (s *Server) Logout(ctx context.Context, request api.LogoutRequestObject) (api.LogoutResponseObject, error) {
	// This route is secured, so verified claims are in the context; their
	// absence means the request somehow reached here unauthenticated.
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return api.Logout401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
			Code: "unauthorized", Message: "missing credentials",
		}}, nil
	}

	// With a specific refresh token, revoke just that session's family (and only
	// if it belongs to the caller). Without one, there is nothing to scope to,
	// so log the caller out everywhere. Both paths are idempotent: an absent,
	// unverifiable, or already-revoked token still returns 204.
	if request.Body != nil && request.Body.RefreshToken != nil && *request.Body.RefreshToken != "" {
		if rc, err := s.tokens.VerifyRefresh(*request.Body.RefreshToken); err == nil && rc.UserID == claims.UserID {
			if err := s.store.RevokeFamily(ctx, rc.Family); err != nil {
				return nil, err
			}
		}
	} else if err := s.store.RevokeAllForAccount(ctx, claims.UserID); err != nil {
		return nil, err
	}

	return api.Logout204Response{}, nil
}

func (s *Server) ShutdownServer(ctx context.Context, request api.ShutdownServerRequestObject) (api.ShutdownServerResponseObject, error) {
	// Gated to development: without a wired trigger the route behaves as if it
	// does not exist. In normal wiring a disabled route is already hidden ahead
	// of this handler (see hideRoute in NewHTTPHandler), so this check is
	// defense-in-depth: it keeps the capability invisible (404, not 403) and
	// guards s.shutdown() below from a nil call if the handler is ever reached
	// without that wrapper.
	if s.shutdown == nil {
		return api.ShutdownServer404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
			Code: "not_found", Message: "not found",
		}}, nil
	}

	// Admin only, re-reading fresh account state like the other admin routes.
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.ShutdownServer403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.ShutdownServer401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	// Trigger the graceful shutdown, then return 202. The trigger only starts
	// the drain (it cancels the server's run context); the server's Shutdown
	// call waits for in-flight requests — including this one — to finish, so the
	// 202 is written and delivered before the process exits.
	s.shutdown()
	return api.ShutdownServer202Response{}, nil
}

func (s *Server) PurgeRefreshTokens(ctx context.Context, request api.PurgeRefreshTokensRequestObject) (api.PurgeRefreshTokensResponseObject, error) {
	// Admin only, re-reading fresh account state like the other admin routes.
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.PurgeRefreshTokens403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.PurgeRefreshTokens401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	// Purge everything already expired as of now (the token service's clock, so
	// this matches issuance/verification and is deterministic under WithClock).
	purged, err := s.store.PurgeExpiredRefreshTokens(ctx, s.tokens.Now().Unix())
	if err != nil {
		return nil, err
	}
	return api.PurgeRefreshTokens200JSONResponse{Purged: purged}, nil
}

func (s *Server) GetMe(ctx context.Context, request api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	// Resolve the caller from the verified token, re-reading fresh account state
	// so a since-deactivated or removed account is rejected.
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return unauthorized(msg), nil
	}

	return api.GetMe200JSONResponse{
		User: api.User{
			Id:       account.ID,
			Username: account.Email,
			Roles:    accountRoles(account),
		},
	}, nil
}

func (s *Server) ListMyGames(ctx context.Context, request api.ListMyGamesRequestObject) (api.ListMyGamesResponseObject, error) {
	// Resolve the caller from the verified token, re-reading fresh account state
	// like the other /me handlers so a since-deactivated account is rejected.
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return myGamesUnauthorized(msg), nil
	}

	memberships, err := s.store.GamesForAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}

	games := make([]api.MyGame, len(memberships))
	for i, m := range memberships {
		games[i] = api.MyGame{
			Id:       m.GameID,
			Code:     m.Code,
			IsActive: m.IsActive,
			Handle:   m.Handle,
			IsGm:     m.IsGM,
		}
	}
	return api.ListMyGames200JSONResponse{Games: games}, nil
}

// myGamesUnauthorized builds the 401 response for ListMyGames.
func myGamesUnauthorized(message string) api.ListMyGames401JSONResponse {
	return api.ListMyGames401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

// ChangeMyPassword lets an authenticated account replace its own password. The
// caller proves ownership with the current password (verified against the stored
// hash, as in Login) before the new one is applied; on success every session for
// the account is revoked so a stolen refresh token cannot outlive the password it
// was minted under.
func (s *Server) ChangeMyPassword(ctx context.Context, request api.ChangeMyPasswordRequestObject) (api.ChangeMyPasswordResponseObject, error) {
	// Resolve the caller from the verified token, re-reading fresh account state
	// like the other /me handlers: a since-deactivated or removed account may not
	// change its password.
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return changePasswordUnauthorized(msg), nil
	}
	if request.Body == nil {
		return changePasswordBadRequest("missing request body"), nil
	}

	// Verify the current password against the stored hash before applying the
	// change. Credentials is the only method that exposes the hash.
	_, hashedSecret, err := s.store.Credentials(ctx, account.Email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return changePasswordUnauthorized("account no longer exists"), nil
	case err != nil:
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hashedSecret), []byte(request.Body.CurrentPassword)) != nil {
		return changePasswordUnauthorized("current password is incorrect"), nil
	}

	// Hash the new password, enforcing the shared secret-length policy. A too-short
	// secret is a client error (400), not a server fault.
	newHash, err := auth.HashSecret(request.Body.NewPassword)
	switch {
	case errors.Is(err, auth.ErrSecretTooShort):
		return changePasswordBadRequest("new password is too short"), nil
	case err != nil:
		return nil, err
	}

	if err := s.store.UpdateAccountByID(ctx, account.ID, store.AccountUpdate{HashedSecret: &newHash}); err != nil {
		return nil, err
	}

	// Revoke every refresh session for the account so a session created under the
	// old password cannot be rotated forward. The caller's own access token stays
	// valid until it expires; they re-authenticate to obtain a fresh session.
	if err := s.store.RevokeAllForAccount(ctx, account.ID); err != nil {
		return nil, err
	}

	return api.ChangeMyPassword204Response{}, nil
}

// changePasswordUnauthorized builds the 401 response for ChangeMyPassword.
func changePasswordUnauthorized(message string) api.ChangeMyPassword401JSONResponse {
	return api.ChangeMyPassword401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

// changePasswordBadRequest builds the 400 response for ChangeMyPassword.
func changePasswordBadRequest(message string) api.ChangeMyPassword400JSONResponse {
	return api.ChangeMyPassword400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code:    "bad_request",
		Message: message,
	}}
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

// refreshUnauthorized builds the 401 response for RefreshToken. Every refusal
// uses the same generic message so the response never distinguishes an unknown,
// expired, revoked, or reused token.
func refreshUnauthorized(message string) api.RefreshToken401JSONResponse {
	return api.RefreshToken401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
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

// ListGames returns the games visible to the authenticated caller under the
// game-management visibility rules: a non-admin sees every game they were ever
// assigned to (active or dropped membership) except admin-hidden ones, while an
// admin sees every game including hidden ones. The optional status query
// parameter narrows the result to one lifecycle status. The store applies the
// visibility itself; the handler only resolves the caller and, like the other
// /me-style handlers, re-reads fresh account state rather than trusting the token.
func (s *Server) ListGames(ctx context.Context, request api.ListGamesRequestObject) (api.ListGamesResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return listGamesUnauthorized(msg), nil
	}

	games, err := s.store.ListGames(ctx, account.ID, account.IsAdmin, gameStatusFilter(request.Params.Status))
	if err != nil {
		return nil, err
	}

	out := make([]api.Game, len(games))
	for i, g := range games {
		out[i] = apiGame(g)
	}
	return api.ListGames200JSONResponse{Games: out}, nil
}

// GetGame returns a single game's metadata when it is visible to the caller.
// Visibility is the same rule as ListGames applied to one game: an admin sees any
// game; a non-admin sees a game only if they were ever assigned to it and it is
// not admin-hidden. An unknown or not-visible game is a 404, so a non-admin
// cannot distinguish a game that does not exist from one they may not see.
func (s *Server) GetGame(ctx context.Context, request api.GetGameRequestObject) (api.GetGameResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return getGameUnauthorized(msg), nil
	}

	game, err := s.store.GameByID(ctx, request.GameId, account.ID, account.IsAdmin)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return api.GetGame404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
			Code: "not_found", Message: "game not found",
		}}, nil
	case err != nil:
		return nil, err
	}
	return api.GetGame200JSONResponse(apiGame(game)), nil
}

// gameStatusFilter converts the optional ListGames status query parameter to the
// plain-string filter the store expects, preserving a nil (no filter).
func gameStatusFilter(status *api.GameStatus) *string {
	if status == nil {
		return nil
	}
	s := string(*status)
	return &s
}

// listGamesUnauthorized builds the 401 response for ListGames.
func listGamesUnauthorized(message string) api.ListGames401JSONResponse {
	return api.ListGames401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

// getGameUnauthorized builds the 401 response for GetGame.
func getGameUnauthorized(message string) api.GetGame401JSONResponse {
	return api.GetGame401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

func (s *Server) ListTurns(ctx context.Context, request api.ListTurnsRequestObject) (api.ListTurnsResponseObject, error) {
	// TODO: enforce access to game and return turns.
	return nil, errNotImplemented
}

func (s *Server) GetTurn(ctx context.Context, request api.GetTurnRequestObject) (api.GetTurnResponseObject, error) {
	// TODO: enforce access to game/turn and return turn.
	return nil, errNotImplemented
}

func (s *Server) ValidateOrders(ctx context.Context, request api.ValidateOrdersRequestObject) (api.ValidateOrdersResponseObject, error) {
	// TODO: parse and validate orders without creating a submission.
	return nil, errNotImplemented
}

func (s *Server) SubmitOrders(ctx context.Context, request api.SubmitOrdersRequestObject) (api.SubmitOrdersResponseObject, error) {
	// TODO: validate, authorize faction ownership, and create a submission.
	return nil, errNotImplemented
}

func (s *Server) GetOrderSubmission(ctx context.Context, request api.GetOrderSubmissionRequestObject) (api.GetOrderSubmissionResponseObject, error) {
	// TODO: enforce access to submission and return it.
	return nil, errNotImplemented
}
