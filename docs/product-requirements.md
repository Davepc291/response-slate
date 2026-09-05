# Greenwich Fire Responder V3 — Product Requirements

This document records confirmed product behavior and requirements for future
implementation. It does not imply that the features below are implemented.
Unresolved policy and implementation details are listed under Open Questions.

## Purpose and scope

Greenwich Fire Responder V3 is an unofficial Greenwich Fire monitoring and
responder-information application. It is not affiliated with or endorsed by the
Greenwich Fire Department and must not present itself as an official CAD system.

The application processes SDRTrunk radio recordings into incidents and unit-status
updates. V3 must initially operate in shadow/read-only mode, with no authority over
the existing CAD. It records proposed changes without publishing them to the
production CAD.

Police monitoring is excluded because police traffic is encrypted. Apparatus
out-of-service functionality is paused and excluded from the first V3 milestones.
PWA/mobile installation is a later milestone.

## Target architecture

SDRTrunk recording → metadata extraction → FFmpeg normalization → Whisper
transcription → Go classification/state engine → PostgreSQL → API/WebSocket →
Angular desktop and phone interface.

Each processing stage must preserve a link to the source recording and metadata
so classifications and proposed state changes can be traced through the pipeline.
API and WebSocket outputs in the initial mode represent shadow information only;
they must not become production CAD updates.

## Functional requirements

### FR-1: Recording ingestion and talkgroup roles

Extract recording metadata, including the talkgroup ID (TGID) and radio ID (RID)
when present. Normalize audio using FFmpeg and transcribe it using Whisper before
classification by the Go engine.

| TGID | Channel | Role | May create incidents |
| --- | --- | --- | --- |
| 57201 | CH1A | Primary dispatch | Yes |
| 57202 | CH2B | Status/operations | No |
| 57203 | CH3B | Status/operations | No |
| 57204 | CH4C | Status/operations | No |

Only TGID 57201 may create incidents, even when another talkgroup contains
dispatch-like language or a complete address.

### FR-2: Primary board units and stations

Display the primary board units in this exact order:
**DC, E2, E3, E4, E5, SQ1, SQ8, T1**.

| Unit | Station |
| --- | --- |
| DC | HQ |
| E2 | STN2 |
| E3 | STN3 |
| E4 | STN4 |
| E5 | STN5 |
| SQ1 | HQ |
| SQ8 | STN8 |
| T1 | HQ |

SQ8 is not a volunteer unit. Trusted units outside this primary board are listed
below; their presence in the RID mapping does not expand the primary board order.

### FR-3: Trusted radio identification

Use the following trusted RID mapping:

| RID | Unit |
| --- | --- |
| 578054 | SQ8 |
| 578055 | T1 |
| 578056 | E2 |
| 578057 | E12 |
| 578059 | SQ1 |
| 578061 | E5 |
| 578064 | E62 |
| 578065 | E71 |
| 578066 | E11 |
| 578068 | TANKER2 |
| 578073 | E4 |
| 578074 | E3 |
| 578105 | CAR5 |
| 578000 | CAR1 |
| 578053 | CAR4 |

Do not map RID **577811**. Unknown or unmapped RIDs must be logged and must not
change trusted units, including when their transcripts mention a trusted unit.

### FR-4: Status recognition and presentation

| Status or transition | Display color |
| --- | --- |
| AVAILABLE / QUARTERS | Green |
| ENROUTE / RESPONDING | Yellow |
| ACTIVE / ONSCENE | Red |
| Direct ENROUTE → QUARTERS transition | White |
| ONAIR / TRAINING | Blue |

Recognize these phrases:

| Classification | Phrases |
| --- | --- |
| ENROUTE | responding; enroute |
| ONSCENE | on scene; arrived; on location; 10-23 |
| QUARTERS / AVAILABLE | clear; available; back in service; returning to quarters; RTQ |
| ONAIR | on air; training |

Normalize `EN_ROUTE` to `ENROUTE` and `ON_SCENE` to `ONSCENE` before state
evaluation. The white transition is required; its duration and subsequent
presentation are unresolved and must not be silently assumed.

