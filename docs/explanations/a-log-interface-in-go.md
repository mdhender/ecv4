# A Log Interface in Go

{{< callout type="warning" >}}
**This is an exploration, not a commitment.** The interface below is a thinking
tool. It records the decisions we reached and *why*, so the reasoning survives
even if the code does not. Names, signatures, and the choices themselves are all
still open. Nothing here is settled.
{{< /callout >}}

The earlier essays argue that the engine is organized around a log — an
append-only, ordered, multiply-read sequence of intent and fact (see
[The Stream Is the Engine](the-stream-is-the-engine.md)). This
essay asks a narrower question: what does that log look like as a Go interface,
for a single Play-By-Email game?

## The Starting Point

We began with the smallest thing that could be called a log:

```go
type Log interface {
    Append(m Message) (ID, error)
}

type ID uint64

// Message can serialize and deserialize itself using the JSON v2 interfaces.
type Message interface {
    MarshalJSONTo(*jsontext.Encoder) error
    UnmarshalJSONFrom(*jsontext.Decoder) error
}
```

`ID` is the logical clock from
[Snapshots and the Event Pointer](snapshots-and-the-event-pointer.md):
monotonically increasing, unique, never reused. It guarantees ordering, not wall
time. `Message` is a payload that knows how to read and write its own JSON.

A log that only appends is a write-only file. Everything that follows is the
work of turning it into something the engine can actually use.

## Payload Is Not Record

The first gap appears as soon as you try to read. `Append` takes a `Message`,
but a reader cannot be handed a bare `Message` back. It needs the `ID` that was
assigned, and it needs to know whether the record has been tombstoned. The
payload the writer supplied is not the record the log stores.

So the log distinguishes the **envelope** it owns from the **payload** the caller
owns:

```go
type Record[M Message] struct {
    ID      ID
    Deleted bool
    Msg     M
}
```

The envelope carries only what the log is responsible for: the assigned ID and
the tombstone flag. (A wall-clock `AppendedAt` could live here too, but purely
for humans — the *logical* clock is the ID.) The payload stays opaque.

## Reading Is the Point

The whole premise — state as a projection, many readers at independent positions — is about reading. The interface needs three ways in:

```go
Read(id ID) (Record[M], error)   // random access: the as-of and audit case
Since(from ID) (Cursor[M], error) // sequential consumption from a position
Head() (ID, error)                // where the log currently ends
```

`Read` answers "what is at this position." `Since` is the workhorse: a phase
engine or a projection consumes records in order from where it left off.

```go
type Cursor[M Message] interface {
    Next() (Record[M], bool, error)
    Close() error
}
```

`Head` gives a reader the boundary so it knows when it has caught up.
Note what `Head` is *not*: it is not "the next unresolved record."
Resolution status is engine state, not log state.
The log reports where the sequence ends; the engine decides what counts as unfinished work.

We deliberately left out a live, blocking "follow" mode (`tail -f`).
A PBEM engine processes a turn to completion against a bounded log; it does not need a long-lived subscription.
That can be added later if a live dashboard ever wants it.

## Decoding Without a Registry

Reading back runs into a subtlety created by the self-marshaling `Message`
interface. `UnmarshalJSONFrom` is a method on an *existing value*. To decode a
record, the log must first construct the right concrete type, then call its
unmarshaler. How does it know which type to construct?

The textbook answer is a registry: a `Kind() string` discriminator on every
message and a `map[string]func() Message` the log consults.
We chose the simpler path instead: **make the log generic over an injected decoder.**

```go
func Open[M Message](store Store, decode func(*jsontext.Decoder) (M, error)) (Log[M], error)
```

The log holds no registry and learns no kinds.
At construction it is handed a `decode` function, and that function is the single seam where the game's message taxonomy lives.

