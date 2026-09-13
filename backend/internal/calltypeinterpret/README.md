# Step 5B4: conservative call-type interpretation

This offline package applies narrow syntactic context rules to
[Step 5B3 candidate evidence](../calltypecandidate/README.md), using the
[validated catalog](../calltypedata/README.md) derived from
[Step 5B1](../../../docs/call-type-vocabulary-v1.md).
It does not duplicate the phrase vocabulary or implement another extractor.
Interpretation is not verified dispatch truth.

## API and result contract

`New() (*Interpreter, error)` constructs an immutable interpreter.
`Interpret(transcript string) Result` accepts exactly one transcript. Calls retain
no transcript state, never combine transmissions, and are safe to run concurrently.
Each result owns its collections.

`Result.Transcript` retains the unchanged original string. `State` describes
call-type resolution only:

| State        | Meaning                                   | CallType             |
| ------------ | ----------------------------------------- | -------------------- |
| `resolved`   | Exactly one distinct accepted call type   | That canonical value |
| `ambiguous`  | More than one distinct accepted call type | Empty                |
| `unresolved` | No accepted call type                     | Empty                |

`Alternatives` contains distinct accepted types in first-evidence order, without
priority. Repeated evidence for one type can resolve to that type; competing
accepted types are never settled by first, last, frequency, length, or severity.
Unsupported or incomplete mentions do not themselves add an alternative.

The ordered `CallTypes`, `AlarmLevels`, and `Qualifiers` collections remain
separate. They contain accepted and rejected `Evidence` records, including every
repeated candidate. Each record embeds the original candidate and adds
`Accepted`, `RejectionReason`, and `AssociatedCallType` (for associated qualifiers).
Accepted records have an empty rejection reason.

Catalog metadata (`Exact`, `Kind`, `CanonicalValue`, and
`RequiresDispatchContext`) remains unchanged. `Evidence` text and zero-based
UTF-8 byte offsets `[Start, End)` are preserved exactly:
`transcript[Start:End] == Evidence`. These are byte offsets, not character indices.
Each evidence collection retains transcript order.

## Accepted narrow context grammar

- Clause boundaries are period, semicolon, question mark, exclamation mark, CR,
  and LF. Candidate evidence crossing any of these boundaries is rejected.
- Catalog call-type phrases already beginning with `respond` must start their
  clause, apart from whitespace.
- Other call-type candidates require exactly `respond`, `respond to`, or
  `respond to a` as their clause prefix. Cue comparison ignores case and collapses
  whitespace; no other prefix words are accepted.
- A call-type candidate must end its clause, apart from whitespace. The only
  suffix exception is an ALARM RESD or ALARM COMM candidate followed by a comma
  and one complete alarm qualifier that ends the same clause.
- A qualifier associates only with that preceding accepted alarm candidate and
  only if the entire transcript resolves to that alarm type. The intervening
  text must trim to exactly a comma. Ambiguous or missing alarm types leave
  qualifier evidence unassociated.
- STILL, MINOR, BOX, and WORKING FIRE evidence is accepted independently only
  when it occupies a complete standalone clause.
- Any straight or curly quote/apostrophe anywhere in the transcript rejects all
  candidates. This intentionally includes contractions and unmatched quotes;
  the implementation does not infer quotation scope.

Phrase matching and its punctuation normalization belong to Step 5B3. The
interpreter imposes the stricter clause and prefix/suffix rules above. It has no
additional approved phrase aliases.

General fire activation and smoke activation are qualifiers, never standalone
call types. Alarm levels cannot select a type. Generic alarm, CO wording without
approved symptom evidence, highways, addresses, and units cannot invent a type.

## Rejections and audit evidence

| Rejection reason                   | Meaning                                                                         |
| ---------------------------------- | ------------------------------------------------------------------------------- |
| `quoted_or_apostrophe_text`        | A quote or apostrophe occurs anywhere in the input                              |
| `cross_clause_evidence`            | Candidate text contains a clause boundary                                       |
| `unsupported_dispatch_prefix`      | The call-type prefix does not satisfy the response grammar                      |
| `unsupported_dispatch_suffix`      | Extra clause text follows a call-type candidate outside the qualifier exception |
| `unsupported_level_context`        | Alarm-level evidence is not a standalone clause                                 |
| `no_unambiguous_same_clause_alarm` | Qualifier evidence lacks an eligible association                                |

Quotation rejection precedes cross-clause checks; call-type prefix rejection
precedes suffix rejection. Rejected candidate metadata and offsets remain
available for auditing. Unknown text with no candidate produces no evidence
record.

Invalid UTF-8 or unsafe controls/format characters are rejected by Step 5B3,
yielding an unresolved result with no evidence. The original input is retained;
there is no separate input-error status.

## Deliberate limitations

The closed grammar rejects negated, historical, hypothetical, and operational
wording when it introduces unsupported prefix or suffix text. It is not a
general semantic detector, and does not inherit such context across clauses.
Addresses after a call phrase, unit preambles, conjunctions, multiple qualifiers
in one clause, and broader response wording remain unsupported.

Overlapping Step 5B3 candidates are preserved. For example, in
`respond natural gas leak`, the complete candidate can be accepted while the
contained `gas leak` candidate is rejected for its unsupported prefix.

Ambiguous and unresolved outcomes are intentional. No primary incident is
selected and no operational decision is made. There is no address-behavior
change, database access, API, Whisper/transcription integration, unit/status or
incident-state update, CAD action, or production wiring.

## Tests

The synthetic [tests](interpret_test.go) cover all 12 call types, four levels,
both qualifiers, repetition, conflicting evidence, rejected context, incomplete
and invalid input, byte offsets, determinism, concurrency, and call isolation.

Run from `backend/`:

```sh
go test ./internal/calltypeinterpret
```
