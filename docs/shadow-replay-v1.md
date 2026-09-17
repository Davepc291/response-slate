# Shadow Replay Contract v1 - Step 6C / Step 6D

Status: Approved replay design for future Step 6D implementation.

Implementation status: Not started. Step 6C creates this document only.

Contract identifier: `shadow-replay-v1`.

Reviewed baseline: `70206067679e8ca008d571b54cecb40a0104fb69` (`7020606`).
The working tree was clean and HEAD and local `origin/main` pointed to that
commit. Green CI is supplied baseline context, not a claim of operational safety.

## Purpose and milestone boundary

Approve a separate bounded, sequential, in-memory caller of the completed shadow
processor. The runner accepts an explicitly supplied sequence of transcripts and
returns their complete independent audit records in the same order. It establishes
no incident identity, assignment, current apparatus state, or dispatch truth.

Step 6C is this replay-contract milestone. Step 6D is the future runner
implementation milestone. Neither is the roadmap's Phase 7 PWA/mobile milestone
in the [product requirements](product-requirements.md). The broader
[project roadmap](../README.md) remains future product direction, not permission
to add integrations to this runner.

This document approves the design, not implementation in Step 6C. Step 6D requires
a separate instruction to begin. No implementation files, existing-file changes,
staging, commits, or pushes are part of Step 6C.

## Authoritative dependencies

- [Shadow processor contract](shadow-processor-v1.md).
- [Completed shadow processor README](../backend/internal/shadowprocessor/README.md).
- [Completed processor implementation](../backend/internal/shadowprocessor/processor.go).
- [Completed processor tests](../backend/internal/shadowprocessor/processor_test.go).
- [Dispatch interpretation contract](dispatch-interpretation-v1.md).
- [Dispatch package](../backend/internal/dispatchinterpretation/README.md).
- [Unit vocabulary contract](unit-vocabulary-v1.md).
- [Unit-status vocabulary contract](unit-status-vocabulary-v1.md).
- [Unit pipeline package](../backend/internal/unitpipeline/README.md).

The shadow processor's single-input public API and all native package behavior
remain unchanged. Its exclusion of a batch API remains in force for that package;
this contract approves a separate caller-side sequence API in `shadowreplay`.
Returning a sequence does not authorize either package to retain replay history.

Historical approved contracts may still describe their implementation as not
started. That historical wording is not a reason to edit them or reinterpret
their approved semantics. The completed code at the reviewed baseline supplies
the implementation context; this document changes none of those files.

Older conflicting status examples in product requirements or acceptance tables
are superseded by `docs/unit-status-vocabulary-v1.md` for this work. CLEAR remains
unresolved; returning and RTQ retain ENROUTE TO QUARTERS rather than proving
arrival; QUARTERS and IN SERVICE remain distinct; ON AIR and TRAINING remain
separate. Recognition of OUT OF SERVICE does not authorize operational behavior.
There is no implicit mapping into historical SQL status values.

## Approved future package and files

The Step 6D implementation is limited to:

```text
backend/internal/shadowreplay/
    runner.go
    runner_test.go
    README.md
```

No runner file is created by Step 6C. Step 6D must not change existing recognizers,
pipelines, the shadow processor, or their contracts to make replay work. If an
incompatibility is discovered, stop and report it instead of changing native APIs
or inventing additional interpretation rules.

## Approved public interface

```go
func New() (*Runner, error)

func (r *Runner) Run(
    inputs []shadowprocessor.Input,
) ([]shadowprocessor.AuditRecord, error)
```

`New` constructs one reusable `shadowprocessor.Processor` through its unchanged
constructor. Constructor failure returns a nil runner and a safe error; no
partially usable runner is returned. Use `New` before `Run`; the zero value is
not initialized. No public configuration, dependency injection, callbacks, or
additional input/output wrapper types are approved.

The input is an already materialized caller-supplied slice. The output is a fresh
caller-owned slice of complete existing `shadowprocessor.AuditRecord` values.
There is no new per-record status, failure, identity, or provenance schema.
The document identifier does not replace any returned audit's `Version` field.

## Whole-sequence admission and exact limits

