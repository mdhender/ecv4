---
title: "Intent, Resolution, and Fact"
linkTitle: "Intent, Resolution, and Fact"
weight: 2
toc: true
---

## Introduction

When developers talk about game engines, they often begin with state.

There are units, provinces, characters, armies, items, resources, skills, buildings, ships, spells, and maps. The database stores their current values. The engine applies procedures that mutate those values. Movement changes locations. Combat changes health. Production changes inventory. Recruitment changes population. Reports describe the results.

That model is familiar, but for a Play-By-Email game it starts in the wrong place.

A PBEM game is not fundamentally an interactive simulation. It is a turn-resolution machine. Players submit intent. The engine resolves that intent according to the rules of the game. Conflicts are discovered. Outcomes are produced. Reports are generated. The next turn begins.

The central object in such a system is not the unit, the province, the army, or the database row.

The central object is the event stream.

More precisely, a PBEM engine should be organized around the transformation of **intent** into **fact**.

That distinction changes the shape of the engine.

Orders do not update state. Orders express intent. Turn phases consume intent, resolve it against a stable view of the game, and emit facts. Some facts alter game state. Some facts exist only to explain what happened. Some resolutions create new derived intent that must be handled by later phases.

The engine becomes less like a large procedure that mutates a database and more like a small, disciplined machine that repeatedly answers one question:

> What is the next unresolved thing, and what facts follow from it?

## Orders Are Not Actions

Consider two players.

Anne controls Biff.

Joe controls Skar.

Anne submits:

```text
MOVE 3,4
ATTACK ANYONE
```

Joe submits:

```text
MOVE 3,4
DEFEND
```

A traditional implementation might parse Anne's orders and immediately start changing state. Biff moves. Biff attacks. Skar moves. Skar defends. The engine must then decide how to unwind the apparent order of those operations and impose the actual turn sequence.

That is dangerous because the player's text file has started to look like execution.

In a PBEM game, an order is not an action. It is a statement of intent.

Anne is not moving Biff when she writes `MOVE 3,4`. She is asking the game to attempt that move during the movement phase.

Joe is not granting Skar a defensive bonus when he writes `DEFEND`. He is asking the game to put Skar into a defensive posture at the appropriate point in the turn.

The order parser should validate syntax and perform shallow semantic checks.

It can answer questions like:

- Does this order have the correct form?
- Does the referenced unit exist?
- Is the destination meaningful?
- Is this unit allowed to submit this kind of order?
- Does the order refer to an obvious impossibility?

The parser should not resolve the turn.

It should not decide whether Biff reaches 3,4 before combat begins.

It should not decide whether Skar's defense applies.

It should not decide whether Biff finds a target of opportunity.

Instead, the parser emits structured intent records:

```text
Intent(Anne, Move(Biff, Location(3,4))
Intent(Anne, Attack(Biff, TargetOfOpportunity))

Intent(Joe, Move(Skar, Location(3,4))
Intent(Joe, Defend(Skar))
```

Those records are accepted player intent.

Nothing has happened yet.

## Intent, Fact, and Derived Intent

The word "event" is overloaded. It is tempting to call everything in the stream an event, but that hides an important distinction.

A PBEM engine needs at least three categories of stream records.

The first is **intent**.

Intent describes something that should be attempted or resolved.

```text
Intent(Anne, Move(Biff, Location(3,4))
Intent(Anne, Attack(Biff, TargetOfOpportunity))
Intent(Joe, Defend(Skar))
```

The second is **fact**.

Fact describes something that has happened.

```text
Fact(Arrived(Bif, Location(3,4)))
Fact(Arrived(Skar, Location(3,4)))
Fact(Defending(Skar, Location(3,4), +18))
Fact(Wounded(Biff, ...))
Fact(ExperienceGained(Skar, ...))
```

The third is **derived intent**.

Derived intent is work created by the simulation rather than by a player's order.

```text
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
Intent(CombatEngine, PursuitAvailable(Skar, Biff, Location(3,4)))
Intent(CombatEngine, LootDropped(Biff, Location(3,4), ...))
```

Derived intent is not a player order. It is also not a completed fact. It is a new obligation discovered during resolution.

This distinction is the heart of the design.

The engine transforms intent into fact. In doing so, it may produce more intent. The cycle continues until the stream contains no unresolved work.

## Turn Phases as Small Engines