### FR-5: Unit state engine and shadow behavior

- Support the normal flow ENROUTE → ONSCENE → QUARTERS.
- Allow ENROUTE to transition directly to QUARTERS.
- Duplicate transmissions must not repeatedly change the same state.
- Unknown RIDs must not change trusted units.
- Every accepted or rejected classification must retain an audit record.
- Shadow mode must record proposed changes without publishing them to the
  production CAD or taking authority over it.

Keep proposed shadow state distinguishable from production CAD state. Other
transition rules, including ONAIR entry and exit, remain open.

### FR-6: Incident detection and lifecycle

Detect dispatch tones, address, cross streets, call type, and dispatched units.
Only TGID 57201 may create an incident. Do not create an incident whose address
is `PENDING`; incomplete candidates must remain separate from created incidents.

Support I-95 northbound and southbound, and Merritt Parkway northbound and
southbound, with exits represented in the incident location.

Use single-incident locking and duplicate suppression. Auto-clear an incident
when all assigned units return. In shadow mode these lifecycle actions are
proposals only, with no production CAD publication. Lock scope, duplicate windows,
and the precise return condition require decisions listed below.

### FR-7: Desktop and phone interface

- Use a responsive Angular interface for desktop and phone.
- Include no map.
- The main wallboard must not require page scrolling.
- Place a yellow pending area at the top, show active incidents in red, and
  include unit rows and a clock.
- Preserve the primary board unit order and station assignments above.
- Clearly identify shadow information and the application's unofficial status.
- Defer PWA/mobile installation to a later milestone.
- Exclude apparatus out-of-service functionality from the first V3 milestones.

## Nonfunctional requirements

These are implementation requirements. Numerical targets and operational policies
that have not been agreed are called out under Open Questions.

### Reliability

- Persist processing outcomes and proposed state changes so restarts do not lose
  accepted work or apply the same transmission repeatedly.
- Make retries and replay idempotent, preserving an audit trail for duplicates.
- Handle malformed metadata, unreadable audio, and transcription failures without
  corrupting trusted unit state or silently creating incidents.
- Keep incident locking and state persistence consistent under concurrent work.
- Make processing failures and stale data visible to operators.

### Security

- Secrets and recordings must never be committed to Git. Use sanitized or
  synthetic text fixtures for committed tests; keep audio fixtures outside Git.
- Keep credentials outside source code and use least-privilege service access.
- Enforce the shadow boundary so processing, retries, replay, and interface
  actions cannot publish changes to the production CAD.
- Restrict access to recordings, transcripts, audit data, and database services;
  define authentication and authorization before shared deployment.
- Require protected transport for network deployments and avoid exposing secrets
  through logs, API responses, or diagnostic output.

### Observability and audit

- Retain an audit record for every accepted and rejected classification, including
  duplicates and unknown-RID rejections.
- Record source reference, available TGID/RID metadata, relevant transcript,
  classification, decision and reason, prior/proposed state, timestamp, shadow
  mode, and processing/model version where applicable.
- Correlate records across ingestion, normalization, transcription, classification,
  and persistence. Represent unavailable metadata explicitly.
- Provide structured logs and metrics for processing outcomes, failures, retries,
  duplicate suppression, unknown RIDs, processing delay, and pending work.
- Expose service health and make failures diagnosable without disclosing secrets.

### Backups and recovery

- Back up PostgreSQL state and audit records on an agreed schedule, protect backup
  access, and keep backups outside Git.
- Test restoration into an isolated environment before relying on backups.
- Document recovery procedures and verify that restored processing remains in
  shadow mode and does not duplicate state changes.
- Decide recording retention and backup scope separately from database backups.

### Tests

- Automate classification, RID mapping, state transitions, incident eligibility,
  duplicate handling, and audit assertions.
- Add integration tests for persistence, concurrency, retry, and restart behavior.
- Test API/WebSocket delivery and responsive interface behavior when implemented.
- Verify the absence of production CAD writes throughout end-to-end shadow tests.
- Use deterministic sanitized or synthetic fixtures and keep recordings and
  credentials out of the repository.

