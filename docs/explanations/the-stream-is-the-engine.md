# The Stream Is the Engine

This essay draws the separate design notes together into one picture.
It restates the central thesis — that a PBEM engine is organized around an event stream — and then follows that thesis through the consequences the other essays work out: how intent becomes fact, why the stream stays flat, how each phase chooses its own resolution rules, and why a snapshot is best understood as a pointer into a log.

The companion essays develop each idea in depth:
[Intent, Resolution, and Fact](intent-resolution-and-fact.md)
for the core model,
[A Turn Traced](a-turn-traced.md)
for the worked example,
[Derived Intent and the Flat Stream](derived-intent-and-the-flat-stream.md)
for the routing problem, and
[Snapshots and the Event Pointer](snapshots-and-the-event-pointer.md)
for state.
This essay assumes them and connects them.

Throughout, one running example illustrates the ideas. Anne controls Biff. Joe
controls Skar. Anne orders Biff to move and attack; Joe orders Skar to move and
defend. The full trace lives in [A Turn Traced](a-turn-traced.md); fragments of
it appear here to ground each idea.

## Starting in the Wrong Place

Most game engines begin with state.
There are units, provinces, armies, and
items; a database stores their values; procedures mutate those values. That
model is familiar, and for a real-time simulation it is correct.

A PBEM game is not a real-time simulation. It is a turn-resolution machine.
Players submit intent. The engine resolves that intent according to the rules.
Conflicts are discovered, outcomes are produced, reports are generated, and the
next turn begins.

The central object in such a system is not the unit or the database row. It is
the event stream — more precisely, the transformation of **intent** into
**fact**. That shift changes the shape of the engine. Orders do not update
state; orders express intent. Phases consume intent, resolve it against a
stable view of the world, and emit facts. The engine becomes a small machine
that repeatedly answers one question: *what is the next unresolved thing, and
what facts follow from it?*

## Orders Are Not Actions

When Anne writes `MOVE 3,4` and `ATTACK ANYONE`, she is not moving Biff and she
is not starting a fight. She is asking the game to attempt those things at the
appropriate points in the turn. The order is a statement of intent, not an
action.

This is why the order parser does not resolve the turn. It validates syntax,
checks that the unit exists, checks that the destination is meaningful, and
emits structured intent:

```text
Intent(Anne, Move(Biff, Location(3,4)))
Intent(Anne, Attack(Biff, TargetOfOpportunity))
Intent(Joe, Move(Skar, Location(3,4)))
Intent(Joe, Defend(Skar))
```

Those records are accepted player intent. Nothing has happened yet.
The parser creates intent, the engine resolves intent, the reporting system renders facts, and the user interface submits orders and displays results.
Each system can evolve without becoming the others — the decoupling that keeps web concerns from invading the engine before a unit can cross a map.

## Three Kinds of Record

The word "event" is overloaded.
The stream needs three distinct kinds of record, and conflating them hides the design.

**Intent** describes something to be attempted or resolved.
`Intent(Anne, Move(Biff, Location(3,4)))`.

**Fact** describes something that has happened, in past tense.
`Fact(Arrived(Biff, Location(3,4)))`.

**Derived intent** is work created by the simulation rather than by a player.
When combat forces Biff back, the combat engine emits `Intent(CombatEngine, Retreat(Biff, Location(3,4)))`.
It is not a player order and not a completed fact; it is a new obligation discovered during resolution.

The engine transforms intent into fact.
In doing so it may produce more intent.
The cycle continues until the stream contains no unresolved work.
That asymmetry — a record that is accepted but not yet resolved, and may never resolve — is load-bearing.
A player can issue an order to the ruler of a province that currently has no ruler; the intent is accepted and waits.
A transaction log records only what committed; this stream records the attempt as well.

## Grounding: The Transaction Log

The structure underneath all of this is a log, in the sense Jay Kreps describes in *I Heart Logs* (see [Bibliography](../references/bibliography.md)).
A reader arriving from a database background will recognize it immediately as a write-ahead log or a transaction log, and that analogy is worth making explicit because it grounds everything that follows.

A transaction log has a few defining properties:

- **It is append-only.** Records are added at the end. Existing records are not
  modified in place.
- **It is ordered.** Every record has a position, and that order is total.
  Readers observe records in the order they were written.
- **State is a projection of the log, not the other way around.** A database
  table is what you get by applying every committed change in order. The table
  is a convenience; the log is the truth. Lose the table and you can rebuild it
  by replaying the log.
- **Snapshots are checkpoints, not replacements.** A log can be replayed from
  the beginning, but that is expensive, so systems periodically record the
  projected state at a known position. The snapshot lets a reader start from the
  middle instead of the beginning. It never supersedes the log.
- **Many readers, independent positions.** Different consumers read the same log
  at their own pace, each tracking its own offset.

