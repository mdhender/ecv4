# Engine Contract

*Reference — the interface between the application and the game engine
(`internal/ec`). For the reasoning behind the design, see the essay
[The Stream Is the Engine](../explanations/the-stream-is-the-engine.md); for the
randomness model, [`seed-derivation.md`](seed-derivation.md).*

## The app/engine line

The **app** (transport handlers, auth, `store`, `cli`) owns authentication,
authorization, and all I/O. The **engine** (`internal/ec`) is a **pure functional
core**: values in, values out. It has no `ctx`, no store, no SQL, and no database
awareness. It never imports `internal/api` or `internal/store`.

Three type-worlds meet only in the handler:

```
api.*  (wire, generated)  ⇄  [handler translates]  ⇄  ec.* (records, state)
```

An **engine handler** authorizes the caller, then invokes the engine through the
**runner** (below). The engine assumes an authorized caller and performs no auth.

## The stream is the engine

The engine is organized around one **append-only event stream** per game, not
around mutable tables. The stream holds three kinds of record:

- **Intent** — something to be attempted (a parsed order, or work the simulation
  discovers). Accepted but unresolved; it may never resolve.
- **Fact** — something that happened, past tense. Facts are the source of truth.
- **Derived intent** — new intent the simulation emits during resolution (e.g. a
  forced retreat). It defers resolution to a later phase; it does not nest.

**State is the fold of facts alone.** Intent and derived intent sit in the log but
do not contribute to state. Records are never modified or deleted; every record
has a unique, monotonically increasing id, and order is that id.

A fourth, administrative flavor — **control** — records engine runs (below) and
turn boundaries; a state fold ignores it.

## Engines are uniform stream readers

Every engine has the *same shape*, whether it parses orders or resolves combat:
read the records past its own cursor, resolve them against a chosen snapshot of
state, and emit new records. Order parsing is itself modeled this way (see
"Phases, order, and the GM"), so parsers and resolvers are symmetric.

The engine side of that shape is a pure function:

```go
// internal/ec — pure. No ctx, no store, no I/O.
func (p Phase) Resolve(in []Record, asOf SnapFunc, b Bounds, seed Seed) (facts []Fact, derived []Intent, err error)
```

- `in` — the records past this engine's input cursor (its new work).
- `asOf` — a **pure** snapshot function, `func(EventID) State` (below).
- `b` — well-known boundary ids: `Bounds{TurnStart, PhaseStart}`.
- `seed` — the game's master seed, for seed-derived randomness.

The engine chooses, per its own rules, whether to resolve `in` one record at a
time (strict) or as a batch against a single snapshot (simultaneous). The stream
does not encode that choice.

## The runner (thin shared shell)

One generic **runner** drives every engine. There is no per-command shell and no
relational load/diff/apply — a fact *is* the update, so there is nothing to
detect or write back beyond appending records.

```go
// app side — the ONE loop for every engine. Owns stream I/O + the transaction.
func Run(ctx, game, engine) error {
    in         := stream.after(cursor[engine])      // records past this engine's cursor
    asOf       := snapshotter(game)                 // pure fold over in-memory facts (below)
    facts, drv := engine.Resolve(in, asOf, bounds, seed)
    stream.append(controlRunRecord(engine), facts, drv)   // append-only
    cursor[engine] = stream.maxID()                 // advance this engine's cursor
    // all in one transaction
}
```

Each engine invocation is one transaction: read past the cursor, resolve, append
the emitted records, advance the cursor. Because the engine is pure and the runner
generic, the runner never learns what any engine does.

## Two positions: input cursor and as-of snapshot

An engine run carries two independent positions:

- **Input cursor** — *which records it consumes*: everything past its own
  position. Per-engine, per-game, and **persisted** (`ec_engine_cursor`). This is
  what makes every engine uniformly re-runnable: a second run only ever sees
  records newer than the first, never reprocessing old work.
- **As-of snapshot** — *which fold of facts it resolves against*. A **frozen**
  snapshot the phase chooses: `TurnStart` (simultaneity — everyone acts from where
  the turn began), `PhaseStart` (must see earlier phases' facts, e.g. load-goods
  seeing a wagon an earlier phase bought), or any other id. Orthogonal to the
  input cursor.

The runner hands the phase a pure `asOf` handle plus the boundary ids and stays
ignorant of which the phase picks:

```go
// runner builds a PURE as-of function over facts already loaded into memory.
asOf := func(id EventID) State { return ec.Fold(baseline, factsUpTo(id)) } // no I/O
```

`asOf` is a pure query over values — the engine cannot tell (and does not care)
whether it is backed by a database or a slice, so purity and testability hold. Its
`baseline` is the latest persisted snapshot so the runner need not fold from
genesis; a genesis baseline is fine until games get long.

## Randomness

Deterministic and replayable; part of the pure core.

```go
type Seed struct{ Hi, Lo uint64 }
func NewSeed() Seed
func (m Seed) Derive(path ...Leg) *rand.PCG   // one independent stream per path
```

The master `Seed` is assigned once at initialization and never mutated. Streams
are keyed by a semantic `path` whose `phase` leg lines up with the phase engines
here (general pattern `turn → phase → entity → purpose`).

**Outcomes are recorded as facts.** A die roll's result lands in a `Fact`, so the
state fold is independent of engine or seed version — the log stays true even when
engine code later changes. The seed's job is to make *re-resolution* reproducible
(the same seed regenerates the same facts), not to reconstruct state (that is the
fact fold). See [`seed-derivation.md`](seed-derivation.md).

## Phases, order, and the GM

