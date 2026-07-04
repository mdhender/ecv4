# Cursor or Iterator: Reading the Log

{{< callout type="warning" >}}
**This is an exploration, not a commitment.** It weighs two ways to return a
sequential read from the log. Either could win; the point is to record the
trade-offs while they are fresh.
{{< /callout >}}

[A Log Interface in Go](a-log-interface-in-go.md) gave `Since` a custom cursor type:

```go
Since(from ID) (Cursor[M], error)

type Cursor[M Message] interface {
    Next() (Record[M], bool, error)
    Close() error
}
```

Go 1.23 added range-over-func iterators and the `iter` package, which opens a
second option: `Since` could return an iterator instead. This essay compares the
two by reading the same thing both ways — a movement phase draining the move
intents for a turn — and then weighs what changes.

## The Cursor Style

A cursor is pull-based. The caller drives it, checks for the end, checks for
errors, and is responsible for releasing it.

```go
func (p *MovementPhase) Run(log Log[GameMessage], from ID) error {
    cur, err := log.Since(from)
    if err != nil {
        return err
    }
    defer cur.Close()

    for {
        rec, ok, err := cur.Next()
        if err != nil {
            return err
        }
        if !ok {
            break
        }

        if move, isMove := rec.Msg.(MoveIntent); isMove {
            p.resolveMove(move)
        }
    }
    return nil
}
```

Three obligations sit on the caller here: check `err`, check `ok` for the end,
and `defer cur.Close()`. The cursor is a value you hold, which has its own appeal
— you can store it in a struct, hand it to another function, and consume it in
pieces across call boundaries.

## The Iterator Style

An iterator is push-based. The generator drives itself and the caller writes an
ordinary `range` loop. The natural signature carries the error alongside each
element:

```go
Since(from ID) iter.Seq2[Record[M], error]
```

`iter.Seq2[Record[M], error]` is just
`func(yield func(Record[M], error) bool)`. The same phase becomes:

```go
func (p *MovementPhase) Run(log Log[GameMessage], from ID) error {
    for rec, err := range log.Since(from) {
        if err != nil {
            return err
        }
        if move, isMove := rec.Msg.(MoveIntent); isMove {
            p.resolveMove(move)
        }
    }
    return nil
}
```

The `ok` that the cursor returned is gone — it is absorbed into the range
mechanics, which end the loop when the generator returns. Two of the caller's
three obligations remain (check `err`), and the third — cleanup — moves into the
generator, which is the interesting part.

## The Cleanup Difference

Here is the iterator's implementation sketch. The cleanup is a `defer` inside the
generator, and it runs no matter how the loop ends — exhaustion, `return`, or an
early `break`:

```go
func (l *fileLog[M]) Since(from ID) iter.Seq2[Record[M], error] {
    return func(yield func(Record[M], error) bool) {
        f, err := l.openAt(from)
        if err != nil {
            yield(zero[Record[M]](), err) // setup error rides the stream
            return
        }
        defer f.Close() // fires on exhaustion, return, or caller break

        for l.more(f) {
            rec, err := l.decodeNext(f)
            if err != nil {
                yield(zero[Record[M]](), err)
                return
            }
            if !yield(rec, nil) {
                return // caller broke; the defer still runs
            }
        }
    }
}
```

This is the strongest argument for the iterator. With the cursor, `Close()` is
the caller's job, and it is easy to skip on an early exit:

```go
for {
    rec, ok, err := cur.Next()
    if err != nil {
        return err // ← did we forget cur.Close()? (defer saves us, if we wrote it)
    }
    if !ok {
        break
    }
    if rec.ID == target {
        return nil // ← and here?
    }
}
```

`defer cur.Close()` covers those exits *if* the caller remembered to write it.
The generator's `defer f.Close()` cannot be forgotten by the caller, because the
caller never sees the file at all. The cleanup guarantee moves from convention to
structure.

## The Error Difference

The iterator's cost is error discipline. With the cursor, the end of the stream
(`ok == false`) and an error are separate signals. With the iterator they share
a channel: every element arrives as `(Record[M], error)`, and the caller must
check `err` on each turn of the loop.

```go
for rec, err := range log.Since(from) {
    if err != nil {
        return err // skip this and you process a zero-valued Record
    }
    use(rec)
}
```

Forget that check and you operate on a zero `Record`. The cursor's
`Next() (Record[M], bool, error)` made the error harder to ignore because it was
a distinct return value you had to name.

The setup error is the other wrinkle. The cursor reports a bad `from` or an
unavailable store immediately, as the second return value of `Since`. The
single-value iterator has nowhere to put that except the first yielded
`(zero, err)` — so the error does not surface until you start ranging. If
fail-fast matters more than a clean call site, you are pushed back to
`(iter.Seq2[Record[M], error], error)`, two error channels, which is uglier than
the cursor you started with.

## You Do Not Lose Pull Semantics

Returning an iterator does not foreclose cursor-style consumption. `iter.Pull2`
turns any `Seq2` back into a pull-based pair, including the `stop` that plays the
role of `Close`:

```go
next, stop := iter.Pull2(log.Since(from))
defer stop()

rec, err, ok := next()
for ok {
    if err != nil {
        return err
    }
    use(rec)
    rec, err, ok = next()
}
```

So the iterator is the more fundamental form. A consumer that genuinely needs to
hold a half-drained stream as a value — passing it across function boundaries,
consuming it in pieces — can recover exactly that with `iter.Pull2`. The reverse
is not true: a `Cursor` cannot be dropped into an ordinary `range` loop.

Position checkpointing survives either way. A reader records the last `Record.ID`
it saw — the record carries its ID regardless of how it arrived — which is the
same coordinate the snapshot store uses and fits the "readers own their position"
decision from the interface essay.

## What Choosing the Iterator Would Imply

- **`Rewind` should match.**
  It returns `Cursor[M]` today; for consistency it would become `iter.Seq2[Record[M], error]`.
  Its include-deleted behavior is unaffected — that is simply what the generator chooses to yield.
- **The floor becomes Go 1.23.**
  Range-over-func and `iter` are not available earlier.
  Not a constraint for a greenfield engine, but worth naming.

## Where This Leaves It

The iterator is the better default *if* the house style accepts
`iter.Seq2[Record[M], error]` and the per-iteration error check. The cleanup
guarantee alone probably earns it: a reader cannot leak the underlying file or
rows by forgetting a `Close()`, because it never holds them.

The cursor stays preferable in one case — when a partially consumed stream is a
first-class value passed around the program, rather than something drained in a
single loop. Even then, `iter.Pull2` narrows the gap to almost nothing.

Neither is decided. What is settled is the shape of the trade: the iterator
trades a clean call site and structural cleanup for a shared value/error channel;
the cursor trades caller-managed cleanup for separate end and error signals and a
stream you can hold.
