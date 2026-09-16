# Step 5D3: conservative unit-status vocabulary audit v1

**Documentation only: Dave has approved the conservative v1 contract below.**
Step 5D4 is authorized only after this Step 5D3 change is reviewed, committed,
pushed, and green in CI. This change adds no recognizer and does not begin Step 5D4.

At the original audit, the working tree was clean at **fbf16f1**. That request
named fb1f6f1; the actual
HEAD differs by transposed characters. CI green is supplied context, not a
verification of operational truth. Searches used `rg` across backend, database
migrations/tests, product and package documentation, and frontend source.
No live recordings, private transcripts, datasets, experiment outputs, live
database, or legacy project were consulted. Examples below are synthetic.
Existing repository values are historical audit evidence, not new v1 approval.

## Repository findings

| Source                                                                                                                                                                                                                  | Evidence and limitation                                                                                                                                                                  |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [Product requirements](product-requirements.md), FR-4/FR-5                                                                                                                                                              | Four recognition groups, display colors, EN_ROUTE/ON_SCENE normalization, proposed flows and duplicate suppression                                                                       |
| Product requirements, AT-01–08 and Open Questions                                                                                                                                                                       | Synthetic status examples; AVAILABLE versus QUARTERS, internal display aliases, ONAIR entry/exit, and white-indicator duration are recorded as open in that historical source            |
| [Migration 000001](../database/migrations/000001_radio_status_foundation.sql), unit_status_decisions                                                                                                                    | previous_status/proposed_status allow QUARTERS, ENROUTE, ONSCENE, ONAIR; nullable evidence fields; accepted/rejected decisions and shadow_mode constrained true                          |
| [SQL tests](../database/tests/000001_schema_test.sql)                                                                                                                                                                   | Test all four allowed values and reject AVAILABLE, EN_ROUTE, ON_SCENE; synthetic Engine 5 on scene decision. Seed/constraint tests do not prove recognition or operational state         |
| [Database README](../database/README.md), Schema                                                                                                                                                                        | Audit trail exists; transition engine remains future work. detected_unit is evidence text, not a unit foreign key; no current board-status table is described                            |
| [HTTP router](../backend/internal/httpapi/router.go), [health](../backend/internal/httpapi/health.go), [readiness](../backend/internal/httpapi/ready.go), [monitor snapshot](../backend/internal/operations/monitor.go) | GET health/ready/transcription operations only; status, worker_state, provider_state, observed_at and job counts are infrastructure fields, not apparatus status or unit-status requests |
| [Frontend component](../web/src/app/app.ts), [template](../web/src/app/app.html), [tests](../web/src/app/app.spec.ts)                                                                                                   | Scaffold UI, not an implemented apparatus board. Template colors/styles do not establish operational status semantics                                                                    |
| [Unit recognition README](../backend/internal/unitrecognition/README.md), [tests](../backend/internal/unitrecognition/recognize_test.go)                                                                                | Mention-only API; engine 2 on scene and engine 2 is training are explicitly rejected as unsupported_context                                                                              |
| [Evaluation vocabulary](../backend/internal/transcripteval/vocabulary-v1.txt), [tests](../backend/internal/transcripteval/eval_test.go), [documentation](offline-transcription-evaluation.md)                           | on scene, clear, 10-4, good copy, canceled, transported one, last unit on scene, disregard that last are evaluation terms, not an approved status mapping                                |
| [Prompt source](../backend/internal/transcriptexperiment/prompts/greenwich-v1.txt)                                                                                                                                      | Repeats evaluation terms; no provider output or private experiment result is used as evidence                                                                                            |
| [Unit vocabulary](unit-vocabulary-v1.md), [shadow contract](dispatch-interpretation-v1.md)                                                                                                                              | Unit identity alone establishes neither assignment nor status; no incident/CAD authority or cross-transmission inference                                                                 |

Raw/normalized transcript storage and recording timestamps are evidence metadata.
Transcription/processing statuses such as pending, processing, completed, failed,
and skipped are pipeline states. SQL RETURNING clauses and health “available”
wording are search false positives, not spoken status phrases.

## Approved canonical statuses

The complete canonical catalog is DISPATCHED, ENROUTE, ONSCENE, QUARTERS,
IN SERVICE, OUT OF SERVICE, ON AIR, TRAINING, and ENROUTE TO QUARTERS.
ON AIR and TRAINING are separate canonical statuses. ARRIVED, AVAILABLE, and
RESPONDING are not separate canonical statuses.

