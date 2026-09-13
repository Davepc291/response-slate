# Response Slate call-type vocabulary audit v1 — Step 5B1

**Status: Dave-approved conservative v1 model, exact phrases, and recognition rules.**
This update records Dave’s approved decisions only. No recognition code is implemented;
Step 5B2 remains a separate offline-only milestone.

## Scope and evidence standard

Audited repository commit: `8b73396411b9a26a44762b10fb9f4f40f6d2ac2a`.
HEAD and the local `origin/main` reference matched, and the working tree was clean
before this document was created. GitHub CI passing is supplied starting context,
not evidence that call-type recognition exists or is operationally correct.

The audit used `git ls-files` to identify 165 tracked files and `rg -n -i` to
search code, tests, migrations, frontend files, dictionaries, documentation,
scripts, workflow, and configuration. Searches covered the requested labels,
spacing/hyphen/underscore variants, call/incident type names, alarm levels, and
broader alarm, fire, activation, symptom, medical, gas, wire, water, structural,
rescue, accident, investigation, and highway terms. Matches were inspected in
context; file-name/import and dependency-checksum matches are not vocabulary.

No private transcripts, exported datasets, recordings, experiment outputs,
external terminology sources, or legacy projects were used. The historical audit quotes only short phrase
fragments from committed source or synthetic tests. The approved catalog below
also records Dave's explicitly supplied phrases;
no actual incident example is reproduced. ThinLine Radio terminology was not
imported. Response Slate is independent.

**Confirmed** below means an exact occurrence or behavior is confirmed in source,
not that a radio phrase or operational call type has Dave's approval. **Planned**
means a requirement exists but recognition is not implemented. **Uncertain**
means a detail lacked approval at audit time. The approved model below supersedes
those earlier model and phrase questions. **Conflicting**
is reserved for incompatible definitions; absence of evidence is not a conflict.

## Existing implementation and source inventory

| Source                                                                                                                                                                  | Finding                                                                                                                                                                                                |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| [Product requirements](product-requirements.md), FR-6 and AT-09                                                                                                         | Detection of `call type` is planned. No enumerated call-type vocabulary or phrase-to-type mapping is supplied. Open Questions explicitly asks which call-type vocabulary to use.                       |
| [Address-role interpreter](../backend/internal/addressrole/interpret.go), `dispatchCue`                                                                                 | The exact bridges `to a residential alarm at` and `to residential alarm at` support a primary **address** role. The result has no call-type field.                                                     |
| [Address-role tests](../backend/internal/addressrole/interpret_test.go), `TestDispatchAndCrossStreets` and `TestUnsupportedAndNoEvidence`                               | A synthetic positive address fixture uses `Respond to a residential alarm at`; a negative clause-boundary fixture uses `Respond to an alarm`. These test address roles, not call classification.       |
| [Address-role documentation](../backend/internal/addressrole/README.md), Primary rules                                                                                  | Repeats the residential-alarm bridges and explicitly describes a narrow address grammar. This is duplicate documentation of the same evidence, not another approved synonym.                           |
| [Address pipeline](../backend/internal/addresspipeline/README.md)                                                                                                       | Composes address extraction and role interpretation only; no call-type or unit-status parser.                                                                                                          |
| [Migrations](../database/README.md), 000001–000006                                                                                                                      | No incident table, call-type table, call-type/incident-type column, or call-type enum was found. See the distinctions below for actual fields.                                                         |
| [Angular component](../web/src/app/app.ts), [template](../web/src/app/app.html), [routes](../web/src/app/app.routes.ts), [tests](../web/src/app/app.spec.ts)            | Scaffold UI and tests; no implemented incident/call-type labels or selector. The component title is `web` and routes are empty.                                                                        |
| [Evaluation vocabulary](../backend/internal/transcripteval/vocabulary-v1.txt) and [candidate prompt](../backend/internal/transcriptexperiment/prompts/greenwich-v1.txt) | Unit names, acknowledgments, status-like phrases, and dispatch context; no call-type mapping. These are source-controlled vocabularies, not recognition implementations or private experiment results. |
| [Project overview](../README.md), Current features and roadmap                                                                                                          | Incident and unit-status engines remain planned. No additional call-type catalog is defined.                                                                                                           |

