# AGENTS.md

## Background

This repository is an experiment for a Go game web server.
The server exposes a public REST API that players can use to write their own clients.
There is no official frontend application in this scaffold; the API contract is the product surface.

The project intentionally chooses boring, inspectable components:

- Go 1.22+ standard-library `net/http`
- OpenAPI 3.0.3 for the public contract
- `oapi-codegen` for generated Go DTOs and server stubs
- JWT Bearer authentication
- Explicit application-level authorization checks

RPC, gRPC, Connect, and GraphQL are out of scope for this experiment.
REST is preferred because players can test it with common HTTP tools and can write clients in any language without adopting an RPC toolchain.

## Mission

Build a small, understandable, contract-first game API.
Optimize for clarity, churn tolerance, and player/developer ergonomics, not for framework cleverness.

The OpenAPI file at `api/openapi.yaml` is the source of truth for the transport contract.
When the API changes, update the contract first, regenerate code, then fix the server implementation.

## Expected architecture

Keep boundaries clean:

- `api/openapi.yaml` contains the public contract.
- `internal/api` contains generated code only.
- `internal/handlers` adapts HTTP/API DTOs to application services.
- `internal/auth` verifies JWTs and puts claims on the request context.
- `internal/domain` should hold game concepts when added.
- `internal/store` should hold persistence interfaces and implementations.

Generated OpenAPI DTOs are transport types.
Do not use them as database rows or long-lived domain objects unless deliberately reviewed.

## Build and generation commands

From the repository root:

```bash
make install-tools
make generate
make test
make build
```

The generator command is configured in `api/oapi-codegen.yaml` and writes to:

```text
internal/api/openapi.gen.go
```

The initial checked-in skeleton server compiles without generated code and serves only `/healthz` and `/openapi.yaml`.
After generation, copy the handler stubs from `internal/handlers/*.stub` and wire the generated router into `cmd/game-server`.

## Response Structure

- normal application/json
- JSON:API-inspired conventions where useful
  - consistent error shape
  - consistent pagination shape
  - consistent links/meta fields
  - stable resource identifiers
  - clear relationship URLs

## Updating the API contract

Use this loop for every API change:

1. Edit `api/openapi.yaml`.
2. Keep `operationId` names stable unless deliberately making a breaking change.
3. Run `make generate`.
4. Run `go test ./...`.
5. Fix handler compile errors.
6. Add or update examples in the OpenAPI spec for player-facing workflows.
7. Commit `api/openapi.yaml` and generated code together.

Avoid undocumented endpoints.
If an endpoint exists, it belongs in OpenAPI.

## Authentication and authorization expectations

The OpenAPI security scheme documents Bearer JWT usage.
It does not enforce security by itself.

Middleware responsibilities:

- Parse `Authorization: Bearer <token>`.
- Verify token signature.
- Check expiration and relevant issuer/audience claims if used.
- Attach application claims to the request context.

Handler/service responsibilities:

- Enforce role checks, such as player, GM, or admin.
- Enforce object-level checks, such as access to a specific game, turn, faction, or order submission.
- Return consistent OpenAPI error objects.

Do not place game authorization logic inside generated code.

## Testing expectations

At minimum, add tests for:

- Authentication middleware happy path and failure cases.
- Authorization checks around game/faction/turn access.
- Order validation and submission workflows.
- OpenAPI contract stability for public player-facing endpoints.

Prefer tests that use plain `net/http/httptest` and JSON.
Keep curl examples in `docs/` or scripts when useful.

## Style expectations

- Favor boring Go over abstractions.
- Let generated code be generated; do not hand-edit `internal/api/openapi.gen.go`.
- Keep handlers thin and move game rules into services/domain packages.
- Prefer explicit errors and status codes.
- Avoid global mutable state except for temporary experiment scaffolding.
- Keep the API usable from curl.

## Dependency expectations

Do not add frameworks casually.
A dependency needs a clear purpose.
Good reasons include JWT verification, database driver, migrations, logging adapters, or test helpers.
Bad reasons include replacing a small amount of understandable stdlib code.

## Player-client expectation

Assume player clients may be written in Go, Python, JavaScript, shell scripts, spreadsheets, or unknown tools.
The contract and examples should be clear enough that a player can authenticate, list games, fetch turns, validate orders, and submit orders without reading server source code.

## Versions

Wired into the build today (`go.mod`):

- Go: `1.26.4` (per `go.mod`; needs 1.22+ for pattern routing).
- CLI/config: `github.com/peterbourgon/ff/v4` `v4.0.0-beta.1` (ff.Command tree in `cmd/game-server`; `game-server version` and `--addr` flag).
- Versioning: `github.com/maloquacious/semver` `v0.4.0`; version lives in root `version.go` (`ecv4.Version()`). `game-server version` prints `ecv4.Version().Short()` (e.g. `0.1.0-alpha`).

Planned but not yet added (do not assume these are importable until they are in `go.mod`):

- `math/rand/v2` PCG source for PRNG streams (stdlib; not yet used).
- SQLite driver: `zombiezen.com/go/sqlite` (+ `zombiezen.com/go/sqlite/sqlitemigration`) (pure Go, no CGO).
- Password hashing: `golang.org/x/crypto/bcrypt` (passwords hashed in Go, never sent as plaintext to SQLite).
