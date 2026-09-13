# Step 5B5: offline call-type pipeline

`New() (*Pipeline, error)` constructs immutable matchers once.
`Process(transcript string) Result` accepts exactly one unchanged transcript,
invokes [candidate extraction](../calltypecandidate/README.md), then
[interpretation](../calltypeinterpret/README.md). Both use the existing
[validated catalog](../calltypedata/README.md). This package only composes
Steps 5B2–5B4; it contains no vocabulary, normalization, or resolution rules.

The native interpreter accepts a string and extracts its own evidence internally,
so extraction occurs twice per call. The wrapper preserves that API without
modifying dependencies or copying their algorithms.

Results contain the original `Transcript`, all ordered `Candidates`, the complete
native `Interpretation`, and `State` copied from `Interpretation.State` at return.
Separate call-type, alarm-level, and qualifier evidence is available through
`Interpretation.CallTypes`, `Interpretation.AlarmLevels`, and
`Interpretation.Qualifiers`. Accepted and rejected evidence, reasons, canonical
metadata, repeated mentions, and original UTF-8 byte offsets are unchanged.
Every evidence span is `[Start, End)` into the original transcript.

The existing `resolved`, `ambiguous`, and `unresolved` states are intentional.
Only Step 5B4 selects a resolved canonical type; conflicting accepted types stay
ambiguous. Levels and qualifiers cannot invent a type. No context grammar is
expanded: quoted, negated, historical, hypothetical, operational, and incomplete
text retain the existing conservative treatment and limitations.

The pipeline retains no transcript history or result cache. Concurrent calls are
safe, including 1,000 calls through a single pipeline in the tests. Each result
owns its mutable collections; callers can change them without affecting earlier
or future results or internal state. Callers must synchronize mutations if they
share one returned result. Construct with New; the zero value is not ready to use.
Invalid input retains its original string and the native unresolved/no-evidence
behavior, without a new validation or error policy.

Interpretation is conservative syntactic classification, not verified dispatch
truth. No incident creation, primary operational decision, CAD action, inference
from units/addresses/highways, or production integration occurs. The package adds
no database, network, filesystem, logging, or environment dependencies.

From `backend/`:

```sh
go test ./internal/calltypepipeline -v
go vet ./...
go test ./...
```