Admission occurs before the first call to `Process`. Check record count first,
then the total string-byte budget. Both maxima are inclusive:

| Limit | Exact maximum | Enforcement owner |
| --- | ---: | --- |
| Input records | 1,000 | Replay runner, before any processing |
| Total input string bytes | 4 MiB = 4,194,304 bytes | Replay runner, before any processing |
| Each transcript | 65,536 bytes | Unchanged shadow processor, per record |

Count `len` of each Go string value, including the named string types. For every
input element, count exactly these seven fields:

1. `Source.Kind`
2. `Source.Reference`
3. `Source.TextKind`
4. `Source.RecordingRef`
5. `Source.AttemptRef`
6. `Source.Model`
7. `Transcript`

Every occurrence counts, including duplicate records and strings sharing backing
storage. Count original bytes, not runes, UTF-16 units, tokens, trimmed values,
normalized values, encoded JSON, or unique strings. Empty fields contribute zero.
Invalid UTF-8 and unknown source-kind/text-kind strings still contribute their
exact byte lengths. Do not count struct/slice headers, allocation overhead, or
generated results. A future change to `Input` or `Source` requires review of this
accounting contract; do not silently omit new fields.

Admission arithmetic must be overflow-safe. Maintain a remaining budget initially
equal to 4,194,304; before subtracting each field length, reject if that length
exceeds the remaining budget. Never form an unchecked sum of field lengths or
record totals before comparing with the limit. No huge allocation is needed to
test arithmetic edge cases through a private admission helper.

If either admission limit is exceeded, return a nil result slice and a safe error.
Process zero records, including any otherwise valid prefix. Do not truncate,
sample, split into sub-runs, partially admit the sequence, or manufacture audit
records for unprocessed inputs. Scanning may stop when rejection is established;
successful admission must account for the whole sequence before processing.

Sequence admission does not duplicate envelope or transcript validation. For an
admitted sequence, even an invalid source or a 65,537-byte transcript is passed
unchanged to the processor and receives its existing failed-input audit record.
A per-record failure does not reject the whole sequence at admission. Conversely,
passing the sequence budget does not waive the processor's transcript limit.

Nil and non-nil empty input slices both succeed: return a non-nil, zero-length
`[]shadowprocessor.AuditRecord` and a nil error, with no `Process` calls. This
explicit empty result is distinct from the nil result on admission failure.

These are input admission limits, not a hard bound on result size, total heap
usage, or elapsed execution time. Native evidence can amplify output size. No
output truncation, cancellation API, timeout, or resource-recovery claim is added.

## Sequential execution and record preservation

For an admitted sequence of length N, invoke the unchanged processor exactly once
for input 0, then input 1, through input N-1. Each call must return before the next
starts. No goroutines or worker pool process records within one run.

Store the entire returned audit record directly at the corresponding output
position. The output length is N, including failed records. Output position i
corresponds to input position i; no new index or record identifier is required.
Do not reconstruct audit fields or native results field by field.

Preserve source metadata and transcript bytes without changing `Source.Kind` to
`replay` automatically. Synthetic, replay, raw-model, normalized, and human-reference
provenance retain the processor's existing validation and meaning. References are
opaque identifiers, never a request to open a path or verify a recording. A human
reference label is caller-supplied provenance, not verified truth.

Preserve duplicate inputs and duplicate source references. Repetition does not
authorize result reuse, caching, duplicate suppression, or operational no-ops.
Each duplicate receives its own processor call and fresh native results.

Each complete record retains its original `Version`, `ShadowOnly`, `Input`,
`Result`, and `Stages`. Preserve native pointers, nil versus empty collections,
reasons, candidates, alternatives, rejected evidence, ordering, associations,
`StatusIndex` relationships, and original UTF-8 byte offsets. The empty outer
result convention does not authorize changing any inner native collection.

The processor retains its input/dispatch/units stage order and independently
passes the identical unchanged transcript to dispatch first and units second.
The runner neither calls these branches itself nor feeds their results into one
another. An earlier audit never informs processing of a later input.

## Failure continuation and safe errors

