---
title: "Snapshots and the Event Pointer"
linkTitle: "Snapshots and the Event Pointer"
weight: 5
toc: true
---

The [first essay](/explanation/intent-resolution-and-fact.md) describes three roles for
state: authoritative history in the event stream, working state in memory
during resolution, and a durable snapshot at the end of each turn. That
framing is useful, but it leaves the relationship between the three imprecise.

This essay sharpens that relationship around one idea: a snapshot is not a
bag of attribute values. A snapshot is an event pointer.

## The Stream Is a Log

The event stream described in the earlier essays is a log in the sense that
Jay Kreps uses in *I Heart Logs* (see [Bibliography](/reference/bibliography.md)):
append-only, ordered, and multiply readable. Every record in the log — intent
or fact — receives a monotonically increasing unique ID. Readers advance
through the log independently, each maintaining its own pointer.

That last point matters. The movement phase engine has a pointer. The combat
phase engine has a pointer. The report writer has a pointer. The GM audit tool
has a pointer. None of them interfere with each other's position. They all read
the same log.

The engine does not process records in parallel. It advances through the log
sequentially, one record at a time, in ID order. Every reader must observe
every entry in the order it was added. There are no shortcuts and no skipping.

## State Is a Projection

Given that model, state has a precise definition:

> The state at pointer P is the sum of the initial game state and every fact
> with ID ≤ P.

Intent records appear in the log but do not contribute to state. State is the
fold of facts only. Two different pointers may yield identical state if
everything between them is intent records — but the pointer positions are
still distinct.

The initial game state is not a special case. The game starts with an empty
log. Setup emits facts — map tiles, starting positions, unit attributes,
faction definitions — and the pointer advances through all of them. When the
GM opens order entry, intents begin mixing in. The engine treats setup facts
and resolution facts identically. There is no "before the game starts" state
outside the log.

## A Snapshot Is an Event Pointer

A snapshot is a (pointer, projected state) pair.

The pointer says: the world exists exactly as it did at this position in the
log. We are not aware of any fact before it, nor any fact after it.

The projected state is a cache of the fold from the beginning of the log to
that pointer. It exists for performance — so that the next turn does not have
to replay every fact since the game began. It is not authoritative. The log
is authoritative. The projected state can always be reconstructed by replaying
facts from any earlier snapshot's pointer to this one.

This is what the [first essay](/explanation/intent-resolution-and-fact.md) means when it
says a snapshot is "not a replacement for the stream — it is a summary." The
summary is a cache of a computation the log can reproduce.

## Phase Engines and Local Snapshots

A phase engine that resolves a batch of intents simultaneously — as discussed
in [Derived Intent and the Flat Stream](/explanation/derived-intent-and-the-flat-stream.md)
— takes a local snapshot at the start of its batch. That snapshot is a pointer
into the log at the moment the batch begins. The phase engine resolves all
intents in the batch against state at that pointer. It does not see facts
emitted during its own batch.

The local snapshot is temporary. It is not persisted. When the batch
completes, the phase engine's local pointer is discarded. Working state has
been updated by the facts emitted during the batch. The next phase engine
begins from a pointer that includes those facts.

This is how the [worked example](/explanation/a-turn-traced.md) produces correct results:
the attack phase reads Skar's defense bonus from working state because the
defense phase ran first and its fact was committed before the attack phase's
pointer was established.

## Multiple Readers

Each phase engine, report writer, and tool maintains its own pointer. This is
the same model Kreps describes for distributed log consumers — each consumer
has an independent offset into the log.

In a distributed system, this produces Kafka-scale complexity. Here, the
readers are not arbitrary distributed consumers. They are phase engines running
in a controlled sequence, once per turn. The pointer model is the same. The
operational complexity is bounded.

## Logical Deletes and Rewinding

Records are never physically deleted from the log. IDs are never reused.

If a record must be removed — during a rewind, for example — it is logically
deleted: marked with `is_deleted = true` and excluded from the state fold. The
ID is retired. The gap in the sequence is permanent evidence that a record
existed and was discarded.

A rewind to pointer P is a specific operation:

1. Every record with ID > P is logically deleted.
2. The next-available-ID counter continues from its current value. It does not
   reset.
3. New resolution begins from P, emitting records with fresh IDs that continue
   past the old maximum.

The result looks like this in the log:

```text
1  Intent(Anne, Move(Biff, Location(3,4)))
2  Fact(Arrived(Biff, Location(3,4)))
3✗ Fact(Wounded(Biff, 5hp))              ← logically deleted
4✗ Intent(CombatEngine, Retreat(...))    ← logically deleted
5  Fact(Wounded(Biff, 3hp))              ← new resolution
6  Intent(CombatEngine, Retreat(...))    ← new resolution
```

Records 3 and 4 are tombstones. Their IDs are retired. The gap from 3–4 to 5
is permanent evidence of the rewind.

Player-submitted intents before the rewind point survive untouched. Only
derived work — facts and derived intents produced by the engine — is
tombstoned. This is the operational form of the rule from the
[first essay](/explanation/intent-resolution-and-fact.md):

> Preserve accepted player intent; discard derived turn work.

## What a Snapshot Is Not

A snapshot is not a database checkpoint in the crash-recovery sense. It does
not need to be written atomically during turn processing. It is written once,
at the end of a turn, after all facts have been committed and the GM has
approved the results.

A snapshot is not a replacement for the log. If the snapshot is lost or
corrupted, it can be reconstructed by replaying facts from the previous
snapshot's pointer forward. The log is the source of truth.

A snapshot is not a description of what the world contains. It is a
description of where we are in the history of what happened. The attribute
values are a consequence of that position, not the definition of it.
