# API Background Recommendation

This file captures the recommendation that led to this scaffold.

---

My recommendation: **OpenAPI-first + `oapi-codegen` + Go 1.22+ `net/http`**, with JWT handled as explicit middleware and authorization checks kept in your application layer.

That gives you the thing you actually want:

```text
openapi.yaml
   ↓
generated Go DTOs
generated handler interface/stubs
generated request/response wrappers
public API contract for players
Swagger/Redoc docs for humans
optional generated clients later
```

## Recommendation

Use this stack:

```text
OpenAPI 3.0.3 or 3.1
+ oapi-codegen
+ stdlib net/http server generation
+ JWT Bearer auth documented in OpenAPI
+ authn middleware before generated handlers
+ authz checks inside handlers/service layer
```

I would start with **`oapi-codegen`**, not `ogen`, for this experiment.
`oapi-codegen` is specifically designed to convert OpenAPI specs into Go code for server implementations, clients, and HTTP models, with a bias toward idiomatic, relatively simple generated Go.
It also added support for a pure Go 1.22+ `net/http` server target, which fits your “Go 1.22+ / likely 1.26+” direction very well.

The stdlib router is now a more reasonable default than it used to be.
Go 1.22 enhanced `net/http.ServeMux` with method-aware patterns and wildcards like `POST /items/create` and `/items/{id}`.
That means you can avoid committing to `chi`, Echo, Gin, etc. until you have a clear reason.

## I agree with REST here, with one caveat

I would not say REST is inherently more testable than RPC.
A protobuf/Connect API can also be very testable.

But for **your stated goal** — “players may write their own clients” — REST + OpenAPI is the better choice.
The OpenAPI Specification is explicitly intended for describing HTTP APIs in a way that supports documentation, code generation, clients, servers, and automated tests.

That matters because your players can use:

```text
curl
HTTPie
Postman
Bruno
Insomnia
Swagger UI
generated clients
hand-written clients
plain old HTTP libraries
```

For a public game API, that accessibility matters more than theoretical purity.

## Why not `ogen` as the first choice?

`ogen` is very interesting.
It generates statically typed clients and servers, parses path/header/query parameters into typed structures, generates validation from the spec, avoids reflection, and uses generated JSON encoding.

That is appealing.
But for an experiment with churn, I would start with the simpler-feeling toolchain.

My read:

```text
oapi-codegen = easier default, simpler generated code, good Go integration
ogen         = stricter, more generated machinery, potentially stronger validation story
```

I would prototype with `oapi-codegen`.
If you later feel you need stronger generated validation, stricter optional/nullable handling, or generated high-performance JSON handling, then try `ogen` against the same `openapi.yaml`.

## Suggested project layout

```text
game-server/
  api/
    openapi.yaml
    oapi-codegen.yaml

  internal/
    api/
      openapi.gen.go        # generated; do not edit
    auth/
      jwt.go
      middleware.go
      claims.go
    handlers/
      server.go             # implements generated interface
      auth.go
      games.go
      turns.go
      orders.go
    domain/
      game.go
      turn.go
      orders.go
    store/
      users.go
      games.go

  cmd/
    game-server/
      main.go
```

Keep generated DTOs separate from domain objects.
It is tempting to use generated OpenAPI structs everywhere, but I would avoid that.
Treat them as **transport DTOs**.

```text
HTTP JSON DTOs  ≠  domain model  ≠  database row
```

That separation will help when the API churns.

## Suggested generation config

Something like this:

```yaml
package: api

generate:
  models: true
  embedded-spec: true
  strict-server: true
  std-http-server: true

output: internal/api/openapi.gen.go
```

Then add a `generate.go` file:

```go
package api

//go:generate go tool oapi-codegen -config ../../api/oapi-codegen.yaml ../../api/openapi.yaml
```

For Go 1.24+, `oapi-codegen` recommends using Go’s tool dependency support with `go get -tool`, then invoking it via `go tool`, which is a nice fit if you expect other contributors to regenerate code consistently.

