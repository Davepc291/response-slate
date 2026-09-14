# Step 5C2: offline shadow dispatch interpretation

Implements the [approved contract](../../../docs/dispatch-interpretation-v1.md).
The contract does not specify a Go package/type name; this package follows the
existing pipeline convention: `New() (*Pipeline, error)` and
`Process(transcript string) Result`.

Process invokes address first, then call type, with the exact same original string.
Both complete native results are retained independently. Result fields are
`Version` (always `dispatch-interpretation-v1`), `ShadowOnly` (always true),
`Transcript`, `Address`, `CallType`, `AddressState`, `CallTypeState`, and
`ShadowState`. Mirrored fields agree when returned.

| Address state | Type resolved | Type unresolved | Type ambiguous |
| --- | --- | --- | --- |
| resolved | both_resolved | address_only_resolved | contains_ambiguity |
| unsupported | type_only_resolved | neither_resolved | contains_ambiguity |
| no evidence | type_only_resolved | neither_resolved | contains_ambiguity |
| ambiguous | contains_ambiguity | contains_ambiguity | contains_ambiguity |

These are the only five summary states. Unknown future native states panic as
a programming/contract error rather than silently fall back. Invalid transcript
input is not such an error: it retains current native in-memory outcomes.

All candidates, grouped mentions, cross streets, call types, alarm levels,
qualifiers, rejected evidence, and native reasons remain unchanged and separate.
Evidence retains original UTF-8 byte offsets [Start, End). No shared diagnostics,
normalization, serialization, or cross-component inference is introduced.
Partial resolution is valid.

Construct once and reuse concurrently. The pipeline has no transcript history.
Each result owns its mutable collections and nested pointers; mutation cannot
affect other results or the pipeline. Callers sharing one returned result must
synchronize their own mutations. Use New; the zero value is not initialized.

Existing duplicate extraction (twice per component) and differing grammars remain
temporary technical debt. Neither is refactored or silently harmonized here.

Shadow summaries are non-operational. Even both_resolved establishes neither
event identity nor a relationship between address and type, dispatch truth, or
readiness. There is no incident creation, unit assignment, alarm-level selection,
CAD authority, logging, persistence, configuration, or production wiring.

Run from backend:

```sh
go test ./internal/dispatchinterpretation -v
go vet ./...
go test ./...
```

Tests cover all 12 matrix cells directly, approved transcript fixtures with native
parity, byte spans, invalid input, isolation, and 1,000 concurrent calls including
result mutations. Some matrix combinations are not currently reachable through
native transcripts; matrix tests do not invent transcripts to force them.