A PBEM turn is naturally phased:

- Movement happens before combat.
- Defensive postures may be established before attacks are resolved.
- Production may happen before maintenance.
- Recruitment may happen before training.

The exact phase order belongs to the game design, but the architectural shape is common.

Each phase can be treated as a small engine.

A phase engine has a narrow job:

1. Read the stream records relevant to that phase.
2. Resolve them against a stable view of state.
3. Emit facts and any derived intent.
4. Advance the stream.

The movement phase consumes movement intent:

```text
Intent(Anne, Move(Biff, Location(3,4))
Intent(Joe, Move(Skar, Location(3,4))
```

It resolves them and emits facts:

```text
Fact(Arrived(Bif, Location(3,4)))
Fact(Arrived(Skar, Location(3,4)))
```

The defense phase consumes defense intent:

```text
Intent(Joe, Defend(Skar))
```

It resolves that intent and emits a fact:

```text
Fact(Defending(Skar, Location(3,4), +18))
```

The attack phase consumes attack intent:

```text
Intent(Anne, Attack(Biff, TargetOfOpportunity))
```

It resolves the attack against the facts established by earlier phases. Biff and Skar are now in the same location. Skar is defending. Combat can proceed.

The attack phase may emit:

```text
CombatStarted(Biff, Skar)
CombatRoundResolved(...)
Fact(Wounded(Biff, ...))
Fact(ExperienceGained(Skar, ...))
```

It may also emit derived intent:

```text
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
```

A later retreat phase consumes that derived intent and emits:

```text
Fact(Retreat(Biff, Location(3,4), Location(3,5)))
```

The engine is not one giant turn-resolution procedure.

It is a pipeline of small engines. Each engine consumes specific records and emits new records. The turn completes when no unresolved work remains.

## The Engine Does Not Parse Orders

This separation gives the engine a clean boundary.

The engine does not parse player orders.

Order entry, file upload, syntax checking, semantic validation, authentication, authorization, and player identity all sit outside the engine. Those concerns matter, but they are not the engine.

The engine receives structured records.

It does not care whether those records came from:

- a PBEM text file,
- a web form,
- a GM console,
- a test fixture,
- a Discord bot,
- a migration script,
- or a future client that has not been written yet.

That decoupling is a practical win.

Many game projects stall because web concerns invade the engine too early. Login, registration, password reset, permissions, player dashboards, and report delivery can consume months of work before the game itself can move a unit across a map.

An event-driven engine keeps the boundary clear.

The parser creates intent.

The engine resolves intent.

The reporting system renders facts.

The user interface submits orders and displays results.

Each system can evolve without becoming the other.

## The Event Stream as Narrative

An event stream is not merely a work queue. It is also the narrative of the turn.

A state-oriented system can answer:

> Where is Biff now?

An event-oriented system can also answer:

> Why is Biff there?

That distinction matters.

If Biff is wounded, the current-state table may record his health. The stream can show the path:

```text
Intent(Anne, Attack(Biff, TargetOfOpportunity))
Fact(CombatStarted(Biff, Skar))
Fact(Defending(Skar, Location(3,4), +18))
Fact(CombatRoundResolved(...))
Fact(Wounded(Biff, ...))
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
Fact(Retreat(Biff, Location(3,4), Location(3,5)))
```

That narrative is useful to everyone.

The report writer can turn it into player-facing prose.

The developer can use it to debug the engine.

The GM can use it to audit a disputed result.

The test suite can use it to verify that the rule fired for the right reason.

The event stream explains the current world.

## State Still Matters

None of this eliminates state.

The engine still needs state to resolve events.

If Biff fights Skar, combat needs to know their attributes. It may need terrain, weather, equipment, skills, fatigue, wounds, morale, formation, fortification, and special rules.

Those inputs must be stable for the duration of the resolution.

Combat should not accidentally read a changing Biff midway through a round and produce nondeterministic results. It should resolve against an immutable slice of state relevant to that combat.

The practical model is:

```text
G0 + e -> r
r mutates G0 into G1
```

Where:

- `G0` is the working game state before the event,
- `e` is the intent or derived intent being resolved,
- `r` is the resolution,
- and `G1` is the resulting game state.

For combat, the resolution may use a frozen input:

```text
combat_input = snapshot(Biff, Skar, terrain, modifiers)
combat_input + AttackIntent -> combat_result
combat_result mutates G
```

