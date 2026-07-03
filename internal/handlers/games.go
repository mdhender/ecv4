package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/gamerules"
	"github.com/mdhender/ecv4/internal/store"
)

// CreateGame creates a game. It is admin-only (a game-scoped GM role cannot
// apply before any game exists), validates the code and name in Go so a bad
// request is a 400 rather than a DB-CHECK 500, and maps a duplicate code to 409.
func (s *Server) CreateGame(ctx context.Context, request api.CreateGameRequestObject) (api.CreateGameResponseObject, error) {
	if _, authErr, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	} else if authErr != nil {
		if authErr.forbidden {
			return api.CreateGame403JSONResponse{ForbiddenJSONResponse: authErr.forbiddenBody()}, nil
		}
		return api.CreateGame401JSONResponse{UnauthorizedJSONResponse: authErr.unauthorizedBody()}, nil
	}

	if request.Body == nil {
		return createGameBadRequest("request body is required"), nil
	}

	if !gamerules.ValidCode(request.Body.Code) {
		return createGameBadRequest(gamerules.ErrInvalidCode.Error()), nil
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

// statusOrder is the linear lifecycle chain. A status's index gives the
// forward/backward direction of a transition: draft(0) → recruiting(1) →
// active(2) → paused(3) → complete(4) → archived(5).
var statusOrder = []api.GameStatus{
	api.Draft, api.Recruiting, api.Active, api.Paused, api.Complete, api.Archived,
}

// statusIndex returns the position of s in statusOrder, or -1 if s is not a known
// status.
func statusIndex(s api.GameStatus) int {
	for i, v := range statusOrder {
		if v == s {
			return i
		}
	}
	return -1
}

// UpdateGame applies a partial update to a game: a status transition, a
// name/description edit, or the admin is_active hard-hide. Each field carries its
// own rule from the action matrix, enforced per-field and only for fields that
// represent an actual change:
//   - status advances forward (skips allowed) for an active GM or admin; backward
//     is a 409 except paused→active (un-pause) and out-of-archived, both admin-only.
//   - name/description require an active GM or admin.
//   - isActive (hard-hide) is admin-only and orthogonal to status.
//
// An archived game is frozen: the only accepted change is an admin moving its
// status to another state (the locked archived-freeze exception).
func (s *Server) UpdateGame(ctx context.Context, request api.UpdateGameRequestObject) (api.UpdateGameResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return updateGameUnauthorized(msg), nil
	}

	if request.Body == nil {
		return updateGameBadRequest("request body is required"), nil
	}
	body := request.Body
	if body.Status == nil && body.Name == nil && body.Description == nil && body.IsActive == nil {
		return updateGameBadRequest("no changes requested"), nil
	}

	// Visibility gate, shared with GetGame: unknown or not-visible game → 404. A
	// non-admin can only reach a game they were assigned to that is not hidden, so
	// only an admin can ever act on an admin-hidden game.
	game, err := s.store.GameByID(ctx, request.GameId, account.ID, account.IsAdmin)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return updateGameNotFound("game not found"), nil
	case err != nil:
		return nil, err
	}

	// An admin bypasses the GM requirement; a non-admin's role comes from their own
	// membership, which GameByID guarantees exists.
	activeGM := account.IsAdmin
	if !account.IsAdmin {
		caller, err := s.store.MemberForGame(ctx, request.GameId, account.ID)
		if err != nil {
			return nil, err
		}
		activeGM = caller.IsActive && caller.IsGM
	}

	current := api.GameStatus(game.Status)

	// Archived freeze: everything but a status change is frozen (admins included).
	// The status change itself (out of archived, admin-only) is handled below.
	if current == api.Archived && (body.Name != nil || body.Description != nil || body.IsActive != nil) {
		return updateGameForbidden("an archived game is frozen; only an admin may change its status"), nil
	}

	var upd store.GameUpdate

	// status
	if body.Status != nil {
		target := *body.Status
		if !target.Valid() {
			return updateGameBadRequest("unknown status"), nil
		}
		if target != current { // an actual transition
			ci, ti := statusIndex(current), statusIndex(target)
			switch {
			case current == api.Archived:
				// The sole archived exception: an admin moving the game out of archived.
				if !account.IsAdmin {
					return updateGameForbidden("only an admin may change the status of an archived game"), nil
				}
			case ti > ci:
				// Forward, skips allowed.
				if !(account.IsAdmin || activeGM) {
					return updateGameForbidden("only an active game master or an admin may advance a game's status"), nil
				}
			case current == api.Paused && target == api.Active:
				// Un-pause is the one non-archived backward move, admin only.
				if !account.IsAdmin {
					return updateGameForbidden("only an admin may un-pause a game"), nil
				}
			default:
				return updateGameConflict(fmt.Sprintf("cannot change status from %q to %q", current, target)), nil
			}
			st := string(target)
			upd.Status = &st
		}
	}

	// name / description: an active GM or an admin. (Archived already rejected above.)
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			return updateGameBadRequest("name must not be empty"), nil
		}
		if name != game.Name {
			if !(account.IsAdmin || activeGM) {
				return updateGameForbidden("only an active game master or an admin may edit game metadata"), nil
			}
			upd.Name = &name
		}
	}
	if body.Description != nil {
		desc := *body.Description
		if game.Description == nil || desc != *game.Description {
			if !(account.IsAdmin || activeGM) {
				return updateGameForbidden("only an active game master or an admin may edit game metadata"), nil
			}
			upd.Description = &desc
		}
	}

	// isActive: admin-only hard-hide, orthogonal to status.
	if body.IsActive != nil {
		if *body.IsActive != game.IsActive {
			if !account.IsAdmin {
				return updateGameForbidden("only an admin may change a game's visibility"), nil
			}
			v := *body.IsActive
			upd.IsActive = &v
		}
	}

	// Every requested field may already match the game's state; a pure no-op returns
	// the game unchanged rather than touching the store.
	if upd.Status == nil && upd.Name == nil && upd.Description == nil && upd.IsActive == nil {
		return api.UpdateGame200JSONResponse(apiGame(game)), nil
	}

	updated, err := s.store.UpdateGame(ctx, request.GameId, upd)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return updateGameNotFound("game not found"), nil
	case err != nil:
		return nil, err
	}
	return api.UpdateGame200JSONResponse(apiGame(updated)), nil
}

