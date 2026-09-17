# Shadow Replay Evaluation Contract v1 - Step 6G / Step 6H

Status: Approved evaluation design for future Step 6H implementation.

Implementation status: Not started. Step 6G creates this document only.

Contract identifier: `shadow-replay-evaluation-v1`.

Reviewed baseline: `d061c9d52fed34305a40bdd749fcd0518d7c4f04` (`d061c9d`).
The working tree was clean and HEAD and local `origin/main` pointed to that
commit. Green CI is supplied baseline context, not a claim of operational safety.

## Purpose and milestone boundary

Approve the smallest deterministic, controlled evaluation of the completed shadow
pipeline. The evaluator accepts caller-selected, already materialized review-export
JSONL, passes it through unchanged `shadowimport.Import`, explicitly projects
`Record.Input` values into `shadowreplay.Run`, and pairs every admitted audit with
the importer's channel and TGID by index.

It proves composition, positional correspondence, native-result preservation, and
visible failure. It does not prove incident identity, unit operational state,
transcription accuracy, or board readiness by numeric cutoff.

Step 6G is this evaluation-contract milestone. Step 6H is the future evaluator
implementation milestone. Neither is the first SHADOW / REPLAY board preview, and
neither is the roadmap's Phase 7 PWA/mobile milestone in the
[product requirements](product-requirements.md). The broader
[project roadmap](../README.md) remains future product direction, not permission
to add integrations here.

This document approves the design, not implementation in Step 6G. Step 6H requires
a separate instruction to begin. No implementation files, existing-file changes,
staging, commits, evaluation runs against private datasets, or pushes are part of
Step 6G.

## Non-goals

The evaluator does not:

- Discover, watch, or choose files.
- Open paths, read a database, or contact Whisper or any network service.
- Decode audio, rerun transcription, or load recordings.
- Filter, sort, deduplicate, or split imported records.
- Reconcile dispatch with units, address with call type, or unit with status.
- Tune vocabularies, grammars, rules, or thresholds against held-out results.
- Create incidents, update unit state, or write CAD.
- Invent an accuracy pass mark for `both_resolved` or associations.

A later CLI that reads an explicit path may exist only under a separately approved
milestone. That CLI would supply bytes and a declared set kind; this package would
still not search a directory or select the newest file.

## Authoritative dependencies