## JWT in the OpenAPI contract

In the OpenAPI document, define Bearer JWT auth like this:

```yaml
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT

security:
  - bearerAuth: []
```

OpenAPI 3 describes bearer authentication with `type: http` and `scheme: bearer`, and the token is sent as:

```http
Authorization: Bearer <token>
```

Then override it per operation where needed:

```yaml
paths:
  /auth/login:
    post:
      security: []
      operationId: login
      # ...

  /games:
    get:
      security:
        - bearerAuth: []
      operationId: listGames
      # ...
```

Important: do **not** expect OpenAPI or the generator to solve authorization.
Use the OpenAPI security section to document which endpoints require credentials.
Use your Go middleware and service logic to enforce it.

For example:

```text
JWT middleware:
  - verify signature
  - check expiration
  - parse subject/user id
  - parse roles/scopes
  - attach claims to context

Handler/service layer:
  - can this user see this game?
  - can this user submit orders for this faction?
  - can this GM close this turn?
  - can this admin create a season?
```

That matters especially for game APIs because most interesting authorization is object-level: player X may access game 12, but only faction 7, and only until the turn deadline.

## Contract style I would use

Use operation IDs aggressively and make them boring:

```yaml
operationId: login
operationId: refreshToken
operationId: listGames
operationId: createGame
operationId: getGame
operationId: listTurns
operationId: getTurn
operationId: submitOrders
operationId: validateOrders
operationId: getOrderSubmission
```

These names become generated Go method names, so stable naming helps.

For resources, I would start with:

```text
POST   /auth/login
POST   /auth/refresh
POST   /auth/logout

GET    /me

GET    /games
POST   /games
GET    /games/{gameId}

GET    /games/{gameId}/turns
GET    /games/{gameId}/turns/{turnId}

POST   /games/{gameId}/turns/{turnId}/orders:validate
POST   /games/{gameId}/turns/{turnId}/orders:submit
GET    /games/{gameId}/turns/{turnId}/orders/{submissionId}
```

The `:validate` / `:submit` suffix is not pure REST noun style, but it is common and practical for command-like actions.
For game workflows, I would prefer clarity over pretending every action is a CRUD resource.

## How I would handle churn

Because you expect churn, I would put guardrails around regeneration:

```text
1. Edit api/openapi.yaml
2. Run make generate
3. Fix compile errors in handlers
4. Run contract tests
5. Commit openapi.yaml and generated code together
```

Example `Makefile`:

```make
generate:
	go generate ./internal/api

test:
	go test ./...

check: generate test
	git diff --exit-code
```

During heavy churn, committing generated code is useful because players and collaborators can inspect diffs without needing your exact generator installed.
Later, you can revisit whether generated files should stay committed.

## Public player-facing docs

Serve the spec directly:

```text
GET /openapi.yaml
GET /docs
```

For `/docs`, use Swagger UI, Redoc, Scalar, or Stoplight Elements.
Since you are not delivering a frontend app, this is just developer documentation, not “the game UI.”

Also include examples in the OpenAPI spec.
Your players will thank you.

```yaml
examples:
  submitOrders:
    value:
      orders: |
        UNIT 101 MOVE N
        UNIT 102 SCOUT E
```

## My suggested decision

Use:

```text
OpenAPI 3.0.3 initially
+ oapi-codegen
+ std-http-server
+ strict-server
+ embedded-spec
+ custom JWT middleware
+ explicit object-level authz in handlers/services
```

I would only switch from this if one of these becomes painful:

```text
Need stronger generated validation       → try ogen
Hate writing YAML                        → try Goa or TypeSpec
Need generated multi-language SDKs       → consider OpenAPI Generator or Speakeasy later
Need flexible query API                  → GraphQL, but not for this experiment
```

For this game server experiment, the boring choice is the right choice: **OpenAPI-first with `oapi-codegen` and stdlib `net/http`**.