State is therefore a first-class concern during resolution.

The event stream records how state changed.

The working state supplies the context required to decide what the next facts should be.

## Authoritative State During Turn Processing

A useful distinction emerges:

- The event stream is the authoritative history.
- The in-memory model is the authoritative working state.
- The end-of-turn snapshot is the authoritative current state for the next turn.

During a turn run, the engine may keep the mutable game state in memory for performance and simplicity. It resolves the next item, applies the result, and continues.

At the end of the turn, the engine can write a new durable snapshot of the game world.

That snapshot is not a replacement for the stream. It is a summary.

The stream answers why.

The snapshot answers what now.

A PBEM engine does not need to replay the entire game from turn zero every time it runs. It can begin from a known snapshot and process the accepted intent for the current turn. That is a practical compromise between pure event sourcing and state-oriented persistence.

## Restartability and Rewinding

Restartability becomes simpler when the engine is designed to process the next unresolved item.

The engine does not need to understand restarting.

It does not need a special "resume from failed combat" mode.

It does not need to know whether the GM is replaying a turn.

It only needs to process the stream in front of it.

If the GM wants to restart a turn, the GM performs an administrative operation on the stream.

The important rule is:

> Preserve accepted player intent; discard derived turn work.

Player-submitted intent should survive a rewind. Those records are the result of order intake. They represent what the players submitted for the turn.

Engine-derived facts and derived intent may be discarded and rebuilt.

This implies a practical separation:

```text
Accepted intent stream
Derived resolution stream
```

or at least a stream model that can distinguish accepted player intent from engine-produced records.

Rewinding to an earlier point does not mean deleting player orders. It means discarding resolution records after a chosen point and letting the engine process again.

From the engine's perspective, nothing special happened.

Its world is still simple:

```text
read next unresolved record
resolve it
emit results
repeat
```

## Completion Means Different Things to Different Actors

A turn is not "complete" in one universal sense.

For the engine, the turn is complete when no unresolved work remains.

For the GM, the turn is complete when the results have been reviewed and approved.

For the player, the turn is complete when reports are available and next-turn orders may be entered.

These are different states in the surrounding application.

- The engine should not confuse them.
- The engine does not approve results.
- The engine does not publish reports.
- The engine does not open order entry.
- The engine drains the stream according to the rules of the turn.

Other systems decide what to do with the result.

## Testing the Engine

This model is attractive because it is testable.

Each phase consumes known inputs and emits known outputs. Tests can be written at the level of domain behavior rather than implementation details.

A Gherkin-style scenario might say:

```gherkin
Given Biff is in province 3,3
And Skar is in province 3,4
And Biff has an accepted move intent to province 3,4

When the movement phase runs

Then the stream contains Fact(Arrived(Bif, Location(3,4)))
And Biff's working location is 3,4
```

A combat scenario might say:

```gherkin
Given Biff and Skar are in province 3,4
And Skar is defending
And Biff has an accepted attack intent against targets of opportunity

When the attack phase runs

Then the stream contains Fact(CombatStarted(Biff, Skar))
And the stream contains a combat result
And the combat result uses Skar's defensive bonus
```

The test does not need to inspect every database row.

It verifies that the right intent produced the right facts.

That makes the event stream not only the engine's organizing principle but also the testing surface.

## What This Essay Is Not About

Several important topics are deliberately outside the scope of this essay.

SQLite is an excellent operational store for a Go PBEM engine, but the choice between SQLite tables, flat files, golden diffs, and export formats deserves its own treatment.

Gherkin may be an excellent testing DSL for turn resolution, but the design of the test harness is a separate topic.

Authentication and authorization matter, but they should not be allowed to consume the engine design.

Report generation deserves its own discussion because reports are neither merely templates nor merely database queries. They are player-specific renderings of resolved facts.

Those are all important essays.

This essay is narrower.

It argues that the engine itself should be organized around the event stream.

## Conclusion

A PBEM engine should not begin with the database schema.

It should not begin with the web interface.

It should not begin with authentication.

It should not even begin with units and provinces.

It should begin with the turn.

A turn accepts intent, resolves that intent, records facts, and sometimes creates new intent. The cycle continues until nothing remains unresolved.

That model produces a clean engine boundary.

Orders do not mutate state.

The parser does not resolve turns.

The engine does not parse orders.

Reports do not invent outcomes.