## Acceptance tests

Unless stated otherwise, run these against isolated shadow state with audit
collection enabled. Every classification decision must have an audit record.

| ID | Input or setup | Expected result |
| --- | --- | --- |
| AT-01 | E5 is ENROUTE; TGID 57202 + RID 578061 + “Engine 5 on scene” | Proposed E5 ONSCENE in shadow mode; accepted audit record; no production CAD update and no new incident. |
| AT-02 | Known RID 578061 sends “responding”, then “arrived”, then “back in service” through an allowed starting state | Proposed E5 flow ENROUTE → ONSCENE → QUARTERS; yellow, red, then green presentation; each decision audited. |
| AT-03 | E5 is ENROUTE; RID 578061 sends “returning to quarters” | Direct proposed transition to QUARTERS accepted; transition displayed white; no production CAD update. |
| AT-04 | Classification inputs contain EN_ROUTE and ON_SCENE | State evaluation receives ENROUTE and ONSCENE respectively. |
| AT-05 | Replay the same accepted “Engine 5 on scene” transmission twice | At most one state change; repeated classification attempts audited as duplicates/no-ops. |
| AT-06 | Unknown RID, including 577811, transmits “Engine 5 on scene” | Unknown/unmapped RID logged; rejection audited; trusted E5 state unchanged. |
| AT-07 | Parameterize every RID/unit pair in FR-3 | Each RID resolves to exactly its listed unit; 577811 remains unmapped. |
| AT-08 | Parameterize all phrases in FR-4 | Each phrase produces its listed classification; transition authorization is evaluated separately. |
| AT-09 | TGID 57201 dispatch fixture contains tones, a complete street address, cross streets, call type, and assigned units | Detect and retain these fields; create one proposed shadow incident with an audit trail. |
| AT-10 | Same otherwise valid dispatch fixture on TGIDs 57202, 57203, and 57204 | No incident created; rejection reason identifies talkgroup ineligibility. |
| AT-11 | TGID 57201 dispatch has address PENDING | No incident created; incomplete candidate remains distinct from an incident; decision audited. |
| AT-12 | Four complete dispatch fixtures: I-95 northbound, I-95 southbound, Merritt Parkway northbound, Merritt Parkway southbound, each with an exit | Proposed incident location preserves the correct highway, direction, and exit. |
| AT-13 | Deliver the same eligible dispatch concurrently to two workers | Locking and duplicate suppression produce one proposed incident, with auditable handling of both attempts. |
| AT-14 | Incident has E2 and E5 assigned; E2 returns while E5 remains ONSCENE, then E5 returns | No clear after only E2 returns; proposed auto-clear when both satisfy the agreed return condition; no production CAD write. |
| AT-15 | Render the board at each agreed desktop and phone viewport | No map; main wallboard requires no page scrolling; yellow pending area at top, red active incidents, clock, and ordered primary unit rows are visible. |
| AT-16 | Inspect primary unit rows and station labels | Order is DC, E2, E3, E4, E5, SQ1, SQ8, T1; stations match FR-2; SQ8 is not labeled volunteer. |
| AT-17 | Process an accepted, rejected, duplicate, and unknown-RID classification | Each retains a correlated audit record with its decision and reason. |
| AT-18 | Replay processing after a worker restart against persisted shadow data | No duplicate state changes or incidents; audit history remains available; no production CAD writes. |
| AT-19 | Inspect initial milestone interface and capabilities | Unofficial/shadow status is clear; police monitoring, maps, apparatus out-of-service controls, and PWA installation are absent. |
| AT-20 | Restore a database backup into an isolated test environment | Proposed incident/unit state and audit records are recoverable; replay remains idempotent and shadow-only. |

AT-02 starting-state policy, AT-14 return semantics, and AT-15 viewport/overflow
criteria must be finalized through the open questions before their full sign-off.
Tests must not substitute guessed policies for those decisions.

## Phased implementation roadmap

