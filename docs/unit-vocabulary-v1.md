# Step 5D1: Greenwich unit vocabulary and identity audit v1

**Dave approved the v1 unit model and authorized “Step 5D2 offline unit recognition only.”**
No required v1 unit or phrase approvals remain pending. This update is
documentation only; it creates no recognition code.

Audited clean HEAD: `c77e854`. GitHub CI green is supplied starting context, not
independent verification of apparatus identities or operational correctness.
Repository searches used `rg` across code, migrations, tests, frontend, docs,
and operational files for unit identifiers, apparatus terms, aliases, RID/TGID
metadata, and all requested volunteer candidates. This is a source audit, not a
live database or radio audit. No legacy project, private transcript, recording,
dataset, experiment result, or network service was consulted.

## Evidence standard and source inventory

Existing requirements can document earlier approved behavior without automatically
approving a new v1 identity catalog. “Repository-confirmed” here means present in
source, not field-verified, current, or newly approved. The catalog and policies below are Dave's explicit approvals, separate from
historical repository evidence. No other aliases or mappings are approved. Legacy behavior and remembered
mappings are audit evidence only; nothing is imported from the legacy project.

| Source                                                                                                                                                                                                              | Repository finding                                                                                                                                        | v1 disposition                                                              |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| [Product requirements](product-requirements.md), FR-2/FR-3, AT-01/06/07/16, Open Questions                                                                                                                          | Eight ordered board units and stations; SQ8 explicitly not volunteer; earlier trusted RID table; DC mapping and spoken/RID conflict questions remain open | Preserve historical evidence; approved text-only model below                |
| [Migration 000001](../database/migrations/000001_radio_status_foundation.sql)                                                                                                                                       | Eight primary units, eight other units, 15 trusted RID seeds, four talkgroups, append-only status decisions                                               | Existing schema/seeds only, no automatic v1 enrollment                      |
| [Schema tests](../database/tests/000001_schema_test.sql) and [database README](../database/README.md)                                                                                                               | Verify board order/stations, non-primary null stations, mapping counts and exclusions                                                                     | Tests verify seed consistency, not real-world identity                      |
| [Recording parser](../backend/internal/recordings/filename.go) and [tests](../backend/internal/recordings/filename_test.go)                                                                                         | Parse RID/TGID and distinct system/site, alias/channel, and canonical channel labels; explicitly no unit identification                                   | Metadata cannot establish apparatus identity                                |
| [Recording migration](../database/migrations/000002_recording_ingestion.sql) and [persistence](../backend/internal/database/recordings.go)                                                                          | Persist recording metadata including labels and raw numeric identifiers                                                                                   | No spoken alias registry                                                    |
| [Address-role code](../backend/internal/addressrole/interpret.go) and [tests](../backend/internal/addressrole/interpret_test.go)                                                                                    | Engine plus digits can be an address-response addressee; Engine 4 occurs in synthetic fixtures                                                            | Address grammar is not a unit resolver or approval of every engine number   |
| [Evaluation vocabulary](../backend/internal/transcripteval/vocabulary-v1.txt), [evaluation tests](../backend/internal/transcripteval/eval_test.go), [evaluation documentation](offline-transcription-evaluation.md) | Engine 2, Engine 5, Chief and other terms occur as evaluation vocabulary; Engine 20 is a boundary-negative example                                        | Vocabulary presence is not an approved unit alias                           |
| [Source-controlled candidate prompt](../backend/internal/transcriptexperiment/prompts/greenwich-v1.txt)                                                                                                             | Repeats Engine 2, Engine 5, Chief, GEMS, medic                                                                                                            | Prompt source only; no experiment outputs used; no identity mapping implied |
| [Angular component](../web/src/app/app.ts), [template](../web/src/app/app.html), [tests](../web/src/app/app.spec.ts)                                                                                                | Angular scaffold and Hello, web test; no operational unit board labels found                                                                              | No frontend roster implementation to adopt                                  |
| [Combined shadow contract](dispatch-interpretation-v1.md)                                                                                                                                                           | Explicitly forbids unit assignment and cross-transmission inference                                                                                       | Unit vocabulary work must not change that boundary                          |