The PBEM event stream is an application of this model, with one extension a
database log does not need. A database log carries only committed mutations —
the equivalent of facts. The game stream also carries intent and derived
intent: records that describe attempted and pending work, not just completed
work. Everything else transfers directly. State is a projection of facts.
Snapshots are checkpoints. Each phase engine, the report writer, and the GM
audit tool are independent readers with their own positions.

Where this differs from Kafka in practice is scale and control. The readers are
not arbitrary distributed consumers racing each other; they are phase engines
running in a defined sequence, once per turn. The conceptual model is the same.
The operational complexity is bounded.

## State Is a Projection; A Snapshot Is a Pointer

Given the log, state has a precise definition:

> The state at position P is the sum of the initial game state and every fact
> at or before P.

Intent records sit in the log but do not contribute to state. State is the fold
of facts alone. The initial state is not special: the game begins with an empty
log, setup emits facts (map tiles, starting positions, attributes), and the
pointer advances through them. When the GM opens order entry, intent begins
mixing in. Setup facts and resolution facts are treated identically.

This is why a snapshot is best understood not as a bag of attribute values but
as a **(pointer, projected state) pair**. The pointer says: *the world exists
exactly as it did at this position; we are aware of no fact before it and no
fact after it.* The projected state — `State(Biff, Health(3hp),
Location(3,5))` — is a cache of the fold up to that pointer. It exists so the
next turn need not replay from the beginning of the game. It is never
authoritative. The log is authoritative, and the projection can always be
rebuilt by replaying facts from an earlier pointer.

The same idea operates at two scales. The end-of-turn snapshot is a pointer
taken after every fact has committed and the GM has approved the turn. A phase
engine that resolves a batch of intents takes a *local* snapshot — a pointer at
the moment its batch begins — and resolves the whole batch against state at that
pointer. The local snapshot is temporary and never persisted; when the batch
finishes, working state has advanced and the next phase reads from there.

## Phases as Small Engines

A turn is naturally phased: movement before combat, defensive posture before
attacks, production before maintenance. Each phase is a small engine with a
narrow job — read the records relevant to it, resolve them against working
state, emit facts and any derived intent, advance.

The worked example shows the chain. Movement resolves both move intents:

```text
Fact(Arrived(Biff, Location(3,4)))
  → State(Biff, Location(3,4))
Fact(Arrived(Skar, Location(3,4)))   ← Skar was already here; state unchanged
```

The defense phase emits Skar's bonus into working state. The attack phase then
reads that bonus without re-examining the defend intent — because the defense
phase ran first and its fact had already committed:

```text
Fact(Defending(Skar, Location(3,4), +18))
  → State(Skar, ..., DefenseBonus(+18))
```

Two consequences from the trace are worth stating plainly. **Phase order
determines what each phase sees** — the attack phase sees the defense bonus only
because defense ran earlier. And **a no-op still produces a fact** — Skar's
move to a hex he already occupied still emits `Arrived`, because the stream
records the attempt, not merely the delta.

## Nothing Happens Simultaneously

The engine is always sequential. It advances through the log one record at a
time, in order. Biff and Skar both move "during the movement phase," but the
first move intent in the log is resolved before the second. There is no true
simultaneity at the implementation level — there cannot be.

The design question is therefore not *whether* resolution is sequential but
*at what granularity each phase engine takes its snapshot.* Two models exist:

- **Strict sequential.** Each intent is resolved against live working state,
  and each fact mutates state before the next intent is read. Whoever appears
  first in the log gains an implicit advantage.
- **Batch.** The phase engine reads all relevant intents first and resolves
  them against a single snapshot taken at the start of the batch. No intent in
  the batch sees the effects of another. This is how a Diplomacy-style movement
  phase models simultaneity without abandoning sequential execution.

Combat makes the difference concrete, because the two models describe genuinely
different games. Under an **initiative** model, Biff strikes first; if the blow
is lethal, Skar dies before he can swing, and his attack — derived intent — is
voided:

```text
Fact(Attacked(Biff, Skar, 14dmg))
  → State(Skar, Dead)
-- Skar's attack is voided --
```

Under a **simultaneous** model, both blows are resolved against the pre-round
snapshot; Skar dies but still lands his attack:

```text
Fact(Attacked(Biff, Skar, 14dmg))
Fact(Attacked(Skar, Biff, 3dmg))   ← resolved against the pre-round snapshot
  → State(Skar, Dead)
  → State(Biff, Health(5hp))        ← still takes damage
```

Neither is more correct than the other. The point is that **each phase engine
is programmed to resolve its records per its own rules.** The stream does not
encode resolution scope; it is only a sequence of records. The complexity lives
inside each phase engine, where the game designer's rules belong. A new phase
with different semantics can be added without touching the stream or any other
phase.

## The Flat Stream

When combat emits `Intent(CombatEngine, Retreat(Biff, Location(3,4)))`, how does
a later phase know that record applies to this combat, and how does the rest of
the turn see the damage Biff took? One tempting answer is to give combat its own
sub-stream: gather the participants, resolve combat in isolation, and merge the
results back.

