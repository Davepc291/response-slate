# Shadow transcript import v1

Implements the [approved import contract](../../../docs/shadow-transcript-import-v1.md)
as a private, deterministic, read-only mapper from caller-selected Step 3A
review-export JSONL bytes to importer-owned records. Construct with `New()`, then
call `Import([]Source)`. A constructed importer supports concurrent calls and
retains no sources or history. The zero value is not ready for use.

Each `Source` is already materialized JSONL plus a `TextKind` of `raw_model` or
`human_reference`. `Import` returns `[]Record`. Each `Record` keeps a
`shadowprocessor.Input` beside exact `Channel string` and `TGID int64` copies from
the native JSONL schema. The importer never opens paths, discovers files, watches
directories, or selects the newest source.

`raw_model` selects only `raw_model_transcript`; `human_reference` selects only
`human_reference_transcript`. Empty selected text is preserved with no fallback.
`Input.Source.Kind` is always `replay`. `dataset_id`, fingerprint, decimal attempt
id, and model map to the processor source fields. Transcript bytes are not
rewritten. Channel and TGID are copied by ordinary assignment: no trim, allowlist,
guessing, or fill. Empty channel and TGID `0` are kept. They are evidence only and
are not copied into `Input` or sent to replay.

Admission is whole-import. Nil or empty `[]Source` returns a non-nil empty slice.
Limits are 1,000 sources, 8 MiB per source and in total, 1 MiB JSON lines, 1,000
records, and 4,194,304 seven-field `Input` string bytes, matching replay. Channel
and TGID are outside that byte budget. Count checks precede byte checks. Overflow-
safe remaining-budget arithmetic rejects a length above the remaining budget
before subtracting. Over-limit requests return a nil slice and a fixed safe error
before any prefix is kept. The processor's 65,536-byte transcript limit is applied
later, not here.

The importer does not call `shadowreplay.Run` or `Process`. A caller that wants
replay projects `Record.Input` values explicitly. Import rejection is distinct from
later per-record processor failures. Errors never contain paths, transcripts,
identifiers, or secrets.

Tests use synthetic JSONL only. Run from `backend`:

```text
gofmt -l internal/shadowimport
go test ./internal/shadowimport ./internal/shadowreplay ./internal/shadowprocessor
go vet ./...
go test ./...
go test ./internal/shadowimport -cover
go test -race ./internal/shadowimport
```

Race verification requires a supported C compiler; ordinary tests do not substitute
for a successful race-detector run. Local Windows race testing is blocked when GCC is
unavailable.