There are **zero currently implemented call-type classifications**. The only
specific call-type-like expression confirmed in executable matching code is
`residential alarm`, within the address grammar. Generic `alarm` also occurs in
a negative synthetic test. No incident-type constants were found elsewhere.

## Dave-approved conservative v1 phrase catalog

These approvals come from Dave, not inferred repository behavior. This catalog
does not mean a recognizer has been implemented. Only the exact phrases below,
under the shared rules, are authorized; display labels are not implicit aliases.

### Alarm levels — separate from call types

| Approved exact phrase | Alarm level  |
| --------------------- | ------------ |
| "still alarm"         | STILL        |
| "minor alarm"         | MINOR        |
| "box alarm"           | BOX          |
| "working fire"        | WORKING FIRE |

Alarm levels never select a call type. No severity ordering, escalation behavior,
or unit assignment is authorized by this catalog.

### Alarm call types

**Explicit dispatch/response context is required for every call type in this
catalog.** For alarm calls, the approved phrases include that response wording.

| Approved exact phrase            | Call type  |
| -------------------------------- | ---------- |
| "respond residential alarm"      | ALARM RESD |
| "respond to residential alarm"   | ALARM RESD |
| "respond to a residential alarm" | ALARM RESD |
| "respond commercial alarm"       | ALARM COMM |
| "respond to commercial alarm"    | ALARM COMM |
| "respond to a commercial alarm"  | ALARM COMM |

Generic "alarm" alone remains unresolved. Bare residential/commercial wording
is not an additional approved phrase.

### Alarm qualifiers

- "general fire activation"
- "smoke activation"

These are qualifiers associated with an alarm call, not standalone call types.
A qualifier alone cannot establish a residential/commercial distinction.

### CO calls

Within explicit dispatch/response context:

| Approved exact phrase                    | Call type     |
| ---------------------------------------- | ------------- |
| "CO alarm with symptoms"                 | INVEST CO-YES |
| "carbon monoxide alarm with symptoms"    | INVEST CO-YES |
| "CO alarm without symptoms"              | INVEST CO-NO  |
| "CO alarm no symptoms"                   | INVEST CO-NO  |
| "carbon monoxide alarm without symptoms" | INVEST CO-NO  |
| "carbon monoxide alarm no symptoms"      | INVEST CO-NO  |

A CO alarm without an explicit symptom statement remains unresolved. Missing
symptom information does not mean "without symptoms."

### Gas calls

Within explicit dispatch/response context:

| Approved exact phrase | Call type  |
| --------------------- | ---------- |
| "gas leak"            | HAZARD GAS |
| "natural gas leak"    | HAZARD GAS |
| "gas odor"            | HAZARD GAS |
| "odor of gas"         | HAZARD GAS |

Operational gas conversation must not create a call type. Leak and odor map
to the same label only through these separately approved phrases.

### Wires

Within explicit dispatch/response context:

| Approved exact phrase | Call type |
| --------------------- | --------- |
| "wires down"          | WIRES     |
| "wire down"           | WIRES     |
| "wires arcing"        | WIRES     |
| "wires burning"       | WIRES     |

The bare word "wires" does not qualify.

### Water service

Within explicit dispatch/response context:

| Approved exact phrase | Call type     |
| --------------------- | ------------- |
| "water problem"       | SERVICE WATER |
| "water leak"          | SERVICE WATER |
| "broken water pipe"   | SERVICE WATER |

Hydrant and operational water-supply conversation must be excluded.

### Structural service

Within explicit dispatch/response context:

| Approved exact phrase | Call type          |
| --------------------- | ------------------ |
| "structural problem"  | SERVICE STRUCTURAL |
| "structural damage"   | SERVICE STRUCTURAL |

Building descriptions and ordinary fire-alarm information must not imply this type.

### Elevator rescue

Within explicit dispatch/response context:

| Approved exact phrase           | Call type       |
| ------------------------------- | --------------- |
| "elevator rescue"               | ELEVATOR RESCUE |
| "elevator entrapment"           | ELEVATOR RESCUE |
| "person trapped in an elevator" | ELEVATOR RESCUE |

### Motor-vehicle accident

Within explicit dispatch/response context:

| Approved exact phrase                  | Call type    |
| -------------------------------------- | ------------ |
| "motor vehicle accident with injuries" | MVA INJURIES |
| "motor vehicle accident, injuries"     | MVA INJURIES |
| "MVA with injuries"                    | MVA INJURIES |
| "car accident with injuries"           | MVA INJURIES |