That design defers the problem to the merge. Suppose Biff retreats to
Location(3,5), which holds the lair of the spider queen. She attacks. Her combat
must see Biff at 3hp, not the 8hp he started with. Whether it does depends
entirely on when the sub-stream's results were applied to working state — and
the merge is exactly as hard as the original problem.

The routing problem turns out to be smaller than it looks. Derived intent does
not need to find its way back to the combat that produced it. The record carries
its own context:

```text
Intent(CombatEngine, Retreat(Biff, Location(3,4)))
```

The retreat phase does not ask which combat produced this. It reads the intent,
reads working state for Biff, and resolves it. There is no routing problem
because derived intent is not a message back to the originating combat — it is a
new obligation on the main stream. The deeper principle:

> Derived intent defers resolution. It does not nest it.

Combat runs, emits facts and derived intent, and is done. Facts commit to
working state immediately. Derived intent waits for its phase. The spider queen
scenario then resolves with no merge at all:

```text
-- attack phase --
Fact(Wounded(Biff, 5hp))
  → State(Biff, Health(3hp))
Intent(CombatEngine, Retreat(Biff, Location(3,4)))

-- retreat phase --
Fact(Retreat(Biff, Location(3,4), Location(3,5)))
  → State(Biff, Location(3,5))
Intent(RetreatEngine, CheckEncounter(Biff, Location(3,5)))

-- encounter phase, reading State(Biff, Health(3hp), Location(3,5)) --
Intent(SpiderQueen, Attack(Biff, TargetOfOpportunity))
```

By the time the spider queen's attack resolves, working state already carries
3hp, because the `Wounded` fact committed before the retreat was processed.
Working state is always current. The stream stays flat: every phase reads the
same working state, every fact commits to it, and derived intent from any phase
enters the same queue. The cost is paid in phase design — retreat must run after
combat, encounter checks after retreat — but that ordering is explicit and
testable rather than hidden inside a merge.

## Monotonic IDs and Reader Positions

Every record — intent or fact — receives a unique, monotonically increasing ID.
IDs are never reused, and records are never modified or deleted: the log is
strictly append-only.

Progress through the log is a matter of position, not mutation. Each reader — a
phase engine, the report writer, a GM tool — tracks its own offset and asks the
same question: *what is the next record with an ID greater than mine?* "The next
unresolved thing" is exactly "the next record past my position." A reader that
resolves a record advances its own offset; the record it consumed is left
untouched, and any results it produces are appended with fresh IDs at the end.

From the engine's perspective there is nothing special about starting, resuming,
or replaying. It reads the next record past its position, resolves it, emits
results, and repeats.

## Completion Means Different Things

A turn is not "complete" in one universal sense. For the engine, it is complete
when no unresolved work remains. For the GM, when the results have been reviewed
and approved. For the player, when reports are available and next-turn orders
may be entered. The engine should not confuse these. It does not approve
results, publish reports, or open order entry. It drains the stream according to
the rules of the turn, and other systems decide what to do with the result.

## Remaining Design Questions

Several questions are genuinely open and deserve care before implementation
hardens.

**Who emits encounter checks?** In the spider queen trace, the retreat phase
emits the check. Alternatively a dedicated encounter phase could sweep all
changed locations. The answer depends on whether encounter triggering is a
property of retreat specifically or of any location change.

**What happens when derived — or accepted — intent outlives the turn?** The
order to a nonexistent provincial ruler is accepted but cannot resolve. Does it
wait for a future turn, expire, or produce an error fact?

**Can derived intent be rejected?** Some impossibilities surface only during
resolution, and derived intent has no player to reject it. If a retreat
destination is impassable, what fact does the engine emit, and what becomes of
working state?

Two questions from earlier drafts are now settled (see
[engine-contract.md](../references/engine-contract.md)):

**Where does phase ordering live?** *Resolved: the GM owns it, at runtime.* Phase
order is not static config but the GM's sequence of engine invocations — she may
run A, then B, then A again — and each invocation is recorded as a control record,
so the turn's construction is auditable. Each engine advances its own input cursor,
so re-running one drains only new records.

**How is randomness represented?** *Resolved: both, at different jobs.* Die-roll
outcomes are stored as facts, so folding facts rebuilds state without the seed or
engine; the seed exists so that *re-resolving* a turn reproduces the same facts.
See [seed-derivation.md](../references/seed-derivation.md).

## Conclusion

A PBEM engine should not begin with the database schema, the web interface,
authentication, or even units and provinces. It should begin with the turn: a
turn accepts intent, resolves it against current state, records facts, and
sometimes creates new intent, until nothing unresolved remains.

That model produces a clean boundary. Orders do not mutate state. The parser
does not resolve turns. The engine does not parse orders. Reports do not invent
outcomes. Each phase is a small engine that consumes records and emits records,
resolving them by its own rules. State remains necessary, but it is no longer
the whole story — it is a projection of the log, and a snapshot is just a
pointer into that log.

The event stream is not an implementation detail. It is the organizing
principle of the engine. The stream is the engine.