The existing offline pipelines do not implement a Greenwich unit recognizer.
The database has status-decision storage, not a current board-state implementation.
“Alias” elsewhere often means duplicate audio metadata or an SDRTrunk channel
label; neither is a spoken unit alias.

## Approved v1 catalog

Exactly **26 included units and 26 exact spoken phrases**: eight career and
18 volunteer units, one phrase per unit. Display labels are canonical codes.
The only roster kinds are **career** and **volunteer**.

### Fixed career roster

The numbered order, stations, display labels, classes, and career classification
are approved. SQ8 is career/non-volunteer.

| Order | Canonical ID | Display label | Roster kind | Apparatus class | Station | Exact approved phrase |
| ----- | ------------ | ------------- | ----------- | --------------- | ------- | --------------------- |
| 1     | DC           | DC            | career      | command         | HQ      | car 4                 |
| 2     | E2           | E2            | career      | engine          | STN2    | engine 2              |
| 3     | E3           | E3            | career      | engine          | STN3    | engine 3              |
| 4     | E4           | E4            | career      | engine          | STN4    | engine 4              |
| 5     | E5           | E5            | career      | engine          | STN5    | engine 5              |
| 6     | SQ1          | SQ1           | career      | squad           | HQ      | squad 1               |
| 7     | SQ8          | SQ8           | career      | squad           | STN8    | squad 8               |
| 8     | T1           | T1            | career      | truck           | HQ      | truck 1               |

HQ contains DC, SQ1, T1; STN2 contains E2; STN3 contains E3; STN4 contains E4;
STN5 contains E5; STN8 contains SQ8. Station membership never resolves identity.

### Included volunteer units

**These 18 units are not the complete Greenwich volunteer roster.**
They do not join the fixed roster. Volunteer station associations remain
**unknown until separately verified**. Row order is catalog presentation, not
an approved fixed-board position or operational priority.

| Order | Canonical ID | Display label | Roster kind | Apparatus class | Station | Exact approved phrase |
| ----- | ------------ | ------------- | ----------- | --------------- | ------- | --------------------- |
| —     | E21V         | E21V          | volunteer   | engine          | unknown | engine 21             |
| —     | E31V         | E31V          | volunteer   | engine          | unknown | engine 31             |
| —     | E41V         | E41V          | volunteer   | engine          | unknown | engine 41             |
| —     | E51V         | E51V          | volunteer   | engine          | unknown | engine 51             |
| —     | E62V         | E62V          | volunteer   | engine          | unknown | engine 62             |
| —     | L4V          | L4V           | volunteer   | ladder          | unknown | ladder 4              |
| —     | L5V          | L5V           | volunteer   | ladder          | unknown | ladder 5              |
| —     | SQ2V         | SQ2V          | volunteer   | squad           | unknown | squad 2               |
| —     | SQ3V         | SQ3V          | volunteer   | squad           | unknown | squad 3               |
| —     | SQ4V         | SQ4V          | volunteer   | squad           | unknown | squad 4               |
| —     | SQ5V         | SQ5V          | volunteer   | squad           | unknown | squad 5               |
| —     | SQ6V         | SQ6V          | volunteer   | squad           | unknown | squad 6               |
| —     | TK6V         | TK6V          | volunteer   | tanker          | unknown | tanker 6              |
| —     | TK7V         | TK7V          | volunteer   | tanker          | unknown | tanker 7              |
| —     | TK17V        | TK17V         | volunteer   | tanker          | unknown | tanker 17             |
| —     | RESCUE5      | RESCUE5       | volunteer   | rescue          | unknown | rescue 5              |
| —     | RESCUE51V    | RESCUE51V     | volunteer   | rescue          | unknown | rescue 51             |
| —     | RESCUE7      | RESCUE7       | volunteer   | rescue          | unknown | rescue 7              |

