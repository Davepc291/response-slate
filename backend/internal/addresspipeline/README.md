# Step 5A5: offline address-processing composition

Construct with `New()`, then call `Process(transcript string)` with exactly one
transcript. The pipeline invokes the existing `addresscandidate.Extract` first,
then `addressrole.Interpret`, passing the same unchanged original string to both.
The role API accepts a transcript rather than a candidate slice and internally
performs extraction again. This wrapper preserves that API and does not copy or
reimplement any matching, validation, normalization, or role rules.

`Result` contains:

- `Transcript`: the original string, unchanged even for rejected input.
- `Candidates`: every native candidate in transcript order, without grouping.
- `Interpretation`: the complete native `addressrole.Result`, including primary
  mentions, cross streets, unresolved candidates, alternatives, and reasons.
- `State`: the same `addressrole.State` as `Interpretation.State` at return time.

The existing state vocabulary is retained: `resolved`, `ambiguous`, `unsupported`,
and `no evidence`. There is no new `unresolved` enum or fallback policy:
unsupported/no-evidence outcomes have no resolved primary and need later handling.
A resolved result can still include unresolved operational mentions. Ambiguous
results retain all alternatives and select no primary. Repeated mentions remain
separate in `Candidates`; grouping in `Interpretation` is solely the existing
role interpreter's behavior. Cross-street roles are never turned into numeric
candidates or merged into the primary.

Canonical names, dictionary kinds, evidence text, and original UTF-8 byte offsets
are returned unchanged. Every evidence span uses `[Start, End)` into `Transcript`.
Invalid text is handled by the dependencies without an invented address. Keeping
the original string does not make invalid UTF-8 suitable for downstream encoders.

The constructed pipeline contains immutable matchers, no transcript history or
result cache. Concurrent calls are supported. Each result's mutable slices and
pointers belong to that call; changing one result does not change another or the
pipeline. Callers must synchronize their own mutations if sharing a result.

This package only composes extraction and interpretation. It does not verify
dispatch truth, geocode, update CAD state, or connect to production. Ambiguous and
unresolved results require later handling. No fuzzy/phonetic matching, suffix
inference, correction, maps, database access/migrations, API routes, Whisper,
call-type/unit-status parsing, or incident/CAD wiring is added.

From `backend/`:

```powershell
go test ./internal/addresspipeline -v
go vet ./...
go test ./...
```

Tests cover required addresses and access roads, native API parity, repeated and
conflicting mentions, cross streets, unsupported input, exact evidence offsets,
result isolation, deterministic execution, and concurrent calls.
