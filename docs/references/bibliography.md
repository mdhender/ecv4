# Bibliography

Works referenced in the design notes.

---

**I Heart Logs: Event Data, Stream Processing, and Data Integration**
Jay Kreps, O'Reilly Media, 2014.
[Local copy](/docs/20170922-EB-I_Heart_Logs.pdf)

The short book that articulates the log as a universal data structure:
append-only, ordered, multiply readable with independent offsets. The PBEM
event stream is an application of this model. Kreps's treatment of state as a
projection from a log, and of snapshots as compaction checkpoints, underpins
the design described in
[Snapshots and the Event Pointer](../explanations/snapshots-and-the-event-pointer.md).

---

**Patterns for Things That Change With Time**
Martin Fowler.
[martinfowler.com/eaaDev/timeNarrative.html](https://martinfowler.com/eaaDev/timeNarrative.html)

Fowler's catalog of temporal patterns, including *Effectivity* — modelling a
value as valid over a date (or, for us, an ID) range. The clearest short
introduction to the idea that "what is true" depends on "as of when." Background
for [Storing State as Timebound Facts](../explanations/storing-state-as-timebound-facts.md).

---

**Developing Time-Oriented Database Applications in SQL**
Richard T. Snodgrass, Morgan Kaufmann, 2000. Available online.

The standard reference on valid-time and bitemporal modelling in SQL. Heavier
than needed for the design notes, but the authoritative treatment of period
columns, as-of queries, and the half-open interval convention.

---

**Temporal Features in SQL:2011**
Krishna Kulkarni and Jan-Eike Michels, *ACM SIGMOD Record* 41(3), 2012.

The concise summary of how application-time periods and system-versioned tables
entered the SQL standard. Useful for the conventional vocabulary
(`valid_from` / `valid_to`, `PERIOD`, `AS OF`) that the timebound-fact design
mirrors.