MVA or accident wording without explicit injuries remains unresolved.

### Investigation calls

Within explicit dispatch/response context:

| Approved exact phrase   | Call type           |
| ----------------------- | ------------------- |
| "investigate inside"    | INVESTIGATE INSIDE  |
| "investigation inside"  | INVESTIGATE INSIDE  |
| "investigate outside"   | INVESTIGATE OUTSIDE |
| "investigation outside" | INVESTIGATE OUTSIDE |

Bare "investigate" or "investigation" remains unresolved. Operational conversation
such as "we're investigating" must be excluded.

### Location rules

I-95 and Merritt Parkway remain location concepts. They never select or imply a
call type. Direction and exit handling are separate from this vocabulary.

## Historical terminology reconciliation

All entries below now have approved roles and phrases where applicable. "Not
found" describes the original repository audit, not missing Dave approval.

| Investigated spelling                             | Audit evidence                                                                                                        | Approved disposition                                             |
| ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Still Alarm; Minor Alarm; Box Alarm; Working Fire | Not found as operational vocabulary                                                                                   | Separate STILL, MINOR, BOX, WORKING FIRE alarm levels            |
| residential alarm                                 | Address-role code, synthetic tests, and documentation linked above                                                    | ALARM RESD only through the approved response phrases            |
| Commercial Alarm                                  | Not found                                                                                                             | ALARM COMM                                                       |
| General Fire Activation; Smoke Activation         | Not found                                                                                                             | Alarm qualifiers only                                            |
| CO alarm with symptoms; CO alarm without symptoms | Not found                                                                                                             | INVEST CO-YES; INVEST CO-NO with explicit symptom wording        |
| Gas leak or gas odor                              | Neither found                                                                                                         | HAZARD GAS through the four approved phrases                     |
| Wires                                             | Not found as an operational term                                                                                      | WIRES through qualified phrases, never the bare word             |
| Water problem; Structural problem                 | Not found                                                                                                             | SERVICE WATER; SERVICE STRUCTURAL with conversational exclusions |
| Elevator rescue                                   | Not found                                                                                                             | ELEVATOR RESCUE                                                  |
| Motor-vehicle accident with injuries              | Not found                                                                                                             | MVA INJURIES with explicit injuries                              |
| Investigate inside; Investigate outside           | Not found                                                                                                             | INVESTIGATE INSIDE; INVESTIGATE OUTSIDE                          |
| I-95 and directional variants                     | [Product requirements](product-requirements.md), FR-6 and AT-12                                                       | Location only                                                    |
| Merritt Parkway and directional variants          | Product requirements; [dictionary](../backend/internal/addressdata/greenwich-streets-v1.txt) contains merritt parkway | Location only                                                    |
| alarm in "Respond to an alarm"                    | Negative address-role synthetic fixture linked above                                                                  | Generic alarm remains unresolved                                 |

Residential-alarm bridges are duplicated across address code, tests, and docs;
they were not separate call-type implementations. No incompatible implemented
call-type constants, duplicate enum values, or conflicting frontend labels were
found. Dave's approvals settle the earlier model and phrase uncertainty without
changing existing address behavior.

## Concepts that must remain separate

| Concept         | Boundary                                                                                                                                  |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| Call type       | The twelve approved labels above; no recognition implementation yet                                                                       |
| Alarm level     | STILL, MINOR, BOX, WORKING FIRE; never a call type or inferred assignment                                                                 |
| Alarm qualifier | General fire activation and smoke activation describe an alarm call                                                                       |
| Address         | [Address candidates](../backend/internal/addresscandidate/README.md) retain numeric address evidence; an address cannot imply type        |
| Cross street    | [Address roles](../backend/internal/addressrole/README.md) interpret street roles separately                                              |
| Unit assignment | Unit/RID mappings and planned dispatched-unit extraction do not imply type                                                                |
| Unit status     | [Migration 000001](../database/migrations/000001_radio_status_foundation.sql) QUARTERS/ENROUTE/ONSCENE/ONAIR decisions are not call types |
| Radio channel   | CH1A/CH2B/CH3B/CH4C and talkgroup permissions are metadata, not call types                                                                |
| Incident state  | Planned pending/active/auto-clear behavior is separate from recognition outcomes                                                          |