- [Shadow transcript import contract](shadow-transcript-import-v1.md).
- [Completed shadow import README](../backend/internal/shadowimport/README.md).
- [Completed import implementation](../backend/internal/shadowimport/import.go).
- [Shadow replay contract](shadow-replay-v1.md).
- [Completed shadow replay README](../backend/internal/shadowreplay/README.md).
- [Completed replay implementation](../backend/internal/shadowreplay/runner.go).
- [Shadow processor contract](shadow-processor-v1.md).
- [Completed shadow processor README](../backend/internal/shadowprocessor/README.md).
- [Completed processor implementation](../backend/internal/shadowprocessor/processor.go).
- [Dispatch interpretation contract](dispatch-interpretation-v1.md).
- [Dispatch package](../backend/internal/dispatchinterpretation/README.md).
- [Unit vocabulary contract](unit-vocabulary-v1.md).
- [Unit-status vocabulary contract](unit-status-vocabulary-v1.md).
- [Unit pipeline package](../backend/internal/unitpipeline/README.md).
- [Offline transcription evaluation](offline-transcription-evaluation.md).
- [Controlled transcription experiments](transcription-experiments.md).
- [Step 3A export description](../backend/README.md#step-3a-local-human-transcript-review).

Historical approved contracts may still describe their own implementation as not
started. That historical wording is not a reason to edit them or reinterpret their
approved semantics. The completed code at the reviewed baseline supplies the
implementation context; this document changes none of those files.

Older conflicting status examples in product requirements or acceptance tables
are superseded by `docs/unit-status-vocabulary-v1.md` for this work. CLEAR remains
unresolved; returning and RTQ retain ENROUTE TO QUARTERS rather than proving
arrival; QUARTERS and IN SERVICE remain distinct; ON AIR and TRAINING remain
separate. Recognition of OUT OF SERVICE does not authorize operational behavior.

## Existing-API compatibility

No existing contract prevents a correct design. The following constraints are
accepted rather than changed:

| Constraint | Consequence for this evaluator |
| --- | --- |
| `shadowimport` does not copy JSONL `split` onto `Record` | Development and held-out sets are caller-selected source documents. The evaluator does not re-parse JSONL to filter by `split`. |
| `shadowprocessor.Input` and `AuditRecord` have no channel or TGID fields | Pair importer `Record.Channel` / `Record.TGID` with `AuditRecord` by index. Do not copy them into `Input` or audits. |
| Replay accepts only `[]shadowprocessor.Input` | The evaluator performs the explicit projection shown in the import contract. Import still does not call `Run`. |
| Import and replay forbid file loading | The evaluator also accepts caller-supplied bytes. It does not open paths. |
| Offline transcription evaluation defines WER and critical-term recall, and makes no promotion decision | Do not import those transcription metrics or `transcripteval`. Do not invent a shadow accuracy threshold. |
| Processor `both_resolved` is non-operational | Count it. Never treat it as same-incident proof or a board-preview pass. |
| Unit-status association is a proposal | Count proposed associations. Never treat them as current apparatus state. |

If a future instruction requires filtering by JSONL `split` inside this package,
stop and request a separate import-contract change rather than adding a second
JSONL parser.

## Approved future package and files

The Step 6H implementation is limited to:

```text
backend/internal/shadowreplayeval/
    evaluate.go
    evaluate_test.go
    README.md
```

No evaluator file is created by Step 6G. Step 6H must not change existing
recognizers, pipelines, import, replay, the processor, evaluation or review
packages, or their contracts to make this evaluation work. If an incompatibility
is discovered, stop and report it instead of changing native APIs or inventing
additional interpretation rules.

## Approved public interface

```go
type SetKind string

const (
    SetDevelopment SetKind = "development"
    SetHeldOut     SetKind = "held_out"
)

type Pair struct {
    Channel string
    TGID    int64
    Audit   shadowprocessor.AuditRecord
}

type Summary struct {
    Version               string
    Set                   SetKind
    TextKind              shadowprocessor.TextKind
    Records               int
    ImportAccepted        bool
    ReplayExecutionFailed bool
    ProcessorFailed       int
    ProcessorCompleted    int
    DispatchShadowState   map[string]int
    AddressState          map[string]int
    CallTypeState         map[string]int
    UnitState             map[string]int
    UnitStatusState       map[string]int
    AssociationsProposed  int
    AssociationsUnbound   int
    NativeDisagreement    int
    DuplicateReferences   int
    EmptyChannel          int
    ZeroTGID              int
    PairingIntact         bool
}

type Result struct {
    Pairs   []Pair
    Summary Summary
}

func New() (*Evaluator, error)

func (e *Evaluator) Evaluate(
    set SetKind,
    sources []shadowimport.Source,
) (Result, error)
```

`New` constructs one `shadowimport.Importer` and then one `shadowreplay.Runner`
through their unchanged constructors. Constructor failure returns a nil evaluator
and a safe error; no partially usable evaluator is returned. Use `New` before
`Evaluate`; the zero value is not initialized. No public configuration, file-path
arguments, callbacks, JSON/wire export, or additional identity types are approved.

`Evaluate` is the composition boundary. This package is the only approved caller
that both imports and replays. Import still must not invoke `Run`. Replay still
must not import.

1. Reject an invalid `SetKind` before import.
2. If `sources` is non-empty, reject mixed `TextKind` values before import.
   Validity of a uniform kind remains import's rule; `synthetic` and `normalized`
   therefore fail as unchanged import errors.
3. Call unchanged `Import(sources)`.
4. On import rejection, return a zero `Result` and the importer error unchanged.
5. Project `Record.Input` values in import order, exactly as:

```go
inputs := make([]shadowprocessor.Input, len(records))
for i := range records {
    inputs[i] = records[i].Input
}
audits, err := runner.Run(inputs)
```

6. Pair `records[i].Channel`, `records[i].TGID`, and `audits[i]` at every index
   `i`. Output length equals the admitted import length, including failed
   processor records and duplicates.
7. Build `Summary` from those pairs. Return the `Result` and the replay error
   unchanged when replay reports execution failure.

The evaluator must not call `Process` except through that `Run` projection. It
must not ask import to invoke replay. Channel and TGID stay on `Pair`; they are
not sent to `Run`.

`Result.Pairs` is an in-memory, caller-owned inspection slice for tests and
private review. It contains complete audits, including transcript bytes.
`Summary` is the only structure that may later be copied into a committed
board-readiness note. No serialization or file-writing API is approved.

`Version` is the document identifier `shadow-replay-evaluation-v1`. It does not
replace any audit `Version` field.

## Evidence classes

Counts are separate facts. Do not collapse them into one accuracy number.

| Class | What it is | What it is not |
| --- | --- | --- |
| Parser / import acceptance | `ImportAccepted`: import returned a non-nil `[]Record`, including empty | Proof that JSONL text is a correct dispatch or that `split` was honored |
| Processor execution success | `ProcessorCompleted`, `ProcessorFailed`, `ReplayExecutionFailed`, and stage codes | Native recognition quality, CAD authority, or incident identity |
| Native recognition evidence | Dispatch, address, call-type, unit, and unit-status state histograms | A claim that those states are operationally true |
| Proposed associations | `AssociationsProposed` and `AssociationsUnbound` | Current apparatus state or an assignment |
| Ambiguity / rejection / conflict | Native `ambiguous` / unresolved / unsupported / `contains_ambiguity` counts, plus `NativeDisagreement` | A reason to reconcile branches or invent a winner |

`both_resolved` lives in the native-recognition histogram. It remains two
independent syntactic results in one transcript, not same-incident proof.

## Dataset selection

v1 evaluates only the Step 3A review-export JSONL shape already accepted by
shadow import. The caller supplies already materialized `[]shadowimport.Source`.
There is no default dataset, newest-file rule, or directory scan.

Official sets are declared by `SetKind`, not inferred from JSONL `split`:

| Set | Caller-selected contents | Use |
| --- | --- | --- |
| `development` | Train JSONL, and validation JSONL only when that split exists as its own selected document | Fixture development, descriptive counts, and test authoring |
| `held_out` | Test JSONL only | Human review before any SHADOW / REPLAY board preview |

The evaluator does not read `split`, does not drop mixed-split objects, and does
not re-export subsets. A mixed-split document imported as either set is a caller
error for an official run. Import still returns every object; this evaluation
does not repair that mixing.

[Offline transcription evaluation](offline-transcription-evaluation.md) currently
describes **14 train, 2 test, and 0 validation** private records. Validation is
**unavailable**. Those sizes are descriptive context, not gates. They may change
when a privately held export changes. This evaluator must not require those
counts and must not commit that private export.

TextKind rules for one `Evaluate` call:

- Nil or empty `[]Source` is valid and records an empty `TextKind`.
- Otherwise every `Source.TextKind` must be identical. Mixed kinds reject the
  whole evaluation before import with `shadowreplayeval: mixed text kind`.
- A uniform `raw_model` or `human_reference` is the official path. A uniform
  `synthetic` or `normalized` kind, or any other string, is rejected by import
  unchanged; this package does not add a second text-kind vocabulary.
- Comparing raw-model evidence with human-reference evidence requires two
  independent `Evaluate` calls, then human comparison of the two summaries.
  The evaluator does not blend the two transcripts.

Held-out results must not be used to change vocabularies, grammars, matchers,
failure codes, limits, or this metric list. Development-set counts may describe
behavior. They are not permission to tune against held-out outcomes.

## Provenance pairing

For an admitted import of length N, `Run` returns N audits. `Result.Pairs` has
length N. For every `i` in `[0, N)`:

- `Pairs[i].Channel` equals `records[i].Channel` by ordinary string equality.
- `Pairs[i].TGID` equals `records[i].TGID` by ordinary integer equality.
- `Pairs[i].Audit` is the complete `audits[i]` value, assigned directly.
- `Pairs[i].Audit.Input` equals `records[i].Input`.

No new index field is added. Caller source order and JSONL line order remain the
import order. Duplicates remain separate pairs with independently copied
channel/TGID. Do not sort by `reviewed_at`, fingerprint, channel, TGID, or
dataset id.

`PairingIntact` is true only when N matches, every pair satisfies the equalities
above, and `Audit.ShadowOnly` is true on every pair. A false value is an
evaluation failure, not a native recognition outcome.

Channel and TGID remain evidence only. They do not select dispatch versus status
talkgroups, authorize incident creation, or update apparatus state.

## Empty, rejection, and continuation behavior

Nil and non-nil empty `[]Source` succeed: `Import` and `Run` both return non-nil
empty slices, `Pairs` is a non-nil empty slice, `Summary.Records` is 0,
`ImportAccepted` is true, histogram maps are empty rather than nil,
`PairingIntact` is true, and the error is nil. This is distinct from import
rejection, which returns a zero `Result` (nil `Pairs`, `ImportAccepted` false)
and the importer error.

Invalid JSONL, empty source bytes, mixed schema objects, over-limit sources, and
other whole-import failures remain import's. The evaluator does not repair,
skip, or partially admit them. Whole-import rejection performs no replay and
manufactures no pairs.

Replay admission is not expected to fail after a successful import, because
import already enforces the 1,000-record and 4 MiB seven-field limits. If replay
nonetheless returns a nil audit slice, do not manufacture pairs. Return
`Pairs == nil`, `ImportAccepted` true, `ReplayExecutionFailed` true,
`PairingIntact` false, the replay error unchanged, and no native histograms.
Do not convert that replay failure into an import rejection.

When replay continues after per-record processor failures, store every audit,
pair it, and return the complete `Result` plus
`shadowreplay: one or more records failed`. Mixed-success sequences keep later
successes after earlier failures. Duplicates remain separate pairs. Native
rejected, ambiguous, unresolved, unsupported, incomplete, and invalid-text
outcomes that return normally remain completed execution. They increment
native-state counts, not `ProcessorFailed`.

`ProcessorFailed` counts pairs whose audit has any stage outcome `failed`.
`ProcessorCompleted` counts pairs whose `input`, `dispatch`, and `units` stages
are all `completed`. `ReplayExecutionFailed` is true when the replay error is
non-nil. Those three facts are not operational state.

## Metrics

No approved shadow contract defines an accuracy threshold, WER gate, or
`both_resolved` pass rate. [Offline transcription evaluation](offline-transcription-evaluation.md)
explicitly makes no promotion-readiness decision. This evaluation therefore
**reports counts** and **requires human review**. It must not silently select a
numeric cutoff.

Do not compute transcription WER, exact-match rate, or critical-term recall
here. Those remain Step 4A metrics over raw versus human transcript text, not
native dispatch or unit results.

`Summary` counts are exact integers derived from existing native fields after
pairing. Map keys are the native state strings; missing keys count as zero.
Successful summaries use empty maps rather than nil. Read native fields only
from completed branches; when `Result.Dispatch` or `Result.Units` is nil, skip
those histograms for that pair.

| Histogram | Native source |
| --- | --- |
| `DispatchShadowState` | `Audit.Result.Dispatch.ShadowState` |
| `AddressState` | `Audit.Result.Dispatch.AddressState` |
| `CallTypeState` | `Audit.Result.Dispatch.CallTypeState` |
| `UnitState` | `Audit.Result.Units.Units.State` |
| `UnitStatusState` | `Audit.Result.Units.UnitStatus.State` |

Stable reporting order for human notes is:

Dispatch `ShadowState`, in this order:

1. `both_resolved`
2. `address_only_resolved`
3. `type_only_resolved`
4. `contains_ambiguity`
5. `neither_resolved`

Address state: `resolved`, `unsupported`, `no evidence`, `ambiguous`.

Call-type state: `resolved`, `unresolved`, `ambiguous`.

Unit-recognition state: `resolved`, `unresolved`, `ambiguous`.

Unit-status state: `resolved`, `unresolved`, `ambiguous`.

Additional integers:

| Field | Meaning |
| --- | --- |
| `ImportAccepted` | True when import returned a non-nil slice, including empty |
| `ReplayExecutionFailed` | True when the replay error is non-nil |
| `Records` | `len(Pairs)` |
| `ProcessorFailed` | Pairs with any stage outcome `failed` |
| `ProcessorCompleted` | Pairs whose `input`, `dispatch`, and `units` stages are all `completed` |
| `AssociationsProposed` | Count of unit-status associations whose `Unit` pointer is non-nil |
| `AssociationsUnbound` | Count of unit-status associations whose `Unit` pointer is nil |
| `NativeDisagreement` | Pairs counted once when any unit mention is rejected with `unsupported_context` and any unit-status association has a non-nil `Unit` |
| `DuplicateReferences` | Records whose `Input.Source.Reference` equals an earlier pair's reference |
| `EmptyChannel` | Pairs whose `Channel` is empty |
| `ZeroTGID` | Pairs whose `TGID` is `0` |
| `PairingIntact` | True only when pair length matches import length, every index preserves channel, TGID, and `Audit.Input`, and every audit has `ShadowOnly` true |

`NativeDisagreement` preserves the documented Step 5D2 versus Step 5D4 split
for phrases such as `engine 2 on scene`. It is not an error rate and must not
be minimized by reconciling the branches. Do not remap mention reasons or
association dispositions.

When a native pointer is nil because that stage failed or was skipped, do not
invent states. Count the execution failure only.

Interpretive prohibitions that apply to every count:

- `both_resolved` does not prove that address and call type belong to one
  incident.
- A proposed unit/status association is not current operational unit state.
- Completed execution is not acceptance, correctness, or CAD authority.
- Empty or zero channel/TGID are retained evidence, not repaired identity.

## Sanitized evidence for later review

`Summary` may be copied into a committed board-readiness note only as aggregate
counts, set kind, text kind, version string, and `PairingIntact`. It must not
contain:

- Transcript text or fragments.
- Paths, filenames, or URLs.
- `dataset_id`, fingerprints, attempt ids, or model identifiers.
- Raw channel labels or TGID values (use `EmptyChannel` / `ZeroTGID` counts).
- Credentials, panic payloads, or native evidence strings.

`Result.Pairs` must not be committed. Tests use synthetic JSONL only. Private
review-export bytes, recordings, and full pair dumps stay outside git. Existing
`.gitignore` rules are not a security boundary.

The evaluator must not write reports to disk. A human may transcribe `Summary`
counts into a review checklist.

## Resource limits, errors, ownership

This package adds no new numeric limits. Overflow-safe accounting remains in
import and replay. The inherited maxima, both inclusive, are:

| Limit | Exact maximum | Enforcement owner |
| --- | ---: | --- |
| Caller sources | 1,000 | Importer |
| Each source document | 8 MiB = 8,388,608 bytes | Importer |
| Sum of source documents | 8 MiB = 8,388,608 bytes | Importer |
| JSON line, excluding line ending | 1 MiB = 1,048,576 bytes | Importer |
| Produced input records | 1,000 | Importer, then replay |
| Produced `Input` string bytes | 4 MiB = 4,194,304 bytes | Importer, then replay |
| Each transcript | 65,536 bytes | Unchanged shadow processor, per later `Process` |

Channel and TGID remain outside the seven-field string-byte budget. The
processor transcript limit still applies per record after import. Nil/empty,
whole-import rejection, and replay continuation behave as specified above.

Fixed evaluator error messages, in addition to unchanged import and replay
errors:

| Outcome | Error message | Result returned |
| --- | --- | --- |
| `SetKind` not `development` or `held_out` | `shadowreplayeval: invalid evaluation set` | Zero |
| Mixed `TextKind` among non-empty sources | `shadowreplayeval: mixed text kind` | Zero |
| Constructor failure | `shadowreplayeval: construction failed` | Nil evaluator |

Do not wrap import or replay errors. Do not concatenate per-record errors. No
error may contain paths, transcripts, identifiers, channel, TGID, credentials,
panic payloads, or native evidence.

The evaluator retains only its importer and runner. Each `Evaluate` keeps
sources, pairs, and counters local. It mutates neither caller `Source` bytes nor
returned pairs after return. Mutating one pair must not affect another pair,
another call, or a reusable matcher. This is not a deep-clone API.

One constructed evaluator must support concurrent independent `Evaluate` calls.
Identical `set` and `sources` produce identical `Result` values under the same
pinned implementation and catalogs. The caller retains external revision
provenance.

## Board-preview pass/fail gates

These gates are integrity and process gates. They are **not** accuracy
thresholds. All must pass before the first SHADOW / REPLAY board preview. A
preview remains read-only shadow evidence and still has no CAD authority.

Automated / testable fail conditions:

- Official development or held-out sources are rejected by import.
- `Pairs` length differs from the admitted import length.
- `PairingIntact` is false.
- Duplicates present in the source are not present as separate pairs.
- Native disagreement for `engine 2 on scene` is reconciled or hidden.
- Any audit has `ShadowOnly` false.
- Production evaluator code opens paths, uses the database, performs network
  I/O, calls Whisper, or writes operational state.
- A committed note includes transcript text, paths, or identifiers.

Required human review (no numeric cutoff):

- Read `Summary` for both `raw_model` and `human_reference` on the development
  set, then independently on the held-out set when test records exist.
- Confirm failures remain visible (`ProcessorFailed`, stage codes, replay
  aggregate error) and were not converted into unit or incident state.
- Confirm `both_resolved` and proposed associations are not treated as dispatch
  truth or current apparatus state.
- Record that held-out counts were not used to retune matchers.
- If held-out records are unavailable or few, state that limitation explicitly;
  scarcity is not a pass.

There is no approved gate of the form “N% `both_resolved`” or “WER below X”.
Absence of those numbers is required, not a gap to fill in Step 6H.

## Prohibitions

The package structure must enforce the shadow boundary. The evaluator must not:

- Execute live audio, FFmpeg, playback, or Whisper/transcription.
- Discover files, watch directories, or select newest/latest sources.
- Read or write PostgreSQL or any store.
- Perform network activity, HTTP, WebSockets, or API/frontend integration.
- Mutate CAD or operational state, create incidents, or update unit state.
- Reconcile native branches or tune vocabularies/rules from held-out results.
- Add production wiring, service registration, or a report-file writer.
- Begin Phase 7 PWA/mobile work.

## Required Step 6H verification

All fixtures must be synthetic. The acceptance-test matrix is:

| ID | Case | Pass |
| --- | --- | --- |
| EV-01 | Nil `[]Source` and non-nil empty `[]Source` | Non-nil empty `Pairs`, zero counts, empty maps, `ImportAccepted` true, `PairingIntact` true, nil error |
| EV-02 | `SetKind` other than `development` or `held_out` | Zero result, `shadowreplayeval: invalid evaluation set`, no import |
| EV-03 | Mixed `TextKind` among non-empty sources | Zero result, `shadowreplayeval: mixed text kind`, no import |
| EV-04 | Valid synthetic JSONL with `raw_model` | Import admits every object; explicit projection; one pair per admitted record |
| EV-05 | Index pairing, including empty channel, TGID `0`, and Greenwich-unlike values | `Pairs[i]` preserves channel, TGID, and `Audit.Input`; `PairingIntact` true |
| EV-06 | Duplicate JSON objects | Two pairs with independently copied provenance; `DuplicateReferences` increments |
| EV-07 | Separate `human_reference` call | Selects only `human_reference_transcript`; no fallback to raw model |
| EV-08 | Whole-import rejection (invalid JSONL or over-limit) | Zero result, unchanged import error, replay not invoked |
| EV-09 | Processor validation failure then a later success | Complete paired result, `ReplayExecutionFailed` true, later success kept |
| EV-10 | Recovered native-stage failure then a later success | Same continuation and pairing as EV-09 |
| EV-11 | Native state histograms | Exact native strings; `both_resolved` counted and not treated as incident identity |
| EV-12 | `engine 2 on scene` | Unit mention rejected with `unsupported_context`; status `Unit` non-nil; `NativeDisagreement` increments by one |
| EV-13 | Sanitized `Summary` | No transcript, path, dataset id, fingerprint, attempt id, channel label, or TGID value |
| EV-14 | Production imports | No `transcripteval`, review, database, HTTP, or audio packages; no file open |
| EV-15 | Ownership and repeatability | Mutating one pair does not affect another call; identical inputs yield identical results |
| EV-16 | Concurrency | 1,000 concurrent `Evaluate` calls remain isolated |
| EV-17 | Race detector | Required when a supported C compiler exists; local Windows GCC absence is a limitation, not a substitute pass |

Invalid, duplicate, empty, and mixed-success sequences are covered by EV-01,
EV-06, EV-08, EV-09, and EV-10. Do not add tests that require private review
export, live audio, or network access.

## Read-only review checklist

Use this checklist on sanitized `Summary` values. Do not attach pair dumps.

- [ ] Development `raw_model` evaluation imported and paired.
- [ ] Development `human_reference` evaluation imported and paired.
- [ ] Held-out evaluations run, or unavailability/scarcity recorded.
- [ ] `PairingIntact` is true on every official run.
- [ ] Duplicate sources remained duplicate pairs.
- [ ] Processor failures, if any, are visible and not operational state.
- [ ] Dispatch `ShadowState` histogram inspected; `both_resolved` not treated as
      same-incident proof.
- [ ] Unit/status association counts inspected; not treated as live unit state.
- [ ] Native disagreement remains visible.
- [ ] Channel/TGID used only as evidence counts (`EmptyChannel` / `ZeroTGID`).
- [ ] No held-out-driven matcher or threshold change.
- [ ] No transcript, path, or identifier appears in any committed note.
- [ ] Human reviewer name/date recorded outside this repository or in a note
      that contains only sanitized counts.
- [ ] First SHADOW / REPLAY board preview still blocked if any box is unchecked.

## Unresolved questions

These do not block this contract:

1. An explicit-path CLI that reads a caller-supplied JSONL file remains a
   separate milestone.
2. A committed Markdown template filled with a specific private-run's counts is
   a later review artifact, not a Step 6H code output.
3. The privately held 14/2/0 split sizes may change; this contract does not freeze
   them as gates.
5. Mixed-split JSONL cannot be rejected here without re-parsing `split` or
   changing import. Official runs rely on caller-selected documents.
