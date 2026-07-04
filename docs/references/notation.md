# Notation

Shorthand used in the design notes to represent stream records and working state.
The notation is illustrative, not a specification of any implementation format.

For definitions of the terms used here, see the [Glossary](glossary.md).

## Stream records

The event stream contains three kinds of records.

### Intent

```text
Intent(source, Action(entity, args...))
```

`source` is the player or system component that issued the intent.
`Action` names the operation to be attempted.

Examples:

```text
Intent(Anne, Move(Biff, Location(3,4)))
Intent(Anne, Attack(Biff, TargetOfOpportunity))
Intent(Joe, Defend(Skar))
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
```

The last example is derived intent: the source is a phase engine, not a player.

### Fact

```text
Fact(Event(entity, args...))
```

`Event` names what happened, in past-tense terms.

Examples:

```text
Fact(Arrived(Biff, Location(3,4)))
Fact(Defending(Skar, Location(3,4), +18))
Fact(Wounded(Biff, 5hp))
Fact(Retreat(Biff, Location(3,4), Location(3,5)))
```

Facts are emitted by phase engines.
They are not player submissions.

## Working state

```text
State(entity, Attribute(value), ...)
```

`State(...)` is not a stream record.
It represents the current value of an entity's attributes in the working game state — the snapshot the engine reads when resolving the next record.

Examples:

```text
State(Biff, Health(8hp), Location(3,4))
State(Skar, Health(12hp), Location(3,4), AttackFactor(6), DefenseFactor(4))
```

A fact may cause working state to change:

```text
Fact(Wounded(Biff, 5hp))
  → State(Biff, Health(3hp), Location(3,4))
```

The stream records the cause; working state reflects the result.

## What this notation does not cover

- **Authorization metadata** — which player may issue orders to an entity — is not a game-world attribute and is not represented in `State(...)`.
- **Persistence formats** — SQLite schemas, flat-file layouts, and snapshot structures — are outside this notation.
- **Random inputs** — whether die rolls appear as facts or are reproduced from a seed is an open design question.