// updateGameUnauthorized builds the 401 response for UpdateGame.
func updateGameUnauthorized(message string) api.UpdateGame401JSONResponse {
	return api.UpdateGame401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code: "unauthorized", Message: message,
	}}
}

// updateGameBadRequest builds the 400 response for UpdateGame.
func updateGameBadRequest(message string) api.UpdateGame400JSONResponse {
	return api.UpdateGame400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}

// updateGameForbidden builds the 403 response for UpdateGame.
func updateGameForbidden(message string) api.UpdateGame403JSONResponse {
	return api.UpdateGame403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse{
		Code: "forbidden", Message: message,
	}}
}

// updateGameNotFound builds the 404 response for UpdateGame.
func updateGameNotFound(message string) api.UpdateGame404JSONResponse {
	return api.UpdateGame404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
		Code: "not_found", Message: message,
	}}
}

// updateGameConflict builds the 409 response for UpdateGame.
func updateGameConflict(message string) api.UpdateGame409JSONResponse {
	return api.UpdateGame409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse{
		Code: "conflict", Message: message,
	}}
}

// ListGameMembers returns a game's roster — every assignment, GMs and players,
// active and dropped — when the game is visible to the caller. Visibility is the
// same rule GetGame applies: an admin sees any game; a non-admin only a game they
// were ever assigned to that is not admin-hidden. An unknown or not-visible game
// is a 404, so a non-member cannot probe for a game's existence. Like the other
// authenticated handlers it re-reads fresh account state rather than trusting the
// token.
func (s *Server) ListGameMembers(ctx context.Context, request api.ListGameMembersRequestObject) (api.ListGameMembersResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return listMembersUnauthorized(msg), nil
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

// AddGameMember assigns an account to a game as a GM or a net-new player. It
// enforces the game-management action matrix: the caller must be an admin or an
// active GM of the game; adding a GM is allowed in any status except archived,
// while adding a player is recruiting-only (an admin bypasses that window, but
// archived freezes every update). An omitted handle defaults to player_N in the
// store; a duplicate handle or an already-assigned account is a 409.
func (s *Server) AddGameMember(ctx context.Context, request api.AddGameMemberRequestObject) (api.AddGameMemberResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return addMemberUnauthorized(msg), nil
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
		if !gamerules.ValidHandle(handle) {
			return addMemberBadRequest(gamerules.ErrInvalidHandle.Error()), nil
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

// UpdateGameMember applies a partial update to an existing membership:
// reactivate a dropped member (isActive:true), promote a player to GM
// (isGm:true), or change a handle. Each field carries its own rule from the
// action matrix, enforced per-field so a request changing several fields must
// satisfy every rule. Only fields that represent an actual change are applied and
// authorized, so a redundant no-op is accepted without a spurious 403. An
// archived game rejects every update (admins included).
func (s *Server) UpdateGameMember(ctx context.Context, request api.UpdateGameMemberRequestObject) (api.UpdateGameMemberResponseObject, error) {
	account, msg, err := s.authenticatedAccount(ctx)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return updateMemberUnauthorized(msg), nil
	}

	if request.Body == nil {
		return updateMemberBadRequest("request body is required"), nil
	}
	body := request.Body
	if body.IsActive == nil && body.IsGm == nil && body.Handle == nil {
		return updateMemberBadRequest("no changes requested"), nil
	}

	// Visibility gate, shared with GetGame: unknown or not-visible game → 404.
	game, err := s.store.GameByID(ctx, request.GameId, account.ID, account.IsAdmin)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return updateMemberNotFound("game not found"), nil
	case err != nil:
		return nil, err
	}

	// The target membership must exist; a non-member cannot be updated (add first).
	target, err := s.store.MemberForGame(ctx, request.GameId, request.AccountId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return updateMemberNotFound("member not found"), nil
	case err != nil:
		return nil, err
	}

	// Archived freezes every membership update, admins included. The only archived
	// exception — changing status out of archived — is a lifecycle action, not this
	// endpoint.
	if game.Status == string(api.Archived) {
		return updateMemberForbidden("the game is archived and cannot be modified"), nil
	}

	// The caller's standing: an admin bypasses the GM requirement; a non-admin's
	// role comes from their own membership, which GameByID guarantees exists.
	isSelf := account.ID == request.AccountId
	activeGM := account.IsAdmin
	if !account.IsAdmin {
		caller, err := s.store.MemberForGame(ctx, request.GameId, account.ID)
		if err != nil {
			return nil, err
		}
		activeGM = caller.IsActive && caller.IsGM
	}
	recruiting := game.Status == string(api.Recruiting)

	var upd store.MemberUpdate

	// isActive: reactivate (true) or drop / self-deactivate (false). Only an actual
	// change is applied and authorized.
	if body.IsActive != nil {
		if *body.IsActive {
			// Reactivate a dropped member: admin or an active GM. A player cannot
			// reactivate themselves — coming back is not self-service.
			if !target.IsActive {
				if !(account.IsAdmin || activeGM) {
					return updateMemberForbidden("only an active game master or an admin may reactivate a member"), nil
				}
				v := true
				upd.IsActive = &v
			}
		} else {
			// Drop (soft-deactivate; the row is never physically deleted): a member
			// may drop their own role at any time, and an admin or an active GM may
			// drop another member. This can legitimately leave the game with no active
			// GM or player — accepted; admin is the recovery path. activeGM already
			// includes admins.
			if target.IsActive {
				if !(isSelf || activeGM) {
					return updateMemberForbidden("only the member, an active game master, or an admin may drop a member"), nil
				}
				v := false
				upd.IsActive = &v
			}
		}
	}

	// isGm: promote only. Demotion (GM→player) is out of scope and always rejected.
	if body.IsGm != nil {
		if !*body.IsGm {
			return updateMemberForbidden("demoting a game master is not supported"), nil
		}
		if !target.IsGM { // an actual promotion
			if !(account.IsAdmin || activeGM) {
				return updateMemberForbidden("only an active game master or an admin may promote a member"), nil
			}
			if !account.IsAdmin && !recruiting {
				return updateMemberForbidden("players can only be promoted while the game is recruiting"), nil
			}
			v := true
			upd.IsGM = &v
		}
	}

	// handle: an active GM or an admin manages the roster and may rename any member
	// in any non-archived status (the 'player_' prefix is a GM/admin concern, so it
	// is allowed). A plain player may rename only themselves, only while recruiting,
	// and not to a 'player_' handle.
	if body.Handle != nil {
		handle := strings.TrimSpace(*body.Handle)
		if !gamerules.ValidHandle(handle) {
			return updateMemberBadRequest(gamerules.ErrInvalidHandle.Error()), nil
		}
		if handle != target.Handle { // an actual rename
			switch {
			case activeGM:
				// admin or active GM: any non-archived status, 'player_' permitted.
			case isSelf:
				if !recruiting {
					return updateMemberForbidden("a handle can only be changed while the game is recruiting"), nil
				}
				if strings.HasPrefix(handle, "player_") {
					return updateMemberBadRequest("a handle may not begin with 'player_'"), nil
				}
			default:
				return updateMemberForbidden("only the member, a game master, or an admin may change a handle"), nil
			}
			upd.Handle = &handle
		}
	}

	// Every requested field may already match the target's state; a pure no-op
	// returns the member unchanged rather than touching the store.
	if upd.IsActive == nil && upd.IsGM == nil && upd.Handle == nil {
		return api.UpdateGameMember200JSONResponse(apiMember(target)), nil
	}

	member, err := s.store.UpdateMember(ctx, request.GameId, request.AccountId, upd)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return updateMemberNotFound("member not found"), nil
	case errors.Is(err, store.ErrHandleTaken):
		return updateMemberConflict("handle already in use"), nil
	case errors.Is(err, store.ErrConflict):
		return updateMemberConflict("member could not be updated due to a conflict"), nil
	case err != nil:
		return nil, err
	}

	return api.UpdateGameMember200JSONResponse(apiMember(member)), nil
}

// updateMemberUnauthorized builds the 401 response for UpdateGameMember.
func updateMemberUnauthorized(message string) api.UpdateGameMember401JSONResponse {
	return api.UpdateGameMember401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
		Code: "unauthorized", Message: message,
	}}
}

// updateMemberBadRequest builds the 400 response for UpdateGameMember.
func updateMemberBadRequest(message string) api.UpdateGameMember400JSONResponse {
	return api.UpdateGameMember400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
		Code: "bad_request", Message: message,
	}}
}

// updateMemberForbidden builds the 403 response for UpdateGameMember.
func updateMemberForbidden(message string) api.UpdateGameMember403JSONResponse {
	return api.UpdateGameMember403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse{
		Code: "forbidden", Message: message,
	}}
}

// updateMemberNotFound builds the 404 response for UpdateGameMember.
func updateMemberNotFound(message string) api.UpdateGameMember404JSONResponse {
	return api.UpdateGameMember404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
		Code: "not_found", Message: message,
	}}
}

// updateMemberConflict builds the 409 response for UpdateGameMember.
func updateMemberConflict(message string) api.UpdateGameMember409JSONResponse {
	return api.UpdateGameMember409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse{
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
