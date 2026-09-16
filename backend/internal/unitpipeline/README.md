# Step 5D5: offline unit composition

`New() (*Pipeline, error)` constructs one `unitrecognition.Matcher` and one
`unitstatus.Matcher`, propagating either constructor error without a partial
pipeline. Use New; the zero value is not initialized.

`Process(transcript string) Result` invokes unit recognition first and status
recognition second, independently passing exactly the same unchanged string.
Neither native result is fed into the other recognizer.

```go
type Result struct {
    Transcript string
    Units      unitrecognition.Result
    UnitStatus unitstatus.Result
}
```

Both complete native results remain intact: nil versus empty collections,
ordering, repetition, rejected evidence, reasons, alternatives, candidates,
associations, StatusIndex references, and original UTF-8 byte offsets `[Start, End)`.
No merged evidence, sorting, filtering, deduplication, reconciliation, winner,
aggregate state, readiness state, or new resolution policy is introduced.

The [unit contract](../../../docs/unit-vocabulary-v1.md) and
[status contract](../../../docs/unit-status-vocabulary-v1.md) remain independent.
For `engine 2 on scene`, Units retains Step 5D2's `unsupported_context` rejection
while UnitStatus can retain Step 5D4's accepted bounded association. Accepted
lexical status evidence is not itself an accepted association. An association
is a proposal, never verified operational state.

Questions (including mixed terminal punctuation), negation, commands, history,
hypotheticals, quotations, and unsupported context keep the native outcomes and
reasons. The wrapper does not classify speech roles. Conflicts stay ambiguous;
incomplete evidence stays incomplete. Each recognizer handles invalid input using
its own existing validation policy. Their different Unicode/control and
punctuation rules are not harmonized. The original string is retained even when
it contains invalid UTF-8; no encoding or serialization occurs here.

The pipeline retains only immutable matchers, no transcript, result, timestamp,
history, or mutable per-call state. Calls never combine transmissions. Fresh
native results preserve their caller-ownership guarantees; mutating one result
cannot affect another call, matcher, or independent branch. This envelope is not
a deep-clone API: assigning an existing Result to another variable uses ordinary
Go value-copy semantics and may share its slices/pointers. Callers sharing a
returned result must synchronize mutations.

Private function-parameter seams test constructor failures, execution order,
and synthetic native ambiguity without public injection APIs, alternate catalogs,
or global mutable hooks. No recognition logic is duplicated in this package.

No database/schema, API/wire contract, frontend, CAD, Whisper/transcription,
RID/TGID/channel/assignment/speaker inference, incident/apparatus state, automatic
transitions, persistence, networking, external AI, dependencies, or production
wiring is added. Step 5D2 and Step 5D4 remain unchanged.

From `backend`:

```sh
gofmt -l internal/unitpipeline
go test ./internal/unitpipeline ./internal/unitrecognition ./internal/unitstatus
go vet ./...
go test ./...
go test ./internal/unitpipeline -cover
go test -race ./internal/unitpipeline ./internal/unitrecognition ./internal/unitstatus
```

Tests compare complete native outputs for all 26 unit phrases, all 20 status
phrases, and representative contract failure cases. They cover source offsets,
ordering, constructor errors, synthetic ambiguity and nil/empty results,
ownership isolation, repeated calls, and 1,000 concurrent calls. Race detection
requires cgo and a supported C compiler; GCC is unavailable in the current local
Windows environment. No system software is installed by this package.
