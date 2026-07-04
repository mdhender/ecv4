---
title: "Derived Intent and the Flat Stream"
linkTitle: "Derived Intent and the Flat Stream"
weight: 4
toc: true
---

The [first essay](/explanation/intent-resolution-and-fact.md) establishes that a PBEM engine
transforms intent into fact, and that resolution sometimes produces derived
intent that later phases must handle. The
[worked example](/explanation/a-turn-traced.md) shows this in motion: the combat engine
emits a retreat intent mid-turn, and a later phase resolves it.

A question follows immediately from that picture.

The main stream may contain thousands of records by the time combat runs. The
attack intent is one record among many. When the combat engine emits a retreat
intent, how does the retreat phase know that record applies to this combat and
not some other? And if combat runs in isolation, how does the rest of the turn
see the damage Biff took?

## The Sub-Stream Temptation

One answer is to give combat its own stream.

When the attack phase encounters an attack intent, it could gather state for
the participants, open a private combat stream, inject the relevant intent and
facts into that stream, and resolve combat in isolation. Derived intent produced
during combat — retreat, pursuit, loot — would live in the private stream until
combat concludes. The results would then be merged back into the main stream.

This is a coherent design. It solves the routing problem locally: derived intent
produced inside a combat sub-stream stays associated with that combat and cannot
be confused with intent from a different engagement happening elsewhere in the
turn.

The problem appears when results re-enter the main stream.

Consider: Biff fights Skar and loses. Combat ends. The sub-stream emits
`Fact(Wounded(Biff, 5hp))` and `Intent(CombatEngine, Retreat(Biff,
Location(3,4)))`. These records merge back into the main stream. The retreat
phase resolves the retreat. Biff moves to Location(3,5).

Location(3,5) is the lair of the spider queen.

The spider queen attacks. Her combat phase needs to know that Biff has 3hp, not
the 8hp he started the turn with. If working state reflects the wound — because
the `Wounded` fact was committed before the retreat was processed — the spider
queen sees the right number. But if the sub-stream's results were not yet
applied to working state at the time the spider queen's combat resolves, Biff
fights at full health.

The sub-stream defers this problem rather than solving it. The merge is where
the complexity lives, and a merge of nested resolution results into a shared
working state is not obviously simpler than the original problem.

## Derived Intent Does Not Need to Find Its Origin

The routing problem that motivates sub-streams turns out to be smaller than it
appears.

Derived intent does not need to find its way back to the combat that generated
it. It does not need to be associated with a particular engagement. The retreat
intent carries enough context in the record itself:

```text
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
```

The retreat phase does not ask which combat produced this. It reads the intent,
reads working state for Biff, and resolves the retreat. The entity and location
are sufficient. There is no routing problem because derived intent is not a
message back to the originating combat — it is a new obligation placed on the
main stream.

The sub-stream model creates the routing problem by trying to keep derived
intent local to the combat that produced it. The simpler design is to not do
that.

## Derived Intent Defers Resolution; It Does Not Nest It

The deeper principle:

> Derived intent defers resolution. It does not nest it.

Combat runs. It emits facts and derived intent to the main stream. It is done.
Facts commit to working state immediately. Derived intent waits for the
appropriate phase.

The spider queen scenario then flows naturally from the
[worked example](/explanation/a-turn-traced.md). After the attack phase:

```text
Fact(Wounded(Biff, 5hp))
  → State(Biff, Health(3hp))
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
```

The retreat phase resolves the retreat:

```text
Fact(Retreat(Biff, Location(3,4), Location(3,5)))
  → State(Biff, Location(3,5))
Intent(RetreatEngine, CheckEncounter(Biff, Location(3,5)))
```

An encounter phase resolves the spider queen's attack. When it reads working
state, it finds:

```text
State(Biff, Health(3hp), Location(3,5))
```

The wound is already there. Working state is always current because facts commit
as they are emitted. No merge is required. The engine never needs to hold
partial results from a sub-stream in suspension while deciding when to apply
them.

## Why the Stream Stays Flat

The engine is a loop:

```text
read next unresolved record
resolve it against current working state
emit facts and any derived intent
repeat
```

Sub-streams break this loop by introducing a second loop running against a
separate view of state. When the inner loop finishes, its results must be
reconciled with the outer loop's state — and the reconciliation point is exactly
where the complexity that motivated the sub-stream returns.

A flat stream avoids this. Every phase reads the same working state. Every fact
commits to the same working state. Derived intent from any phase enters the same
queue and is picked up by the appropriate later phase. The engine does not need
to know that a retreat intent came from combat rather than from some other
source. It reads the record, resolves it, and continues.

The cost of this approach is phase design. The game designer must ensure that
phases run in an order that gives each phase the facts it needs in working
state. The retreat phase must run after combat, not before. An encounter-check
phase must run after retreat. That ordering is a game design constraint, but it
is explicit and testable, not hidden inside a sub-stream merge.

## Remaining Design Questions

**Who emits encounter checks?**

In the example above, the retreat phase emits
`Intent(RetreatEngine, CheckEncounter(Biff, Location(3,5)))`. Alternatively, a
dedicated encounter phase could sweep all new locations at the end of the turn's
movement and retreat work. The right answer depends on whether encounter
triggering is a property of retreat specifically or of any location change.

**What happens when derived intent outlives the turn?**

The worked example resolves everything within one turn. But the civil war
scenario is different: if a player submits an order to the ruler of a province
and no ruler exists, the intent is accepted but cannot be resolved. Does it wait
in the stream for a future turn, expire, or produce an error fact? That is a
stream lifecycle question the engine design must eventually answer.

**Can derived intent be rejected?**

A player can submit an impossible order — the parser catches obvious cases, but
some impossibilities only become visible during resolution. Derived intent has
no player to reject it; the engine produced it. If derived intent cannot be
resolved (the retreat destination is impassable, for example), what fact does
the engine emit and what does working state become?

**Does phase ordering belong in the stream or in the scheduler?**

The flat stream depends on phases running in the right order. That order could
be encoded in the stream itself (each record carries a phase tag), in a phase
scheduler that determines which records are eligible at each step, or in the
phase engines themselves (each phase only picks up records it recognizes). These
have different implications for restartability and for the ability to add new
phases without touching existing ones.