This decision has a consequence worth stating plainly: with no `Kind` in the envelope, **the type discriminator has to live inside the message's own JSON.**
`MarshalJSONTo` writes the tag; `decode` peeks at it to pick the concrete type.
The discrimination problem does not vanish — it moves out of the log and into a contract between `MarshalJSONTo` and `decode` that the log does not enforce.

Two honest caveats:

- **The generics buy less than they appear to.** Because one stream carries many
  concrete message types, `M` is an *interface* — the game's message sum type —
  not a concrete struct. A reader still type-switches on `record.Msg`. What
  generics give is a tighter boundary: `Append` won't accept a foreign
  `Message`, and `Read` returns the game's sum type rather than bare `Message`.
  If that tightening isn't worth the ceremony, a non-generic `Log` with
  `decode func(*jsontext.Decoder) (Message, error)` is equivalent in power.
- **This will not scale.** An injected decoder is fine for one game's message
  set. A registry, or per-record type tags resolved centrally, would serve a
  larger or pluggable system better. We accepted the limit on purpose: at a
  single-game scale, the simpler design wins.

## Who Owns Position

The design calls for many readers, each at its own offset. The fork was whether
the log *manages* those offsets (Kafka-style consumer groups) or readers track
their own.

We kept the log a pure sequence. Each reader owns its position. A projection
already has to record its last-applied ID — that ID is the shared coordinate
between the log and the snapshot store described in
[Storing State as Timebound Facts](/explanation/storing-state-as-timebound-facts.md).
Duplicating that bookkeeping inside the log would buy nothing. Managed offsets
can be a later convenience layer if one is ever needed.

## Appending

`Append` does two things as one atomic step: it stores the message and it
advances the head.

```go
Append(M) (ID, error)          // atomic: store + advance head
AppendBatch([]M) ([]ID, error) // atomic: N consecutive IDs, one head advance
```

Three properties we committed to:

- **Only `Append` advances the head.** `Delete` and `Rewind` never move it. This
  is what keeps the ID counter monotonic and the never-reuse rule intact: a
  rewound suffix's IDs can never be handed out again, because nothing but
  `Append` allocates, and `Append` only ever moves forward.
- **Batch append is atomic.** A single phase often emits several facts plus
  derived intent. If a crash could leave half of them in the log, the projection
  and the log would disagree. `AppendBatch` lands all or none.
- **`Append` is durable before it returns.** The "log is authoritative" claim
  depends on it: a returned ID must mean *committed*.

`Append` is deliberately *not* idempotent. Every call is a new record with a new
ID — that is the whole point of an append-only log. At-most-once append on a
client key would be a separate mechanism, and we kept it out.

## Deleting

`Delete` tombstones one record, logically. It never physically removes anything,
and it never moves the head.

```go
Delete(id ID) error
```

It returns only an error — if you called `Delete(5)`, you already know what
record 5 is; there is no reason for the log to hand it back.

`Delete` is **idempotent with respect to tombstoning**, which falls out
naturally from it being a flag flip:

- Delete a live record → tombstone it, return `nil`.
- Delete an already-tombstoned record → no-op, return `nil`. Same final state,
  so no error.
- Delete an ID that was never assigned (`> Head()`, or `0`) → `ErrNotFound`.
  This is not a repeat-delete; it is a bug or corruption signal, and swallowing
  it would hide a real error.

That last line is clean only because of two invariants we already hold: deletes
are logical, and `Append` assigns IDs contiguously. So every ID in `[1, Head()]`
exists as a record — live or tombstoned — and there are no gaps. "Exists but
tombstoned" and "never existed" are cleanly separable, and only the second is an
error.

Idempotency is not pedantry here. It makes retries and crash recovery safe: a
`Delete` that timed out can be reissued without reasoning about whether the first
landed.

## Rewinding

Rewinding is a stream operation
([Storing State as Timebound Facts](storing-state-as-timebound-facts.md)
develops why). `Delete` is the primitive; `Rewind` is a method, because only the
implementation can be atomic and touch the log's metadata. It delegates to the
same tombstoning `Delete` performs.

