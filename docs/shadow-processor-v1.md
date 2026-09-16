# Shadow Processor Contract v1

Status: Approved for documentation on September 16, 2026

Implementation status: Not started

Contract version: `shadow-processor-v1`

## Purpose

This document defines the smallest safe Step 6 read-only shadow processor for
Response Slate. The processor accepts one explicitly supplied synthetic or replay
transcript, runs the existing dispatch and unit pipelines independently, and
returns their complete native results with an execution audit record.

The processor does not create incidents, change apparatus state, update CAD, or
establish operational truth. It is an offline composition and audit boundary only.

The approved upstream contracts remain authoritative:

- [Dispatch interpretation contract](dispatch-interpretation-v1.md)
- [Unit vocabulary contract](unit-vocabulary-v1.md)
- [Unit-status vocabulary contract](unit-status-vocabulary-v1.md)
- [Dispatch interpretation package](../backend/internal/dispatchinterpretation/README.md)
- [Unit pipeline package](../backend/internal/unitpipeline/README.md)

## Approved package boundary

The future implementation is limited to:

```text
backend/internal/shadowprocessor/
    processor.go
    processor_test.go
    README.md
```

This documentation milestone does not authorize those files to be created yet.

No CLI, replay-file loader, database adapter, API handler, WebSocket handler,
serialization contract, production registration, or background service is part of
v1.

## Approved public interface

```go
func New() (*Processor, error)
func (p *Processor) Process(input Input) (AuditRecord, error)
```

The approved contract types are:

```go
type SourceKind string

const (
    SourceSynthetic SourceKind = "synthetic"
    SourceReplay    SourceKind = "replay"
)

type TextKind string

const (
    TextSynthetic      TextKind = "synthetic"
    TextRawModel       TextKind = "raw_model"
    TextNormalized     TextKind = "normalized"
    TextHumanReference TextKind = "human_reference"
)

type Source struct {
    Kind      SourceKind
    Reference string
    TextKind  TextKind

    // Optional opaque caller-supplied provenance.
    RecordingRef string
    AttemptRef   string
    Model        string
}

type Input struct {
    Source     Source
    Transcript string
}

type Result struct {
    Dispatch *dispatchinterpretation.Result
    Units    *unitpipeline.Result
}

type StageRecord struct {
    Stage       string
    Outcome     string
    FailureCode string
}

type AuditRecord struct {
    Version    string
    ShadowOnly bool
    Input      Input
    Result     Result
    Stages     []StageRecord
}
```

Pointers distinguish an unavailable branch from a successfully returned native
result containing zero values or nil collections. They do not make the envelope a
deep-clone API.

## Source metadata

Every input requires:

- `Source.Kind` equal to `synthetic` or `replay`.
- A nonblank opaque `Source.Reference`.
- `Source.TextKind` equal to `synthetic`, `raw_model`, `normalized`, or
  `human_reference`.

`RecordingRef`, `AttemptRef`, and `Model` are optional opaque provenance. They
must not affect recognition, interpretation, ordering, or stage outcomes.

`RecordingRef` is an identifier only. It must not contain or authorize opening a
filesystem path. `human_reference` identifies caller-supplied provenance; it is
not processor-verified truth. Comparing model text with a human reference requires
two independent calls.

The initial interface excludes:

- RID, TGID, channel, speaker, and assignment inference.
- Filesystem paths and audio handles.
- Credentials and secrets.
- Arbitrary metadata maps.
- Processor-generated timestamps or identifiers.

## Native-result preservation

The processor preserves the complete existing:

- `dispatchinterpretation.Result`, including its complete address and call-type
  branches.
- `unitpipeline.Result`, including its complete unit-recognition and unit-status
  branches.

Native results are assigned directly to their branches. The processor must not:

- Reconstruct results field by field.
- Merge evidence collections.
- Translate or normalize native states or reasons.
- Sort, group, filter, or deduplicate evidence.
- Reconcile disagreements.
- Select winners or add precedence.
- Convert nil collections to non-nil empty collections or the reverse.
- Rebase UTF-8 byte offsets.

The identical unchanged `Input.Transcript` string is passed independently to both
outer pipelines. Existing source text, evidence, alternatives, candidates,
rejection reasons, associations, `StatusIndex` relationships, and native ordering
remain unchanged.

## Execution order

`New` constructs the dispatch pipeline first and the unit pipeline second. A
constructor failure returns a nil processor and an error. A partially usable
processor must never be returned.

For every valid call, `Process` executes these stages in fixed order:

1. `input`
2. `dispatch`
3. `units`

