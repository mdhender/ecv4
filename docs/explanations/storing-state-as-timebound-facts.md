# Storing State as Timebound Facts

This is a design exploration, not an authoritative specification.
It sketches one way to store the snapshot described in [Snapshots and the Event Pointer](snapshots-and-the-event-pointer.md) so that state as of any point in the stream can be recovered with a query.

## Two Stores, One Coordinate

The earlier essays describe two things that are easy to conflate:

- The **stream** (the log) — the authoritative, append-only history of intent and fact.
- The **snapshot** — the projected state the engine reads when resolving the next record.

This essay treats them as living in **separate stores**.
The log may sit in one system; the snapshot may sit in another, with a different shape and different technology.
Nothing here assumes they are colocated.
They share exactly one thing: the **event ID**.

The event ID is a logical timestamp.
It does not record wall-clock time.
It guarantees *ordering* — record 12 was appended after record 8 and before record 15 — and nothing else.
Because the two stores share the event ID as a coordinate, the snapshot store can describe its contents in terms of positions in the log without ever holding the log itself.

That shared coordinate is what makes the rest of this design work.

## Background: Valid-Time Temporal Tables

The technique here is not new.
It is **valid-time temporal modelling**, the same idea behind SQL:2011 *application-time periods*: a row carries the range over which its value is true, and queries ask what was true "as of" some point.
The literature calls the range columns `valid_from` / `valid_to`; Martin Fowler calls the pattern *Effectivity*.
(See the [Bibliography](../references/bibliography.md) for Fowler, Snodgrass, and the SQL:2011 summary.)

The only twist for a PBEM engine is the axis.
Ordinary temporal tables measure validity in dates. Here, validity is measured in **event IDs**.
A fact is true across a range of stream positions, not a range of calendar time.

We name the range columns `start_at` and `end_at`.

## The Timebound Fact

A timebound fact is true over a half-open interval of event IDs:

```text
start_at <= as_of < end_at
```

`start_at` is the ID at which the fact becomes true. `end_at` is the ID at which it stops being true — a position *outside* the range.
The fact is in effect for every ID from `start_at` up to but not including `end_at`.

Two query parameters use the same predicate:

- **`current_ID`** — the position the engine has reached while processing the
  stream. "Is this fact true right now?" is `start_at <= current_ID < end_at`.
- **`as_of`** — any chosen position, not necessarily the current one. "Was this
  fact true as of when ID *k* was processed?" is `start_at <= k < end_at`.

They are the same test. `current_ID` is just `as_of` pinned to where processing
happens to be.

A fact that is true until the end of time uses a sentinel for `end_at`:
`MaxInt`. No `NULL` values. The predicate stays uniform — `as_of < MaxInt` is
always true — so the currently-true row needs no special-casing in queries.

It can feel unnatural that `end_at` names an ID where the fact is *not* true.
The payoff is chaining. When a value changes, the old row's `end_at` and the new
row's `start_at` are the *same* number. Intervals butt against each other with
no gap and no overlap, and no value ever has to be offset by one.

```text
health
entity | value | start_at | end_at
Biff   | 8hp   | 1        | 15        ← true for IDs 1..14
Biff   | 3hp   | 15       | MaxInt    ← true for IDs 15..
```

At exactly ID 15 the new row is true (`15 <= 15 < MaxInt`) and the old row is
not (`1 <= 15 < 15` is false). The boundary belongs to the new row, cleanly.

## Grain: One Attribute Per Table

The goal is to keep each table's context small: **one attribute per timebound table** — a `health` table, a `location` table, a `defense_bonus` table.
Tight grain keeps intervals tight.
A unit that moves every turn but is never wounded churns rows in `location` and leaves `health` untouched.

This is a goal, not an imperative.
Some attributes naturally travel together and may share a table; the design does not forbid it.
But the default lean is narrow.

## Timebound Rows Are Not Stream Records

This is the distinction the design rests on, and it is worth stating plainly.

A **stream record** is a point event. `Fact(Wounded(Biff, 5hp))` appended at ID
15 records that something *happened* at one instant. It is immutable. It lives in
the log.

A **timebound row** is interval state. `(Biff, 3hp, [15, MaxInt))` records that a
value *holds* across a span of IDs. It lives in the snapshot store, a different
store entirely.

The stream record is the cause; the timebound row is the queryable consequence.
The `Wounded` fact at ID 15 is what *closes* Biff's `8hp` interval and *opens*
his `3hp` interval. One immutable event in the log produces one close and one
insert in the snapshot store.

The snapshot store is therefore a **projection** of the log. It holds no
information the log could not regenerate. It exists because answering "what is
Biff's health as of ID 12?" should be an indexed lookup, not a replay of the
entire stream.

## The CRUD Lifecycle

Because timebound rows are interval state, the usual CRUD operations take a
specific shape.

### Create

A fact comes into effect with no predecessor. Insert one open-ended row.

```text
-- Biff enters play at setup, ID 1
INSERT health (Biff, 8hp, start_at=1, end_at=MaxInt)
```

### Read

State as of a position is the row whose interval contains it, per table.

```text
-- Biff's health as of ID 12
SELECT value FROM health
WHERE entity = 'Biff' AND start_at <= 12 AND 12 < end_at
-- → 8hp
```

