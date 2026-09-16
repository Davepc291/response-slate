# Shadow processor v1

Implements the [approved contract](../../../docs/shadow-processor-v1.md) as an
offline, caller-owned in-memory composition boundary. Construct with `New()`, then
call `Process(Input)` for each explicitly supplied synthetic or replay transcript.
A constructed processor supports concurrent calls and retains only immutable native
pipelines. The zero value is not ready for use.

`Source.Kind` must be `synthetic` or `replay`, `Reference` must be nonblank, and
`TextKind` must be `synthetic`, `raw_model`, `normalized`, or `human_reference`.
Optional recording, attempt, and model references are opaque provenance only;
recording references are identifiers, not filesystem paths. Human reference text
is caller provenance, not verified truth. Comparison requires independent calls.

Validation checks reference, source kind, text kind, then transcript size. Empty
text and exactly 65,536 bytes are valid; larger text fails without truncation.
Invalid envelopes return the complete input, a failed input stage, skipped native
stages, nil result pointers, and a safe fixed-code error.

Construction runs dispatch first, then units, and returns no processor on failure.
Valid calls record `input`, `dispatch`, and `units` in that order, passing identical
unchanged transcript bytes independently to both native pipelines. Complete native
results are assigned directly, preserving nil/empty collections, evidence order,
alternatives, candidates, reasons, associations, and UTF-8 byte offsets. No result
informs the other branch; there is no reconciliation or aggregate operational state.

Stages use `completed`, `failed`, or `skipped`. Fixed failure codes are
`missing_source_reference`, `invalid_source_kind`, `invalid_text_kind`,
`transcript_too_large`, and `native_stage_panicked`. Completed stages have an empty
failure code. Native invalid, rejected, unresolved, unsupported, incomplete, and
ambiguous results remain completed execution outcomes, not operational acceptance.

Each native invocation has its own panic boundary. A panic leaves that result nil,
records failure, and permits the independent branch to complete. Any native panic
returns a safe error alongside available results. Panic payloads are not formatted,
logged, or retained. Unreturned internal results cannot be recovered; fatal runtime
termination and hangs are outside this guarantee. Test seams remain private and
are neither stored callbacks nor mutable global hooks.

Every call returns fresh native results and a fresh stage slice. Mutations cannot
affect other calls or reusable matchers. Ordinary caller copies can share pointers
and slices: this envelope is not a deep-clone API, and callers synchronize their
own shared mutations. Callers own replay history and place concurrent results in
their original input positions. Duplicate inputs and references are retained.
Determinism assumes the same implementation and embedded catalogs; callers retain
external revision provenance because `shadow-processor-v1` is not a build ID.

Native disagreements remain visible: unit recognition may reject `engine 2 on
scene` while unit status proposes an association. CLEAR stays unresolved, ON AIR
and TRAINING remain separate, and conflicts retain ambiguity. Native grammar,
Unicode validation, and speech-role limitations remain independent; the processor
does not promise uniform speech-role safety.

`ShadowOnly` is always true and is descriptive, not a security boundary. There are
no database, filesystem, network, API, frontend, audio, CAD, operational-model,
callback, logging, persistence, or production-wiring dependencies. No batch API,
serialization guarantee, durable audit claim, timestamps, identifiers, cache, or
processor-owned history is provided.

Tests cover complete standalone parity across the native catalogs and edge cases,
validation, resource boundaries, offsets, private failure seams, ownership, replay
ordering, determinism, and 1,000 concurrent calls. Run from `backend`:

```text
go test ./internal/shadowprocessor -cover
go test -race ./internal/shadowprocessor
```

Race verification requires a supported C compiler; ordinary tests do not substitute
for a successful race-detector run.
