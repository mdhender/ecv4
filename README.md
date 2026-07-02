# ecv4 — Go Game Server

A contract-first REST API for an experimental Go game web server. Players use the
API to write their own clients, so **the API contract is the product** — there is
no official frontend. `api/openapi.yaml` is the source of truth for the transport
contract; the Go server is generated from it and implements the behavior behind it.

The stack is deliberately boring and inspectable:

- Go standard-library `net/http` (Go 1.22+ pattern routing)
- OpenAPI 3.0.3 as the public API contract
- [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) for generated DTOs and a strict server interface
- JWT Bearer authentication, with application-level authorization in Go
- SQLite (pure-Go `zombiezen.com/go/sqlite`) with append-only migrations

RPC, gRPC, Connect, and GraphQL are intentionally out of scope. REST is preferred
because players can exercise it with common HTTP tools and write clients in any
language — Go, Python, JavaScript, shell, a spreadsheet — without adopting an RPC
toolchain. The contract and its examples should be clear enough to authenticate,
list games, fetch turns, validate orders, and submit orders without reading server
source.

Agents working in this repo should also read [`CLAUDE.md`](CLAUDE.md), which covers
the architecture internals and coding conventions in more depth.

## Layout

```text
api/openapi.yaml         Public API contract (source of truth)
api/oapi-codegen.yaml    oapi-codegen configuration
internal/api/            Generated code only (do not hand-edit)
internal/handlers/       Adapts HTTP/API DTOs to services; implements the strict server
internal/auth/           JWT issue/verify + bearer middleware
internal/store/          Typed data-access layer over the SQLite pool
internal/database/       Database create/open + append-only migrations
cmd/game-server/         Server entry point and CLI
docs/                    curl examples, MVP status, background notes
```

## Build and run

```bash
make install-tools   # one-time: go install oapi-codegen
make generate        # regenerate internal/api/openapi.gen.go from the spec
make test            # go test ./...
make build           # build to bin/game-server
make run             # run the server
```

The server listens on `:9987` by default. For a live-rebuild dev loop, run `air`
(config in `.air.toml`), which also serves on `:9987`.

```bash
curl http://localhost:9987/healthz
curl http://localhost:9987/openapi.yaml
```

See `docs/curl-examples.md` for login and authenticated request examples.

## CLI

`cmd/game-server` is an [`ff`](https://github.com/peterbourgon/ff)-based command
tree. With no subcommand it runs the server. Subcommands:

```bash
game-server version                          # print the version
game-server database create <PATH>           # create ecv4.db in an existing dir (or :memory: to verify migrations)
game-server database account create --email <e> [--is-admin] [--secret <s>]
game-server database account update --email <e> [--is-active[=false]] [--is-admin[=false]] [--secret <s> | --generate-secret]
game-server database account reset-password --email <e> [--secret <s> | --generate-secret]   # generates one if omitted
game-server database account list            # print all accounts (id, active, admin, email); read-only, no server needed
```

The shared `--development` flag enables the `POST /admin/shutdown` route when
serving and seeds a known admin when used with `database create`.

## Configuration

Config comes from flags or `ECV4_`-prefixed environment variables. `.env` files
load before flags are parsed, selected by `ECV4_ENV` (default `development`). Key
variables:

- `ECV4_DB_DIR` — directory holding `ecv4.db`
- `ECV4_JWT_SECRET` — HMAC signing key, **must be ≥32 bytes** for HS256. If unset, a
  random ephemeral secret is generated and all tokens are invalidated on restart —
  set a stable value in production.
- `ECV4_DEVELOPMENT`, `ECV4_DEVELOPMENT_ADMIN_EMAIL`, `ECV4_DEVELOPMENT_ADMIN_SECRET`
  — control development mode and the optional seeded admin.

## Development loop

The OpenAPI file is edited first, then code is regenerated to match:

```bash
$EDITOR api/openapi.yaml     # 1. change the contract (keep operationId names stable)
make generate                # 2. regenerate transport code
go test ./...                # 3. fix handler compile errors and implement behavior
                             # 4. commit the contract and generated code together
```

Committing generated code alongside the spec keeps API diffs easy to review.

## Auth model

The contract declares Bearer JWT auth (`Authorization: Bearer <token>`), enforced
by middleware that verifies the token and attaches claims to the request context.
The spec's `security` requirements are the single source of truth for which routes
need a token; public routes opt out with `security: []`.

Authentication proves *who* you are; **authorization is object-level and lives in
handlers/services**, not in generated code or the middleware:

- Can this user see this game?
- Can this user submit orders for this faction?
- Can this GM close this turn?

Access tokens are short-lived (15m); refresh tokens (24h) are persisted, rotated on
`/auth/refresh`, and revoked on `/auth/logout`. Presenting an already-rotated
refresh token revokes the whole token family as a theft signal.

## License

See [LICENSE](LICENSE). Copyright © 2026 Michael D Henderson.
