# Shadow Transcript Import Contract v1 - Step 6E / Step 6F

Status: Approved import design for future Step 6F implementation.

Implementation status: Not started. Step 6E creates this document only.

Contract identifier: `shadow-transcript-import-v1`.

Reviewed baseline: `fe45c2b4d79f2cd53d81a540a28c404e9f0caf05` (`fe45c2b`).
The working tree was clean and HEAD and local `origin/main` pointed to that
commit. Green CI is supplied baseline context, not a claim of operational safety.

## Purpose and milestone boundary

Approve a separate private, deterministic, read-only transcript-import boundary.
The importer accepts caller-selected, already materialized JSONL sources in the
existing Step 3A review-export shape and maps each JSON object to one importer-owned
`Record`. Each record keeps the unchanged `shadowprocessor.Input` beside the native
`channel` and `tgid` values. A later caller may explicitly project `Record.Input`
values into `shadowreplay.Run`. The importer does not invoke replay. It establishes
no incident identity, assignment, current apparatus state, or dispatch truth.

Step 6E is this import-contract milestone. Step 6F is the future importer
implementation milestone. Neither is the roadmap's Phase 7 PWA/mobile milestone
in the [product requirements](product-requirements.md). The broader
[project roadmap](../README.md) remains future product direction, not permission
to add integrations to this importer.

This document approves the design, not implementation in Step 6E. Step 6F requires
a separate instruction to begin. No implementation files, existing-file changes,
staging, commits, or pushes are part of Step 6E.

## Non-goals

The importer does not:

- Discover, watch, or choose files.
- Open paths, read a database, or contact Whisper or any network service.
- Run the shadow processor or replay runner.
- Rewrite, correct, normalize, or infer transcript text.
- Sort, filter, deduplicate, or split transmissions.
- Guess channel, TGID, RID, speaker, or recording identity.
- Create operational state or CAD updates.

A later CLI that reads an explicit path may exist only under a separately approved
milestone. That CLI would supply bytes; this package would still not search a
directory or select the newest file.

## Authoritative dependencies

