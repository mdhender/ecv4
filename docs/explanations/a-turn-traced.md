# A Turn Traced

This page traces the sample turn from [Intent, Resolution, and Fact](intent-resolution-and-fact.md) using the [notation](../references/notation.md).
The purpose is to show how intent, fact, derived intent, and working state interact as each phase runs.

## Setup

Anne controls Biff. Joe controls Skar. Both players have submitted orders.

**Starting state**

```text
State(Biff, Health(8hp), Location(3,3), AttackFactor(1), DefenseFactor(12))
State(Skar, Health(12hp), Location(3,4), AttackFactor(6), DefenseFactor(4))
```

## Order parsing phase
Orders are parsed. Valid orders are placed into the stream.

**Accepted player intent**

```text
Intent(Anne, Move(Biff, Location(3,4)))
Intent(Anne, Attack(Biff, TargetOfOpportunity))
Intent(Joe, Move(Skar, Location(3,4)))
Intent(Joe, Defend(Skar))
```

State is unchanged. Nothing has happened yet.

## Movement phase

The movement phase reads the two move intents and resolves them against working state.

```text
Fact(Arrived(Biff, Location(3,4)))
  → State(Biff, Location(3,4))

Fact(Arrived(Skar, Location(3,4)))
  → State(Skar, Location(3,4))   ← Skar was already here; state unchanged
```

Biff's location has changed in working state. The fact for Skar is still
emitted — the intent was processed regardless of outcome.

## Defense phase

The defense phase reads Joe's defend intent and resolves it against Skar's
current working state.

```text
Fact(Defending(Skar, Location(3,4), +18))
  → State(Skar, Health(12hp), Location(3,4), AttackFactor(6), DefenseFactor(4), DefenseBonus(+18))
```

The defense bonus is now part of working state. The attack phase will read it
without needing to re-examine the defense intent.

## Attack phase

The attack phase reads Anne's attack intent and resolves it against working
state. Biff and Skar now share a location. Skar has a defense bonus. Combat
proceeds.

```text
Fact(CombatStarted(Biff, Skar))
Fact(CombatRoundResolved(Biff, Skar, ...))
Fact(Wounded(Biff, 5hp))
  → State(Biff, Health(3hp))
Fact(ExperienceGained(Skar, ...))
  → State(Skar, Experience(...))

Intent(CombatEngine, Retreat(Biff, Location(3,4)))
```

The last record is derived intent. The source is the combat engine, not a
player. It appears in the stream alongside the facts and will be picked up by
the retreat phase.

## Retreat phase

The retreat phase reads the derived retreat intent and resolves it against
Biff's current working state.

```text
Fact(Retreat(Biff, Location(3,4), Location(3,5)))
  → State(Biff, Location(3,5))
```

## End of turn

No unresolved work remains. Working state at turn end:

```text
State(Biff, Health(3hp), Location(3,5))
State(Skar, Health(12hp), Location(3,4), AttackFactor(6), DefenseFactor(4), DefenseBonus(+18), Experience(...))
```

This becomes the snapshot that seeds the next turn.

## What the trace shows

**Facts mutate working state; intent does not.** The stream records what was
attempted and what resulted. Working state reflects only the results.

**Phase order determines what each phase sees.** The attack phase reads
`DefenseBonus(+18)` from working state because the defense phase ran first
and emitted a fact that mutated state. A different phase order would produce
different inputs.

**Derived intent is first-class in the stream.** The retreat intent from the
combat engine appears between facts, not in a side channel. The retreat phase
picks it up the same way any phase picks up player intent.

**A no-op still produces a fact.** Skar's move to a location he already
occupied emits `Arrived(Skar, Location(3,4))` because the intent was
processed, even though working state did not change. The stream records the
attempt, not just the delta.