Dispatch executes before units. Both receive identical transcript bytes. Neither
native result is passed into the other pipeline.

The processor retains no transcript, result, timestamp, history, cache, counter,
random identifier, retry state, or mutable per-call field.

## Stage outcomes and failure codes

Approved stage outcomes are:

- `completed`
- `failed`
- `skipped`

Approved fixed failure codes are:

| Failure code | Meaning |
| --- | --- |
| `missing_source_reference` | The required opaque source reference is blank. |
| `invalid_source_kind` | Source kind is not `synthetic` or `replay`. |
| `invalid_text_kind` | Text kind is not one of the four approved values. |
| `transcript_too_large` | The transcript exceeds 65,536 bytes. |
| `native_stage_panicked` | An independently invoked native pipeline panicked. |

An empty `FailureCode` accompanies a completed stage. Execution failures remain
separate from native rejection, ambiguity, unresolved outcomes, invalid-transcript
states, and native reason strings.

`completed` means only that the stage returned normally. It does not mean resolved,
accepted, correct, verified, operationally actionable, or safe for CAD use.

Error messages must not include panic payloads, stack traces, credentials,
transcript fragments, or native evidence text.

## Input validation and resource limit

The v1 transcript limit is 65,536 bytes inclusive. The limit is measured in bytes,
not runes, characters, words, or tokens.

- Empty transcript text is a valid fixture.
- A transcript of exactly 65,536 bytes is permitted.
- A transcript larger than 65,536 bytes fails explicitly.
- Text is never truncated.
- The complete input remains present in the returned audit record when validation
  fails.

On an input-envelope or size failure:

- The `input` stage is `failed` with the applicable fixed code.
- The `dispatch` and `units` stages are `skipped`.
- Both result pointers remain nil.
- `Process` returns the audit record and a safe error.

The processor must not add shared transcript normalization or new Unicode policy.
Each native pipeline retains its existing validation behavior.

## Partial failure and panic boundary

Branch-level panic recovery is approved so the independent branch can still run.

| Failure | Required behavior |
| --- | --- |
| Dispatch panic | Mark dispatch `failed` with `native_stage_panicked`, leave `Dispatch` nil, then attempt units. |
| Unit-pipeline panic | Preserve any completed dispatch result, mark units `failed`, and leave `Units` nil. |
| Both branches panic | Preserve the input and both failed stage records; both result pointers remain nil. |
| Native rejection or ambiguity | Preserve the complete native result and mark the execution stage `completed`. |

Any native-stage panic causes `Process` to return the audit record and a safe error.
The error must not conceal successfully returned independent branch data.

Recovery can preserve only results returned by an outer native pipeline. If an
outer pipeline panics after an internal stage completed, the internal unreturned
result is unavailable. Fatal runtime termination and hangs are outside this
in-process recovery guarantee.

## Replay and deterministic ordering

The caller's supplied replay sequence defines order. A caller may build an ordered
`[]AuditRecord`; the processor does not own or accumulate that history.

- Duplicate inputs and duplicate source references are retained.
- No chronological ordering is inferred across independent calls.
- Concurrent callers place results into their original input positions rather than
  append them in completion order.
- No result from one call may influence another call.

Identical input and metadata must produce identical audit output under the same
pinned implementation and embedded catalog build. Reproducibility across revisions
requires the caller to retain external build/revision provenance. The
`shadow-processor-v1` string does not identify a source revision by itself.

## Caller ownership and isolation

Each call returns fresh native results and a fresh stage-record slice. Mutating any
reachable collection or selected pointer returned from one call must not alter:

- Another branch in the same result where the native contracts promise isolation.
- A prior or future call.
- Either reusable native matcher.
- A concurrently executing call.

Ordinary assignment of an already returned result may share slices or pointers.
Callers are responsible for synchronizing their own shared mutations. The processor
does not promise deep cloning of caller-copied results.

## Approved native disagreements

The shadow processor preserves rather than resolves native grammar and validation
differences.

Examples include:

- Step 5D2 may reject `engine 2 on scene` while Step 5D4 proposes a bounded
  association.
- Address, call type, unit, and status retain different validation policies and
  speech-role limitations.
- CLEAR remains unresolved.
- ON AIR and TRAINING remain separate statuses.
- Conflicting statuses remain ambiguous without outer precedence.
- Native invalid, rejected, ambiguous, unresolved, unsupported, and incomplete
  results remain completed execution outcomes.

The processor must not claim uniform speech-role safety that the native packages do
not provide. It must expose complete native results and limitations independently.

## Audit ownership and storage