Returning toward quarters remains different from arrival at quarters. QUARTERS
and IN SERVICE remain separate concepts. The exact phrase available maps to
QUARTERS by explicit approval; this does not authorize other availability or
arrival inferences. CLEAR alone remains unresolved and has no v1 mapping.
There is no implicit mapping from these labels to existing SQL values. Approval
of OUT OF SERVICE covers offline evidence only; operational functionality remains
deferred.

## Approved exact phrase mappings and repository audit

"Not found" means no operational phrase mapping found in the inspected repository;
it does not override Dave's explicit approval of a phrase below.

| Exact phrase          | Repository evidence                                                 | Approved canonical candidate |
| --------------------- | ------------------------------------------------------------------- | ---------------------------- |
| dispatched            | Product FR-6 prose about dispatched units, not a recognition phrase | DISPATCHED                   |
| responding            | FR-4 ENROUTE; AT-02                                                 | ENROUTE                      |
| en route              | No exact spoken mapping found; EN_ROUTE is a machine spelling       | ENROUTE                      |
| in route              | No exact spoken mapping found                                       | ENROUTE                      |
| enroute               | FR-4                                                                | ENROUTE                      |
| on the way            | No exact spoken mapping found                                       | ENROUTE                      |
| on scene              | FR-4, AT-01, SQL synthetic fixture, evaluation vocabulary           | ONSCENE                      |
| on location           | FR-4 ONSCENE                                                        | ONSCENE                      |
| arrived               | FR-4 and AT-02                                                      | ONSCENE                      |
| 10-23                 | FR-4                                                                | ONSCENE                      |
| returning             | No standalone mapping; SQL RETURNING is unrelated                   | ENROUTE TO QUARTERS          |
| returning to quarters | FR-4 and AT-03                                                      | ENROUTE TO QUARTERS          |
| RTQ                   | FR-4                                                                | ENROUTE TO QUARTERS          |
| back to quarters      | No exact mapping found; not back in service                         | QUARTERS                     |
| available             | FR-4 and historical Open Questions                                  | QUARTERS                     |
| in service            | Occurs inside back in service; no standalone mapping                | IN SERVICE                   |
| back in service       | FR-4, AT-02; historical QUARTERS / AVAILABLE grouping               | IN SERVICE                   |
| out of service        | Product operational functionality is paused; no spoken mapping      | OUT OF SERVICE               |
| on air                | FR-4 ONAIR                                                          | ON AIR                       |
| training              | FR-4 ONAIR / TRAINING; Step 5D2 rejects operational training text   | TRAINING                     |

These are all 20 approved phrases. Do not infer any unlisted synonym, tense,
abbreviation expansion, suffix, fuzzy match, phonetic correction, or code
expansion. Literal RTQ and 10-23 approval permits no generic expansion.

| Unmapped or excluded form                                          | Repository evidence                      | v1 disposition                                                       |
| ------------------------------------------------------------------ | ---------------------------------------- | -------------------------------------------------------------------- |
| CLEAR                                                              | FR-4; evaluation vocabulary              | Unresolved; no mapping                                               |
| EN_ROUTE; ON_SCENE                                                 | FR-4/AT-04, SQL rejection tests          | Excluded standalone status mappings                                  |
| ACTIVE                                                             | FR-4 display grouping and Open Questions | Excluded standalone status mapping                                   |
| 10-4; good copy                                                    | Evaluation vocabulary/prompt             | Excluded standalone status mappings; acknowledgements                |
| canceled; transported one; last unit on scene; disregard that last | Evaluation vocabulary/prompt             | Excluded standalone status mappings; context cannot establish status |

RESPONDING is not a separate canonical status: the approved spoken phrase
responding maps only to ENROUTE. Existing display/internal groupings do not add
aliases. Recognizing on scene within excluded contextual wording such as last
unit on scene does not make that wording an accepted positive association.

## Concept boundaries and transition audit

| Concept                       | Boundary                                                                     |
| ----------------------------- | ---------------------------------------------------------------------------- |
| Canonical interpreted status  | Proposed classification of supported text under the approved catalog         |
| Exact spoken phrase           | Individually approved surface form, not a canonical/display alias by default |
| Display label / display color | Presentation choices, not recognition or transition rules                    |
| Unit mention                  | Approved identity evidence only; cannot establish a status                   |
| Sender identity               | Not established by a mentioned unit; no RID inference                        |
| Assignment                    | Distinct from status and from a dispatcher command                           |
| Incident association          | Not implied by a unit/status pair                                            |
| Talkgroup/channel             | Source metadata only; no unit or status inferred                             |
| Timestamp                     | Evidence time, not priority or transition proof                              |
| Current persisted status      | Not known from one transcript or supplied by this proposed offline stage     |
| Proposed transition           | Requires separate approved state/history policy; not a recognized phrase     |