A record fails for runner purposes exactly when `Process` returns a non-nil error.
Store that call's complete audit record and continue with every remaining admitted
input. This includes input validation failures and native-stage panics recovered
by the existing processor. Do not retry, stop early, hide a completed branch, or
replace the record with an empty placeholder.

After all admitted records have been attempted, return the complete ordered slice
and one safe aggregate error if any record failed. If no record failed, return the
slice and a nil error. The aggregate reports execution failure only; it is not an
aggregate operational state, accuracy metric, or readiness decision.

Use fixed safe runner error messages for the following outcomes:

| Outcome | Error message | Records returned |
| --- | --- | --- |
| Record-count admission failure | `shadowreplay: too many input records` | Nil |
| String-byte admission failure | `shadowreplay: input string bytes exceed limit` | Nil |
| One or more processor calls failed | `shadowreplay: one or more records failed` | All admitted records, in input order |

Constructor errors must also remain safe. No error may contain transcript
fragments, source-field contents, credentials, panic payloads, stack traces, or
native evidence. Do not concatenate per-record errors into the aggregate. No
additional exported error types or per-record failure codes are required.

Existing `Stages` and their fixed failure codes remain the sole per-record
execution audit. Do not add stages or remap native states or rejection reasons.
Native rejected, ambiguous, unresolved, unsupported, incomplete, and invalid-text
outcomes that return normally remain completed execution outcomes. They do not
cause a runner error merely because interpretation did not resolve.

Panic recovery remains at the processor's native-stage boundaries. The runner
does not add blanket recovery or fabricate records for an unexpected panic outside
those boundaries. Fatal runtime termination, hangs, and unreturned internal native
results remain outside the recovery guarantee. Continued execution is guaranteed
for processor errors and recovered native-stage panics, not those exclusions.

## Ownership, reuse, and concurrency

The runner retains only its reusable processor. Admission counters, failure flags,
input references, and results are local to each `Run`. It stores no transcript,
history, result cache, timestamp, generated identifier, retry state, or per-run
mutable field between calls. It neither accumulates nor owns an audit archive.

Each invocation returns a fresh outer result slice and the fresh native records
from its processor calls. Mutating any reachable returned collection or selected
pointer must not affect another record, an earlier or later run, an independent
native branch where isolation is promised, a reusable matcher, or a concurrent run.
The runner does not mutate the caller's input slice or source metadata.

Callers must not mutate the supplied input slice or its elements while `Run` is
admitting or processing them. Callers synchronize mutations of shared returned
results. Ordinary assignment of an already returned slice or record may share
backing storage and pointers; this is not a deep-clone API for caller-made copies.

One constructed runner must support concurrent independent `Run` calls. Records
within each run remain sequential; different runs may interleave. There is no
global order, global lock requirement, timestamp precedence, or ordering inferred
across runs. Caller-owned output positions preserve each supplied sequence.

Identical input sequences produce identical audit records under the same pinned
implementation and embedded catalogs. The caller retains external revision/build
provenance; neither contract version string attests a source revision or runtime.

## Prohibited behavior and deferred integrations

The runner must not normalize, correct, sort, filter, deduplicate, retry, merge,
reconcile, select winners, infer assignments, or create operational state. No
evidence crosses records, transmissions, runs, RIDs, channels, or timing windows.
There is no RID/TGID/channel/speaker interpretation, event association, state
transition, incident creation/closure, apparatus mutation, display/color policy,
automatic dispatch, scoring, or model-promotion decision.

Prohibited dependencies and side effects include:

- File loading, discovery, archives, or private transcript import.
- Serialization, JSON/wire formats, and audit export.
- Persistence, database access, schema changes, stores, or repositories.
- Networking, HTTP clients, APIs, WebSockets, and frontend changes.
- Audio input/processing, playback, FFmpeg, transcription, and Whisper requests.
- Callbacks, event publication, service registration, background jobs, and CLIs.
- Production wiring, environment/configuration loading, and CAD interaction.
- Logging transcript/provenance contents or persisting diagnostic output.

