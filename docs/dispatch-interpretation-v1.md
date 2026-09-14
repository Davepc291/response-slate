# Step 5C1: approved shadow dispatch-interpretation contract v1

**Dave approved all ten Step 5C1 design decisions. Documentation only; Step 5C2 is not implemented here.**
Audited clean HEAD `d7448f2`. CI green is supplied starting context, not
evidence of dispatch correctness. This document adds no implementation.

## Audited components

| Component            | Sources inspected                                                                                                                                                                 | Existing behavior                                                                             |
| -------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Address pipeline     | [API](../backend/internal/addresspipeline/pipeline.go), [tests](../backend/internal/addresspipeline/pipeline_test.go), [README](../backend/internal/addresspipeline/README.md)    | Original transcript, all numeric candidates, role result, native state                        |
| Call-type pipeline   | [API](../backend/internal/calltypepipeline/pipeline.go), [tests](../backend/internal/calltypepipeline/pipeline_test.go), [README](../backend/internal/calltypepipeline/README.md) | Original transcript, all phrase candidates, interpretation, native state                      |
| Address role model   | [Implementation](../backend/internal/addressrole/interpret.go)                                                                                                                    | Primary/alternative address groups, cross streets, unresolved candidates, result-level reason |
| Call-type role model | [Documentation](../backend/internal/calltypeinterpret/README.md)                                                                                                                  | Separate accepted/rejected call types, levels, qualifiers, and per-evidence reasons           |

Tests already cover native-output parity, repeated mentions, ambiguity, byte
offsets, result isolation, and concurrent reuse. Call-type tests include 1,000
calls through one shared pipeline. These are existing test findings from source
inspection; no Go tests were run for this documentation milestone.

## Independent processing of one transcript

A future wrapper would construct both pipelines once, then pass the exact same
original string independently to each `Process(transcript)`. Neither pipeline
receives the other's result. Do not trim, normalize, split, rewrite, or concatenate
the input before calling either pipeline.

Approved deterministic invocation order is address then call type. This is
execution order only, never operational priority. Both outputs are retained even
if only one resolves. Construction errors should prevent creating a usable
wrapper rather than fabricate an unresolved processing result.

Evidence from different calls or transmissions must never be combined, even if
their wording, units, locations, or recording times seem related. No timing
window, previous-result cache, grouping by radio identity, or inferred association
belongs in this contract.

## Approved in-memory result

Dave approved the field names below. This is an in-memory design, not an implemented Go type or JSON schema.

| Field           | Approved content                                       |
| --------------- | ------------------------------------------------------ |
| `Version`       | Fixed contract identifier `dispatch-interpretation-v1` |
| `ShadowOnly`    | Always true; no configurable operational mode          |
| `Transcript`    | Original string, unchanged                             |
| `Address`       | Complete native `addresspipeline.Result`               |
| `CallType`      | Complete native `calltypepipeline.Result`              |
| `AddressState`  | Exact native `Address.State` at return                 |
| `CallTypeState` | Exact native `CallType.State` at return                |
| `ShadowState`   | Mechanical summary from the table below                |

Every duplicated transcript/state field must agree at return time. Native results
remain authoritative. Do not flatten or discard their evidence to make a unified
address/type record.

Preserve the following separately:

- Address candidates: house number, canonical street, dictionary kind, evidence
  and byte offsets; repeated candidates stay separate.
- Address role output: syntactically supported primary or alternatives with all
  mentions and response cues, cross-street groups with mentions/cues, unresolved
  candidates, and native `Reason`.
- Call-type candidates: exact catalog phrase, kind, canonical value,
  dispatch-context-required flag, evidence, and byte offsets, including overlaps.
- Call-type interpretation: separate `CallTypes`, `AlarmLevels`, `Qualifiers`,
  alternatives, accepted flags, rejection reasons, and qualifier associations.

Offsets are original zero-based UTF-8 bytes `[Start, End)`, not rune counts,
UTF-16 indices, or normalized-text offsets. Slicing the same original string
must reproduce `Evidence` (or `Text` on address role evidence) exactly.
Cross-street roles are not numeric addresses; alarm levels and qualifiers are
not call types.

## Native states and approved overall shadow states

Address uses `resolved`, `ambiguous`, `unsupported`, and `no evidence`.
Call type uses `resolved`, `ambiguous`, and `unresolved`.
When this document says an address is unresolved, it refers collectively to
`unsupported` or `no evidence`; it does not introduce a replacement native enum.