The V suffix is canonical/display metadata and is not spoken. Canonical codes
such as E2, SQ1, T1, E21V, and L4V are display identifiers, not approved spoken
inputs. No additional alias is implied by a code, class, or station.

## Approved exclusions and identity collisions

- MINI11 is excluded from v1 and may be reviewed later.
- E12, E62, E71, E11, TANKER2, CAR5, CAR1, and CAR4 are excluded as independent
  v1 recognition identities. Their existing database records remain unchanged.
- CAR4 is not a separate v1 identity: **car 4 maps only to DC**.
- E62 and E62V remain separate identifiers. E62 is excluded;
  **engine 62 maps only to E62V**. This does not merge their database identities.
- Exclude chief, GEMS, medic, engine 20, and generic engine, truck, squad, tanker,
  ladder, and rescue. None establishes an approved canonical unit.
- No station-only, partial-word, or unsupported unit-number resolution.

## Historical repository findings retained

The original audit found no occurrences of any of the 19 requested volunteer/
apparatus candidates. Dave's later approval supplies 18 entries; MINI11 is
excluded. That approval does not turn historical absence into repository evidence
or establish a complete real-world roster.

Existing non-primary database IDs are the eight exclusions above. Their stations
and display orders are null; primary_board=false does not itself mean volunteer.
The schema has no career/volunteer classification.

Repository-attested spoken forms were Engine 2 (including engine 2 and ENGINE 2
test variants), Engine 4, and Engine 5. Chief, GEMS, and medic appeared in evaluation/
prompt vocabulary without an identity mapping; Engine 20 was a negative boundary
example. Those sources are linked in the inventory. The approved lowercase
phrases above now define v1; other phrases were supplied explicitly by Dave,
not inferred from legacy behavior. Address grammar accepting Engine plus digits
is still not a unit recognizer.

### RID handling: explicitly deferred

FR-3 and migration 000001 contain 15 earlier trusted RID-to-unit seeds, with SQL
tests checking their consistency. Numeric mappings are not copied here.
The schema records rid, unit_code, trusted, active, notes, and timestamps.
It does not establish whether a radio is a portable, vehicle mobile, console,
person-associated device, or something else. Physical identity remains unknown
from this audit; a seed does not prove permanent apparatus ownership or assignment
history. DC has no confirmed RID in FR-3. A RID in a recording fixture likewise
cannot create a mapping.

The repository prohibits mapping RID 577811 while allowing unknown RIDs in
raw transmission metadata. These facts remain historical evidence, not permission
to import any mapping into this catalog.

**Approved Step 5D2 scope is text-only, with no RID-to-unit mappings.**
Unknown RIDs never resolve units. Existing database mappings remain unchanged.
Spoken mention evidence and sender RID are separate and never silently override
each other; a mention does not identify the sender. RID interpretation requires
a separate future milestone and Dave approval of provenance, validity, entity
meaning, reassignment, and permitted use. Deployment-specific RID data should
be private/local rather than part of the public spoken catalog. Do not import
legacy mappings or potentially sensitive development notes.

### Channel handling: explicitly deferred

Earlier authoritative repository requirements, seeds, recording parser/tests,
and [evaluation input validation](../backend/internal/transcripteval/input.go)
use the existing labels below. Dave's review candidates conflict with them.

| TGID  | Existing repository label / purpose | Review candidate | Disposition                     |
| ----- | ----------------------------------- | ---------------- | ------------------------------- |
| 57201 | CH1A / primary dispatch             | Dispatch         | Deferred separate channel audit |
| 57202 | CH2B / status operations            | CH1A             | Deferred separate channel audit |
| 57203 | CH3B / status operations            | CH2B             | Deferred separate channel audit |
| 57204 | CH4C / status operations            | CH3C             | Deferred separate channel audit |

All talkgroup/channel metadata is excluded from v1 unit recognition.
Do not reconcile these conflicts, change seeds/parsing, or infer a unit from TGID
or channel. CH3B, CH4C, and CH3C are not silently equated. The existing restriction
that only TGID 57201 can create incidents, and acceptance of unit status on all
four seeded talkgroups, are unchanged metadata rules, not recognition authority.
Numeric TGID, stored channel label, raw alias_channel_label, and system/site
remain separate.

