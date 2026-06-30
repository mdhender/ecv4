# Go Game Server Scaffold

Contract-first REST scaffold for an experimental Go game web server.

The intended stack is deliberately boring:

- Go 1.22+ standard-library `net/http`
- OpenAPI 3.0.3 as the public API contract
- `oapi-codegen` for generated DTOs and server stubs
- JWT Bearer authentication documented in OpenAPI and enforced in Go middleware

## Contents

```text
api/openapi.yaml             Public API contract
api/oapi-codegen.yaml        oapi-codegen configuration
internal/api/                Generated Go package destination
internal/auth/               JWT middleware placeholders
internal/handlers/*.stub     Handler implementation starter files
cmd/game-server/             Small skeleton server
AGENTS.md                    Build/test/update expectations for AI/code agents
api-background.md            Background recommendation that led to this scaffold
```

## First run

```bash
make run
curl http://localhost:8080/healthz
curl http://localhost:8080/openapi.yaml
```

The initial server only serves `/healthz` and `/openapi.yaml`.
After generating code and implementing handlers, replace the skeleton mux in `cmd/game-server` with the generated oapi-codegen router.

## Generate DTOs and server stubs

Install the generator:

```bash
make install-tools
```

Generate the Go code:

```bash
make generate
```

This writes:

```text
internal/api/openapi.gen.go
```

Then copy the starter handler examples:

```bash
cp internal/handlers/server.go.stub internal/handlers/server.go
cp internal/handlers/wiring.go.stub internal/handlers/wiring.go
```

Let the compiler guide the exact generated type names:

```bash
go test ./...
```

## Development loop

```bash
# 1. Edit API contract
$EDITOR api/openapi.yaml

# 2. Regenerate transport code
make generate

# 3. Fix handler compile errors and implement behavior
go test ./...

# 4. Commit the contract and generated code together
```

## Auth model

The OpenAPI contract declares Bearer JWT auth:

```http
Authorization: Bearer <token>
```

The scaffold does not choose a production JWT package or key strategy.
Add that inside `internal/auth` after deciding how tokens are signed and rotated.

Authorization is expected to be object-level and should live in handlers/services:

- Can this user see this game?
- Can this user submit orders for this faction?
- Can this GM close this turn?

Do not rely on OpenAPI generation to enforce those rules.

## Notes

- The OpenAPI contract uses `operationId` values that should become stable Go method names.
- DTOs are transport objects. Do not let generated API structs become your persistence or domain model by accident.
- During heavy churn, committing generated code is helpful because API diffs are easier to review.
