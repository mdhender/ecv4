# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A contract-first REST game server in Go. The OpenAPI file `api/openapi.yaml` is
the source of truth for the transport contract; `oapi-codegen` generates DTOs and
a strict server interface from it, and handlers implement that interface. There is
no frontend — the API is the product, and player clients may be written in any
language, so it must stay usable from curl. `README.md` covers the mission and
developer orientation; this file is the architecture + conventions reference.

## Commands

```bash
make generate        # regenerate internal/api/openapi.gen.go from api/openapi.yaml
make test            # go test ./...
make build           # build to bin/game-server
make run             # go run ./cmd/game-server (serves the API)
make install-tools   # go install oapi-codegen (needed before first `make generate`)

air                  # live-rebuild dev loop; serves on :9987 (see .air.toml)
go test ./internal/handlers/ -run TestLoginSuccessIssuesTokens   # single test
```

Default listen address is `:9987` (`internal/config`). The README's `:8080` and
`make run`-only description are stale — the server is a full CLI now (below), and
`data/alpha` / `data/claude` are disposable dev databases.

## The contract-first loop

Every API change follows this order (do not skip or reorder):

1. Edit `api/openapi.yaml` (keep `operationId`s stable unless intentionally breaking).
2. `make generate` — rewrites `internal/api/openapi.gen.go`. **Never hand-edit generated code.**
3. `go test ./...` — the compiler is the guide: `var _ api.StrictServerInterface = (*Server)(nil)` in `internal/handlers/server.go` breaks if a handler signature drifts from the regenerated interface.
4. Implement/fix handlers, commit contract + generated code together.

## Architecture / layering

Request flow: `cmd/game-server` builds the mux → `handlers.NewHTTPHandler` wraps
the strict server with oapi-codegen's router + the `requireBearer` middleware →
handlers call the store → store runs SQL against the pool from `internal/database`.

- **`internal/api`** — generated only. Transport DTOs; never use as domain/DB types.
- **`internal/handlers`** — implements the generated `StrictServerInterface`. Thin adapters: auth/account handlers are real; the game handlers (`ListGames`, `GetTurn`, `SubmitOrders`, …) are deliberate stubs returning `errNotImplemented`, which `NewHTTPHandler`'s strict `ResponseErrorHandlerFunc` maps to **501** (not an empty 200). Malformed bodies → 400; other errors → 500 with the message hidden.
- **`internal/store`** — typed query methods over a `sqlitemigration.Pool`. The only place SQL lives outside migrations. Returns `ErrNotFound` / `ErrConflict`; the hashed secret never leaves this layer.
- **`internal/database`** — owns creation + migration. `Create` is the **only** function allowed to bring a DB file into existence; everything else uses `Open`, which refuses to create and runs pending migrations on every open (this is the upgrade path). `CreateMemory` / `CreateSharedMemory` back tests. Foreign keys are a per-connection PRAGMA set via `PrepareConn` on every pooled connection.
- **`internal/auth`** — `TokenService` issues/verifies HS256 JWTs. Access tokens (15m) carry identity+roles; refresh tokens (24h) carry a distinct audience + a family id, are persisted, rotated on `/auth/refresh`, and revoked on `/auth/logout`. **Presenting an already-rotated refresh token revokes the whole family** (theft signal). Use `WithClock` to inject time in tests.
- **`internal/httputil`** — request logging, health, the raw-spec handler, and the shared JSON error envelope (`{code, message, requestId?}`).

## Auth model (spec-driven)

`api/openapi.yaml` declares a global `security: [bearerAuth]`; public operations
opt out with `security: []` (`/healthz`, `/version`, `/auth/login`, `/auth/refresh`).
`requireBearer` (`internal/handlers/auth.go`) reads the `api.BearerAuthScopes`
context marker the generated wrappers set only for secured operations — so the spec
is the single source of truth for which routes need a token; there is no separate
allow-list. Role/object-level checks (admin, GM, faction ownership) live in
handlers/services, never in generated code. Handlers re-read fresh account state
from the store rather than trusting token claims (an account may have been
deactivated since issue).

## Migrations

`internal/database/migrations.go` holds an ordered, **append-only** `[]string`.
Once a migration ships it must never be edited or reordered — `sqlitemigration`
tracks how many have run. `application_id` is fixed at `0x65637634` ("ecv4") and
opening a file with a mismatched id is rejected. Tables are `STRICT`; accounts and
games are never deleted (toggle `is_active`).

## CLI (`cmd/game-server`, built on `peterbourgon/ff/v4`)

Root command with no subcommand runs the server. Subcommands:
`version`, `database create <PATH>` (PATH is an existing dir, or `:memory:` to just
verify migrations), `database account create`, `database account update`.
The shared `--development` flag enables the `POST /admin/shutdown` route when
serving and seeds a known admin with `database create`. Config comes from flags
or `ECV4_`-prefixed env vars.

## Environment / config

`.env` files load before flags are parsed, selected by `ECV4_ENV` (default
`development`); `internal/dotenv` handles precedence. Notable vars: `ECV4_DB_DIR`,
`ECV4_JWT_SECRET` (must be ≥32 bytes for HS256; if unset a random ephemeral secret
is generated and all tokens die on restart — set it in production),
`ECV4_DEVELOPMENT`, and `ECV4_DEVELOPMENT_ADMIN_EMAIL` / `ECV4_DEVELOPMENT_ADMIN_SECRET`
(used only by the `--development` admin seed).

## Testing conventions

Prefer `net/http/httptest` + JSON. Existing handler tests build a server over a
`CreateSharedMemory` DB and inject a fixed clock via `auth.WithClock`. Cover auth
middleware happy/fail paths, refresh rotation + family revocation, and admin gating.

## Style & conventions

- Favor boring, inspectable Go over abstractions. Keep handlers thin; push game
  rules into service/domain packages. Prefer explicit errors and status codes.
  Avoid global mutable state except for temporary experiment scaffolding.
- Let generated code be generated — never hand-edit `internal/api/openapi.gen.go`.
- Keep game authorization logic out of generated code (it lives in handlers/services).
- Response bodies are `application/json` using JSON:API-inspired conventions where
  useful: a consistent error shape (the `httputil` envelope), consistent pagination
  and `links`/`meta` fields, stable resource identifiers, and clear relationship URLs.
- Don't add frameworks or dependencies casually — a new dependency needs a clear
  purpose (JWT, DB driver, migrations, logging, test helpers). Replacing a small
  amount of understandable stdlib code is not one. RPC/gRPC/Connect/GraphQL are out
  of scope: REST keeps the API testable with common HTTP tools from any language.
- Every endpoint belongs in `api/openapi.yaml`; avoid undocumented routes. Add or
  update spec examples for player-facing workflows.
