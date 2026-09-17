# Shadow replay v1

Implements the [approved replay contract](../../../docs/shadow-replay-v1.md) as a
bounded, sequential, in-memory caller of the completed shadow processor. Construct
with `New()`, then call `Run([]shadowprocessor.Input)` with an already materialized
sequence. A constructed runner supports concurrent `Run` calls and retains only its
reusable processor. The zero value is not ready for use.

`New` constructs one `shadowprocessor.Processor` through its unchanged constructor.
Constructor failure returns a nil runner and a safe error. There is no public
configuration, dependency injection, callback, or additional envelope type. Output
values are the existing `shadowprocessor.AuditRecord` values; this package adds no
per-record status, identity, or provenance schema.

Admission finishes before the first `Process` call. Record count is checked first
(maximum 1,000, inclusive), then the total input string-byte budget (maximum
4,194,304 bytes, inclusive). The seven counted fields are `Source.Kind`,
`Source.Reference`, `Source.TextKind`, `Source.RecordingRef`, `Source.AttemptRef`,
`Source.Model`, and `Transcript`. Every occurrence counts, including duplicates and
strings that share backing storage. Lengths are Go string bytes. Admission arithmetic
rejects a field whose length exceeds the remaining budget before subtracting, so the
check cannot wrap. Nil and empty input slices return a non-nil zero-length result and
a nil error, with no processor calls. An over-limit sequence returns a nil result and
a safe error and processes zero records, including any otherwise valid prefix.

Each admitted input is passed unchanged to the processor exactly once, in caller
order, and the complete returned audit record is stored at the same output position.
Duplicate inputs and duplicate source references are retained. Native pointers, nil
versus empty collections, reasons, candidates, alternatives, rejected evidence,
ordering, associations, `StatusIndex` relationships, and original UTF-8 byte offsets
are preserved. The runner does not call dispatch or units itself, feed results across
records, or change `Source.Kind` to `replay`.

A record fails for runner purposes only when `Process` returns a non-nil error.
Validation failures and recovered native-stage panics are stored and the remaining
admitted inputs continue. After every admitted record has been attempted, a failed
run returns the complete ordered slice and `shadowreplay: one or more records failed`.
Native rejected, ambiguous, unresolved, unsupported, incomplete, and invalid-text
outcomes that return normally remain completed execution and do not cause that
aggregate error. Errors never contain transcript fragments, source-field contents,
credentials, panic payloads, stack traces, or native evidence.

Each `Run` keeps admission counters, failure flags, and results local. The runner
mutates neither the caller's inputs nor any reusable matcher. Returned slices and
native records are caller-owned; mutating one record must not affect another record,
run, or independent native branch. This is not a deep-clone API for caller-made
copies. Identical sequences are repeatable under the same pinned implementation and
embedded catalogs.

`ShadowOnly` remains the processor's descriptive marker. There is no incident
identity, assignment, apparatus state, CAD authority, serialization, persistence,
network or API surface, audio handling, callback, service registration, or production
wiring. Private constructor and invocation seams stay unexported.

Tests cover admission boundaries, overflow-safe counting, direct `Process` parity,
ordering, duplicates, failure continuation, synthetic recovered-stage records,
ownership, repeatability, and 1,000 concurrent `Run` calls. Run from `backend`:

```text
gofmt -l internal/shadowreplay
go test ./internal/shadowreplay ./internal/shadowprocessor
go vet ./...
go test ./...
go test ./internal/shadowreplay -cover
go test -race ./internal/shadowreplay ./internal/shadowprocessor
```

Race verification requires a supported C compiler; ordinary tests do not substitute
for a successful race-detector run. Local Windows race testing is blocked when GCC is
unavailable.