Recorded-audio replay, Whisper reruns, private transcript import, persistence, and
board integration remain deferred to separately approved milestones. Existing
evaluation/experiment parsing, normalization, duplicate rejection, and ordering
rules must not be imported into this sequence runner. Its synthetic tests require
no private datasets, recordings, database, API, frontend, or live service.

`ShadowOnly` remains true in unchanged processor records. It is descriptive, not
a security boundary; package dependencies and absence of side effects enforce
the scope. Neither a completed stage nor `both_resolved` proves incident identity
or operational truth. Preserve native grammar and validation disagreements,
including Step 5D2 rejection versus Step 5D4 association for `engine 2 on scene`.
Do not refactor documented duplicate extraction or bounded native matching here.

## Required Step 6D verification

All fixtures must be synthetic. Required tests and inspection include:

### Admission boundaries

- Nil and empty inputs return a non-nil empty slice and nil error with zero calls.
- Record counts 1, 999, 1,000, and 1,001; over-limit rejection runs no prefix.
- Total string bytes 4,194,303, 4,194,304, and 4,194,305; exact inclusive behavior.
- Every one of the seven fields contributes bytes, including named string kinds,
  optional provenance, blank fields, unknown values, duplicates, shared string
  storage, multibyte text, invalid UTF-8, and unsafe controls.
- Overflow-safe checks reject lengths exceeding the remaining budget without
  wrapping; test large length arithmetic privately without huge allocations.
- Count rejection takes precedence when both admission limits fail.
- An over-budget late record prevents all processing of an otherwise valid prefix.
- Admitted transcripts of 0, 65,535, 65,536, and 65,537 bytes retain the processor's
  exact behavior. Sequence-boundary fixtures include metadata in their totals.

### Parity, order, duplicates, and failures

- Compare complete audit records against direct independent `Process` calls,
  including every native field and nil/empty distinction.
- Prove exactly one invocation per admitted element, unchanged inputs, sequential
  start/return ordering, output-position correspondence, and duplicate retention.
- Retain native parity across all 42 call-type, 26 unit, and 20 status phrases,
  dispatch summary states, ambiguity, rejection, partial and unknown text, clause
  boundaries, speech-role limitations, conflicting statuses, and native disagreements.
- Check documented spans, emoji/multibyte prefixes, invalid UTF-8, and unchanged
  `StatusIndex` relationships. Never join split evidence across inputs or runs.
- Exercise success/failure/success, failure first/last, consecutive failures,
  all failures, and all completed native rejections. Verify exact aggregate-error
  behavior and retention of every successful independent result.
- Exercise missing reference, invalid source/text kind, oversized transcripts,
  and dispatch-only, units-only, and both-stage recovered panic results, including
  sensitive synthetic payloads and `panic(nil)` behavior already owned upstream.
- Verify constructor failure returns no runner and errors disclose no input,
  provenance, panic payload, or native evidence.
- Use only private, unexported constructor/invocation seams where needed. No global
  mutable hooks, exported injection API, or changes to processor tests/seams are
  authorized. Runner tests may forward synthetic processor failure records; native
  panic recovery itself remains tested in the existing shadowprocessor package.

### Ownership, concurrency, and scope

- Mutate every reachable returned collection/pointer across duplicate records,
  prior/future runs, and promised independent branches; prove no contamination.
- Verify the runner never mutates inputs and retains no history after success,
  admission rejection, or individual processing failure.
- Prove repeatability and one reusable runner handling 1,000 mixed concurrent
  `Run` calls with independent per-run ordering and result mutation isolation.
- Run supported-toolchain race tests for replay and its shadow/native dependencies.
  Local Windows race testing is presently blocked by missing GCC; ordinary tests
  or green CI without race detection do not prove race-detector success. Report
  the limitation explicitly until a supported environment passes those tests.
- Run formatting, targeted Go tests, coverage, `go vet ./...`, `go test ./...`,
  and `git diff --check` when implementation is separately authorized. Inspect
  dependencies and the diff for prohibited integrations and existing-file changes.

No lost field, changed byte offset, reordered/dropped record, hidden failure,
shared-state contamination, silent truncation, new inference, unsafe disclosure,
or forbidden side effect is acceptable. This document defines future verification
requirements; it does not claim Step 6D tests or implementation have run.
