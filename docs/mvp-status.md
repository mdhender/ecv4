# MVP-1 status — validation / gap report

Date: 2026-07-01 · Branch: `main` · App version: `0.3.0-alpha` · DB schema: 5

This is a validation pass against the four short-term MVP goals. Each item is
marked ✅ done / ⚠️ partial / ❌ missing, with `file:line` evidence and the
tests that cover it. Game endpoints (`/games`, `/turns`, `orders:*`) are out of
scope and are not counted as gaps.

## Summary table

| # | Goal | Status |
|---|------|--------|
| 1 | API server with authn/authz + graceful shutdown | ✅ |
| 2 | Server-enforced admin role gating management routes | ✅ |
| 3 | Minimal CLI: create DB, create admin, reset password | ✅ (see 3c naming) |
| 4 | Dogfood routes (accounts, health, version, shutdown route) | ✅ (with 2 caveats) |

Overall: the MVP is functionally accomplished end to end. Two design caveats and
a few notes are listed under **Gaps & risks**.

## Goal 1 — server + authn/authz

| Item | Status | Evidence | Tests |
|------|--------|----------|-------|
| Boots, serves, signal-driven graceful shutdown (drain + close pool) | ✅ | `runServer` uses `signal.NotifyContext` and `srv.Shutdown` with a 10s deadline; pool closed after drain — `cmd/game-server/main.go:246`, `:352`, `:359` | (live) |
| `POST /auth/login` issues access + refresh | ✅ | `internal/handlers/server.go:66` | `login_test.go`, `TestLoginSuccessIssuesTokens` |
| `GET /me` accepts access token, re-reads fresh state | ✅ | `internal/handlers/server.go:257` | `getme_test.go` (5 tests) |
| `POST /auth/refresh` rotates; reuse kills family | ✅ | `internal/handlers/server.go:139` | `refresh_logout_test.go`, `TestRefreshReusedTokenRevokesFamily` |
| `POST /auth/logout` revokes | ✅ | `internal/handlers/server.go:207` | `TestLogoutRevokesPresentedFamily`, `TestLogoutWithoutTokenRevokesEverySession` |
| Public vs. secured driven by spec `security` | ✅ | `requireBearer` reads `api.BearerAuthScopes` — `internal/handlers/auth.go:19`; global `security: [bearerAuth]` with `security: []` on public ops — `api/openapi.yaml:31` | `auth_test.go` (4 tests) |

Live-verified: rotate → reuse old refresh → 401 and the rotated token is also
dead (family revoked); logout → 204 then refresh reuse → 401; invalid access
token → 401.

## Goal 2 — admin role

| Item | Status | Evidence | Tests |
|------|--------|----------|-------|
| `is_admin` column | ✅ | migration 0002 `accounts` — `internal/database/migrations.go:37` | `store` tests |
| `accountRoles` maps `is_admin` → `api.Admin` | ✅ | `internal/handlers/server.go:299` | `TestGetMeAdminRole` |
| `requireAdmin` re-reads fresh state, 403 (not 401) for non-admin | ✅ | `internal/handlers/accounts.go:29` | `TestListAccountsNonAdminIs403`, `TestGetAccountNonAdminIs403`, `TestCreateAccountNonAdminIs403`, `TestUpdateAccountNonAdminIs403` |

Live-verified: player token → `GET /accounts` → 403 `admin privileges required`.
A deactivated account's still-valid token → `/me` → 401 immediately (fresh-state
re-read), and it can no longer log in.

## Goal 3 — minimal CLI

| Item | Status | Evidence |
|------|--------|----------|
| (a) create DB, incl. in-memory migration verify | ✅ | `database create <PATH>` / `:memory:` — `cmd/game-server/main.go:84` |
| (b) create admin | ✅ | `database account create --email … --is-admin` — `cmd/game-server/main.go:145` |
| (c) reset admin password | ⚠️ naming | no dedicated `reset-password` verb; done via `database account update --email … --generate-secret` (or `--secret`) — `cmd/game-server/main.go:180` |

Live-verified against throwaway `data/claude`: created DB, created admin
`boss@example.com`, reset via `--generate-secret` and via `--secret`, then logged
in over HTTP with the new password.

Recommendation for 3c: `update --secret/--generate-secret` fully covers reset.
The only gap is discoverability. **Recommend** a thin `database account
reset-password --email …` alias that forwards to the update path (trivial). Not
required for correctness. No automated CLI tests exist (the CLI package has no
`_test.go`); the logic it calls (`store`, `auth`) is covered.

## Goal 4 — dogfood routes

| Route | Status | Evidence | Tests |
|-------|--------|----------|-------|
| `GET /healthz`, `GET /version` (public) | ✅ | `server.go:51`, `:56` | live |
| `GET /accounts`, `POST /accounts`, `GET /accounts/{id}`, `PATCH /accounts/{id}` (admin) | ✅ | `internal/handlers/accounts.go` | `accounts_test.go` (14 tests) |
| Enveloped shapes (`AccountResponse`, `ListAccountsResponse`, `AuthTokens`) | ✅ | responses in `accounts.go` / `server.go` | as above |
| `POST /admin/shutdown` (dev-only) | ✅ w/ caveat | handler `server.go:236`; `WithShutdown` wiring `main.go:288`; spec `api/openapi.yaml:237` | `shutdown_test.go` (4 tests) |

Live full dogfood loop passed: admin login → create account → login as new
account → `/me` → admin deactivates → deactivated account is locked out of both
`/me` and login.

