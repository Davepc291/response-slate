# Alert and Notification Engine Contract v1 — Step 8A

Status: Approved design contract for a future phased implementation. No code,
schema, or configuration is authorized by this document.

Implementation status: Not started. Step 8A creates this document only.

Contract identifier: `alert-notification-engine-v1`.

Reviewed baseline: `939b7642003a509e30839393eab646598fac2dad` (`939b764`). The
working tree was clean, and HEAD and local `origin/main` matched that commit
when this contract was created. Green CI is supplied baseline context, not a
claim of operational safety.

## Purpose and milestone boundary

Step 8A defines the smallest safe design for a future tone-detection,
keyword-detection, and notification pipeline over SDRTrunk recordings already
ingested by the existing pipeline. It exists to let a human volunteer receive
a synthetic, non-operational alert when a recording matches a configured tone
pattern or keyword, without granting the alerting system any authority over
incidents, unit status, or the production CAD.

This document approves a design and a phased roadmap. It does not implement
tone detection, keyword detection, an alert-event API, authentication,
database migrations, OneSignal, live radio, or any frontend control. Step 8B
through Step 8F, defined under [Phased implementation plan](#12-phased-implementation-plan),
each require a separate authorization to begin. No implementation files,
schema changes, existing-file changes, staging, commits, or pushes are part of
Step 8A.

## Authoritative dependencies

- [Shadow processor contract](shadow-processor-v1.md)
- [Shadow replay contract](shadow-replay-v1.md)
- [Shadow transcript import contract](shadow-transcript-import-v1.md)
- [Shadow replay evaluation contract](shadow-replay-evaluation-v1.md)
- [Mobile/PWA beta contract](mobile-pwa-beta-v1.md)
- [Mobile phone experience contract](mobile-phone-experience-v1.md)
- [Offline transcription evaluation](offline-transcription-evaluation.md)
- [Controlled transcription experiments](transcription-experiments.md)
- [Product requirements](product-requirements.md), FR-1, FR-6, and roadmap items 2–7
- [Database README](../database/README.md) and migrations `000001`–`000006`
- [Recordings filename parser](../backend/internal/recordings/filename.go)
- [Recording ingestion migration](../database/migrations/000002_recording_ingestion.sql)
- [Audio analysis package](../backend/internal/audioanalysis/analyzer.go)
- [Transcription package](../backend/internal/transcription/worker.go)
- [Config package](../backend/internal/config/config.go)
- [Operations monitor](../backend/internal/operations/monitor.go)

Historical approved contracts may still describe their own implementation as
not started, or as complete. That wording is not a reason to edit them or
reinterpret their approved semantics. This document changes none of those
files.

## Conflicts found during review

Two existing approved contracts explicitly prohibit the eventual behavior this
engine is designed toward. This section records those conflicts; it does not
resolve them.

| Existing prohibition | Source | Consequence for this contract |
| --- | --- | --- |
| "Register push notifications or notification permission prompts" is forbidden | [Mobile/PWA beta contract](mobile-pwa-beta-v1.md#prohibitions) | Step 8D (synthetic notification relay) cannot ship inside the existing PWA package boundary without a separate amendment to that contract's approved file/prohibition list. |
| "Add notifications, push subscriptions, background sync, geolocation, SMS, email, analytics, telemetry, or third-party beacons" is a non-goal | [Mobile phone experience contract](mobile-phone-experience-v1.md#non-goals-and-prohibitions) | The `/mobile` phone prototype cannot register a service-worker push subscription or request notification permission until that contract is amended or superseded for notifications specifically. |
| No Workbox, Firebase, Capacitor, Cordova, Ionic, or **OneSignal** service-worker runtime is approved | [Mobile/PWA beta contract](mobile-pwa-beta-v1.md#approved-future-files) | OneSignal's web SDK and service-worker file are explicitly named as forbidden dependencies in the current PWA package. Any future OneSignal integration is a new, separately reviewed exception to that file, not an extension of it. |

No existing backend package, database migration, or Go module name collides
with the package and table names proposed below. `backend/internal/alerts`,
`backend/internal/tonedetection`, `backend/internal/keyworddetection`, and
`backend/internal/notifyrelay` do not exist. No `alert_events`,
`tone_configurations`, `keyword_configurations`, `detection_audit`, or
`notification_deliveries` table exists in migrations `000001`–`000006`.
Migration `000007` is the next unused version number.

## 1. Safety boundary

This engine is a **read-only evidence and alerting layer**. It has no
authority over CAD, incidents, or unit status, matching the existing shadow
boundary established by
[shadow-processor-v1](shadow-processor-v1.md#no-cad-authority).

- All work through Step 8F is **synthetic and shadow-only**. No detector,
  event, or notification runs against live SDRTrunk audio, a live recordings
  directory, or a production notification audience until a separately
  authorized, explicitly named live-shadow milestone exists.
- Every alert produced by this engine carries the exact phrase **`NOT LIVE
  CAD`** in its safe display summary, matching the existing board and mobile
  warning convention (`SHADOW / REPLAY — NOT LIVE CAD`).
- This engine has **no operational authority**. It must never gain a code
  path that calls incident-creation, unit-status, or CAD-write logic.
  Detection, tone matching, and keyword matching are evidence-producing
  functions only, in the same sense that
  [dispatch interpretation](dispatch-interpretation-v1.md) and
  [unit-status vocabulary](unit-status-vocabulary-v1.md) results are evidence,
  not truth.
- An alert event **cannot create an incident, modify unit status, or write to
  any table owned by the existing radio-status/incident schema**
  (`radio_transmissions`, `unit_status_decisions`, or any future incident
  table). Alert tables are a separate, additive schema surface described in
  [Storage and retention](#9-storage-and-retention).
- **Browser connectivity is not proof of radio or backend availability.**
  `navigator.onLine`, a successful push-registration call, or a delivered
  notification prove only that the viewer's browser and the push relay were
  reachable at that moment. None of them prove SDRTrunk is recording, the Go
  backend is processing, the database is reachable, or any specific talkgroup
  is currently monitored. Every future notification-consuming surface must
  repeat this caveat, consistent with the existing
  [PWA connectivity limitation](mobile-pwa-beta-v1.md#security-limitations).
- **No phone microphone access.** This engine never requests
  `getUserMedia`, records audio from a phone or browser, or transmits
  client-captured audio anywhere. All audio input is SDRTrunk recording files
  already ingested server-side by the existing pipeline. A future native
  application (see [Phased implementation plan](#12-phased-implementation-plan))
  does not change this restriction without its own separate contract.

## 2. Input architecture

### Server-side processing only

SDRTrunk recording files are the only audio input. They are processed
entirely server-side by the existing ingestion pipeline
([recordings package](../backend/internal/recordings/watcher.go)) and the
existing [audio analysis package](../backend/internal/audioanalysis/analyzer.go).
This engine adds no new file-discovery, upload, or client-audio path. Tone and
keyword detectors described below consume:

- Decoded PCM already produced during audio analysis (for tone detection), or
- Transcript text already produced by the existing transcription worker (for
  keyword detection).

No detector opens a recording file itself, watches a directory, or duplicates
the existing [SDRTrunk filename parser](../backend/internal/recordings/filename.go).

### Talkgroup and channel metadata

The engine reuses the existing four-channel Greenwich talkgroup mapping
exactly as recorded in [product requirements FR-1](product-requirements.md#fr-1-recording-ingestion-and-talkgroup-roles)
and the `channel_label` column added by
[migration 000002](../database/migrations/000002_recording_ingestion.sql):

| TGID | Channel | Existing role | Alert eligibility |
| --- | --- | --- | --- |
| 57201 | CH1A | Primary dispatch; may create incidents | Eligible for tone and keyword detection |
| 57202 | CH2B | Status/operations; may not create incidents | Eligible for tone and keyword detection |
| 57203 | CH3B | Status/operations; may not create incidents | Eligible for tone and keyword detection |
| 57204 | CH4C | Status/operations; may not create incidents | Eligible for tone and keyword detection |

Alert eligibility is **independent of incident-creation eligibility**. A
keyword or tone match on CH2B, CH3B, or CH4C can raise an alert exactly as
CH1A can; it still creates no incident. Which talkgroups are actually
monitored in a future phase is an [unresolved question](#14-unresolved-questions),
not a decision made here.

### Evidence-only identity

Every detection references its source recording using the same
identity primitives the existing schema already establishes, and no others:

- `audio_fingerprint` (`pcm-s16le-16000-mono-v1:sha256:` + 64 lowercase hex
  digits) when audio content has been analyzed.
- `source_identity` (SHA-256 of `sdrtrunk-path-v1` + cleaned absolute path)
  when only metadata-only ingestion has occurred.
- TGID and channel label, exactly as already stored on `radio_transmissions`.

**TGID evidence remains evidence only.** A detection's TGID/channel value does
not grant that talkgroup the ability to create incidents, select a dispatch
role, or override the FR-1 table above. This mirrors the existing rule that
[imported channel/TGID values are evidence, not operational truth](shadow-transcript-import-v1.md#provenance-mapping).

**Audio fingerprint and source-record identity** are opaque identifiers only,
exactly as constrained by the existing shadow contracts: never a filesystem
path, never opened by the detector, never used for anything but joining a
detection back to its source `radio_transmissions` row.

### Transcript evidence may be absent or uncertain

Keyword detection depends on a transcript existing and being finished
(`transcription_status = 'completed'`). Tone detection depends only on
analyzed PCM (`processing_status = 'completed'` and `audio_probe IS NOT
NULL`), and therefore can run before, after, or entirely without a
transcript. A detector must treat a missing, empty, or still-`pending`/`failed`
transcript as **an absent evidence class**, not as a negative keyword result
and not as a reason to skip tone detection. Whisper transcripts are already
documented as untrusted and can hallucinate ([migration 000004 notes](../database/README.md#migration-000004-remote-transcription));
keyword evidence inherits that uncertainty and must never be presented as
verified.

## 3. Tone detection

Tone detection is a signal-processing evidence source over decoded PCM
produced by the existing [audio analysis pipeline](../backend/internal/audioanalysis/pcm.go).
It runs independently of transcription and keyword detection.

### Supported pattern shapes

- **Two-tone sequential patterns**: two fixed frequencies played in a fixed
  order for configured durations (the common fire-dispatch "quick call" tone
  pair).
- **Long tones**: a single sustained frequency held for a configured minimum
  duration (for example, a monitor or paging tone).
- **Configurable complex patterns**: an ordered sequence of more than two
  tone segments (frequency + duration pairs), with configured inter-segment
  gap tolerance, to support future multi-tone or warble patterns without a
  new detector type.

All three shapes are expressed as one configuration primitive: an ordered
list of `{frequency_hz, min_duration_ms, max_duration_ms}` segments plus
inter-segment gap bounds. A long tone is a one-segment list; a two-tone
sequence is a two-segment list.

### Detection method

Detection is **FFT-based frequency analysis** over fixed-size windows of the
decoded 16 kHz mono PCM already produced by audio analysis. No new audio
decode, FFmpeg invocation, or PCM re-derivation is introduced; the detector
consumes the same canonical PCM the existing fingerprint is computed from.

Every tone set is configured with:

- **Frequency tolerance** (Hz or cents): how far a detected peak frequency may
  drift from the configured frequency and still count as a match.
- **Minimum duration** (ms): the shortest sustained detection window that
  counts as a real tone segment rather than a transient.
- **Maximum gap** (ms): the longest silence or off-frequency gap allowed
  between sequential segments before the sequence is considered broken.
- **Confidence**: a normalized score (0–1) derived from signal-to-noise ratio
  and frequency-bin energy concentration during the matched window,
  configurable as a minimum-acceptance threshold per tone set.

### Per-talkgroup tone sets

Tone sets are configured per talkgroup/channel, not globally. The same
physical dispatch tone can be configured once per channel it is expected on,
with independently tunable tolerance, duration, and confidence, because
channel audio quality and use differ (CH1A primary dispatch vs. CH2B–CH4C
status/operations).

### Required guardrails

- **Detection must never rely on filename alone.** SDRTrunk filenames encode
  timestamp, site, alias channel label, TGID, and RID
  ([filename.go](../backend/internal/recordings/filename.go)); they contain
  no tone information. A tone match must always be the output of FFT analysis
  over decoded PCM, never inferred from a filename pattern, alias label, or
  RID.
- **False-positive handling**: every candidate match records its measured
  frequency, duration, and confidence in the detection audit record (see
  [Storage and retention](#9-storage-and-retention)) regardless of outcome, so
  a human can review borderline matches without re-processing audio.
- **Partial-tone handling**: a sequence that starts but does not complete
  within `max_gap` is recorded as a non-matching detection attempt with a
  `partial` reason code, not silently dropped and not upgraded to a match.
- **Noisy-audio handling**: a segment whose computed confidence is below the
  tone set's configured minimum is rejected with a `low_confidence` reason
  code even if frequency and duration otherwise align.
- **Duplicate-record handling**: the existing `audio_duplicate_of` alias
  mechanism from [migration 000003](../database/migrations/000003_audio_analysis.sql)
  applies unchanged. A detector must run against the canonical transmission
  only (`audio_duplicate_of IS NULL`); it must not independently re-detect
  and re-alert on alias rows, mirroring how transcription and evaluation
  already treat aliases as non-operational duplicates.

## 4. Keyword detection

Keyword detection is a text-evidence source over completed transcripts. It
runs independently of tone detection and can be enabled even when no tone set
is configured for a talkgroup.

### Matching rules

- **Whole-word matching**: a keyword matches only at token boundaries, using
  the same maximal-run-of-letters-or-numbers tokenization already defined by
  [offline transcription evaluation's normalization](offline-transcription-evaluation.md#metrics-greenwich-evaluation-v1)
  (`Engine 2, on-scene!` tokenizes to `engine 2 on scene`). `clear` must not
  match `unclear` or `cleared`, exactly as the existing critical-term
  vocabulary already requires.
- **Case-insensitive matching**: matching uses the same Unicode lowercase
  mapping as the existing evaluation normalization.
- **Normalized transcript matching**: matching runs against the normalized
  token sequence, not raw transcript bytes, so punctuation and casing
  differences never change a match outcome.

### Keyword lists

- Multiple **named keyword lists** are supported (for example, a
  "structure-fire" list and a "mutual-aid" list configured independently).
- Lists are **configurable per talkgroup**, mirroring tone sets: the same
  named list can be attached to one or more channels with independent
  enable/disable state.
- Each keyword entry may declare:
  - **Required context**: another phrase or list that must also appear in
    the same transcript (or within a configured token window) for the
    keyword to count as a match, reducing false positives from a bare word.
  - **Excluded context**: a phrase whose presence suppresses an otherwise
    matching keyword (for example, suppressing a "structure fire" keyword
    match when the same transcript also contains "false alarm" or
    "canceled").
  - **Ambiguity handling**: a keyword match with unresolved required/excluded
    context is recorded with an `ambiguous` state rather than silently
    promoted to a firm match or silently dropped, mirroring the existing
    native-ambiguity handling in
    [dispatch interpretation](dispatch-interpretation-v1.md) and
    [unit-status vocabulary](unit-status-vocabulary-v1.md).

### Required limitation

**Keywords must not independently prove an incident, address, call type, or
unit status.** A keyword match is text evidence about a transcript, exactly
as [critical-term recall in Step 4A](offline-transcription-evaluation.md#critical-terms-greenwich-critical-terms-v1)
is occurrence-count evidence, not alignment- or context-verified fact. A
keyword list must never be wired to call
[dispatch interpretation](dispatch-interpretation-v1.md),
[address](dispatch-interpretation-v1.md), or
[unit-status](unit-status-vocabulary-v1.md) resolution logic, and must never
be treated as a substitute for those pipelines.

## 5. Alert event contract

An alert event is a small, versioned, safe-to-display record produced by
either detector. It is the only object a future relay (Step 8D) or preference
system (Step 8E) may read.

### Proposed fields

| Field | Type | Notes |
| --- | --- | --- |
| `event_id` | opaque string | Server-generated identifier; never derived from private evidence. |
| `schema_version` | string | Fixed value `alert-event-v1` for this contract generation. |
| `created_at` | timestamptz | Server clock, not a claim of radio transmission time. |
| `source_kind` | enum | `synthetic` or `shadow_replay`, mirroring [`shadowprocessor.SourceKind`](shadow-processor-v1.md#source-metadata); never `live` until a separate live-shadow milestone is approved. |
| `channel` | enum | `CH1A`, `CH2B`, `CH3B`, or `CH4C`; evidence label only, per [Section 2](#2-input-architecture). |
| `tgid` | integer | Evidence label only; does not select dispatch role. |
| `detector_kind` | enum | `tone` or `keyword`. |
| `tone_set_id` | opaque string, nullable | Present only when `detector_kind = tone`. |
| `keyword_list_id` | opaque string, nullable | Present only when `detector_kind = keyword`. |
| `confidence` | number (0–1), nullable | From the detector's own scoring; `null` when a detector defines no numeric confidence. |
| `state` | enum | `matched`, `ambiguous`, `partial`, or `low_confidence`, matching the detector-specific reason codes above. |
| `synthetic` | boolean | Always `true` through Step 8F. |
| `shadow_only` | boolean | Always `true`; descriptive, not a security boundary, exactly as `ShadowOnly` is described in [shadow-processor-v1](shadow-processor-v1.md#no-cad-authority). |
| `dedup_key` | string | Deterministic key described in [Section 6](#6-deduplication-and-delay). |
| `expires_at` | timestamptz | See [Section 6](#6-deduplication-and-delay). |
| `display_summary` | string | Short, sanitized, human-readable text; see below. |

### Explicitly excluded from any notification payload

The alert event contract, and every downstream notification payload built
from it, must never include:

- Private transcript text or transcript fragments.
- Recording file paths or filenames.
- Credentials, tokens, or connection strings of any kind.
- Raw audio bytes or audio URLs.
- Dataset identifiers, `dataset_id`, or evaluation-pair identifiers from the
  Step 4A/6E–6H review and evaluation pipelines.
- Reviewer labels, notes, or exclusion reasons from `transcript_reviews`.

`display_summary` may name the channel, the tone-set or keyword-list label,
and the fixed phrase `NOT LIVE CAD`; for example: `"CH1A tone match: quick-call
alert (synthetic). NOT LIVE CAD."`. It must never echo transcript text,
because a keyword match's context might itself be sensitive.

## 6. Deduplication and delay

### Idempotent publishing

Publishing an alert event must be idempotent per `dedup_key`. `dedup_key` is a
deterministic function of `(source recording identity, detector_kind,
tone_set_id or keyword_list_id)`, so republishing the same detection (for
example, after a retry) produces the same key and never a second visible
alert.

### Cooldown and delay window

- A **configurable cooldown** applies per `dedup_key` prefix (channel +
  detector + tone-set/keyword-list): once an alert has fired, a repeat match
  within the cooldown window is recorded in the detection audit but not
  redelivered.
- A **configurable alert-delay window** allows a short buffer after first
  detection before publishing, so a detector can absorb late-arriving
  corroborating evidence (for example, a keyword match on a transcript that
  finishes slightly after a tone match on the same recording) without
  publishing two separate alerts for one real event.
- **Retry rules**: a failed publish (see [Section 11](#11-failure-behavior))
  retries with bounded backoff and a maximum attempt count, mirroring the
  existing bounded-retry pattern used by
  [transcription claims](../database/README.md#migration-000004-remote-transcription).
  Retries reuse the same `dedup_key` and must never create a second event for
  one detection.
- **Expiration**: `expires_at` bounds how long an alert event remains
  eligible for delivery or display. An expired, undelivered event is recorded
  as expired, not delivered late, so a volunteer never receives a stale alert
  claiming a current situation.
- **Ordering**: events are ordered by `created_at` for display purposes only;
  ordering never re-derives or re-infers the underlying recording's timestamp
  the way [Step 6C's replay runner explicitly does not infer chronological
  order across independent calls](shadow-replay-v1.md#sequential-execution-and-record-preservation).

### At-most-one visible alert per deduplication key

The cooldown and delay window together guarantee **at most one visible alert
per `dedup_key`** within its cooldown period. Every suppressed duplicate
still writes a detection audit row (Section 9); suppression is a display
decision, never a silent evidence loss.

### Delayed and out-of-order recordings

SDRTrunk recordings can be ingested out of `recorded_at` order (for example,
after a watcher restart, or when audio analysis retries a failed file). The
detector must key deduplication and ordering off deterministic recording
identity (`audio_fingerprint`/`source_identity`) and `created_at` of the
*detection*, not off `recorded_at`, so a delayed recording surfaces as a new,
clearly timestamped alert rather than silently reordering or replacing an
existing one.

## 7. Notification relay

### Future OneSignal integration

A future Step 8D may integrate OneSignal's Web Push REST API as the
notification relay. **No notification implementation is authorized by this
contract.** Step 8D requires its own separate contract that also resolves the
[conflicts recorded above](#conflicts-found-during-review) with the existing
PWA and mobile-phone-experience prohibitions.

### Credential isolation

- **Server-held credentials only.** Any OneSignal REST API key, app ID used
  for authenticated server calls, or webhook secret is held exclusively by
  the Go backend, following the existing `GFR_*` environment-variable
  convention already used for `GFR_DATABASE_URL` and other secrets in
  [config.go](../backend/internal/config/config.go). No such credential is
  ever compiled into, fetched by, or logged from the Angular application.
- **Never expose OneSignal REST credentials in Angular or the PWA.** The only
  OneSignal-related value that may ever reach a browser is a public
  client-side app identifier used by OneSignal's own web SDK for
  subscription registration — never the REST API key, which authorizes
  sending notifications and must remain server-side exactly as
  `GFR_DATABASE_URL` never reaches the frontend today.

### Sound and platform limitations

- **Standard web-push notification sounds only.** Web Push on both Android
  Chrome and desktop browsers plays the platform's default notification
  sound; there is no cross-browser API to specify a custom sound file for a
  web push notification.
- This contract explicitly documents that **custom alert sounds (for
  example, a distinct tone per talkgroup) require a later native iOS/Android
  application**, because only a native app with local notification APIs
  (or a PWA installed through mechanisms not yet available cross-platform)
  can bundle and select custom notification sound assets. No synthetic phase
  in this roadmap promises custom sounds.
- iOS Safari web push support and behavior differ by iOS version, matching
  the existing [documented iOS service-worker limitations](mobile-pwa-beta-v1.md#platform-support).
  Any future notification design must document iOS's exact supported
  behavior rather than assume Android parity.

### Tap behavior

A future notification may include a deep link so that tapping it opens a
**safe mobile evidence route** — for example, a future `/mobile/evidence`-style
screen scoped to that alert's `display_summary` and channel — never a raw
transcript view, never a live-audio player, and never an incident-creation or
unit-status-mutation screen.

## 8. User preferences

A future Step 8E may add authenticated, per-user preferences for:

- Enable/disable alerts globally.
- Selected talkgroups/channels to monitor.
- Selected tone sets to monitor.
- Selected keyword lists to monitor.
- Quiet hours (a time window during which matching alerts are suppressed or
  queued rather than delivered).
- Minimum confidence threshold below which a match is not delivered.
- Delay-window/cooldown overrides, within server-enforced bounds.

**User-level preferences cannot be safely implemented until real
authentication and server-side authorization have their own separately
approved contracts.** The existing
[mobile phone experience contract](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype)
already establishes that its welcome/login screen is a **prototype only, not
authentication**, and explicitly forbids a local-only credential substitute.
Preferences that gate what a specific person receives are exactly the kind of
authorization-sensitive feature that contract defers. Step 8E must not ship
before an approved identity provider, threat model, server-side session or
token validation, and server-side authorization contract exist.

## 9. Storage and retention

All tables below are **proposed**, for a future Step 8C migration (the next
free version is `000007`). No migration is created by Step 8A. Proposed
tables follow the existing schema conventions: `timestamptz` everywhere,
natural keys where a natural key exists, generated bigint identities
otherwise, append-only audit tables protected by `ENABLE ALWAYS` triggers as
already used for `unit_status_decisions` and `transcript_reviews`.

| Proposed table | Purpose | Notes |
| --- | --- | --- |
| `tone_configurations` | Versioned tone-set definitions (segments, tolerance, min duration, max gap, confidence threshold, talkgroup scope) | Configuration history via append-only versioned rows, not in-place mutation, matching the immutable-history pattern already used for `transcript_reviews`. |
| `keyword_configurations` | Versioned named keyword lists and per-keyword required/excluded context | Same append-only versioning approach. |
| `detection_audit` | One row per detector evaluation attempt, matched or not, referencing the source transmission by `(id, audio_fingerprint)` or `source_identity` | Records `state` (`matched`/`ambiguous`/`partial`/`low_confidence`), measured values, and the configuration version used. Append-only, mirroring `unit_status_decisions`. |
| `alert_events` | One row per published alert event, matching the [Section 5](#5-alert-event-contract) schema | `dedup_key` unique per cooldown window; no raw transcript or path columns. |
| `notification_deliveries` | One row per relay delivery attempt for an `alert_events` row | Records outcome, attempt count, and relay-safe error code only — never a webhook payload, transcript, or credential. |
| `alert_preferences` | Per-user preference rows from [Section 8](#8-user-preferences) | **Gated**: not created until an approved authentication/authorization contract exists. Listed here for schema planning only. |

### Retention and deletion

- `detection_audit` and `alert_events` retention periods are an
  [unresolved question](#14-unresolved-questions); no default is assumed.
- **No raw private transcripts appear in push records.** `notification_deliveries`
  and any OneSignal-bound payload derive only from `alert_events.display_summary`,
  never from transcript text, matching the [payload exclusions in Section 5](#5-alert-event-contract).
- Deletion of a `detection_audit` or `alert_events` row must not be possible
  through an ordinary UPDATE/DELETE path, consistent with the existing
  append-only audit convention; any future retention job is a separately
  reviewed, explicitly scoped deletion procedure, not ad hoc application code.

## 10. Administration

A future administrator workflow (no earlier than Step 8C/8D) manages tone and
keyword configuration through an explicit, versioned lifecycle:

- **Draft**: a new tone or keyword configuration version is created but not
  active.
- **Validate**: the draft is run against a fixed set of **synthetic test
  fixtures** (known-good and known-bad PCM/transcript samples) before it may
  be activated. A draft that fails its fixtures cannot be activated.
- **Activate**: exactly one configuration version per talkgroup/list is
  active at a time; activation is itself an audited, versioned action.
- **Deactivate**: a talkgroup or list can be disabled without deleting its
  configuration history.
- **Roll back**: reverting to a previous configuration version is itself a
  new versioned activation, never an in-place edit of history, consistent
  with `transcript_reviews`' "supersede, never delete" convention.

**No public configuration-mutation endpoint is authorized.** Every mutation
path above requires the same authenticated/authorized administrator context
gated in [Section 8](#8-user-preferences); none of it exists before that
authorization contract is approved.

## 11. Failure behavior

Every failure mode below must be **visible**, never silently swallowed, and
must **never claim successful delivery it did not achieve**, extending the
existing principle that [the shadow processor's stage outcomes never imply
correctness or CAD authority](shadow-processor-v1.md#stage-outcomes-and-failure-codes).

| Failure | Required behavior |
| --- | --- |
| Audio unavailable (analysis not yet completed, or `failed`) | Tone detection records a `skipped` detection-audit row with a fixed reason code; it never fabricates a tone result. |
| Transcript unavailable (`pending`, `failed`, or not yet requested) | Keyword detection records a `skipped` detection-audit row; it is not treated as "no keywords present." |
| Database unavailable | The detector and relay fail closed: no event is fabricated in memory-only state and presented as durable; the caller receives an explicit error, never a false "queued" response. |
| Relay (OneSignal or successor) unavailable | `notification_deliveries` records the failed attempt with a safe error code; retries follow [Section 6](#6-deduplication-and-delay)'s bounded rules; the alert event itself remains available for a later successful delivery or documented expiration. |
| Notification permission denied (browser) | The client records that permission was denied locally; it must not repeatedly re-prompt in a way that resembles a dark pattern, and must not claim alerts are active when they are not. |
| Stale PWA (old service worker/cached shell) | Follows the existing [stale-preview indicator contract](mobile-pwa-beta-v1.md#update-and-stale-version-handling): explicit `Stale preview...` text and an explicit reload action, never silent auto-update of notification-relevant code. |
| Duplicate or malformed input (recording, transcript, or config) | Rejected explicitly with a fixed safe error/reason code, never partially processed, matching the existing "no partial import" principle from [shadow-transcript-import-v1](shadow-transcript-import-v1.md#failure-semantics-and-safe-errors). |
| Any of the above | **Never silently claim successful delivery.** A UI, log line, or audit row must not report `delivered` unless the relay confirmed it. |

## 12. Phased implementation plan

| Phase | Scope | Authorization |
| --- | --- | --- |
| **8A** | This design contract. Documentation only. | This document. |
| **8B** | Synthetic tone/keyword detector implementation and the in-memory alert-event model, over synthetic fixtures only, following the existing shadow-package pattern (`New()`/`Process`-style constructors, no database, no network). | Separate future instruction. |
| **8C** | Database persistence and audit: migration `000007`+ for the tables in [Section 9](#9-storage-and-retention), append-only triggers, and schema tests following the `database/tests/00000N_schema_test.sql` convention. | Separate future instruction. |
| **8D** | Synthetic notification relay: OneSignal (or an equivalent relay) wired to synthetic alert events only, with server-held credentials, and resolution of the [PWA/mobile-phone-experience conflicts](#conflicts-found-during-review). | Separate future instruction; must also amend or supersede the conflicting existing contracts. |
| **8E** | Authenticated user preferences, gated on an approved authentication/authorization contract per [Section 8](#8-user-preferences). | Separate future instruction; blocked on a separate auth contract. |
| **8F** | Controlled shadow evaluation of the whole pipeline against real (but still non-operational) recordings, mirroring the human-review gates already established by [shadow-replay-evaluation-v1](shadow-replay-evaluation-v1.md#board-preview-passfail-gates). | Separate future instruction. |
| **Native app (later)** | A native iOS/Android application, required only to support custom per-tone notification sounds per [Section 7](#7-notification-relay). Not scheduled; a later, separately scoped decision. | Separate future instruction; not implied by 8A–8F. |

Each phase requires its own explicit authorization to begin, following the
same "documentation milestone does not authorize implementation" pattern used
by every existing Step 6 and Step 7 contract in this repository.

## 13. Acceptance tests

All fixtures for every phase below must be synthetic. No test may require
live radio, a private dataset, or a real OneSignal account.

| ID | Case | Required pass condition |
| --- | --- | --- |
| ANE-01 | Safety boundary | No detector, event, or relay code path can call incident-creation, unit-status-mutation, or CAD-write logic; verified by dependency inspection of the proposed `backend/internal/alerts`, `tonedetection`, `keyworddetection`, and `notifyrelay` packages. |
| ANE-02 | `NOT LIVE CAD` labeling | Every `alert_events.display_summary` and every notification-relevant UI surface includes the exact phrase `NOT LIVE CAD`. |
| ANE-03 | Synthetic-only fixtures | All Step 8B–8D tests use synthetic PCM/transcript/config fixtures; no private recordings, transcripts, or datasets are read. |
| ANE-04 | Deterministic tone fixtures | A fixed synthetic two-tone, long-tone, and complex-pattern fixture each produce the documented `matched` state at their configured tolerance, and a documented non-match just outside tolerance. |
| ANE-05 | Tone tolerance boundaries | Frequency, minimum-duration, maximum-gap, and confidence thresholds each have an inclusive/exclusive boundary test (just inside passes, just outside fails or is `low_confidence`/`partial`). |
| ANE-06 | Keyword whole-word matching | `clear` does not match `unclear`/`cleared`; `Engine 2` does not match `Engine 20`, reusing the existing normalization test vectors from [offline-transcription-evaluation.md](offline-transcription-evaluation.md#metrics-greenwich-evaluation-v1). |
| ANE-07 | Keyword ambiguity | A keyword with unmet required context, and a keyword suppressed by excluded context, both produce `ambiguous`/suppressed states, never a silent promotion to `matched`. |
| ANE-08 | Deduplication | Two detections sharing a `dedup_key` within the cooldown window produce exactly one visible `alert_events` row and two `detection_audit` rows. |
| ANE-09 | Retries | A simulated relay failure retries according to the configured bounded policy and never produces a second `alert_events` row for the same `dedup_key`. |
| ANE-10 | Data minimization | No `alert_events`, `notification_deliveries`, or relay payload row/object contains transcript text, a file path, a credential, raw audio, a dataset ID, or an evaluation-pair identifier, verified by an automated field-name and content scan. |
| ANE-11 | Secret isolation | No OneSignal REST API key or equivalent server secret appears in any Angular/PWA source file, build artifact, or browser-reachable network response, verified by source and build-output scanning. |
| ANE-12 | iPhone Safari PWA behavior | A synthetic Step 8D web-push subscription flow is documented and tested against iOS Safari's actual supported behavior for that iOS version, with limitations stated explicitly, not assumed equal to Android. |
| ANE-13 | Android PWA behavior | A synthetic Step 8D web-push subscription and delivery flow succeeds on Android Chrome in a test environment using only synthetic alert content. |
| ANE-14 | No incident/unit mutation | End-to-end synthetic pipeline tests assert zero writes to `radio_transmissions`, `unit_status_decisions`, or any incident table caused by alert/notification code. |
| ANE-15 | No live notifications during synthetic testing | Every Step 8B–8F automated test run and manual verification step explicitly confirms no notification was delivered to a real device outside an opted-in, clearly labeled synthetic test audience. |

## 14. Unresolved questions

These do not block this contract, but each must be resolved before the phase
that depends on it proceeds.

1. What are the **verified Greenwich tone frequencies and timing** (segment
   frequencies, durations, and gaps) for the department's actual dispatch
   tones? No frequency/timing values are assumed or hardcoded by this
   contract.
2. **Which talkgroups are eligible** for tone detection, keyword detection,
   or both, in the first synthetic-shadow deployment? [Section 2](#2-input-architecture)
   allows all four channels technically; actual monitoring scope is a
   separate decision.
3. What are the specific **cooldown and expiration values** (in seconds/
   minutes) for tone alerts versus keyword alerts, and do they differ by
   talkgroup or configuration?
4. What is the **retention period** for `detection_audit`, `alert_events`,
   and `notification_deliveries` rows, and does it differ by table?
5. Who owns the **OneSignal plan/account**, and under what organizational or
   billing arrangement, before Step 8D is authorized?
6. Which **authentication provider** and server-side authorization model will
   gate Step 8E preferences and Step 10 administration? None is selected or
   implied by this contract.
7. What is the approved **notification wording** beyond the safety-required
   `NOT LIVE CAD` phrase — specifically, how much of the tone-set/keyword-list
   label and channel name should appear in a push notification's title/body
   versus only in the safe display route it links to?
8. Is a **future native iOS/Android application** (required only for custom
   per-tone sounds per [Section 7](#7-notification-relay)) ever scheduled, and
   if so, does it reuse this alert-event schema or require its own?

## Step 8A validation checklist

- [ ] Only `docs/alert-notification-engine-v1.md` is added.
- [ ] Baseline, clean-tree, and `origin/main` equality at `939b764` are
      recorded.
- [ ] Existing shadow, mobile, PWA, transcription, and database contracts are
      read and not edited.
- [ ] Existing backend packages and migrations are inspected; no naming
      collision found; conflicts with existing PWA/mobile-phone-experience
      notification prohibitions are recorded, not resolved.
- [ ] Safety boundary, non-CAD authority, and no-microphone-access statements
      are explicit.
- [ ] Input architecture, tone detection, and keyword detection sections
      avoid inventing unverified Greenwich-specific frequencies or wording.
- [ ] Alert event schema excludes transcripts, paths, credentials, raw audio,
      dataset IDs, and evaluation-pair identifiers.
- [ ] Deduplication, delay, relay, preferences, storage, administration, and
      failure-behavior sections are defined without authorizing
      implementation.
- [ ] Phased plan names 8B–8F and the later native-app option without
      scheduling or authorizing any of them.
- [ ] Acceptance tests distinguish synthetic-only verification from
      platform-specific (iOS/Android) device checks.
- [ ] Unresolved questions are listed and none are silently assumed.
- [ ] No implementation file, schema migration, staging, commit, push, or
      deploy occurs.