A status phrase alone cannot identify a unit. A unit mention alone cannot establish
a status. Future association requires supported unit and status evidence in the
same transcript under an approved narrow grammar. Multiple units may share a
status only if the same clause directly applies it to each. Never distribute a
status to every mentioned unit across unrelated clauses.

Do not associate with a unit from another transmission, RID, talkgroup, or expected
responding assignment. Do not infer from timing proximity or prior state.

Existing FR-5 assumptions are ENROUTE → ONSCENE → QUARTERS, direct ENROUTE →
QUARTERS, duplicate/no-op suppression, and shadow audit records. FR-6's
all-assigned-units-return auto-clear is incident logic. These are historical
requirements, **not approved v1 transitions**. Legacy ONAIR entry/exit, missed/out-of-order
messages, and automatic return completion remain deferred. The approved available
phrase and AVAILABLE display label do not reinstate those behaviors. No
transition engine or automatic precedence is designed here.

Never silently prefer first, last, most severe, newest timestamp, dispatcher over
unit, or unit over dispatcher. Conflicting supported statuses for one unit remain
ambiguous unless exact approved grammar distinguishes their roles. Incomplete
evidence remains unresolved.

## Approved speech roles and association

| Speech category     | Approved conservative distinction                                                      |
| ------------------- | -------------------------------------------------------------------------------------- |
| Dispatch statement  | May report an instruction or assignment; does not prove a completed status change      |
| Self-identification | A unit phrase does not prove the speaker's identity                                    |
| Acknowledgement     | An acknowledgement does not establish a status                                         |
| Command             | Requested action is not reported completion or verified state                          |
| Question            | Asking about status is not asserting it                                                |
| Negation            | Preserve rejected evidence; do not interpret the positive phrase inside it as asserted |
| Historical          | Past status is not current status                                                      |
| Hypothetical        | Conditional possibility is not actual status                                           |
| Quoted speech       | Reported words are not automatically the present speaker's assertion                   |

Three levels must remain distinct: **recognized/reported radio statement**,
**proposed shadow interpretation**, and **verified operational state**. Neither
of the first two establishes the third. Dispatcher, unit, timestamp, source, first/last position, and severity receive no
automatic authority or precedence. Negated, historical, hypothetical, and quoted
evidence cannot become an accepted positive association.

### Existing unit API incompatibility

Step 5D2 intentionally accepts mention-only lines. A line containing engine 2 on
scene retains a rejected unit mention with unsupported_context. Naively composing
a status matcher with accepted Step 5D2 results therefore cannot associate that
fixture. Do not trim/split away status text, promote rejected evidence to accepted,
or weaken the existing matcher silently.

Step 5D4 may perform its own bounded catalog-backed unit matching inside an
approved status-bearing clause for proposed association results. Step 5D2 remains
unchanged: do not trim or split its input, promote its rejected evidence, weaken
its grammar, or modify its API. This bounded duplication of unit matching is
recorded as technical debt; it is not permission to broaden either grammar.

### Approved association grammar

Association is same-transcript, same-clause only. Supported positive shapes are:

- unit status
- unit, status
- unit is status
- coordinated unit-list status
- coordinated unit-list are status

One immediately following status may apply to every unit in the same coordinated
list. One optional comma may separate a unit/list and status. The linker `is`
links one unit; `are` links a coordinated list. These shapes remain subject to the
speech-role rules. Never associate across clauses, sentences, lines, transmissions,
RIDs, talkgroups, timestamps, earlier results, or expected assignments.

## Approved Step 5D4 result contract

Return separate status-evidence results and proposed unit/status association
results. Retain:

- The unchanged input text, exact original phrase evidence, and canonical candidate.
- Original UTF-8 byte offsets [Start, End), separate unit and status evidence spans,
  and clause/cue evidence.
- Accepted, rejected, ambiguous, and unresolved dispositions with deterministic reasons.
- Repeated evidence in source order and deterministic ordering.
- Deep caller-owned results, immutable reusable catalog/configuration, mutation
  isolation, and safe concurrent use, including 1,000 concurrent synthetic calls
  under the race detector.

