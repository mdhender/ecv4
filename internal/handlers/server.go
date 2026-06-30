// Package handlers implements the generated StrictServerInterface from
// internal/api. Most methods are still stubs (returning nil) pending their
// service-layer implementations; the compiler, via the var _ assertion below,
// is the source of truth if generated names drift from these signatures.
package handlers

import (
	"context"

	ecv4 "github.com/mdhender/ecv4"
	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/store"
)

var _ api.StrictServerInterface = (*Server)(nil)

// Server carries the dependencies the handlers need. More are added as handlers
// are implemented; for now it holds the store used by GetVersion.
type Server struct {
	store *store.Store
}

func NewServer(st *store.Store) *Server { return &Server{store: st} }

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
	// TODO: validate credentials, issue access and refresh JWTs.
	return nil, nil
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
	// TODO: read auth.Claims from context and return user details.
	return nil, nil
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