| Address state | Type resolved      | Type unresolved       | Type ambiguous     |
| ------------- | ------------------ | --------------------- | ------------------ |
| resolved      | both_resolved      | address_only_resolved | contains_ambiguity |
| unsupported   | type_only_resolved | neither_resolved      | contains_ambiguity |
| no evidence   | type_only_resolved | neither_resolved      | contains_ambiguity |
| ambiguous     | contains_ambiguity | contains_ambiguity    | contains_ambiguity |

The five approved `ShadowState` values express only this matrix. Ambiguity is
summarized without hiding which component is ambiguous or whether the other
resolved. This is not an operational priority or severity rule. Unknown future
native states require explicit contract review, not a silent fallback.

`both_resolved` means two independent syntactic results exist in one transcript.
It does not prove they describe the same event, authorize pairing candidates, or
establish dispatch truth. `neither_resolved` can still contain useful evidence.
A resolved address can still include unresolved address mentions.

## Accepted, rejected, ambiguous, and unresolved evidence

Keep native reasons verbatim. The address interpreter supplies result-level reasons
such as `no supported primary address` and
`different explicitly supported primary addresses`; it does not supply a reason
or accepted boolean for each numeric candidate. Its `Unresolved` collection
preserves unsupported candidates, but is not an equivalent of the call-type audit
record. Unknown phrases/streets are not exhaustively recorded as rejections.

Call-type interpretation supplies per-evidence reasons such as
`unsupported_dispatch_prefix`, `unsupported_dispatch_suffix`, and
`no_unambiguous_same_clause_alarm`. It has no result-level reason string.
Zero accepted types yields unresolved; multiple distinct accepted types yields
ambiguous with alternatives. Preserve those facts rather than fabricate a native
reason. Any future human-readable explanation should be explicitly derived from
these fields and must not invent missing per-candidate diagnostics.

The approved design adds no merged reason list or common accepted-evidence enum.
This preserves all available acceptance/rejection information while making the
asymmetry visible.

## Findings and incompatibilities to preserve

1. Both pipelines expose compatible `New()` and `Process(string)` shapes, but
   return distinct state types and evidence structures. No adapter should coerce
   `unsupported` and `no evidence` into one hidden state.
2. Each pipeline extracts candidates once for its outer result and again inside
   its interpreter. Composing both therefore invokes address extraction twice and
   call-type extraction twice per transcript. Constructors also build separate
   matchers. Record this duplication; do not refactor existing APIs in Step 5C1.
3. Address accepts response bridges leading into supported numeric addresses.
   Call type generally requires its candidate to end the clause, with only the
   narrow alarm-qualifier exception. Thus
   `respond to a residential alarm at 93 Doubling Road` can resolve an address
   while rejecting ALARM RESD evidence for its suffix. Do not broaden either
   grammar to force agreement.
4. Quote, clause, punctuation, and unsafe-input behavior differ. Call type rejects
   all candidate interpretations if any quote/apostrophe occurs anywhere.
   Address does not share that global policy. Invalid UTF-8 yields address
   `unsupported` with a reason, but type `unresolved` with no candidates.
   Retain the discrepancy rather than claim common semantic validation.
5. Original strings may retain invalid UTF-8. Standard JSON encoding can replace
   invalid bytes and break offset fidelity. This design is in-memory only;
   invalid input retains native outcomes, and JSON/wire encoding is deferred until byte preservation is defined.
6. Address groups repeated primary/cross-street roles while preserving mentions;
   call-type evidence remains per candidate and alternatives are distinct values.
   Keep native grouping and order, without cross-pipeline fuzzy deduplication.

## Synthetic example contracts

All examples below are invented documentation fixtures, not recordings, private
transcripts, real incidents, or verified dispatch truth. They are projected from
the inspected implementation, not a newly executed integration test.

These are abbreviated contract views. Every full result would additionally retain
the complete native results and evidence described above. Square-bracket spans are
byte offsets into the exact ASCII transcript shown.