### Shutdown route — adversarial findings

- **202 delivery / drain: correct.** `s.shutdown()` only cancels the run context;
  `srv.Shutdown` then waits for in-flight requests (including the shutdown
  request) to finish writing before the process exits. Live: client received
  `202`, log showed the shutdown request completing, then `server stopped` →
  `database closed`, process gone, port freed, exit 0. No response/flush race.
- **Context-leak-free:** `defer triggerShutdown()` guards the listener-error
  return — `cmd/game-server/main.go:283`.
- **`--development` flag unification: correct, no regression.** The single root
  flag drives both the shutdown route and the `database create` admin seed;
  seeding still works (dev admin `penny@example.com` seeds on
  `database create --development`).
- **404-before-auth ordering does NOT fully hold (⚠️).** The handler checks
  `s.shutdown == nil` → 404 *before* `requireAdmin`, but the spec marks
  `/admin/shutdown` secured (global `bearerAuth`), so `requireBearer` runs
  **before the handler**. Result:
  - Disabled route + valid admin token → 404 (as intended).
  - Disabled route + **no token → 401**, while a genuinely unknown path
    (`/admin/nope`) → 404 from the mux. So the route's existence leaks to
    unauthenticated probes in prod; it is only "invisible" to callers who
    already hold a valid token. Verified live.

## Gaps & risks

| # | Finding | Recommendation | Size | Issue |
|---|---------|----------------|------|-------|
| 1 | **Out-of-scope game stubs returned empty 200** (`return nil, nil` → strict layer writes nothing). Not a panic/500 — a misleading success. | **FIXED** this pass: stubs return `errNotImplemented`, mapped to 501 with the standard envelope; malformed body → 400 envelope; other errors → 500 without leaking internal text (`internal/handlers/wiring.go`, `server.go`). | done (trivial) | resolved |
| 2 | **Shutdown 404-masking leaks via 401** to unauthenticated probes (see above). The stated "invisible in prod" goal isn't met for no-token callers. | Either accept it (401 vs 404 is a weak signal) **or** mark the op `security: []` and do all auth inside the handler so the 404 check truly runs first, **or** don't register the route at all unless `--development`. Recommend documenting the current behavior; a real fix is small. | small | [#1](https://github.com/mdhender/ecv4/issues/1) |
| 3 | **Prod does not require a fixed JWT secret.** `resolveJWTSecret` only *warns* and generates an ephemeral secret when `ECV4_JWT_SECRET` is unset — regardless of `ECV4_ENV` — so a prod restart silently invalidates all tokens. | Fail startup when the secret is unset and `ECV4_ENV=production`. `cmd/game-server/main.go:444`. | small | [#2](https://github.com/mdhender/ecv4/issues/2) |
| 4 | **3c has no discoverable `reset-password` verb.** | Add a `database account reset-password` alias forwarding to `updateAccount`. | trivial | [#3](https://github.com/mdhender/ecv4/issues/3) |
| 5 | **No automated CLI tests.** `cmd/game-server` has no `_test.go`; CLI flows validated only manually. | Add a smoke test that shells the built binary against a temp DB, or extract the exec bodies into a testable package. | medium | [#4](https://github.com/mdhender/ecv4/issues/4) |
| 6 | **Refresh-token table has no cleanup** (accepted earlier). Rows accumulate; expired/revoked tokens are never pruned. | Note only; add a periodic prune later. | small (later) | [#5](https://github.com/mdhender/ecv4/issues/5) |
| 7 | **Dev JWT secret is ephemeral by design** when unset — every restart invalidates tokens. Intended for `make run`/air. | No change; documented in code and here. | n/a | works as intended |

## Commands run

```
go build ./...        # clean
go vet ./...          # clean
go test ./...         # all packages ok (handlers incl. new stub 501 test)
```

Live CLI (throwaway `data/claude`):

```
game-server database create :memory:                 # migrations verified
game-server database create data/claude              # created ecv4.db
game-server --db-dir data/claude database account create --email boss@example.com --secret … --is-admin
game-server --db-dir data/claude database account update --email boss@example.com --generate-secret
game-server --db-dir data/claude database account update --email boss@example.com --secret brandnewpw123
```

Live HTTP smokes (`--development`, fixed `ECV4_JWT_SECRET`):

```
GET  /healthz                      -> 200 {status:ok}
GET  /version                      -> 200 {application:0.3.0-alpha, database.schemaVersion:5}
GET  /accounts (no token)          -> 401
POST /auth/login (admin)           -> 200 access+refresh
GET  /me (admin)                   -> 200
POST /accounts (admin)             -> 201 enveloped
GET  /accounts (admin)             -> 200 list
POST /auth/login (player)          -> 200
GET  /accounts (player)            -> 403
PATCH /accounts/{id} isActive=false-> 200; deactivated /me -> 401; re-login -> 401
POST /auth/refresh (rotate)        -> 200; reuse old -> 401; rotated -> 401
POST /auth/logout                  -> 204; reuse refresh -> 401
POST /admin/shutdown (no token)    -> 401     # unknown /admin/nope -> 404 (leak, finding #2)
POST /admin/shutdown (player)      -> 403
POST /admin/shutdown (admin)       -> 202, graceful drain, exit 0, port freed
GET  /games (admin)                -> 501 not_implemented   # after fix #1 (was empty 200)
POST /accounts (bad JSON)          -> 400 bad_request envelope
```
