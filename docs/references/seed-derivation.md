# Deterministic randomness in the engine

*Explanation — the reasoning behind how the engine draws random numbers. For the
interface itself (types and signatures), see [`engine-contract.md`](engine-contract.md).*

The engine must be **deterministic and replayable**: the same game, resolved
twice, must produce byte-for-byte the same outcome, and a test must be able to
re-run a scenario and get an identical PRNG stream. This document explains how the
engine gets that property, and why it is built the way it is rather than the more
obvious alternatives.

## The core idea: hierarchical keyed derivation

Each game has one **master seed** — a 128-bit value assigned once at
initialization (see the `initialize` command and `ec_game`). The engine never
draws directly from a single, advancing generator seeded by that master. Instead,
every place that needs randomness derives its *own* independent stream by hashing
the master seed together with a **path** that names the context:

```
stream = H(master_seed, path)
```

where a path is a semantic address such as `(turn, phase, entity, purpose)`. The
hash yields 128 bits, which seed a fresh PCG (`math/rand/v2`) for that stream.

This technique has several names in the literature, all describing the same move:
**hierarchical deterministic (HD) derivation** (as in BIP-32 key trees),
**seed-sequence spawning** (numpy's `SeedSequence`), **counter-based / keyed RNG**
(Philox, Threefry — *Random123*), and, from cryptography, **domain separation**.
The unifying property is that a stream is a *pure function* of `(master, path)`.

## Why the master seed is immutable

The engine treats the master seed as read-only for the entire life of a game. It
is written once at `initialize` and never advanced or rewritten during play. This
has a large payoff: **the engine holds no PRNG state.** There is no "current
position" of a generator to persist between turns and restore on the next one,
because any stream can be re-derived from scratch at any time from `(master,
path)`. Turns read the master; they never write it. `ec_game` is effectively
read-only once initialized.

It also explains why re-seeding a game is dangerous. Because *every* stream in the
game's history derives from the master, changing the master retroactively rewrites
all of that randomness. That is exactly why the `PUT /games/{id}/seed` endpoint
carries an explicit "breaks determinism and repeatability" warning — reseeding is
not a routine edit, it is a rewrite of the game's entire random history.

## Why keyed paths instead of one global stream: blast radius

The alternative — one mutable generator that every subsystem draws from in turn —
couples every outcome to global draw *order*. If a combat routine draws three
numbers today and a refactor makes it draw four tomorrow, every subsequent draw in
every later subsystem shifts. A small, local implementation change silently
changes unrelated outcomes across the whole turn.

Keyed derivation removes that coupling. A stream is addressed by its *semantic
identity* — "the combat roll for unit 42 in the attack phase of turn 7" — not by
how many numbers some earlier routine happened to consume. Reordering or
refactoring one command cannot perturb another command's stream, because the two
are reached by different paths. This is the **blast-radius reduction**: the effect
of a change is bounded to the leaves that change touches.

One important qualification: the isolation is *between* leaves, not *within* one.
Draw order still matters inside a single stream. If a routine derives one stream
and then reorders its own draws from that stream, that routine's results change.
So the practical lever is **leaf granularity** — address randomness finely enough
(per phase, per entity, per purpose) that most refactoring moves draws *across*
leaves, which is stable, rather than *within* a leaf, which is not.

## Single draws and longer streams

A derived leaf is not limited to one random number. `Seed.Derive(path…)` returns a
PCG, so a leaf can back either a single draw or an extended stream. Some commands
genuinely want the latter — combat, for instance, may derive one stream and use it
to shuffle the combatants for an attack, then keep drawing from it for the
resolution. That is fine and expected: the command owns its leaf and may pull as
much as it needs from it. The granularity guidance above still applies — the more a
single stream drives, the more its internal order becomes load-bearing, so split
into sub-leaves where independence matters.

## Replay and testing

Given a master seed and a fixed derivation scheme, any stream in a game is exactly
reproducible. This is what makes scenario replay and deterministic tests possible:
seed a game with a known master, run it, and every draw is pinned. A determinism
test asserts two things — that the *same* path yields an identical stream every
time, and that *different* paths yield independent streams.

## Two replays: fold the facts vs re-resolve

The seed is not the only reproducibility mechanism, and separating the two keeps
their jobs clear. A die roll's *outcome* is recorded as a `Fact` in the event log
(see [engine-contract.md](engine-contract.md)), so there are two distinct replays:

- **Fold the facts** rebuilds current state from the log. It needs neither the
  seed nor the engine — just the recorded facts — so the state a game reaches is
  stable even when engine code later changes. This is the everyday path.
- **Re-resolve** regenerates a turn by running the engine again. *This* is what
  needs the seed: the same master seed makes resolution emit the same facts.

So the seed guarantees that *resolution* is reproducible; the fact log guarantees
that *state* is reproducible. Storing outcomes as facts is what buys the second,
engine-version-independent property; the seed exists for the first.

## What must stay fixed for determinism to hold

Determinism is only as stable as the derivation function, so a few constraints are
non-negotiable:

- **The hash must be stable across processes, platforms, and Go versions.** A
  process-randomized hash such as `hash/maphash` would destroy determinism
  immediately. The engine uses a fixed hash (`crypto/sha256` over a canonical
  encoding), run once per stream creation, not per draw.
- **The path encoding must be canonical and unambiguous** — fixed-width
  big-endian integers, length-prefixed strings, a domain tag — so two distinct
  paths can never collide onto the same bytes.
- **The derivation scheme is versioned by the migration that defines it.** The
  `ec_` schema is append-only and ordered; the migration that establishes the
  engine's derivation *is* its version. A game's schema version therefore already
  tells you which scheme it was played under, so no separate scheme-version column
  is carried. Changing the derivation later is a new migration — a visible,
  ordered, deliberate event — not a silent code edit. The trade-off is intended:
  you cannot quietly change how an existing game derives randomness, which is
  precisely the safety the replay guarantee needs.

(PCG imposes no constraints of its own here — it has no degenerate seed values, so
an all-zero derivation is a valid stream.)

## Alternatives considered

- **One global mutable generator.** Rejected: couples all outcomes to global draw
  order (the blast-radius problem) and forces the engine to persist and restore
  generator state between turns.
- **Persisting per-turn PRNG state.** Unnecessary: every stream is re-derivable
  from `(master, path)`, so there is nothing to store.
- **A separate scheme-version column on `ec_game`.** Rejected: the migration
  sequence already encodes the scheme version, so a dedicated column would
  duplicate information the schema version already carries.

## Boundaries

This document covers *why* the derivation works as it does. The concrete path
vocabulary is deliberately not fixed here — the legs a command uses (`turn`,
`phase`, `entity`, `purpose` is the general pattern) are pinned per command as
each command is designed. The engine's actual types and signatures live in
[`engine-contract.md`](engine-contract.md).