Each turn phase is a small engine that consumes records and emits records.

State remains necessary, but state is no longer the whole story. The event stream explains how state became what it is.

For a PBEM game, that explanation is not a luxury. It is the basis of debugging, testing, auditing, reporting, and trust.

The event stream is not an implementation detail.

It is the organizing principle of the engine.

---

# Addendum: Notes and Prompts for Follow-Up Discussions

## 1. Snapshots, Event Streams, and Persistence

### Working thesis

A PBEM engine can treat the event stream as authoritative history while using snapshots as practical starting points for future turns.

### Notes

The discussion should avoid becoming a generic event-sourcing essay. The interesting PBEM question is not whether the system can replay from turn zero. The question is how much history must remain replayable, how much state can be snapshotted, and what guarantees the GM needs when reviewing or replaying a turn.

Important distinctions:

- Event stream as history.
- In-memory state as working state.
- End-of-turn snapshot as current state.
- Flat files as reviewable artifacts.
- SQLite as operational store.
- Golden diffs as testing strategy.

The essay should probably use "snapshot" rather than "checkpoint" unless discussing crash recovery specifically.

### Prompts

- What must be persisted before the engine starts resolving a turn?
- What must be persisted while the engine is resolving a turn?
- What can safely exist only in memory?
- Is a snapshot a cache, a projection, or an authoritative artifact?
- How does the GM discard derived results without deleting accepted player intent?
- Should snapshots be human-reviewable?
- Should flat-file exports be canonical for tests even if SQLite is canonical for operation?

## 2. SQLite, Flat Files, and Golden Diffs

### Working thesis

SQLite is an excellent operational store for a Go PBEM engine, but flat files are often superior as review and testing artifacts.

### Notes

This essay should focus on engineering tradeoffs rather than ideology.

SQLite gives structure, constraints, indexing, transactions, and good Go integration. It is a strong choice for operational game state.

Flat files provide visibility. They are easy to diff, easy to review, and easy to store as test fixtures. They make it easier to see what changed between turns.

The key insight is that the data store influences the testing strategy.

### Prompts

- Which game artifacts should be stored in SQLite?
- Which artifacts should be exported as flat files?
- What should a golden diff compare?
- Should test fixtures load from flat files, SQLite, or both?
- How should event streams be represented for human review?
- What does a useful failure diff look like after a turn run?
- Can the engine treat SQLite as operational while treating flat files as explanatory?

## 3. Gherkin as a Turn-Resolution Testing DSL

### Working thesis

Gherkin works well for PBEM engine tests because turn resolution rules are naturally expressed as Given/When/Then scenarios.

### Notes

Gherkin is attractive because it can describe rules in domain language.

The danger is writing scenarios that are too broad. A good Gherkin test should focus on one phase, one rule, or one interaction between phases.

The test harness should probably arrange state, inject intent records, run one phase or a small phase group, and assert emitted facts.

### Prompts

- Should a Gherkin scenario test one phase or a whole turn?
- What vocabulary should scenarios use: orders, intent, facts, or state?
- How much of the stream should be asserted?
- Should tests assert exact ordering or only presence of facts?
- How should random combat be made deterministic?
- Should combat use seeded randomness, fixed rolls, or scripted outcomes?
- How can Gherkin remain readable without becoming a second programming language?

## 4. Report Generation from Event Histories

### Working thesis

Reports should be rendered from resolved facts, not invented from final state.

### Notes

A final-state report can say that Biff is wounded.

An event-history report can say how Biff was wounded, who attacked him, whether he retreated, what he saw, and what he learned.

The report writer should not resolve rules. It should render the facts that the engine produced.

This keeps reports honest and testable.

### Prompts

- What facts are private to one player?
- What facts are visible to multiple players?
- What facts are GM-only?
- Should report text be generated during turn resolution or after approval?
- Should report entries be stored as events, projections, or rendered documents?
- Can the same facts produce different reports for different players?
- How much narrative should be stored versus generated?

## 5. The Turn Engine as a REPL

### Working thesis

A PBEM engine can be modeled as a small loop that repeatedly processes the next unresolved record.

### Notes

This is a compact mental model:

```text
while stream has unresolved work:
    record = next_record()
    result = resolve(record)
    append(result)
```

The engine does not know whether it is starting, restarting, replaying, or continuing. Those are administrative concerns.

This essay could be shorter and more technical than the others.

### Prompts