1. **Requirements and development foundation.** Establish the documented scope,
   Go HTTP foundation, isolated PostgreSQL development environment, and test
   conventions. Resolve the state, locking, and retention questions needed for
   subsequent work. No production CAD writes.
2. **Ingestion and transcription.** Add metadata extraction, FFmpeg normalization,
   Whisper transcription, source correlation, failure handling, and replay using
   externally stored test recordings. Verify extraction and observability.
3. **Shadow unit-state engine.** Implement trusted RID mapping, phrase recognition,
   normalization, transition validation, duplicate handling, PostgreSQL persistence,
   and accepted/rejected audit records. Pass unit-state acceptance tests.
4. **Shadow incident engine.** Implement dispatch-tone and field extraction,
   CH1A-only creation, address gating, highway/exit support, locking, suppression,
   and proposed auto-clear. Verify concurrent and replay behavior.
5. **Read-only live interface.** Deliver shadow data through the API/WebSocket to
   the Angular desktop and phone interface with the specified board layout,
   colors, station labels, and no map. Validate responsive and stale-data behavior.
6. **Operational readiness.** Exercise extended shadow operation, access controls,
   monitoring, failure recovery, backup restoration, and end-to-end tests against
   agreed targets before broader deployment.
7. **Later mobile milestone.** Evaluate PWA/mobile installation separately after
   the responsive interface is stable.

This roadmap does not authorize production CAD integration or apparatus
out-of-service functionality. Any change to that scope requires a separate
requirements decision.

## Open Questions

- What SDRTrunk file naming, metadata format, recording completion signal, and
  ingestion mechanism will be used? How are missing TGIDs, RIDs, and timestamps
  handled, and which timestamp determines event order?
- What audio normalization settings, Whisper model, language settings, hardware,
  and transcription confidence thresholds are required?
- What identifies dispatch tones, and are tones required for incident creation?
  How are fields combined across multiple recordings?
- How should ambiguous, negated, or conflicting phrases be handled? When the
  spoken unit differs from the trusted RID mapping, what decision is permitted?
- What are the initial unit states and the complete allowed transition matrix,
  including out-of-order messages, ONAIR entry/exit, and missed ENROUTE messages?
- Are AVAILABLE and QUARTERS stored as one state or distinct states? How are
  ACTIVE, RESPONDING, and TRAINING represented internally?
- How long does a white ENROUTE → QUARTERS indication last, and when does it
  become green? Does “returning to quarters” immediately satisfy incident return
  criteria, or is another confirmation needed?
- DC has no confirmed trusted RID mapping. How will DC receive trusted updates?
  How are mapped units outside the primary board displayed and assigned stations?
- What is the scope and lifetime of single-incident locking? Are multiple active
  incidents allowed, and how are overlapping dispatches or unit reassignments handled?
- What defines a duplicate transmission or incident, and what suppression windows
  apply? How are legitimate repeated calls at the same address distinguished?
- Which fields beyond a non-PENDING address are mandatory for creation? How are
  blank, uncertain, or partial addresses validated? Which highway exit conventions
  and call-type vocabulary should be used?
- Precisely what does “all assigned units return” mean, including AVAILABLE versus
  QUARTERS, reassigned units, and incidents with no recognized assigned units?
- What enters and leaves the yellow pending area, and what happens to unresolved
  candidates? What are the supported viewport sizes and overflow behavior when
  incident volume exceeds a wallboard that cannot scroll?
- What clock timezone and format, accessibility criteria, stale-data indicators,
  and phone layout are required?
- Who may access the application, recordings, transcripts, and audit records?
  What authentication, authorization, network exposure, and transport policies apply?
- What are the reliability, processing latency, throughput, and availability
  targets? What alerts, recipients, and escalation thresholds are required?
- What retention periods, backup frequency, storage location, encryption policy,
  recovery point objective, and recovery time objective apply? Are recordings
  backed up, retained temporarily, or managed by a separate system?
- How long must shadow validation run, who signs off acceptance, and what evidence
  is required? Any future production CAD interaction remains separately scoped.
