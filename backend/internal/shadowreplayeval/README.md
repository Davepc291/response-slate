# Shadow replay evaluation v1

Implements the [approved evaluation contract](../../../docs/shadow-replay-evaluation-v1.md)
as a private, deterministic composition of unchanged
[shadow import](../shadowimport/README.md) and [shadow replay](../shadowreplay/README.md).
Construct with `New()`, then call `Evaluate(set, sources)` with an already
materialized `[]shadowimport.Source`. A constructed evaluator supports concurrent
calls and retains only its importer and runner. The zero value is not ready for use.

`SetKind` is `development` or `held_out` and names the caller-selected documents.
The evaluator does not read JSONL `split`, discover files, or open paths.
One `Evaluate` call admits a single `TextKind`. Mixed kinds reject before import.
`raw_model` versus `human_reference` requires two independent calls.

`New` constructs one importer and then one runner. Constructor failure returns a
nil evaluator and `shadowreplayeval: construction failed`. `Evaluate` rejects an
invalid set with `shadowreplayeval: invalid evaluation set` and mixed text kinds
with `shadowreplayeval: mixed text kind`. Import and replay errors are returned
unchanged. An impossible audit/import length mismatch, reachable in tests through
the private run seam, returns `shadowreplayeval: pairing mismatch`.

After a successful import, `Record.Input` values are projected in import order
into one `Run`. Each `Pair` keeps `records[i].Channel`, `records[i].TGID`, and
`audits[i]` by index. Duplicates remain separate. Channel and TGID are evidence
only and are not copied into `Input`. Native dispatch, unit, and status results
are counted without reconciliation. `both_resolved` is a histogram key, not
incident identity. A non-nil unit-status `Unit` is a proposed association, not
current apparatus state.

Nil or empty sources return non-nil empty pairs, empty histogram maps, and a nil
error. Whole-import rejection returns a zero result and does not call replay.
Replay continuation returns the complete paired result plus
`shadowreplay: one or more records failed`.

`Result.Pairs` is in-memory inspection only. `Summary` holds approved aggregate
counts, histograms, and integrity flags. It does not contain transcripts, paths,
or identifiers. There is no JSON, file, network, or board export.

Tests use synthetic JSONL only. Run from `backend`:

```text
gofmt -l internal/shadowreplayeval
go test ./internal/shadowreplayeval ./internal/shadowimport ./internal/shadowreplay ./internal/shadowprocessor
go vet ./...
go test ./...
go test ./internal/shadowreplayeval -cover
go test -race ./internal/shadowreplayeval
```

Race verification requires a supported C compiler; ordinary tests do not substitute
for a successful race-detector run. Local Windows race testing is blocked when GCC is
unavailable.