- What does "next" mean in a phased turn engine?
- Is ordering encoded in the stream, in phase rules, or in a scheduler?
- Does each phase own its own cursor?
- Can the engine process one record at a time and still handle simultaneous resolution?
- How are conflicts resolved when events compete for the same state?
- What does idempotence mean in this model?
- Can the loop be made deterministic and debuggable?

## 6. Commands, Intent, and Facts

### Working thesis

PBEM engines should distinguish commands, intent records, and facts instead of calling everything an event.

### Notes

Commands ask the system to do something.

Intent records describe accepted work to be resolved.

Facts describe what happened.

This distinction prevents confusion in the event vocabulary.

For example:

```text
AttackCommand
AttackIntent
AttackDeclared
CombatStarted
CombatResolved
UnitKilled
```

These names imply different responsibilities.

The parser may handle commands. The stream may store intent. The engine may emit facts.

### Prompts

- Should player-submitted records be called commands, orders, or intent?
- Should engine-produced work be called derived intent?
- Are facts always past-tense?
- Can a fact fail?
- Should work items and facts live in the same stream?
- What naming convention makes stream records easiest to reason about?
- How should the engine distinguish accepted intent from resolved fact?

## 7. Combat as an Event-Driven Rabbit Hole

### Working thesis

Combat can be event-driven internally, but only if the model preserves deterministic inputs and phase boundaries.

### Notes

Combat is the natural rabbit hole.

A single attack can produce rounds, wounds, morale checks, retreats, pursuit, capture, loot, experience, death, and visibility changes.

The danger is letting combat become an unbounded nested simulation that is impossible to test.

The engine should decide whether combat is resolved as one opaque fact or as a sequence of smaller facts.

### Prompts

- Is combat one event, one phase, or a sub-engine?
- Should combat rounds be recorded individually?
- What combat inputs must be frozen before resolution begins?
- How should randomness be represented in the stream?
- Are die rolls facts?
- Are wounds facts or state mutations?
- When does combat emit derived intent such as retreat or pursuit?
- How much combat detail belongs in player reports?

## 8. Authentication as a Progress Trap

### Working thesis

Authentication and authorization are necessary for a playable system but dangerous to the early development of the engine.

### Notes

This essay should not argue that authn/authz are unimportant. It should argue that they are not the game.

A PBEM engine can be developed and tested with files, fixtures, CLI commands, and GM tools before a polished player-facing authentication system exists.

The architecture should allow authn/authz to wrap order submission without becoming entangled with turn resolution.

### Prompts

- What is the minimum identity model needed to test the engine?
- Can order ownership be represented without a login system?
- When should permissions enter the design?
- How can a CLI or GM tool bypass web auth safely during development?
- What engine APIs should remain independent of user sessions?
- What is the first auth feature that actually matters for playtesting?
- How do you avoid spending three months on accounts before movement works?

## 9. Open Design Questions

These questions remain unresolved and deserve careful thought before implementation hardens.

### Stream structure

Should accepted player intent and derived resolution records live in one stream with types and phases, or in separate streams?

### Ordering

How should the engine determine the next record to resolve?

Possibilities include:

- explicit stream position,
- phase priority,
- per-phase cursor,
- dependency graph,
- scheduler-generated work queue.

### Rewind semantics

Does rewinding physically delete derived records, mark them discarded, or create a new branch?

### Publication

When the GM approves a turn, are the resolution records frozen forever?

### Reports

Are reports generated from facts each time, or are rendered reports persisted as published artifacts?

### Randomness

Are random rolls stored as facts, or can they be reproduced from a seed?

### Schema evolution

If event records are durable history, how should their shape change over time?

### Testing

Should tests assert exact stream contents or only externally visible outcomes?

## 10. Possible Essay Sequence

A coherent series might look like this:

1. **Intent, Resolution, and Fact: Building an Event-Driven PBEM Game Engine**
2. **Snapshots and Streams: Persisting State Between PBEM Turns**
3. **SQLite and Flat Files: Operational Stores and Golden Diffs**
4. **Testing Turn Resolution with Gherkin**
5. **Reports as Rendered Facts**
6. **The Turn Engine as a REPL**
7. **Combat Without the Rabbit Hole**
8. **Authentication Is Not the Engine**

Each essay should keep one star of the show.

The first essay's star is the event stream.

The next essay's star should probably be the snapshot.