A result is resolved only when at least one association is accepted and no
supported conflict remains. Conflicting supported statuses for one unit remain
ambiguous without approved role separation. Otherwise association state is
unresolved. Standalone supported status evidence remains available even when no
unit association resolves. Multiple independent units are not inherently ambiguous.
Unknown unit, unknown status, unsupported context, and conflicting statuses must
not be silently collapsed into a guessed resolved association. No current
persisted state, timestamp precedence, or operational transition is generated.

## Approved narrow matching and collision safeguards

Each call takes exactly one unchanged valid UTF-8 transcript. Use deterministic
ASCII case-insensitive comparison, complete token boundaries, longest match only
at the same start, and original byte offsets. Preserve repeated evidence.
Horizontal space/tab may separate phrase words. CR/LF are association boundaries
and cannot occur inside a phrase. Period, semicolon, colon, exclamation mark,
question mark, CR, and LF terminate association scope. Question marks and
quotation marks retain their speech-role meaning and are not stripped into
assertions. Invalid UTF-8 and unsafe C0 controls other than tab, CR, and LF receive
deterministic invalid-input results. No cross-call or cross-transmission state
is allowed. The exact phrase catalog authorizes no fuzzy, phonetic, suffix,
abbreviation, or correction behavior.

| Collision/risk                         | Approved safeguard                                                                                                  |
| -------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| on scene versus scene                  | Bare scene must not inherit the complete phrase's meaning                                                           |
| in service versus out of service       | Preserve the entire approved phrase and negation; do not erase out                                                  |
| returning versus returning to quarters | Longest same-start phrase wins; destination is not arrival                                                          |
| clear                                  | Ordinary speech or instructions may contain clear; token boundaries must reject unclear/cleared as implicit aliases |
| available                              | Could describe resources, not a unit status                                                                         |
| on air                                 | Could describe communications activity rather than an approved status                                               |
| training                               | Activity/history/discussion is not automatically a status                                                           |
| arrived                                | Exact arrived phrase maps to ONSCENE; no separate ARRIVED status                                                    |
| back in service versus in service      | Nested phrase locations differ; do not silently double-assign or drop evidence                                      |
| RTQ, 10-23                             | Approved literal phrases only; no generic abbreviation/code expansion                                               |
| punctuation/questions                  | Stripping a question mark must not turn a question into an assertion                                                |
| repeated versus conflicting statements | Preserve all mentions; no first/last/frequency preference                                                           |

## Approved display labels and colors

| Canonical status    | Display label   | Color    |
| ------------------- | --------------- | -------- |
| DISPATCHED          | DISPATCHED      | Yellow   |
| ENROUTE             | ENROUTE         | Yellow   |
| ONSCENE             | ONSCENE         | Red      |
| QUARTERS            | AVAILABLE       | Green    |
| IN SERVICE          | IN SERVICE      | Green    |
| OUT OF SERVICE      | OUT OF SERVICE  | Deferred |
| ON AIR              | ON AIR          | Blue     |
| TRAINING            | TRAINING        | Blue     |
| ENROUTE TO QUARTERS | ENR TO QUARTERS | White    |

Historical FR-4 groupings include active / ONSCENE red, ENROUTE / RESPONDING
yellow, AVAILABLE / QUARTERS green, and ONAIR / TRAINING blue. The pending incident
area is yellow; historical direct ENROUTE → QUARTERS wording did not establish a
separate return-travel status. The explicit approvals above now govern this
contract without changing those source documents or the SQL enum.

Colors are presentation only. No automatic color transitions, timers,
return-to-green behavior, automatic status transitions, duplicate operational
actions, incident clearing, or precedence are approved. OUT OF SERVICE color is
explicitly deferred.

Preserve the approved board boundary that volunteers are not permanently listed.
This audit implements no visibility, persistence, color, or board behavior.

## Synthetic examples under the approved contract

These are invented fixtures only, not private recordings or verified reports.
Expected associations below are proposed shadow interpretations, not operational state.