"Current" state is just `as_of = current_ID`, or equivalently the row with `end_at = MaxInt`.

### Update

A value changes.
This is never an in-place edit of `value`.
It is **close plus insert**: close the open row at the change ID, then insert the successor.

```text
-- Biff is wounded at ID 15; health 8hp → 3hp
UPDATE health SET end_at = 15
  WHERE entity = 'Biff' AND end_at = MaxInt
INSERT health (Biff, 3hp, start_at=15, end_at=MaxInt)
```

History is preserved.
The `8hp` interval still answers correctly for IDs 1..14.

### Delete

A fact stops being true with no successor — a posture expires, an item is destroyed, an entity dies.
**Close the open row; insert nothing.**

```text
-- Skar's defensive posture, created at the defense phase, ID 12
INSERT defense_bonus (Skar, +18, start_at=12, end_at=MaxInt)

-- cleared when the next turn begins, ID 40
UPDATE defense_bonus SET end_at = 40
  WHERE entity = 'Skar' AND end_at = MaxInt
```

After ID 40, an as-of query for Skar's defense bonus returns nothing — the fact is no longer true — but every historical interval remains queryable.
This is a temporal delete, not a physical one.

Physical deletion of timebound rows does not happen in normal operation.
The only thing that removes rows is rebuilding the projection (below).

## A Snapshot Is a Query

Given timebound tables, the snapshot from the lead essay — a (pointer, projected state) pair — needs no stored blob.
The pointer is an `as_of` value.
The projected state is the result of running the as-of predicate across every timebound table at that value.

```text
state as of k = for each table: the row where start_at <= k < end_at
```

You do not snapshot at discrete checkpoints and hope you picked the right ones.
You reconstruct state as of *any* ID with one query per table.
Many snapshots for the price of one set of tables.

{{< callout type="info" >}}
**Reporting may want a different projection.**
The timebound store is a projection of the log shaped for one access pattern: "what is true as of ID *k*."
Reporting has a different access pattern — summing, consolidating, and grouping by *turn in the game* — and may justify its own projection in the data-warehouse tradition: aggregate tables keyed by turn, built as a separate reader of the same log.
That is out of scope here and deserves its own essay; the point for now is only that the as-of store is *one* projection, not the only one, and reporting need not be forced through it.
{{< /callout >}}

## Rewinding Is a Stream Operation, Not a State Operation

Rewinding belongs to the **stream**.
The stream's records carry logical deletes: to rewind to position P, the engine marks every record with ID > P as `is_deleted` and retires those IDs, exactly as [Snapshots and the Event Pointer](snapshots-and-the-event-pointer.md) describes.
The log is where rewind *happens*.

The snapshot store does not rewind.
It is **updated to agree** with the rewound log.
Because the two stores are independent, this is a deliberate reconciliation, not a side effect.

The reliable method follows from the projection property: the snapshot store holds nothing the log cannot regenerate, so **discard the projection from P forward and rebuild it** by replaying the surviving facts.
Concretely, after a rewind to P:

- A timebound row with `start_at > P` was opened by a now-deleted fact.
  It should not exist.
  Discard it.
- A timebound row whose `end_at > P` was *closed* by a now-deleted fact.
  Its closing event is gone, so reopen it — set `end_at` back to `MaxInt`.

Rewinding to ID 14, before Biff's wound at 15, returns the `health` table to:

```text
health
entity | value | start_at | end_at
Biff   | 8hp   | 1        | MaxInt
```

The `3hp` row (opened at 15) is discarded; the `8hp` row (closed at 15) is reopened.
The snapshot store now reflects the world the rewound log describes.

The surgical version above is an optimization.
The mental model to hold is simpler: the log is authoritative, the snapshot store is a projection, and any disagreement is resolved by rebuilding the projection — never by trusting the snapshot store over the log.

## Compaction

History accumulates.
Every value change leaves a closed interval behind, and the timebound tables grow without bound.

Compaction is the same idea Kreps describes for logs.
Establish a baseline at some floor ID B: materialize the full as-of-B state as a set of rows starting at B, and discard closed intervals that end at or before B.
The snapshot store then answers as-of queries for any ID ≥ B, and the log below B can be archived.
The cost is that you can no longer reconstruct fine-grained state before the floor — a deliberate trade of history for size.

## What This Is Not, and What Is Open

This design does not assume a particular store for either the log or the snapshot.
It assumes only that both can speak in event IDs.

It does not address persistence formats, indexing strategy, or how the snapshot store is kept transactionally consistent with the engine's in-memory working state during a turn run.

Several questions remain open:

- **Where does the baseline floor sit, and who advances it?**
  Compaction is a policy decision, not an engine mechanism.
- **How are independent stores kept in agreement after a crash mid-turn?**
  If the log commits a fact but the snapshot store does not record the corresponding close-and-insert, the two disagree.
  Rebuild resolves it, but detection is its own problem.
- **Does every attribute belong in a timebound table?**
  Some derived values may  be cheaper to recompute than to version.
- **How does the half-open interval interact with simultaneous resolution?**
  When a phase engine resolves a batch against one snapshot ([nothing happens simultaneously](the-stream-is-the-engine.md#nothing-happens-simultaneously)), several facts may share a `start_at`.
  The model allows it, but the consequences for as-of queries within a batch deserve their own treatment.