```go
Rewind(to ID, tombstone func(M) bool) (Cursor[M], error)
```

The predicate is the interesting part. `Rewind` walks the records with `id > to`,
decodes each through the injected decoder, calls `tombstone(msg)`, and marks the
ones that return `true`. The log inspects message *contents* without ever knowing
a *kind* — the knowledge lives in the caller's predicate.

This quietly recovers a rule we had written off as out of scope: *preserve
accepted player intent; discard derived turn work.*

```go
// keep player-submitted intent in the suffix; tombstone what the engine derived
log.Rewind(turnStart, func(m M) bool { return m.IsDerived() })
```

`IsDerived()` is an application-level method on the game's message type, not a
log concern. The log stays kind-agnostic; the predicate carries the
discrimination.

`Rewind` returns a cursor over exactly what *this* call tombstoned. The cursor
reads with include-deleted semantics (those records are now tombstoned, so the
normal skip-deleted path would hide them) and yields in ID order. Its value is
**audit and observability** — a "what did this rewind discard" report, debug
logging, a downstream event. It is precise in a way that scanning `Since(to)` for
`Deleted == true` afterward is not: that scan cannot tell records this rewind
killed from ones an earlier `Delete` already killed.

It is worth being clear about what the cursor is *not*: it is not required for
the snapshot-store rebuild. That reconciliation keys off `to` alone — discard
projection rows with `start_at > to`, reopen rows with `end_at > to` — and stays
correct precisely because the records you *preserve* are intent, which never
mutated the projection in the first place. The cursor is a nice-to-have, not a
correctness dependency.

One behavior to expect: after a predicate rewind, **locality breaks but
correctness does not.** A preserved player-intent record keeps its low ID, while
re-resolution appends fresh derived records at high IDs. A turn's intent and its
facts are no longer adjacent in the log. That is fine — state is an ID-ordered
fold over *live* facts, and projections skip tombstones, so the final state is
unchanged.

## What the Log Does Not Do

Keeping the boundary clean meant leaving things out on purpose:

- **Resolution status.** "Unresolved work" is engine state. The log returns
  ordered records; the engine tracks what it has resolved.
- **Turn boundaries.** A turn marker is just another record, not a first-class
  log concept.
- **Projections, snapshots, reporting.** Separate stores. The log knows nothing
  of timebound tables or report aggregates.
- **Managed consumer offsets, optimistic-concurrency guards, idempotency keys,
  live follow.** Each is a reasonable future layer; none earns its place in v1.

The concurrency contract is the matching simplification: a single writer is fine
for a turn engine, and we document that rather than engineer around it.

## The Interface, Assembled

```go
type Log[M Message] interface {
    Append(M) (ID, error)
    AppendBatch([]M) ([]ID, error)
    Read(id ID) (Record[M], error)
    Since(from ID) (Cursor[M], error)
    Head() (ID, error)
    Delete(id ID) error
    Rewind(to ID, tombstone func(M) bool) (Cursor[M], error)
    Close() error
}

type Record[M Message] struct {
    ID      ID
    Deleted bool
    Msg     M
}

type Cursor[M Message] interface {
    Next() (Record[M], bool, error)
    Close() error
}
```

## Open Questions

- **Is the generic boundary worth the ceremony,** or is a non-generic `Log` with
  an injected `decode func(*jsontext.Decoder) (Message, error)` the honest choice
  given `M` is always an interface anyway?
- **Where does the discriminator live in the message JSON,** and how do
  `MarshalJSONTo` and `decode` stay in agreement without the log enforcing it?
- **Does `Since` need an include-deleted option** for audit readers, or is the
  Rewind cursor enough?
- **What is `Store`?** This essay treats the backing store as a hole. Whether it
  is SQLite, flat files, or something else — and how `Append`'s durability is
  actually guaranteed — is its own discussion.