## Approved identity meaning and concept separation

An exact supported phrase establishes only that a unit was mentioned in the text.
It does not establish dispatched, assigned, responding, speaking, self-identifying,
or any operational status. Different supported units are independent valid
mentions, not a conflict. Repeated mentions remain separate.

Volunteer units become eligible for later temporary display only when explicitly
supported in the current transmission. Step 5D2 does not update the board or
define display persistence. Self-identification is unit evidence only until a
later role/status stage. No evidence carries into another transmission.

| Concept                   | Approved boundary                                                                       |
| ------------------------- | --------------------------------------------------------------------------------------- |
| Canonical unit identifier | Catalog identity, not proof of permanent physical vehicle identity                      |
| Display label             | Canonical code; not a spoken input                                                      |
| Spoken phrase             | Only the exact approved phrase associated with the unit                                 |
| Apparatus class           | Approved command/engine/squad/truck/ladder/tanker/rescue metadata, never identity alone |
| Station                   | Approved career relationship or unknown volunteer station; never a resolver             |
| Roster kind               | career or volunteer                                                                     |
| Radio ID                  | Deferred source identity, separate from spoken evidence                                 |
| Source talkgroup          | Deferred channel metadata, excluded from unit recognition                               |
| Operational status        | Not established or changed by a unit mention                                            |

Never infer a unit from station alone, address, call type, alarm level, apparatus
class alone, highway, another transmission, timing proximity, or an unknown RID.

## Approved matching and context policy

- Exactly one transcript per call; preserve it unchanged. Retain no state or
  evidence between calls/transmissions.
- Deterministic lowercase matching, complete token boundaries, and longest
  approved phrase at the same starting token.
- Preserve all repeated mentions in transcript order, exact original evidence,
  and zero-based UTF-8 byte offsets [Start, End). Slicing the original input
  must reproduce the evidence.
- Invalid UTF-8 or unsafe controls reject the transcript.
- No fuzzy matching, phonetic matching, abbreviation expansion, number-word
  conversion, or transcription correction.
- Reject quoted, negated, historical, hypothetical, and unsupported operational
  discussion with deterministic reasons. Quoted discussion is not made valid
  merely by punctuation normalization.

Approved narrow punctuation permits spaces and tabs; commas, periods, colons,
and semicolons; parentheses around a complete mention; and hyphens between
supported phrase tokens. Line breaks are boundaries. Symbols or words inserted
inside a phrase prevent matching. This does not authorize arbitrary punctuation
removal or parentheses inside an incomplete phrase.

Required token protections:

- engine 2 must not match engine 21.
- engine 5 must not match engine 51.
- tanker 7 must not match tanker 17.
- rescue 5 must not match rescue 51.

The longer complete approved phrase may independently identify its own unit;
these protections do not exclude engine 21, engine 51, tanker 17, or rescue 51.

## Approved Step 5D2 result and ownership

The result contains the original transcript and:

- resolved, ambiguous, or unresolved state;
- every supported mention in transcript order;
- canonical unit ID and display label;
- career/volunteer roster kind and apparatus class;
- station or unknown;
- exact approved phrase;
- original evidence text and zero-based UTF-8 byte offsets;
- deterministic ambiguity or rejection reasons.

Different supported identities are not ambiguity. A single evidence item mapping
to conflicting identities is ambiguous. No supported evidence is unresolved.
Supported independent mentions resolve without selecting a single winning unit.
The approved catalog currently assigns each phrase to only one identity; tests
must still cover ambiguity handling without adding production aliases.

The matcher must be deterministic, immutable, concurrency-safe, and isolated per
transcript. Returned collections must not mutate matcher state or future results.
No JSON, database schema, or operational result contract is introduced here.

## Approved synthetic verification examples

These are synthetic test ideas, not private transcripts, recordings, datasets,
experiment results, or verified dispatch events. All 26 phrases and metadata
rows must receive coverage, not only the examples below.

