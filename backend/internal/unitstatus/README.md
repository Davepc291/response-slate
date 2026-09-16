# Step 5D4: offline unit-status evidence

`unitstatus.New()` constructs an immutable matcher using the existing
`unitrecognition.New().Catalog()` as its only unit identity source. Call
`Matcher.Recognize` with one unchanged transcript. Nothing is wired into the
production pipelines or operational state.

The [approved status contract](../../../docs/unit-status-vocabulary-v1.md) defines
all 20 phrases and nine statuses. Each association selects one unique catalog
identity. Explicitly coordinated units in one approved clause may each receive
the immediately following status. Ambiguous identities remain unassociated;
unrelated clauses never share a status. Repeated evidence remains separate.
ON AIR and TRAINING remain distinct; CLEAR has evidence but no canonical mapping.

## Evidence and associations

`Result` retains `Transcript`, source-ordered `StatusEvidence`, separate
`Associations`, and association `State`/`Reason`. Each evidence span stores exact
original text and UTF-8 byte offsets `[Start, End)`. Each association stores its
status evidence and index, clause and linking cue spans, candidate unit evidence,
disposition, and deterministic reason. `Unit` is non-nil only for an accepted
proposed association. Candidates are audit evidence, never fallback assignments.

An accepted status-evidence item means the exact supported phrase occurred. It
does not establish an asserted status or unit association. Questions and quoted
text retain rejected evidence; CLEAR remains unresolved. Negated, historical,
hypothetical, command, acknowledgement, and other unsupported context cannot
pass the closed association grammar, even when supported phrases occur within it.
Neither evidence nor an accepted association establishes verified operational state.

Accepted single-unit shapes are `unit status`, `unit, status`, and `unit is
status`. Coordinated forms are `unit and unit status` and `unit and unit are
status`; additional units must also be joined explicitly by `and`. Comma-only
lists and `or` alternatives are unsupported. One optional comma may precede the
status or linker. Only spaces/tabs may separate
phrase words. ASCII case comparison preserves the original text. Complete token
boundaries prevent numeric, suffix, Unicode, and symbol collisions. Longest match
wins only at the same start; nested evidence at different starts remains visible.
For example, `back in service` retains nested `in service` evidence, but only the
complete clause shape can associate.

Period, semicolon, colon, exclamation mark, question mark, CR, and LF end clause
scope. Adjacent terminal punctuation remains together: `...?`, `!?`, and `?!`
remain questions, with unchanged evidence offsets. CR/LF still end scope
independently. Questions cannot become assertions. As a conservative quotation rule,
any straight/curly quote or apostrophe rejects associations throughout the input,
including unmatched quotes. Invalid UTF-8 and unsafe C0 controls except tab/CR/LF
produce invalid-input results without evidence. No context crosses calls,
transmissions, or clause boundaries.

Conflicting otherwise-supported statuses for the same unit remove both accepted
associations and make result state `ambiguous`; all candidates and status evidence
remain available. At least one accepted association with no supported conflict
makes state `resolved`. Otherwise it is `unresolved`. Multiple statuses outside
the closed grammar remain unassociated; there is no first/last or severity rule.

All returned slices, candidate collections, and unit pointers are caller-owned;
mutating one cannot change another association or call. The matcher retains no
transcript state. Callers sharing a returned result must synchronize mutations.

## Bounded duplication and scope

The clause matcher independently matches approved unit catalog phrases without
calling Step 5D2 `Recognize`, resubmitting altered text, or consuming/promoting its
rejected evidence. Step 5D2's grammar, behavior, result types, and API are unchanged.
This small duplication of matching logic is deliberate technical debt authorized
by Step 5D3; the catalog itself is not duplicated. The narrower status-clause
punctuation rules do not inherit Step 5D2's broader mention-only punctuation.

No database, API route, frontend, CAD, display/color, RID/channel interpretation,
Whisper/transcription, networking, persistence, or automatic transition is added.

## Synthetic validation

From `backend`:

```sh
gofmt -l internal/unitstatus
go test ./internal/unitstatus ./internal/unitrecognition
go vet ./...
go test ./...
go test -race ./internal/unitstatus ./internal/unitrecognition
```

Tests cover every approved status phrase and all existing unit identities,
single-unit and coordinated-list grammar, exclusions and CLEAR, speech roles,
mixed terminal punctuation, multiple/ambiguous units,
conflicts, clause/transmission isolation, numeric and phrase collisions, nested
evidence, both documented byte-offset examples and a multibyte emoji prefix,
invalid input, source ordering, mutation isolation, and 1,000 concurrent calls.
Race detection requires a working cgo/C compiler toolchain.
