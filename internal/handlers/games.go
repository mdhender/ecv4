package handlers

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// gameCodePattern is the game-code rule enforced at the service layer. It mirrors
// the games.code CHECK the database applies (migration 0006): two or more
// uppercase ASCII letters and nothing else. Enforcing it here turns a bad code
// into a clear 400 instead of letting the DB CHECK surface as an opaque 500.
var gameCodePattern = regexp.MustCompile(`^[A-Z][A-Z]+$`)

// CreateGame creates a game. It is admin-only (a game-scoped GM role cannot
// apply before any game exists), validates the code and name in Go so a bad
// request is a 400 rather than a DB-CHECK 500, and maps a duplicate code to 409.
func (s *Server) CreateGame(ctx context.Context, request api.CreateGameRequestObject) (api.CreateGameResponseObject, error) {
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.CreateGame403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse{
				Code: "forbidden", Message: authErr.message,
			}}, nil
		}
		return api.CreateGame401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
			Code: "unauthorized", Message: authErr.message,
		}}, nil
	}

	if request.Body == nil {
		return createGameBadRequest("request body is required"), nil
	}

	if !gameCodePattern.MatchString(request.Body.Code) {
		return createGameBadRequest("code must be two or more uppercase ASCII letters (A-Z)"), nil
	}

	name := strings.TrimSpace(request.Body.Name)
	if name == "" {
		return createGameBadRequest("name is required"), nil
	}

	game, err := s.store.CreateGame(ctx, request.Body.Code, name, request.Body.Description)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return api.CreateGame409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse{
				Code: "conflict", Message: "a game with that code already exists",
			}}, nil
		}
		return nil, err
	}

	return api.CreateGame201JSONResponse(apiGame(game)), nil
}

// ListGameMembers returns a game's roster — every assignment, GMs and players,
// active and dropped — when the game is visible to the caller. Visibility is the
// same rule GetGame applies: an admin sees any game; a non-admin only a game they
// were ever assigned to that is not admin-hidden. An unknown or not-visible game
// is a 404, so a non-member cannot probe for a game's existence. Like the other
// authenticated handlers it re-reads fresh account state rather than trusting the
// token.
func (s *Server) ListGameMembers(ctx context.Context, request api.ListGameMembersRequestObject) (api.ListGameMembersResponseObject, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return listMembersUnauthorized("missing credentials"), nil
	}

	account, err := s.store.AccountByID(ctx, claims.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return listMembersUnauthorized("account no longer exists"), nil
	case err != nil:
		return nil, err
	case !account.IsActive:
		return listMembersUnauthorized("account is not active"), nil
	}

	// Gate on visibility with the same rule as GetGame: reusing GameByID means the
	// roster read can never reveal a game the caller may not see, and an unknown or
	// not-visible game is an indistinguishable 404.
	if _, err := s.store.GameByID(ctx, request.GameId, account.ID, account.IsAdmin); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return api.ListGameMembers404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
				Code: "not_found", Message: "game not found",
			}}, nil
		}
		return nil, err
	}

	members, err := s.store.MembersForGame(ctx, request.GameId)
	if err != nil {
		return nil, err
	}

	out := make([]api.Member, len(members))
	for i, m := range members {
		out[i] = apiMember(m)
	}
	return api.ListGameMembers200JSONResponse{Members: out}, nil
}

// apiMember maps a store roster member to its API representation.
func apiMember(m store.Member) api.Member {
	return api.Member{
		AccountId: m.AccountID,
		Handle:    m.Handle,
		IsGm:      m.IsGM,
		IsActive:  m.IsActive,
	}
}

// listMembersUnauthorized builds the 401 response for ListGameMembers.
func listMembersUnauthorized(message string) api.ListGameMembers401JSONResponse {
	return api.ListGameMembers401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code:    "unauthorized",
		Message: message,
	}}
}

// apiGame maps a store game to its API representation.
func apiGame(g store.Game) api.Game {
	return api.Game{
		Id:          g.ID,
		Code:        g.Code,
		Name:        g.Name,
		Status:      api.GameStatus(g.Status),
		Description: g.Description,
	}
}

// createGameBadRequest builds the 400 response for CreateGame.
func createGameBadRequest(message string) api.CreateGame400JSONResponse {
	return api.CreateGame400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}