| Synthetic input                                           | Required behavior                                             |
| --------------------------------------------------------- | ------------------------------------------------------------- |
| car 4                                                     | DC mention, command class, HQ; not CAR4                       |
| engine 62                                                 | E62V volunteer mention, unknown station; not E62              |
| engine 2; engine 5                                        | Two independent mentions, resolved without a winner           |
| engine 2; engine 2                                        | Both evidence mentions retained                               |
| (squad 8)                                                 | SQ8 career mention; parentheses surround the complete mention |
| engine-21                                                 | E21V mention; not E2                                          |
| engine 51; tanker 17; rescue 51                           | E51V, TK17V, RESCUE51V; no shorter-number matches             |
| E2; SQ1; T1; E21V; L4V                                    | Display codes do not qualify as spoken inputs                 |
| chief; GEMS; medic; engine 20                             | Unsupported identities                                        |
| engine; truck; squad; tanker; ladder; rescue              | No canonical identity                                         |
| station 2                                                 | No unit inferred from station                                 |
| "engine 2"; not engine 2; yesterday engine 2; if engine 2 | Context rejection with deterministic reasons                  |
| engine + 2; engine unknown 2; engine 2x; xengine 2        | No inserted-symbol/word or partial-token matching             |
| engine followed by a line break then 2                    | Do not join across the boundary                               |
| Call A: engine; call B: 2                                 | Never combine transmissions                                   |
| Unknown/unmapped RID without supported text               | No unit resolution; no RID mapping introduced                 |

Synthetic tests are approved for all included units and exact phrases, metadata,
exclusions, boundary collisions, narrow punctuation, quoted/negated/historical/
hypothetical/operational contexts, repetition, independent units, ambiguity and
rejection reasons, original text/offsets, invalid input, determinism, mutation
isolation, concurrency, and cross-transmission isolation.

## Dave approval checklist for Step 5D2

- [x] Approved all eight career IDs, exact fixed order, display labels, stations,
      career classification, and SQ8 non-volunteer status.
- [x] Approved exactly 18 volunteer units for v1, not a complete Greenwich roster;
      canonical display labels and unknown stations are explicit.
- [x] Approved all 26 exact spoken phrases, V as non-spoken metadata, and exclusion
      of canonical codes as spoken inputs.
- [x] Approved both roster kinds and every apparatus class in the catalog.
- [x] Excluded MINI11 and all eight non-primary database IDs as independent v1
      identities, without changing their database records.
- [x] Approved car 4 only to DC and engine 62 only to E62V; no identifier merging.
- [x] Excluded chief, GEMS, medic, engine 20, and generic apparatus-class terms.
- [x] Approved text-only recognition; RID mappings/interpretation are deferred,
      existing mappings unchanged, future deployment data private/local.
- [x] Excluded all channel metadata; channel conflicts remain deferred without
      seed or parsing changes.
- [x] Approved mention-only meaning, independent units, repeated evidence,
      current-transmission volunteer eligibility, and no board/status updates.
- [x] Approved context rejection, lowercase/token/longest-match rules, narrow
      punctuation, all four numeric boundary protections, invalid-input rejection,
      and no fuzzy/phonetic/expansion/conversion/correction behavior.
- [x] Approved result states, metadata, evidence/offsets, ambiguity/rejection
      reasons, immutable concurrent reuse, ownership, and transmission isolation.
- [x] Approved all listed comprehensive synthetic verification cases.
- [x] Dave authorized: **“Step 5D2 offline unit recognition only.”**

No required v1 unit or phrase approvals remain pending. Deferred RID, channel,
volunteer-station verification, display behavior, and excluded-identity reviews
are outside this authorized v1 scope; they are not invented approvals or blockers
to the approved text-only milestone.

Step 5D2 authorization permits no database changes, API routes, Whisper/transcription
changes, frontend changes, unit-status changes, incident changes, CAD actions,
or production wiring. This document update implements no recognition code.