Transcript, matched-phrase, processing-status, transcription-status, attempt-outcome,
and review-verdict fields are evidence or pipeline state, not a call-type catalog.
Address resolution states are not incident state or severity. Evaluation/prompt
vocabulary and dictionary entries do not authorize additional call-type phrases.

## Approved shared recognition rules

1. Match case-insensitively. Normalize only ordinary punctuation and spacing.
   Require complete words and token boundaries.
2. Preserve the original transcript, evidence text, and original UTF-8 byte offsets.
3. Process exactly one transcript per call. Retain no information between calls
   or transmissions.
4. Require explicit dispatch/response context for call types. Exclude quoted,
   negated, and operational conversation. Explicit "no symptoms" and "without
   symptoms" in the approved CO phrases describe the symptom condition; they
   are not permission to match a negated call assertion.
5. Preserve every repeated evidence mention. Repeated evidence for one type may
   resolve to that same type.
6. Different supported types in one transcript remain **ambiguous**. Incomplete
   evidence remains **unresolved**.
7. The longest exact phrase wins **only for overlapping matches at the same
   location**. It cannot discard a distinct mention or resolve conflicting types.
8. Never silently prefer first, last, or most severe. Never infer a type from
   units, address, highway, alarm level, or previous transmissions.
9. Reject invalid UTF-8 and unsafe control characters.
10. No fuzzy matching, phonetic matching, suffix inference, or automatic correction.
11. Step 5B2 remains offline only: no database, API, Whisper, unit-state,
    incident-state, CAD, or production integration.

## Dave confirmation checklist

### Approved phrase groups and model decisions

- [x] All four alarm-level mappings: still alarm, minor alarm, box alarm, working fire.
- [x] All three ALARM RESD response phrases and all three ALARM COMM response phrases.
- [x] Both alarm qualifier phrases; neither is a standalone call type.
- [x] Both INVEST CO-YES phrases and all four INVEST CO-NO phrases; missing symptom
      statements remain unresolved.
- [x] All four HAZARD GAS phrases; operational gas conversation is excluded.
- [x] All four WIRES phrases; bare wires does not qualify.
- [x] All three SERVICE WATER phrases; hydrant and operational supply conversation
      is excluded.
- [x] Both SERVICE STRUCTURAL phrases; building descriptions and ordinary alarm
      information do not imply this type.
- [x] All three ELEVATOR RESCUE phrases.
- [x] All four MVA INJURIES phrases; absent explicit injuries remains unresolved.
- [x] Both INVESTIGATE INSIDE and both INVESTIGATE OUTSIDE phrases; bare and
      operational investigation wording does not qualify.
- [x] I-95 and Merritt Parkway are locations only.
- [x] Generic alarm remains unresolved; alarm levels, types, qualifiers, addresses,
      cross streets, assignments, statuses, channels, and incident state remain separate.

### Approved recognition policy

- [x] Case-insensitive matching; only ordinary punctuation and spacing normalization;
      complete words and token boundaries.
- [x] Preserve original transcript, evidence, and UTF-8 byte offsets.
- [x] Exactly one transcript per call; no retained information across transmissions.
- [x] Explicit dispatch/response context; exclude quoted, negated, operational conversation.
- [x] Preserve every repeated mention; repeated evidence of one type may resolve to it.
- [x] Different supported types are ambiguous; incomplete evidence is unresolved.
- [x] Longest exact phrase applies only to overlapping matches at the same location.
- [x] No first, last, or severity preference and no inference from units, address,
      highway, alarm level, or previous transmissions.
- [x] Reject invalid UTF-8 and unsafe controls.
- [x] No fuzzy/phonetic matching, suffix inference, or automatic correction.
- [x] Step 5B2 offline only; no database, API, Whisper, unit-state, incident-state,
      CAD, or production integration.
- [x] Step 5B1 remains documentation only; no recognition code is created.

### Remaining items

No phrase-level or recognition-policy approvals remain pending for this v1 catalog.
It contains 42 approved phrases: four alarm-level phrases, two qualifier phrases,
and 36 call-type phrases covering twelve labels.

Implementation, its result contract, and synthetic verification fixtures remain
future Step 5B2 work, not additional approved phrases or completed functionality.
They must implement these boundaries without expanding the vocabulary. Private
transcripts, datasets, recordings, and experiment results must not become public
documentation or test fixtures.
