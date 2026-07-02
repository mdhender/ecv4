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

// memberHandlePattern is the handle rule enforced at the service layer. It mirrors
// the game_account_role.handle CHECK (migration 0004): two or more characters,
// starting with a letter, using only letters, digits, '.', '_' or '-'. Enforcing
// it here turns a bad handle into a clear 400 rather than an opaque DB-CHECK 500.
var memberHandlePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]+$`)

// AddGameMember assigns an account to a game as a GM or a net-new player. It
// enforces the game-management action matrix: the caller must be an admin or an
// active GM of the game; adding a GM is allowed in any status except archived,
// while adding a player is recruiting-only (an admin bypasses that window, but
// archived freezes every update). An omitted handle defaults to player_N in the
// store; a duplicate handle or an already-assigned account is a 409.
func (s *Server) AddGameMember(ctx context.Context, request api.AddGameMemberRequestObject) (api.AddGameMemberResponseObject, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return addMemberUnauthorized("missing credentials"), nil
	}

	account, err := s.store.AccountByID(ctx, claims.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return addMemberUnauthorized("account no longer exists"), nil
	case err != nil:
		return nil, err
	case !account.IsActive:
		return addMemberUnauthorized("account is not active"), nil
	}

	if request.Body == nil {
		return addMemberBadRequest("request body is required"), nil
	}
	if request.Body.AccountId <= 0 {
		return addMemberBadRequest("accountId is required"), nil
	}
	handle := ""
	if request.Body.Handle != nil {
		handle = strings.TrimSpace(*request.Body.Handle)
		if !memberHandlePattern.MatchString(handle) {
			return addMemberBadRequest("handle must be two or more characters, start with a letter, and use only letters, digits, '.', '_' or '-'"), nil
		}
	}
	isGM := request.Body.IsGm != nil && *request.Body.IsGm

	// Visibility gate, shared with GetGame: an admin sees any game, a non-admin
	// only a game they were ever assigned to that is not admin-hidden. Unknown or
	// not-visible → 404, so a non-member cannot probe for a game's existence.
	game, err := s.store.GameByID(ctx, request.GameId, account.ID, account.IsAdmin)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return addMemberNotFound("game not found"), nil
	case err != nil:
		return nil, err
	}

	// Role gate: an admin bypasses it; otherwise the caller must be an active GM of
	// the game. A player or a dropped member can see the game but may not add.
	if !account.IsAdmin {
		caller, err := s.store.MemberForGame(ctx, request.GameId, account.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return addMemberForbidden("only an active game master or an admin may add members"), nil
		case err != nil:
			return nil, err
		}
		if !caller.IsActive || !caller.IsGM {
			return addMemberForbidden("only an active game master or an admin may add members"), nil
		}
	}

	if msg := addMemberStatusGate(game.Status, isGM, account.IsAdmin); msg != "" {
		return addMemberForbidden(msg), nil
	}

	member, err := s.store.AddMember(ctx, request.GameId, request.Body.AccountId, handle, isGM)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return addMemberBadRequest("account does not exist"), nil
	case errors.Is(err, store.ErrMemberExists):
		return addMemberConflict("account is already a member; reactivate the existing membership instead"), nil
	case errors.Is(err, store.ErrHandleTaken):
		return addMemberConflict("handle already in use"), nil
	case errors.Is(err, store.ErrConflict):
		return addMemberConflict("member could not be added due to a conflict"), nil
	case err != nil:
		return nil, err
	}

	return api.AddGameMember201JSONResponse(apiMember(member)), nil
}

// addMemberStatusGate reports why the game's status forbids adding a member, or
// "" when it is allowed. Archived freezes every update (admins included). A GM may
// be added in any other status; a player only while recruiting, unless the caller
// is an admin, who bypasses that window.
func addMemberStatusGate(status string, isGM, isAdmin bool) string {
	if status == string(api.Archived) {
		return "the game is archived and cannot be modified"
	}
	if isGM || isAdmin {
		return ""
	}
	if status != string(api.Recruiting) {
		return "players can only be added while the game is recruiting"
	}
	return ""
}

// addMemberUnauthorized builds the 401 response for AddGameMember.
func addMemberUnauthorized(message string) api.AddGameMember401JSONResponse {
	return api.AddGameMember401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code: "unauthorized", Message: message,
	}}
}

// addMemberBadRequest builds the 400 response for AddGameMember.
func addMemberBadRequest(message string) api.AddGameMember400JSONResponse {
	return api.AddGameMember400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}

// addMemberForbidden builds the 403 response for AddGameMember.
func addMemberForbidden(message string) api.AddGameMember403JSONResponse {
	return api.AddGameMember403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse{
		Code: "forbidden", Message: message,
	}}
}

// addMemberNotFound builds the 404 response for AddGameMember.
func addMemberNotFound(message string) api.AddGameMember404JSONResponse {
	return api.AddGameMember404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
		Code: "not_found", Message: message,
	}}
}

// addMemberConflict builds the 409 response for AddGameMember.
func addMemberConflict(message string) api.AddGameMember409JSONResponse {
	return api.AddGameMember409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse{
		Code: "conflict", Message: message,
	}}
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
