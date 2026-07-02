# earl

`earl` is a small command-line client for smoke-testing the game API. It
foolishly attempts to replace `curl`; its one redeeming quality is that it
understands the server's auth model, so it attaches a bearer token
automatically and refreshes it when it expires.

```
earl [FLAGS] <METHOD> <PATH> [BODY]
```

`PATH` is joined to `--base-url`, so `earl get /me` requests
`${EARL_BASE_URL}/me`. On success `earl` prints the HTTP status line and the
(pretty-printed) response body.

```
$ earl get /me
200 OK
{
  "user": {
    "id": 1,
    "roles": [
      "admin"
    ],
    "username": "penny@example.com"
  }
}
```

## Flags

Flags must precede the `<METHOD>` subcommand. Each also reads from an
`EARL_`-prefixed environment variable, which is typically set in
`.env.development.local` (loaded automatically — see [Environment](#environment)).

| Flag             | Env var             | Default                 | Description                                                              |
| ---------------- | ------------------- | ----------------------- | ------------------------------------------------------------------------ |
| `--base-url`     | `EARL_BASE_URL`     | `http://localhost:9987` | Base URL of the game-server.                                             |
| `--authn`        | `EARL_AUTHN`        | *(none)*                | Path to a JSON file holding the session tokens.                          |
| `--authn-email`  | `EARL_AUTHN_EMAIL`  | *(none)*                | Login username used to re-authenticate when the token can't be refreshed. |
| `--authn-secret` | `EARL_AUTHN_SECRET` | *(none)*                | Login password used to re-authenticate when the token can't be refreshed. |

## Methods

There is one subcommand per HTTP method: `get`, `post`, `put`, `patch`, and
`delete`. Each takes a `<PATH>` and an optional `<BODY>`.

```
earl get    /me
earl post   /auth/login data/alpha/payload-auth-login.json
earl delete /games/1
```

## Request body

The optional `<BODY>` argument follows `<PATH>` and its source is auto-detected:

- `-` reads the body from **stdin**.
- An argument that **names an existing file** sends that file's contents. This
  is the common case for a smoke tool — request bodies live in JSON files.
- Anything else is sent **verbatim** as a literal body, e.g. `'{"k":"v"}'`.

```
$ earl post /auth/login data/alpha/payload-auth-login.json   # file
$ earl post /auth/login '{"username":"a","password":"b"}'     # literal
$ cat body.json | earl post /auth/login -                     # stdin
```

A body is sent with `Content-Type: application/json`. There is no validation
that the bytes are actually JSON, so a mistyped file path is sent as a literal
body and the server answers with a 4xx.

## Authentication

`earl` reads the access token from the `--authn` JSON file and attaches it as
`Authorization: Bearer <token>`. The file matches the server's token response
(see `data/alpha/authn.json`):

```json
{
  "accessToken": "…",
  "expiresInSeconds": 900,
  "refreshToken": "…",
  "tokenType": "Bearer"
}
```

**No authn file.** If `--authn` is unset or the file does not exist, `earl`
sends the request **without** a bearer token. This is what public endpoints
like `/auth/login` need, so you can bootstrap a session:

```
$ earl post /auth/login data/alpha/payload-auth-login.json > data/alpha/authn.json
```

A file that exists but is malformed (or has no `accessToken`) is a hard error.

**Refresh on 401.** When a token *was* sent and the server answers `401`,
`earl` re-authenticates, rewrites the `--authn` file with the new tokens (owner-
only permissions, since it holds bearer tokens), and retries the request once:

1. It exchanges the stored `refreshToken` at `POST /auth/refresh`.
2. If there is no refresh token or the refresh is rejected, it logs in fresh at
   `POST /auth/login` using `--authn-email` / `--authn-secret`.
3. If both fail, it prints a diagnostic to stderr and reports the original
   `401` as-is.

```
$ earl get /me
earl: access token rejected; re-authenticated via refresh
200 OK
…
```

The `/auth/*` endpoints authenticate via the request body and ignore the bearer
token, so `earl` never reacts to *their* 401s by refreshing — a `401` from
`/auth/login` is reported directly as bad credentials.

## Environment

Before parsing flags, `earl` loads `.env` files (via `internal/dotenv`) so the
`EARL_*` variables can come from a file. `EARL_ENV` selects which files load and
defaults to `development`; it is read from the process environment, not a flag.

A typical `.env.development.local`:

```
EARL_BASE_URL=http://localhost:9987/
EARL_AUTHN=data/alpha/authn.json
EARL_AUTHN_EMAIL=admin@example.com
EARL_AUTHN_SECRET=your-dev-admin-secret
```

These must match the development admin seeded into the database (see
`ECV4_DEVELOPMENT_ADMIN_EMAIL` / `ECV4_DEVELOPMENT_ADMIN_SECRET`). Use a
throwaway secret — `.env.development.local` is git-ignored, but never commit a
real credential to a tracked file.

With that in place, no flags are needed:

```
$ go run ./cmd/earl get /me
```