- [Shadow processor contract](shadow-processor-v1.md).
- [Completed shadow processor README](../backend/internal/shadowprocessor/README.md).
- [Completed processor implementation](../backend/internal/shadowprocessor/processor.go).
- [Shadow replay contract](shadow-replay-v1.md).
- [Completed shadow replay README](../backend/internal/shadowreplay/README.md).
- [Completed replay implementation](../backend/internal/shadowreplay/runner.go).
- [Dispatch interpretation contract](dispatch-interpretation-v1.md).
- [Dispatch package](../backend/internal/dispatchinterpretation/README.md).
- [Unit vocabulary contract](unit-vocabulary-v1.md).
- [Unit-status vocabulary contract](unit-status-vocabulary-v1.md).
- [Unit pipeline package](../backend/internal/unitpipeline/README.md).
- [Offline transcription evaluation](offline-transcription-evaluation.md).
- [Controlled transcription experiments](transcription-experiments.md).
- [Step 3A export description](../backend/README.md#step-3a-local-human-transcript-review).
- [Review export record](../backend/internal/transcriptreview/review.go).
- [Evaluation JSONL parser](../backend/internal/transcripteval/input.go).

The processor and replay public APIs remain unchanged. Historical approved
contracts may still describe their own implementation as not started. That
historical wording is not a reason to edit them or reinterpret their approved
semantics. The completed code at the reviewed baseline supplies the
implementation context; this document changes none of those files.

Older conflicting status examples in product requirements or acceptance tables
are superseded by `docs/unit-status-vocabulary-v1.md` for this work. CLEAR remains
unresolved; returning and RTQ retain ENROUTE TO QUARTERS rather than proving
arrival; QUARTERS and IN SERVICE remain distinct; ON AIR and TRAINING remain
separate. Recognition of OUT OF SERVICE does not authorize operational behavior.

## Existing-API compatibility

No existing contract prevents a correct design. The following constraints are
accepted rather than changed:

| Constraint | Consequence for this importer |
| --- | --- |
| `shadowprocessor.Input` has no channel, TGID, RID, or timestamp fields | Do not modify that type. Copy native `channel` and `tgid` onto the importer-owned `Record` beside `Input`. Never guess them, never use them for ordering or recognition, and never copy them into `Input`. |
| Replay accepts only an already materialized `[]shadowprocessor.Input` | Import returns `[]Record`. A future caller explicitly projects `Record.Input` values into `Run`. The importer does not call `Run`. |
| Replay and the processor forbid file loading inside those packages | Import also accepts caller-supplied bytes. It does not open paths. |
| Evaluation parsing rejects duplicates, empty references, Greenwich-only channel pairs, unsafe controls, and blank lines, and may compute metrics | Import reuses the JSONL object shape only. It must not call `DecodeDataset`, `DecodeRecords`, or `EvaluateFile`, and must not import those metric, uniqueness, or allowlist rules. |
| Processor `TextKind` includes `synthetic` and `normalized` | This native JSONL format has no such fields. Those kinds are rejected on an import source. Synthetic or normalized replay remains a direct `Input` construction, not this importer. |

Do not overload `Source.Reference`, `RecordingRef`, or `Transcript` to smuggle
channel or TGID into the processor. A later board or audit caller reads
`Record.Channel` and `Record.TGID` as evidence only.

## Approved future package and files

The Step 6F implementation is limited to:

```text
backend/internal/shadowimport/
    import.go
    import_test.go
    README.md
```

No importer file is created by Step 6E. Step 6F must not change existing
recognizers, pipelines, the shadow processor, the replay runner, evaluation or
review packages, or their contracts to make import work. If an incompatibility is
discovered, stop and report it instead of changing native APIs or inventing
additional interpretation rules.

## Approved public interface

```go
type Source struct {
    Bytes    []byte
    TextKind shadowprocessor.TextKind
}

type Record struct {
    Input   shadowprocessor.Input
    Channel string
    TGID    int64
}

func New() (*Importer, error)

func (im *Importer) Import(
    sources []Source,
) ([]Record, error)
```

`Record` is the only approved output wrapper. Its `Channel` and `TGID` field names
and types follow the native JSONL schema (`channel` string, `tgid` integer decoded
as `int64`). No further envelope, status, identity, or sidecar type is approved.

`New` returns a reusable importer that holds no processor, runner, catalog, or
source bytes. Constructor failure is not expected in v1 because there is no
constructed dependency; if a future dependency is added, failure must return a
nil importer and a safe error with no partially usable importer. Use `New` before
`Import`; the zero value is not initialized. No public configuration, dependency
injection, callbacks, or file-path arguments are approved.

The input is an already materialized caller-supplied slice. Each `Source.Bytes`
value is one complete JSONL document selected by the caller. `Source.TextKind`
selects which transcript field becomes `Record.Input.Transcript` for every object
in that document.

The output is a fresh caller-owned `[]Record`. The document identifier does not
replace any later audit's `Version` field. The importer does not invoke `Process`
or `Run`. A future caller that wants replay must project inputs explicitly:

```go
inputs := make([]shadowprocessor.Input, len(records))
for i := range records {
    inputs[i] = records[i].Input
}
audits, err := runner.Run(inputs)
```

That projection is caller-owned. The importer must not perform it, cache it, or
register a replay callback. Projected `Input` values remain unchanged processor
inputs; channel and TGID stay on `Record` and are not sent to `Run`.

## Accepted format

v1 accepts exactly one project-native format: the Step 3A review-export JSONL
object used by [offline evaluation](offline-transcription-evaluation.md) and
written by [transcript review export](../backend/internal/transcriptreview/review.go).
Do not add CSV, plain text, Whisper JSON, experiment sidecars, database rows, or
a generic key/value importer.

Each non-empty source is UTF-8 JSONL without a BOM. One JSON object per line is
required. LF, CRLF, and a final line without a newline are supported as document
framing only. Blank lines are not skipped; they reject the whole import.

Required object keys, with exact names and no aliases:

| Key | JSON type | Import role |
| --- | --- | --- |
| `dataset_id` | string | Opaque `Input.Source.Reference` |
| `audio_fingerprint` | string | Opaque `Input.Source.RecordingRef`; never opened as a path |
| `transcription_attempt_id` | integer | Decimal `Input.Source.AttemptRef` |
| `channel` | string | Exact copy onto `Record.Channel` |
| `tgid` | integer (`int64`) | Exact copy onto `Record.TGID` |
| `duration_ms` | integer | Required present; unused for replay |
| `raw_model_transcript` | string | Selected when `TextKind` is `raw_model` |
| `human_reference_transcript` | string | Selected when `TextKind` is `human_reference` |
| `model` | string | Opaque `Input.Source.Model` |
| `split` | string | Unused for selection or order; required present |
| `reviewed_at` | RFC3339 timestamp string | Unused for order; required present |

Reject missing, null, unknown, duplicate, or incorrectly cased keys; malformed
JSON; trailing tokens after one object; invalid types; and fractional JSON
numbers for the integer fields. `split` must be exactly `train`, `validation`,
or `test`. `reviewed_at` must parse as a nonzero RFC3339 timestamp.
`duration_ms` must be a nonnegative integer. `audio_fingerprint` must match the
export convention `pcm-s16le-16000-mono-v1:sha256:` plus 64 lowercase hex digits.
`dataset_id` must equal `gfr-audio-v1:` plus the lowercase MD5 hex of the
fingerprint bytes. That MD5 convention identifies the export record; it is not an
integrity or security hash and authorizes no file access.

Optional JSON fields are not accepted. There is no RID, path, filename, reviewer,
notes, or audio-bytes key in this format, and none may be added by the importer.

These native identity conventions are retained so this is not a generic
transcript loader. The following evaluation-only rules are **not** imported:

- Duplicate `dataset_id` or fingerprint rejection.
- Greenwich TGID/channel allowlist matching or filling.
- Nonempty normalized human-reference token requirements.
- Token-count, Levenshtein-cell, and WER metric bounds.
- Control-and-format-character rejection of transcript text.
- Evaluation's rejection of an empty dataset file. An empty `[]Source` request
  still succeeds with a non-nil zero-length result.

## Provenance mapping

Every produced `Record` is constructed as follows. No other `Input` or `Record`
field may be invented.

| Destination | Value |
| --- | --- |
| `Input.Source.Kind` | Always `replay`. Never `synthetic`, and never inferred from `split`. |
| `Input.Source.Reference` | Exact `dataset_id` bytes. |
| `Input.Source.TextKind` | Exact caller `Source.TextKind` for that JSONL document. |
| `Input.Source.RecordingRef` | Exact `audio_fingerprint` bytes. Identifier only; never a filesystem open. |
| `Input.Source.AttemptRef` | `transcription_attempt_id` formatted as a decimal integer with no extras. |
| `Input.Source.Model` | Exact `model` bytes. |
| `Input.Transcript` | Exact selected transcript-field bytes. |
| `Record.Channel` | Exact `channel` string bytes. No trim, case fold, alias, or Greenwich label fill. |
| `Record.TGID` | Exact decoded `tgid` `int64`. No string conversion, range mapping, or talkgroup inference. |

`channel` and `tgid` remain required JSON keys. Copy them onto `Record` by ordinary
assignment of the decoded native types. Do not validate them against the
product-requirements Greenwich talkgroup table, do not fill `Channel` from `TGID`
or the reverse, and do not copy either value into `Input`. Empty `channel` and a
supplied `tgid` of `0` are preserved unchanged.

`Record.Channel` and `Record.TGID` are imported evidence only. They do not create
incidents, authorize talkgroup roles, select dispatch versus status channels, or
grant CAD authority. A later shadow/replay board may display them as provenance
without treating them as operational truth.

A human-reference label remains caller-supplied provenance, not verified truth.
Comparing raw-model text with a human reference requires two independent `Import`
calls, each with the corresponding `TextKind`, and then an explicit later replay
projection if desired.

## Transcript selection

`Source.TextKind` is exact and applies to every object in that JSONL document:

| Caller `TextKind` | Selected field |
| --- | --- |
| `raw_model` | `raw_model_transcript` only |
| `human_reference` | `human_reference_transcript` only |

There is no fallback from an empty selected field to the other field, no blend,
no preference for human reference, and no use of `normalized` or `synthetic`.
Any other `TextKind`, including empty, rejects the whole import before parsing
JSON.

Both transcript keys must still be present as strings. A missing selected key is
already a schema failure. An empty selected string is valid and becomes an empty
`Input.Transcript`. Whitespace, invalid native text, and unsafe controls are
preserved without trimming. The unselected transcript field is ignored and is not
counted in replay string-byte admission.

## Ordering and splitting

Preserve caller-selected source order. For `sources` of length S, emit every
record from source 0, then source 1, through source S-1. Within one JSONL
document, preserve line order. Output position `i` corresponds to the `i`th JSON
object in that concatenated sequence. Do not sort by `reviewed_at`, fingerprint,
channel, TGID, dataset id, or filename.

One JSON object produces exactly one `Record` containing one `Input`, the copied
`Channel`, and the copied `TGID`. Do not split on sentences, clauses, line breaks
inside the transcript, speakers, or radio transmissions. Do not merge objects.
Duplicate objects, duplicate `dataset_id` values, duplicate fingerprints, and
duplicate channel/TGID pairs are retained as separate records with independently
copied provenance. Repetition does not authorize reuse, caching, or suppression.

`split` is not a selector. A source containing mixed train, validation, and test
objects imports all of them. There is no implicit `--split`, `all`, newest-record,
or first-record policy.

## Byte-level handling

Count and preserve Go string bytes, not runes, UTF-16 units, tokens, or encoded
JSON.

- Each `Source.Bytes` document must be valid UTF-8. Invalid UTF-8 rejects the
  whole import. Do not replace bytes with U+FFFD.
- A UTF-8 BOM (`EF BB BF`) at the start of a document rejects the whole import.
  Do not strip a BOM and continue.
- Unpaired JSON `\u` surrogate escapes reject the whole import. Do not accept
  `encoding/json`'s historical replacement behavior.
- JSONL CRLF and LF are record separators only. They are not written into
  `Transcript`.
- CR, LF, CRLF, and tab decoded from JSON string values are preserved exactly.
  Do not normalize line endings or Unicode forms inside transcript text.
- JSONL framing is not itself a transcript. Native UTF-8 byte offsets in later
  processor results index `Input.Transcript` after JSON string decode, which is
  the same string a caller would have passed directly to `Process`. Offsets are
  not indexes into the JSONL file.

This JSON string format cannot carry arbitrary invalid UTF-8 that is not
representable in JSON. Such fixtures remain direct `shadowprocessor.Input` values,
not import records. Import must not invent invalid UTF-8.

## Whole-import admission and exact limits

Admission occurs before returning any `[]Record` and before any replay. Check in
this exact order. Both maxima in each numeric row are inclusive. Scanning may
stop when rejection is established; successful admission must account for the
whole request.

1. Nil or empty `[]Source` succeeds with a non-nil zero-length result.
2. Source count, then each source's `TextKind`, then empty/nil `Bytes`, then each
   source's byte length, then the sum of source byte lengths.
3. JSON parsing of every remaining source, including BOM, UTF-8, line, and schema
   checks.
4. Produced record count, then produced seven-field string bytes.

| Limit | Exact maximum | Enforcement owner |
| --- | ---: | --- |
| Caller sources | 1,000 | Importer, before parsing |
| Each source document | 8 MiB = 8,388,608 bytes | Importer, before parsing |
| Sum of source documents | 8 MiB = 8,388,608 bytes | Importer, before parsing |
| JSON line, excluding line ending | 1 MiB = 1,048,576 bytes | Importer, while parsing |
| Produced input records | 1,000 | Importer, before return |
| Produced `Input` string bytes | 4 MiB = 4,194,304 bytes | Importer, before return; same seven `Input` fields as replay |
| Each transcript | 65,536 bytes | Unchanged shadow processor, per later `Process` |

Source-count rejection takes precedence over source-byte rejection. Produced
record-count rejection takes precedence over produced string-byte rejection.
An over-budget late object prevents returning any otherwise valid prefix.

Count `len` of each Go string value, including named string types. For every
produced `Record.Input`, count exactly the same seven fields as replay:

1. `Source.Kind`
2. `Source.Reference`
3. `Source.TextKind`
4. `Source.RecordingRef`
5. `Source.AttemptRef`
6. `Source.Model`
7. `Transcript`

Every occurrence counts, including duplicates and strings sharing backing
storage. Count original bytes, not unique strings, trimmed values, or JSON
encoding. Empty fields contribute zero. Do not count struct headers, unselected
transcript fields, `duration_ms`, `split`, or `reviewed_at`. Do not count
`Record.Channel` or `Record.TGID` in this 4 MiB budget: they are importer-owned
provenance retained for the caller and are not replayed. Counting them would
reject sequences that remain legal for `shadowreplay.Run`. `TGID` is an `int64`,
not a string. `Channel` remains bounded by the existing 1 MiB JSON-line limit.
This is the only limit adjustment required by the wrapper; the 1,000-record and
4 MiB `Input` maxima stay aligned with replay.

Admission arithmetic must be overflow-safe: maintain a remaining budget initially
equal to 4,194,304 and, before subtracting each field length, reject if that
length exceeds the remaining budget. The 8 MiB source-byte budgets use the same
remaining-budget rule on `len(Source.Bytes)`.

If any import admission limit fails, return a nil result slice and a safe error.
Do not truncate, sample, split into sub-imports, or manufacture inputs for
unparsed objects.

Passing import admission does not waive the processor's 65,536-byte transcript
limit. An admitted 65,537-byte selected transcript is returned to the caller and,
if later replayed, receives the processor's existing failed-input audit record.
A later per-record processor failure is not an import rejection.

Nil and non-nil empty `[]Source` both succeed: return a non-nil, zero-length
`[]Record` and a nil error, with no parsing. This explicit empty result is
distinct from the nil result on admission failure. A present `Source` with nil or
empty `Bytes` is not an empty request; it is `empty source`.

These are input admission limits, not a bound on later audit size, heap, or
runtime. No cancellation API, timeout, or resource-recovery claim is added.

## Failure semantics and safe errors

Import either returns the complete ordered `[]Record` or returns nil records.
There is no partial import. Schema, encoding, BOM, blank-line, type, and limit
failures are import rejections. They are not per-record shadow processing
failures and must not be rewritten as `shadowreplay: one or more records failed`.

Use fixed safe importer error messages:

| Outcome | Error message | Records returned |
| --- | --- | --- |
| Source-count admission failure | `shadowimport: too many sources` | Nil |
| Empty or nil source bytes | `shadowimport: empty source` | Nil |
| Per-source or total source-byte failure | `shadowimport: source bytes exceed limit` | Nil |
| `TextKind` not `raw_model` or `human_reference` | `shadowimport: invalid text kind` | Nil |
| Schema, UTF-8, BOM, blank line, surrogate, or type failure | `shadowimport: invalid source` | Nil |
| Produced-record admission failure | `shadowimport: too many input records` | Nil |
| Produced string-byte admission failure | `shadowimport: input string bytes exceed limit` | Nil |

Constructor errors, if they ever occur, must also remain safe. No error may
contain paths, filenames, transcript fragments, `dataset_id`, fingerprints,
channel, TGID, model, credentials, panic payloads, stack traces, or native
evidence. Do not include source indexes or JSON line numbers in the public error
string. No additional exported error types or per-record import codes are
required.

After a successful import, a future caller may project `Record.Input` values into
`shadowreplay.Run` as shown above. The importer must not invoke replay itself.
Replay then applies its own identical 1,000-record and 4 MiB `Input` limits, which
a successful import is constructed not to exceed, and may still return per-record
processor failures. Native rejected, ambiguous, unresolved, unsupported,
incomplete, and invalid-text outcomes remain completed execution after replay;
they are not import errors. Channel and TGID on `Record` do not change replay
admission, native recognition, or CAD authority.

The importer adds no panic recovery. Fatal runtime termination and hangs remain
outside this guarantee.

## Ownership, reuse, and concurrency

The importer retains no source bytes, history, result cache, timestamp, generated
identifier, retry state, or per-call mutable field. Each `Import` keeps counters
and outputs local.

Each invocation returns a fresh outer `[]Record` and fresh nested `Input` values.
Copied `Channel` strings and `TGID` integers belong to that record. Mutating a
returned `Record`, its `Input`, or its `Channel` must not affect another record,
an earlier or later import, or a later explicit replay call. The importer must
not mutate caller `Source` elements or their `Bytes`. Callers must not mutate
`sources` while `Import` is running. Ordinary assignment of a returned slice may
share string backing storage; this is not a deep-clone API.

One constructed importer must support concurrent independent `Import` calls.
There is no global order across calls. Identical `[]Source` values produce
identical `[]Record` values, including identical `Channel` and `TGID`. The caller
retains external revision/build provenance; `shadow-transcript-import-v1` does
not attest a source revision or runtime.

## Security, privacy, and prohibitions

Private transcript text can contain locations, names, and other sensitive
content. Tests must use synthetic JSONL only. Do not commit datasets, recordings,
or private exports. Existing `.gitignore` rules are not a security boundary.

The package structure must enforce the shadow boundary. The importer must not:

- Decode audio, call FFmpeg, play media, or request Whisper/transcription.
- Read or write PostgreSQL or any store.
- Perform network activity, HTTP, WebSockets, or API/frontend integration.
- Change CAD or operational state, reconcile unit status, or tune vocabularies.
- Treat `Record.Channel` or `Record.TGID` as dispatch authority, talkgroup role,
  or an incident-creation rule.
- Discover files, watch directories, or select newest/latest sources.
- Log transcript or provenance contents.
- Begin Phase 7 PWA/mobile work.

`ShadowOnly` remains a later processor marker. It is descriptive, not a security
boundary. Neither a successful import, retained channel/TGID evidence, nor a later
`both_resolved` audit proves incident identity or operational truth.

## Required Step 6F verification

All fixtures must be synthetic. Required tests and inspection include:

### Admission and format

- Nil and empty `[]Source` return a non-nil empty slice and nil error.
- Source counts 1, 999, 1,000, and 1,001; over-limit rejection parses no prefix.
- Per-source and total source bytes 8,388,607, 8,388,608, and 8,388,609.
- JSON line 1,048,576 admitted as a line; 1,048,577 rejects the import.
- Produced records 1, 999, 1,000, and 1,001 with count precedence over byte overflow.
- Produced seven-field string bytes 4,194,303, 4,194,304, and 4,194,305, including
  metadata, named string kinds, duplicates, and shared backing storage.
- Overflow-safe helpers reject lengths above the remaining budget without wrapping.
- An over-budget late object or late source prevents returning a valid prefix.
- BOM, invalid UTF-8 documents, unpaired surrogates, blank lines, unknown keys,
  nulls, duplicate keys, trailing JSON, wrong types, unknown splits, zero
  `reviewed_at`, negative `duration_ms`, and mismatched dataset-id/fingerprint
  pairs reject the whole import.
- Greenwich-unlike channel/TGID pairs that still have the required types are
  accepted and copied unchanged onto `Record.Channel` (`string`) and `Record.TGID`
  (`int64`). Empty `channel` and `tgid` `0` are preserved. Values are not trimmed,
  aliased, stringified, or filled from each other.
- Duplicate `dataset_id` and fingerprint lines are retained, each with its own
  copied channel/TGID.

### Selection, order, and mapping

- `raw_model` selects only `raw_model_transcript`; `human_reference` selects only
  `human_reference_transcript`; empty selected text is preserved.
- Empty selected text does not fall back to the other field.
- `synthetic` and `normalized` `TextKind` values reject before parsing.
- Multiple sources concatenate in caller order; line order inside each source is
  preserved; `reviewed_at` does not reorder. Output position matches that order.
- One object produces one `Record`; duplicates remain; no sentence splitting.
- Mapped `Input` fields equal the table above, including `Kind=replay` and decimal
  `AttemptRef`.
- `Record.Channel` and `Record.TGID` equal the source JSON values byte-for-byte
  and integer-for-integer after decode. Mutating one record's provenance does not
  change another record or a later import.
- CRLF JSONL framing does not alter decoded CRLF or LF inside transcript strings.
- A 0, 65,535, 65,536, and 65,537-byte selected transcript is returned unchanged;
  only the last is a later processor failure, not an import failure.

### Replay compatibility, ownership, and scope

- A successful import's projected `[]Record.Input` slice is admissible to
  `shadowreplay.Run` without replay admission errors caused by count or
  seven-field byte budget. Production import code must not call `Run`.
- Direct `Process` parity on imported transcripts matches the selected bytes,
  including native offsets into `Input.Transcript`.
- Import rejection is distinct from later `shadowreplay: one or more records failed`.
- Errors disclose none of the forbidden contents.
- The importer never mutates caller bytes and retains no history after success
  or rejection.
- Repeated and 1,000 concurrent `Import` calls remain isolated and deterministic,
  including unchanged channel/TGID copies.
- Dependencies include neither evaluation/review/database/network/audio packages
  nor calls to `Run`/`Process` from production import code. Tests may project
  `Record.Input` and call them on synthetic outputs.
- Race tests require a supported C compiler. Local Windows race testing is
  presently blocked by missing GCC; ordinary tests do not prove race-detector
  success.

No lost field, rewritten transcript, discarded or guessed channel/TGID, reordered
or dropped duplicate, silent truncation, unsafe disclosure, or forbidden side
effect is acceptable. This document defines future verification requirements; it
does not claim Step 6F tests or implementation have run.

## Unresolved questions

These do not block this contract:

1. An explicit-path CLI that reads a caller-supplied JSONL file remains a
   separate milestone. v1 does not open files.
2. Experiment-result JSONL that reuses this object shape is accepted if it
   satisfies the schema. The importer does not mark experimental raw text as
   distinct from review-export raw text; `TextKind` and `Model` remain the
   caller's provenance.