The processor returns one caller-owned in-memory `AuditRecord` per call.

The initial contract excludes:

- Processor-owned accumulating history.
- A batch API.
- Database persistence.
- Filesystem transcript or audit archives.
- Serialization or wire-format guarantees.
- A durable or tamper-evident audit claim.

A future durable audit adapter requires separate approval for its schema, encoding,
access control, retention, ordering, integrity, migration, recovery, and privacy
policy.

## No CAD authority

`ShadowOnly` is always true, but that marker is descriptive rather than a security
boundary. The package structure must enforce the boundary.

The processor must contain no:

- Store, writer, repository, or database dependency.
- Filesystem archive or loader.
- Network client, API handler, or WebSocket handler.
- Audio handle or transcription worker.
- Callback or event publisher.
- Operational incident or apparatus model.
- Production command registration or entry point.
- Unit/apparatus-state mutation.
- Incident creation, assignment, closure, or transition.
- Automatic dispatch or status transition.

It processes only caller-supplied text and returns caller-owned in-memory results.

## Verification contract

Implementation is blocked until the future Step 6B package proves all of the
following with synthetic fixtures and dependency inspection.

### Native parity

- Both complete branches equal their direct standalone pipeline outputs, including
  nil-versus-empty collections.
- Address/type both resolved, either alone, and neither resolved.
- Every existing dispatch summary-state combination.
- Repeated and competing addresses, cross streets, and unresolved numeric mentions.
- All 42 call-type phrases with types, levels, qualifiers, overlaps, and rejected
  evidence preserved.
- All 26 unit phrases and all 20 unit-status phrases.
- Intentional disagreement for `engine 2 on scene`.
- CLEAR, ON AIR/TRAINING, returning, RTQ, quarters, and service distinctions.
- Coordinated unit lists, repeated members, unsupported lists, and unknown members.
- Conflicting statuses plus an independent safe unit.
- Mixed question punctuation and ordinary affirmative punctuation.
- Native negation, history, hypothesis, command, quotation, and acknowledgement
  behavior and limitations.
- Nested phrase and numeric-boundary collisions.

### Source and isolation

- Clause and line boundaries remain native behavior.
- Evidence split across calls is never joined.
- Empty, partial, unknown, invalid UTF-8, unsafe-control, and differing Unicode-policy
  fixtures retain original bytes and native outcomes.
- Documented offsets and emoji/multibyte prefixes preserve exact byte indexes.
- Synthetic nil/empty collections, alternatives, candidates, and unfamiliar future
  native strings are forwarded without normalization.
- Duplicate replay records are retained in caller order.
- Changed provenance alone does not change interpretation.

### Failures and resource limits

- Each constructor failure returns no partially usable processor.
- Each branch panic records the correct fixed code and preserves the independent
  completed branch.
- Panic payloads and sensitive synthetic text never appear in errors or logs.
- Missing reference, invalid source kind, and invalid text kind fail before native
  execution.
- Transcript sizes of 0, 65,535, 65,536, and 65,537 bytes have exact documented
  behavior.
- No truncation occurs.

### Ownership and concurrency

- Mutating every reachable returned collection and pointer does not contaminate
  another call or matcher.
- Repeated calls remain deterministic.
- One reusable processor handles 1,000 mixed concurrent calls without shared
  mutable per-call state.
- Race detection must pass in an environment with a supported compiler. Ordinary CI
  does not prove race-detector success.

### Scope inspection

- No existing recognizer or pipeline behavior changes.
- No database, filesystem, network, audio, API, frontend, CAD, or production
  dependency is introduced.
- No logging or persistence of transcript text occurs.
- No new inference, reconciliation, precedence, or operational state is added.

Any lost native field, changed byte offset, merged transmission, hidden failure,
shared-state contamination, new inference, unsafe disclosure, silent truncation, or
forbidden side effect is an implementation blocker.

## Approval checklist

- [x] Package and interface boundary approved.
- [x] Complete native-result preservation approved.
- [x] Required and optional source metadata approved.
- [x] Caller-owned in-memory audit records approved.
- [x] Fixed execution and replay ordering approved.
- [x] Branch-level panic recovery and partial-result behavior approved.
- [x] Stage outcomes and fixed failure-code vocabulary approved.
- [x] 65,536-byte inclusive transcript limit approved.
- [x] External revision provenance boundary approved.
- [x] Native grammar and validation disagreements approved.
- [x] No-CAD-authority structural boundary approved.
- [x] Verification and race-detection requirements approved.

Step 6B implementation remains blocked until this document is reviewed, committed,
pushed, and green in CI.