| Case                      | Synthetic text                                               | Expected boundary                                                                               |
| ------------------------- | ------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| One unit/status           | engine 2 on scene                                            | Retain E2 and ONSCENE evidence separately; accept proposed association, not verified state      |
| Shared status             | engine 2 and engine 5 on scene                               | Accept proposed ONSCENE associations for both under the same-clause list grammar                |
| Separate statuses         | engine 2 on scene; engine 5 responding                       | Preserve separate unit/status scopes; no cross-clause distribution                              |
| Status only               | on scene                                                     | No unit inferred                                                                                |
| Unit only                 | engine 2                                                     | No status inferred                                                                              |
| Conflict                  | engine 2 responding; engine 2 on scene                       | Ambiguous for E2 without approved role separation; no last-wins transition                      |
| Repetition                | engine 2 on scene; engine 2 on scene                         | Retain both mentions; no repeated operational action                                            |
| Negation                  | engine 2 not on scene                                        | Reject positive association with a deterministic reason                                         |
| Quotation                 | "engine 2 on scene"                                          | Quoted evidence is not automatically current asserted status                                    |
| History                   | yesterday engine 2 on scene                                  | Not current state                                                                               |
| Hypothesis                | if engine 2 is available                                     | Not asserted availability                                                                       |
| Command versus report     | engine 2 return to quarters / engine 2 returning to quarters | Instruction and report differ; command wording is a negative fixture, not a new approved phrase |
| Question versus statement | engine 2 on scene? / engine 2 on scene                       | Question must not become assertion after normalization                                          |
| Separate calls            | A: engine 2 / B: on scene                                    | Never combine                                                                                   |
| Offset preservation       | engine 2 on scene                                            | Unit evidence engine 2 [0,8); status evidence on scene [9,17), into the unchanged string        |

The positive association fixtures intentionally expose the Step 5D2 incompatibility.
They are not claims that the existing unit matcher accepts them. Future tests must
also check multibyte prefixes, every approved phrase, token collisions, independent
units, invalid text, repeatability, result mutation isolation, and concurrent calls.
Do not invent unit/status mappings or use live data to broaden this contract.

An additional byte-offset fixture uses `é; engine 2 on scene`: UTF-8 unit evidence
`engine 2` is [4,12), and status evidence `on scene` is [13,21). These offsets
index the unchanged bytes, not Unicode character positions.

## Completed approval checklist and Step 5D4 gate

- [x] Approve all nine canonical statuses; ON AIR and TRAINING are separate.
- [x] Exclude separate ARRIVED, AVAILABLE, and RESPONDING statuses; retain their
      approved exact phrase mappings and the AVAILABLE display label.
- [x] Approve all 20 exact phrases; keep CLEAR unresolved with no v1 mapping and
      explicitly exclude every standalone mapping listed above.
- [x] Keep returning toward quarters distinct from arrival at quarters, and
      QUARTERS distinct from IN SERVICE.
- [x] Approve speech-role rules and separation of recognized evidence, proposed
      shadow interpretation, and verified operational state.
- [x] Approve same-transcript, same-clause association shapes for single units and
      coordinated lists; prohibit associations across all listed boundaries.
- [x] Approve bounded catalog-backed matching in Step 5D4; preserve Step 5D2
      unchanged and record bounded duplication as technical debt.
- [x] Approve separate evidence and association results, spans, cues, dispositions,
      deterministic reasons, resolution/conflict rules, ordering, ownership,
      immutable reuse, mutation isolation, and concurrent safety.
- [x] Approve exact normalization, punctuation, token and numeric boundaries,
      same-start longest matching, original UTF-8 offsets, repeated evidence,
      invalid-input handling, and no cross-call state.
- [x] Approve every display label/color above; explicitly defer OUT OF SERVICE
      color and exclude automatic colors, timers, return-to-green, transitions,
      duplicate operational actions, incident clearing, and precedence. Preserve
      non-permanent volunteer display.
- [x] Approve comprehensive synthetic tests covering all nine statuses and all
      20 phrases; CLEAR and every explicit exclusion; speech roles; every approved
      association shape; single/multiple units; clause/line/transmission isolation;
      collisions and longest-match behavior; conflicts, ambiguity, incomplete and
      unresolved evidence; punctuation and numeric boundaries; UTF-8 byte offsets;
      invalid input and unsafe controls; repetition and deterministic ordering;
      result mutation isolation; concurrent calls including 1,000 synthetic calls
      under the race detector; and separate-transmission isolation.
- [x] Record Dave's explicit authorization of Step 5D4 offline unit-status evidence
      and proposed unit/status association only after this Step 5D3 documentation
      change is reviewed, committed, pushed, and green in CI.

The checked authorization records an approved condition, not completion of the
review, commit, push, or CI gate. This documentation-only change does not begin
Step 5D4 and does not commit or push.

Step 5D4 does not authorize database, API, Whisper, frontend, persisted unit state,
incident state, CAD actions, RID interpretation, channel interpretation, automatic
transitions, or production integration. Legacy transitions and operational
out-of-service functionality remain deferred and are not reinstated.