- **The GM owns phase ordering, at runtime.** Phase order is not static config; it
  is the GM's sequence of engine invocations, and she may run A, then B, then A
  again. Re-running an engine drains only records past its cursor.
- **Each engine invocation is a control record** carrying its from/to cursor
  range, so the turn's construction is fully auditable and replayable ("she ran
  EEA, then EEB, then EEA again").
- **Order parsing is split per phase.** An order engine for a phase (`OE_x`)
  extracts *its* phase's orders from the raw submission and emits intent; the
  matching execution engine (`EE_x`) resolves that intent into facts. Interleaving
  `OE_x`/`EE_x` puts each phase's intent at the right stream position, aligned with
  the facts it should see.
- **Players no longer hand-sort orders.** Because each `OE_x` cherry-picks its
  phase's orders regardless of submission order, the engine sorts orders into phase
  order for the player — removing a long-standing PBEM pain. This requires every
  order to map to a known phase. (Within-phase ordering is a residual, smaller
  problem.)

## Store

All owned by the app; the engine touches none of it.

### Event log

Append-only, `ec_`-prefixed, `STRICT`. Order is the global monotonic `id`; `turn`
is a label (0 = genesis). Canonical DDL lands in the migration when built.

```sql
CREATE TABLE ec_events (
    id         INTEGER PRIMARY KEY,          -- global monotonic; total order + reader cursor
    game_id    INTEGER NOT NULL REFERENCES games(id),
    turn       INTEGER NOT NULL,             -- label; 0 = genesis
    kind       TEXT    NOT NULL              -- intent | fact | derived | control
                 CHECK (kind IN ('intent','fact','derived','control')),
    type       TEXT    NOT NULL,             -- discriminator: "arrived", "attacked", "engine_run", ...
    version    INTEGER NOT NULL,             -- event schema version (for upcasting)
    payload    TEXT    NOT NULL,             -- JSON; pure domain data
    request_id TEXT,                         -- nullable; transport provenance (audit only)
    created_at INTEGER NOT NULL              -- wall-clock (audit only, never ordering)
) STRICT;

CREATE TRIGGER ec_events_no_update BEFORE UPDATE ON ec_events
BEGIN SELECT RAISE(ABORT, 'ec_events is append-only'); END;

CREATE TRIGGER ec_events_no_delete BEFORE DELETE ON ec_events
BEGIN SELECT RAISE(ABORT, 'ec_events is append-only'); END;
```

- `payload` is pure domain JSON; state = fold of `kind='fact'`. A new event type
  needs no migration. Only code replays the log; queries hit the projection.
- `kind` separates work (`intent`/`derived`), truth (`fact`), and administration
  (`control` — engine runs, turn boundaries), so a state fold cheaply ignores
  control.
- `request_id` and `created_at` are audit metadata, **outside the determinism
  guarantee** (they differ on replay, and never feed ordering or seed derivation).
- Append-only is structural, via the triggers. **There is no rewind.**

### Engine cursors

The one piece of mutable, non-truth state: each engine's high-water position per
game. Not derivable from facts (it is reader position).

```sql
CREATE TABLE ec_engine_cursor (
    game_id INTEGER NOT NULL REFERENCES games(id),
    engine  TEXT    NOT NULL,                -- "OEA", "EEA", ...
    last_id INTEGER NOT NULL,                -- highest event id this engine has consumed
    PRIMARY KEY (game_id, engine)
) STRICT;
```

### Projections and snapshots

- **Projection** — the queryable current-state read model the reports and (via
  `asOf`) the engine consume; always reproducible from the fact fold. Fog of war
  is a projection concern (per-player reports derived through visibility rules),
  not a log column.
- **Snapshot** — a `(pointer, projected state)` pair: state as of an id, cached so
  the fold need not start from genesis. Never authoritative. Cadence deferred
  (likely one per turn); a genesis baseline suffices for now.

## Command preconditions and error ownership

| Gate | Owner | Result |
|------|-------|--------|
| Authn / authz (active GM, game visibility) | Handler | 401 / 403 / 404 |
| App-owned lifecycle precondition (game must be `draft`) | Handler | 409 |
| Engine integrity (initialize-once) | Projection constraint (DB) | Typed error → 409 |
| Domain defaults (draw a seed when none supplied) | Engine | — |

## Commands

### `InitializeGame` (issues #76, #77)

Initialization is the game's **genesis** engine run (turn 0 setup). It assigns the
master seed **and generates the initial map** (#77), emitting the setup facts the
projection folds into the starting world:

- Input: an optional seed (`nil` ⇒ the engine draws one via `NewSeed`).
- Emits (at minimum) `Fact(GameInitialized{Seed})`, plus initial-map facts (TBD in
  #77).
- The `GameInitialized` fact projects into `ec_game` (primary key `game_id`),
  which enforces **initialize-once**: a second genesis run's projection violates
  the PK, rolls back the whole transaction, and surfaces as `ErrAlreadyInitialized`
  → 409.
- The `draft`-only precondition is enforced by the handler; the engine never reads
  the `games` table.

## Open decisions

- **World projection shape** — serialized `State` blob vs normalized tables.
  Settled by the first real world model (#77).
- **Snapshot cadence** — not built; likely per-turn.
- **Event schema versioning** — events are truth forever; changed shapes need
  versioned events + upcasting. Convention TBD.
- **Control kind vs fact type** — whether engine-run/turn-boundary records are a
  distinct `control` kind (assumed here) or facts with a reserved type.
- **Within-phase ordering** — phase-partitioned parsing sorts *across* phases;
  intra-phase sequence dependencies are unaddressed.
- **Essay open questions** — encounter-check emission, intent that outlives its
  turn, and whether derived intent can be rejected (see the essay).