| Example and original Transcript                                                     | AddressState | CallTypeState | ShadowState           | Preserved content                                                                                                                                              |
| ----------------------------------------------------------------------------------- | ------------ | ------------- | --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Both: `respond to 93 Doubling Road; respond gas leak`                               | resolved     | resolved      | both_resolved         | Address 93 / doubling road; HAZARD GAS. Address evidence `93 Doubling Road` [11,27); type evidence `gas leak` [37,45). No assertion they refer to one incident |
| Address only: `respond to a residential alarm at 93 Doubling Road`                  | resolved     | unresolved    | address_only_resolved | Address retained; ALARM RESD candidate retained with unsupported_dispatch_suffix                                                                               |
| Type only: `respond gas leak`                                                       | unsupported  | resolved      | type_only_resolved    | No numeric address; native address reason no supported primary address; HAZARD GAS accepted                                                                    |
| Ambiguous address: `respond to 93 Doubling Road; respond to 30 Edgewood Drive`      | ambiguous    | unresolved    | contains_ambiguity    | Both address alternatives and mentions, no primary address                                                                                                     |
| Ambiguous type: `respond gas leak; respond wires down`                              | unsupported  | ambiguous     | contains_ambiguity    | HAZARD GAS and WIRES accepted separately; no resolved type                                                                                                     |
| Level only: `working fire`                                                          | no evidence  | unresolved    | neither_resolved      | WORKING FIRE level evidence accepted independently; no type selected                                                                                           |
| Qualifier only: `smoke activation`                                                  | no evidence  | unresolved    | neither_resolved      | Qualifier retained with no_unambiguous_same_clause_alarm; no invented alarm type                                                                               |
| Independent candidates: `93 Doubling Road; 30 Edgewood Drive; gas leak; wires down` | unsupported  | unresolved    | neither_resolved      | Two unresolved numeric candidates and two rejected call-type mentions; no pairing                                                                              |

For the both-resolved example, an abbreviated approved envelope is:

```text
Version: dispatch-interpretation-v1
ShadowOnly: true
Transcript: respond to 93 Doubling Road; respond gas leak
AddressState: resolved
CallTypeState: resolved
ShadowState: both_resolved
Address: full native addresspipeline.Result, including cue and address evidence
CallType: full native calltypepipeline.Result, including accepted gas-leak evidence
```

Separate-transmission example:

| Independent call | Transcript                    | AddressState | CallTypeState | ShadowState           |
| ---------------- | ----------------------------- | ------------ | ------------- | --------------------- |
| A                | `respond to 93 Doubling Road` | resolved     | unresolved    | address_only_resolved |
| B                | `respond gas leak`            | unsupported  | resolved      | type_only_resolved    |

Never create a third both-resolved result from A and B, even if adjacent in time.
Likewise, separate calls containing `93` and `Doubling Road` cannot form an address.

## Ordering, ownership, and safety boundaries

Preserve each native collection's deterministic order. Address candidates and
call-type candidates remain separate ordered lists. Native grouped alternatives
retain first-mention order and all original mentions. No merged global evidence
ordering is required. Fixed field placement is not candidate priority.

Constructed components are immutable and safe to share across concurrent calls.
A future wrapper should retain no mutable transcript state. Returned slices and
nested pointers belong to their result; mutations must not affect previous or
future calls or internal state. A shared returned result needs caller-managed
synchronization. Serialization ordering, if later added, must be specified
separately.

No part of this result creates an incident, assigns units, selects an alarm level,
modifies CAD state, establishes dispatch truth, or chooses a primary operational
action. Native primary-address syntax and accepted level evidence are retained
only as component output, not promoted to operational decisions.

Never infer missing information from units, addresses, call types, alarm levels,
highways, qualifiers, prior transmissions, or timing proximity. No database,
network, API, Whisper, frontend, unit/incident-state, or production integration
is proposed for the first offline composition step.

## Dave confirmation checklist before Step 5C2

Dave approved all ten decisions below. Approval authorizes the documented design;
it does not claim that Step 5C2 implementation or verification is complete.

- [x] **Approved:** The envelope names, version field, fixed ShadowOnly value,
      and retention of both complete native result types.
- [x] **Approved:** Exact native component states, the five overall summary states,
      and the full matrix, with no readiness, severity, or operational meaning.
- [x] **Approved:** both_resolved does not claim event identity or link an address
      to a type; partial resolution is a valid result.
- [x] **Approved:** Preserve native diagnostic asymmetry without inventing
      per-address rejection reasons or a shared diagnostic schema.
- [x] **Approved:** In-memory-only invalid-input handling retains native outcomes.
      JSON/wire encoding is deferred until byte preservation is defined.
- [x] **Approved temporarily:** Retain duplicate extraction and different context
      grammars without refactoring or normalizing away disagreements in Step 5C2.
      Duplicate extraction remains technical debt, not a permanently accepted design.
- [x] **Approved:** Deterministic address-then-type invocation, native evidence
      ordering, result ownership, and shared immutable concurrent use.
- [x] **Approved:** All proposed Step 5C2 synthetic verification cases, including
      all twelve state combinations, invalid input, offsets, mutation isolation,
      and concurrent calls. These tests are approved future work, not implemented here.
- [x] **Approved:** Strict one-transcript isolation, with no timing-based joining,
      inference, incident creation, unit assignment, alarm-level selection, or
      CAD authority.
- [x] **Approved:** Step 5C2 remains offline composition only, with no production wiring.

No checklist decisions remain pending approval. Documented limitations and
technical-debt findings remain in force. This update changes no component,
interpretation policy, example, or implementation.
