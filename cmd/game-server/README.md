# game-server

The `game-server` command serves the experimental game API and manages the
game database.

```
game-server [FLAGS] <SUBCOMMAND>
```

With no subcommand, `game-server` starts the HTTP server.

## Global flags

| Flag     | Default  | Description          |
| -------- | -------- | -------------------- |
| `--addr` | `:8080`  | HTTP listen address. |

## Running the server

```
game-server [--addr :8080]
```

Starts the skeleton HTTP server and blocks until interrupted (`SIGINT` /
`SIGTERM`). Exposes `GET /healthz` and `GET /openapi.yaml`.

## Subcommands

### `version`

```
game-server version
```

Prints the build version and exits.

### `database`

```
game-server database <SUBCOMMAND>
```

Manages the game database. The database is a single SQLite file named
`ecv4.db`. Callers always supply the *directory* that contains it; the file
name is fixed and never chosen by the caller.

#### `database create`

```
game-server database create [--development] <PATH>
```

Creates a new `ecv4.db` database inside `PATH` and applies the initial
migrations. `PATH` is required and positional; there is no default.

Rules enforced by the command:

- `PATH` must already exist and be a directory. The command **never** creates
  a directory.
- The database file must not already exist. The command **never** overwrites
  or reuses an existing database.

`create` is the only command permitted to bring a database into existence;
every other entry point opens an existing database.

```
$ game-server database create data/alpha
created data/alpha/ecv4.db
```

**Seeding a development admin.** Pass `--development` to seed a known, active
admin account into the new database so local smoke tests have a reliable login.
The seed only happens when all of the following hold; otherwise it is skipped
with an explanatory note (the database is still created):

- `ECV4_ENV` is `development` (the default when unset).
- `ECV4_DEVELOPMENT_ADMIN_EMAIL` and `ECV4_DEVELOPMENT_ADMIN_SECRET` are both
  set (typically in `.env.development.local`).
- `PATH` is a real directory, not `:memory:` (the in-memory database is not
  persisted, so there is nothing to seed).

```
$ game-server database create --development data/alpha
created data/alpha/ecv4.db
seeding development admin account...
created account 1 penny@example.com (is_active=true, is_admin=true)
```

**In-memory smoke test.** A `PATH` of `:memory:` builds an ephemeral in-memory
database, applies the migrations, and discards it. Nothing is written to disk;
this only verifies that the migrations apply cleanly.

```
$ game-server database create :memory:
verified migrations against an in-memory database (nothing persisted)
```

For tests that need a *usable* in-memory handle, call the package functions
directly instead of the CLI:

- `database.CreateMemory(ctx)` — a private, single-connection in-memory
  database. The returned connection is the only handle; closing it destroys
  the database.
- `database.CreateSharedMemory(ctx, name)` — a shared in-memory database
  backed by a connection pool whose connections all see the same data. An
  empty `name` is isolated per call; a non-empty `name` is shared by callers
  using the same name. The database lives only while the pool is open.

## The `data/` directory

`data/` is a convenience location for local databases. Its contents are
git-ignored (see `data/.gitignore`) so test databases such as `data/alpha/`
are never committed; only the `.gitignore` placeholder keeps the directory in
the tree.
