# Notification Relay Contract Amendment v1 — Step 8D-A

Status: Approved design amendment for a future phased implementation. No
code, schema, configuration, provider account, or credential is authorized
by this document.

Implementation status: Not started. Step 8D-A creates this document only.

Contract identifier: `notification-relay-amendment-v1`.

Reviewed baseline: `5d46b70a836571c9238f56fb508f702e28e9c21a` (`5d46b70`). The
working tree was clean, and HEAD and local `origin/main` matched that commit
when this amendment was created. Green CI is supplied baseline context, not a
claim of operational safety.

## Purpose and milestone boundary

[Step 8A](alert-notification-engine-v1.md) named, but did not resolve, a
direct conflict: its own future Step 8D (a synthetic notification relay)
cannot ship inside the existing PWA and mobile-phone-experience package
boundaries without amending the specific clauses that prohibit
notifications, push subscriptions, and OneSignal in those two contracts
([Conflicts found during review](alert-notification-engine-v1.md#conflicts-found-during-review)).

This document, Step 8D-A, is that amendment. It is documentation only. It:

- Identifies the exact existing clauses that block notification work and
  supersedes **only those clauses**, additively, for a future,
  separately-authorized Step 8D-B implementation.
- Leaves every other prohibition in
  [mobile-pwa-beta-v1](mobile-pwa-beta-v1.md) and
  [mobile-phone-experience-v1](mobile-phone-experience-v1.md) — including
  every authentication, authorization, session, and credential-storage
  prohibition — fully in force, unedited, and unamended.
- Defines the future server-side notification relay architecture, its
  database and API surface, its threat model, and its acceptance criteria,
  without creating any of it.
- Does **not** authorize Step 8D-B. A separate, explicit instruction is
  required to begin Step 8D-B, exactly as Step 8A already required for every
  phase in its [phased implementation plan](alert-notification-engine-v1.md#12-phased-implementation-plan).

No implementation file, schema migration, existing-file change, dependency
addition, provider account, credential, staging, commit, push, or deploy is
part of Step 8D-A.

## Authoritative dependencies

- [Alert and Notification Engine Contract v1 — Step 8A](alert-notification-engine-v1.md),
  all sections, especially
  [Section 7 (Notification relay)](alert-notification-engine-v1.md#7-notification-relay),
  [Section 8 (User preferences)](alert-notification-engine-v1.md#8-user-preferences),
  and [Conflicts found during review](alert-notification-engine-v1.md#conflicts-found-during-review).
- [Mobile/PWA beta contract v1](mobile-pwa-beta-v1.md), especially
  [Non-goals](mobile-pwa-beta-v1.md#non-goals),
  [Approved future files](mobile-pwa-beta-v1.md#approved-future-files),
  [Service worker registration](mobile-pwa-beta-v1.md#service-worker-registration),
  [`ngsw-config.json`](mobile-pwa-beta-v1.md#ngsw-configjson), and
  [Prohibitions](mobile-pwa-beta-v1.md#prohibitions).
- [Mobile phone experience contract v1](mobile-phone-experience-v1.md),
  especially
  [Non-goals and prohibitions](mobile-phone-experience-v1.md#non-goals-and-prohibitions)
  and
  [Welcome and login-screen prototype](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype).
- The Step 8B alert-event model:
  [`backend/internal/alerts`](../backend/internal/alerts/alerts.go).
- The Step 8C persistence and pipeline packages:
  [`backend/internal/alertpipeline`](../backend/internal/alertpipeline/alertpipeline.go),
  [`backend/internal/alertstore`](../backend/internal/alertstore/alertstore.go),
  [`backend/internal/alertstore` (Postgres)](../backend/internal/alertstore/postgres.go).
- [Migration `000007` (alert persistence)](../database/migrations/000007_alert_persistence.sql)
  and its [README section](../database/README.md#migration-000007-synthetic-alert-persistence-step-8c).
- [Current PWA service-worker config](../web/ngsw-config.json).
- [Current web app manifest](../web/public/manifest.webmanifest).
- [Config package](../backend/internal/config/config.go), for the existing
  `GFR_*` environment-variable convention.
- [Database README](../database/README.md), migrations `000001`–`000007`.
- [Product requirements](product-requirements.md), line items on
  authentication/authorization before shared deployment and network/transport
  policy.

Historical approved contracts may still describe their own implementation as
not started. That wording is not a reason to edit them. This document
changes none of those files.

## 1. Clauses superseded

This amendment is **additive and narrow**. It does not rewrite, delete, or
reinterpret [mobile-pwa-beta-v1](mobile-pwa-beta-v1.md) or
[mobile-phone-experience-v1](mobile-phone-experience-v1.md). Both documents
remain unedited, immutable history of what was approved at their own
reviewed baselines. Where this section names a clause, that clause's text
**still exists unchanged** in its source document; this amendment document
is what a future Step 8D-B implementer must read *in addition to* the
source document, and where the two disagree on the four items below, this
document controls — and only for these four items.

| # | Source clause (verbatim) | Location | Superseding effect |
| --- | --- | --- | --- |
| 1 | "Add authentication, authorization, user accounts, **push notifications**, SMS, email, geolocation, background sync, or share-target handlers." | [mobile-pwa-beta-v1 § Non-goals](mobile-pwa-beta-v1.md#non-goals) | Only the **push notifications** term is superseded, and only under the gating in [Section 2](#2-this-amendment-does-not-authorize-implementation) below. Authentication, authorization, user accounts, SMS, email, geolocation, background sync, and share-target handlers remain fully prohibited by the original clause, unamended. |
| 2 | "Register push notifications or notification permission prompts." | [mobile-pwa-beta-v1 § Prohibitions](mobile-pwa-beta-v1.md#prohibitions) | Superseded only under the gating in [Section 2](#2-this-amendment-does-not-authorize-implementation); until that gate is met, this prohibition remains in full effect and this amendment adds no permission to register push notifications today. |
| 3 | "Do not add Workbox, Firebase, Capacitor, Cordova, Ionic, **OneSignal**, or a custom service worker runtime." | [mobile-pwa-beta-v1 § Approved future files](mobile-pwa-beta-v1.md#approved-future-files) | Only the **OneSignal** term is conditionally superseded, and only as a **server-side REST API integration in the Go backend**, per [Section 4](#4-future-server-side-notification-relay-architecture). Workbox, Firebase, Capacitor, Cordova, Ionic, and any custom service-worker runtime remain fully prohibited, unamended — see [Section 4.2](#42-no-new-service-worker-runtime) for why no exception to "custom service worker runtime" is needed or requested. |
| 4 | "Add **notifications, push subscriptions**, background sync, geolocation, SMS, email, analytics, telemetry, or third-party beacons." | [mobile-phone-experience-v1 § Non-goals and prohibitions](mobile-phone-experience-v1.md#non-goals-and-prohibitions) | Only the **notifications, push subscriptions** terms are superseded, and only under the gating in [Section 2](#2-this-amendment-does-not-authorize-implementation). Background sync, geolocation, SMS, email, analytics, telemetry, and third-party beacons remain fully prohibited, unamended. |

**Explicitly not superseded**, and not touched by this document in any way:

- [mobile-phone-experience-v1 § Non-goals and prohibitions](mobile-phone-experience-v1.md#non-goals-and-prohibitions):
  "Add authentication, authorization, user accounts, OAuth, identity tokens,
  sessions, cookies, credential storage, or password-reset behavior." This
  clause is the reason Step 8D-B cannot begin — see [Section 2](#2-this-amendment-does-not-authorize-implementation).
- [mobile-phone-experience-v1 § Welcome and login-screen prototype](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype):
  "Real authentication requires a later contract... A local-only substitute
  is forbidden." This amendment does not name, select, or imply that later
  contract; see [Section 3](#3-authentication-prerequisite).
- [mobile-pwa-beta-v1 § Non-goals](mobile-pwa-beta-v1.md#non-goals): every
  term other than "push notifications" (authentication, authorization, user
  accounts, SMS, email, geolocation, background sync, share-target
  handlers) — all remain prohibited.
- [alert-notification-engine-v1 § 8 (User preferences)](alert-notification-engine-v1.md#8-user-preferences):
  its gating statement that "User-level preferences cannot be safely
  implemented until real authentication and server-side authorization have
  their own separately approved contracts" is restated, not weakened, by
  [Section 3](#3-authentication-prerequisite) below.

## 2. This amendment does not authorize implementation

**Step 8D-A authorizes no code, schema, dependency, provider account, or
credential.** It resolves the *documentation* conflict recorded by Step 8A so
that a future, separately authorized Step 8D-B has an unambiguous contract
to implement against. It does not itself start Step 8D-B, in the same way
Step 8A's own approval of its phased plan did not start Step 8B.

Step 8D-B requires its own explicit future authorization, and that
authorization is itself blocked until every item in the
[prerequisite order](#26-prerequisite-order) is satisfied — most importantly,
until [Step 9 authentication/authorization](#3-authentication-prerequisite)
exists and is implemented. Reading and approving this document is not that
authorization and is not Step 9.

## 3. Authentication prerequisite

[Section 1's superseded clauses](#1-clauses-superseded) only ever apply to
**authenticated users**. This document creates no authentication. The
existing prohibition against authentication, authorization, sessions,
tokens, cookies, and credential storage in
[mobile-phone-experience-v1](mobile-phone-experience-v1.md#non-goals-and-prohibitions)
remains in full force, and the existing statement that "[a] local-only
substitute is forbidden"
([mobile-phone-experience-v1 § Welcome and login-screen prototype](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype))
remains in full force.

**No notification implementation of any kind — device registration,
subscription creation, preference storage, relay code, or delivery — is
authorized until a separate contract (referred to here only as "Step 9",
naming no identity provider, and implying no specific design) is approved
and implemented**, covering at minimum:

- An approved identity provider and account model.
- Server-side session or token validation (never a client-only credential
  substitute).
- Server-side authorization rules (roles, eligible recipients, what a given
  authenticated user may register, view, or configure).
- Logout and revocation behavior.
- Audit requirements for authentication events.
- The private-data boundary between authenticated and unauthenticated
  surfaces.

Every section below that describes registration, preferences, consent, or
delivery assumes Step 9 already exists. None of it can be built first.

## 4. Future server-side notification relay architecture

### 4.1 Design summary

The relay is **entirely server-mediated**. The Angular/PWA client never
talks to a push provider (OneSignal or any successor) directly, never loads
a provider's web SDK, and never holds a provider credential of any kind —
not even a "public" app identifier.

1. The browser's **native Push API** (`PushManager.subscribe`), using an
   application-server key pair the Go backend generates and whose private
   half never leaves the backend, creates a standard W3C Push subscription
   (`endpoint`, `keys.p256dh`, `keys.auth`) in the client.
2. The authenticated client sends that raw subscription object to the Go
   backend over an authenticated API call. The subscription object itself
   is not a secret — it is analogous to a mailing address — but it is only
   ever accepted, stored, and used tied to the authenticated user and
   device that created it.
3. The Go backend is the **only** component that ever calls a push
   provider's REST API (for example, OneSignal's), using server-held
   credentials, to relay a notification for that stored subscription.
4. No OneSignal web SDK, no OneSignal service-worker file, and no
   provider-specific browser runtime is added at any point in this design.

This design is deliberately stricter than "OneSignal's web SDK with a
public app ID in the browser," because it removes every provider identifier
from the browser entirely, which is what makes [Section 5](#5-credential-isolation)'s
"no privileged identifier in Angular, ever" requirement satisfiable by
construction rather than by discipline alone.

### 4.2 No new service-worker runtime

[mobile-pwa-beta-v1](mobile-pwa-beta-v1.md#approved-future-files) already
approves adding `@angular/service-worker`, and that package's `SwPush`
service provides native `push` and `notificationclick` event handling
inside the existing generated `ngsw-worker.js` — it does not require a
second service-worker file, `importScripts`, Workbox, Firebase, or a
OneSignal service-worker runtime. This is why [Section 1, row 3](#1-clauses-superseded)
supersedes only the word "OneSignal" in the approved-future-files
prohibition list: "a custom service worker runtime" remains fully
prohibited and is not needed for this design.

### 4.3 Component overview (proposed, not created)

```text
Angular/PWA (authenticated)          Go backend                    Push provider
--------------------------          -----------------------       -----------------
SwPush.requestSubscription()  --->  POST /api/notifications/       (no direct contact)
  (native browser Push API)          devices (authenticated)
                                       |
                                       v
                                notifydevices (store)
                                       |
                                       v
alertpipeline / alertstore     ->  notifyoutbox (bounded retry,
(Step 8C, existing)                  backoff, idempotency,
                                      expiration, dead-letter)
                                       |
                                       v
                                notifyrelay (server-held        --->  Provider REST API
                                  credentials only)                   (dev/preview/prod
                                       |                               app, separately
                                       v                               credentialed)
                                notifydeliveries (audit,
                                  no secrets, no full payload)
```

`notifyoutbox` consumes `alert_events` rows already produced by the existing,
approved [Step 8C alertpipeline](../backend/internal/alertpipeline/alertpipeline.go)
and [alertstore](../backend/internal/alertstore/alertstore.go) packages
unchanged. This amendment adds no new detector, no new alert-event field,
and no change to [Section 5 of the Step 8A contract](alert-notification-engine-v1.md#5-alert-event-contract).

## 5. Credential isolation

- **Server-held credentials only.** Any push-provider REST API key, app ID
  used for authenticated server calls, webhook secret, or VAPID private key
  is held exclusively by the Go backend, using the existing `GFR_*`
  environment-variable convention already used for `GFR_DATABASE_URL` and
  other secrets in [config.go](../backend/internal/config/config.go).
- **Nothing reaches the browser.** Per [Section 4.1](#41-design-summary), no
  OneSignal (or successor) app ID, REST API key, webhook secret, or "public"
  SDK identifier of any kind is compiled into, fetched by, or logged from the
  Angular application. The only key material the browser ever holds is the
  **public** half of the backend-generated VAPID key pair, which is not a
  provider credential and grants no ability to send notifications — it only
  lets the browser's own Push API address a subscription to the backend's
  application server identity, which is how the Web Push standard is
  designed to work.
- **Prohibited locations for OneSignal keys, REST API keys, tokens, or
  privileged identifiers** — none of the following may ever contain one:
  - Angular source, templates, or environment files.
  - The built PWA bundle (any JS, CSS, or source map shipped to a browser).
  - Service-worker caches (`ngsw-worker.js`, `ngsw.json`, or any asset group
    defined in [`ngsw-config.json`](../web/ngsw-config.json)).
  - Git history, commit messages, or any tracked file.
  - Application or access logs, at any log level.
  - `detection_audit`, `alert_events`, `notification_deliveries`, or any
    other database table's audit or free-text columns.
  - Browser storage of any kind (`localStorage`, `sessionStorage`,
    IndexedDB, Cache Storage, cookies).
- **Automated verification** of the above is required before Step 8D-B may
  be considered complete; see [NRA-10 and NRA-11](#27-acceptance-criteria).

## 6. Device and subscription registration

Device/subscription registration is defined **only for authenticated
users**, per [Section 3](#3-authentication-prerequisite). There is no
anonymous, pre-authentication, or "try it before you log in" registration
path.

- Registration is a single authenticated API call carrying the browser's
  native Push subscription object (`endpoint`, `keys.p256dh`, `keys.auth`),
  a client-declared platform hint (for UX only, never trusted for security
  decisions), and nothing else. It never carries a raw device identifier,
  advertising ID, or any identifier the browser does not already expose to
  the Push API itself.
- The server validates the subscription shape, associates it with the
  calling user's server-verified identity (never a client-supplied user ID),
  and stores it. The server never accepts a subscription payload naming a
  different user than the authenticated caller.
- A user may hold more than one registered device (for example, a phone and
  a desktop browser); each is a distinct row, independently revocable.
- Re-registering the same `endpoint` for the same user updates the existing
  row (idempotent); re-registering the same `endpoint` for a *different*
  user is rejected and audited as a possible cross-user registration attempt
  (see [Section 28 threat model](#28-threat-model)).

## 7. Server-side preference enforcement

All of the following are enforced **server-side**, at delivery time, never
trusted from a client-supplied flag on the notification payload itself:

- **User**: only the registered device's owning, currently-authenticated,
  non-disabled user's preferences apply.
- **Role**: if a future role model restricts which alert types a role may
  receive, the role check happens server-side against the current role at
  delivery time, not a cached role from registration time.
- **Talkgroup/channel**: per [Step 8A Section 8](alert-notification-engine-v1.md#8-user-preferences),
  a user's selected `CH1A`/`CH2B`/`CH3B`/`CH4C` subset gates delivery.
- **Tone set**: a user's selected tone-set subscriptions gate delivery of
  tone-detector alert events.
- **Keyword list**: a user's selected keyword-list subscriptions gate
  delivery of keyword-detector alert events.
- **Quiet hours**: a configured time window (in the user's own configured
  time zone, not server-local time) suppresses or queues matching alerts
  per [Step 8A Section 8](alert-notification-engine-v1.md#8-user-preferences);
  this amendment does not choose whether quiet-hours behavior is "suppress"
  or "queue for later," leaving that an [unresolved decision](#29-unresolved-decisions).
- **Enable/disable**: a global off switch always wins over every other
  preference; a disabled user or disabled device receives no delivery
  attempt at all, not a suppressed one (see [Section 10 outbox model](#10-outboxrelay-model)
  for the distinction).

A preference change takes effect for the **next** evaluated delivery; it
never retroactively alters an `alert_events` row or an already-queued
outbox entry's original evaluation record, consistent with the
append-only, supersede-never-mutate convention already used by
`detection_audit`, `alert_events`, and `transcript_reviews`.

## 8. Consent, revocation, and device lifecycle

- **Explicit consent** is required before a device is registered for
  delivery. The browser's native notification-permission prompt
  (`Notification.requestPermission()`) is necessary but not sufficient:
  the server also records an explicit, timestamped consent event tied to
  the authenticated user and the specific device registration, separate
  from the browser permission state, because a browser permission grant is
  revocable outside the application and must not be assumed durable.
- **Revocation** is available to the user at any time (an explicit "stop
  notifications on this device" action) and takes effect for the next
  delivery attempt, not any attempt already handed to the provider.
- **Device replacement**: registering a new subscription for a device the
  user already registered (browser subscription rotation, which the Push
  API can trigger unpredictably) supersedes the prior row for that logical
  device; it does not silently duplicate delivery.
- **Logout**: per [Section 3](#3-authentication-prerequisite), real logout
  behavior belongs to Step 9. Whatever Step 9 defines as logout must, at
  minimum, stop further notification delivery to the session's device
  without necessarily deleting the underlying subscription registration
  (a user may log back in on the same device and expect continuity) —
  the exact behavior is an [unresolved decision](#29-unresolved-decisions).
- **Disabled account**: an account disabled by an administrator immediately
  stops all delivery to every device registered to that account, checked
  server-side at delivery time (see [Section 9](#9-per-userper-device-authorization-at-delivery)),
  not only at registration time.
- **Lost device**: the user (or an administrator, on the user's behalf)
  must be able to revoke a specific device registration without affecting
  the user's other devices.
- **Session revocation**: if Step 9 supports revoking a specific session,
  any device registration created under that session must be revocable as
  part of the same action, so a stolen session cannot be used to silently
  retain a live notification channel after the session itself is killed.

## 9. Per-user/per-device authorization at delivery

Every individual delivery attempt — not only registration — re-checks:

1. The target user is currently authenticated-capable (not disabled, not
   deleted) per Step 9's server-side state.
2. The target device registration is still active (not revoked, not
   superseded by a replacement).
3. Consent for that device is still in effect.
4. The alert event's channel/tone-set/keyword-list matches the user's
   current, not cached, preferences.

A delivery that fails any of the four checks is not sent, and is recorded
as a skipped/unauthorized delivery attempt in the audit trail
([Section 17](#17-delivery-audit)), never silently dropped and never sent
"just this once" to save an outbox retry.

## 10. Outbox/relay model

Alert events flow through a durable outbox, not a fire-and-forget call to
the provider:

- **Bounded retries**: a fixed maximum attempt count per outbox entry, an
  explicit caller/configuration value, never assumed, mirroring the
  existing bounded-retry convention in
  [`GFR_TRANSCRIPTION_MAX_ATTEMPTS`](../backend/internal/config/config.go)
  and [Step 8A Section 6](alert-notification-engine-v1.md#6-deduplication-and-delay).
- **Backoff**: retries use bounded exponential (or otherwise monotonic)
  backoff with a configured ceiling, never immediate tight-loop retry
  against a failing provider.
- **Idempotency**: each outbox entry carries a stable idempotency key
  derived from `(alert_events.event_id, device_id)`, so a retried send
  after an ambiguous provider response (timeout, 5xx) cannot create a
  second visible notification for the same event on the same device.
- **Expiration**: an outbox entry inherits `alert_events.expires_at`
  ([Step 8A Section 5](alert-notification-engine-v1.md#5-alert-event-contract));
  an entry that exhausts its attempts or reaches expiration without
  success is marked expired, never sent late.
- **Cancellation**: if a user revokes consent, disables notifications, or
  is disabled by an administrator while an entry is still queued, the entry
  is canceled, not delivered on its next scheduled attempt.
- **Dead-letter handling**: an entry that exhausts retries without expiring
  or being canceled moves to a dead-letter state, visible to
  administrators/observability, never retried indefinitely and never
  silently discarded.
- **Per-device fan-out**: one `alert_events` row can fan out to multiple
  outbox entries (one per eligible device); each entry has its own retry,
  backoff, and dead-letter state independent of the others.

## 11. Evidence-state gating

**No user-visible notification is produced for a `partial`, `ambiguous`, or
`low_confidence` state, a `rejected` detection, a stale or expired
`alert_events` row, or a cooldown-suppressed duplicate — unless a later,
separately approved policy explicitly authorizes a specific state for
delivery.** This amendment authorizes none of them by default.

This directly extends [Step 8A Section 5's `state` enum](alert-notification-engine-v1.md#5-alert-event-contract)
(`matched`, `ambiguous`, `partial`, `low_confidence`) and
[Section 11's failure-behavior table](alert-notification-engine-v1.md#11-failure-behavior):
until a future policy says otherwise, `notifyoutbox` only ever enqueues
entries for `alert_events` rows whose `state = 'matched'`. Every other
state remains visible in `detection_audit` and `alert_events` for human
review, exactly as today, but produces zero outbox entries.

## 12. Notification text and lock-screen minimalism

Notification title and body text must be **minimal and non-sensitive**,
safe to read on a locked screen by anyone who can see the device:

- Only `alert_events.display_summary` (already sanitized and bounded to 256
  bytes by [Step 8A Section 5](alert-notification-engine-v1.md#5-alert-event-contract)
  and enforced by the `alert_events` table's own CHECK constraint in
  [migration `000007`](../database/migrations/000007_alert_persistence.sql))
  or a further-truncated subset of it may appear in the notification title
  or body.
- The exact phrase `NOT LIVE CAD` must remain present in the visible
  notification text itself, not only in the linked-to page, so a
  lock-screen glance never implies operational authority.
- No transcript fragment, address, unit name beyond the fixed evidence
  labels, or free-text field ever appears in a notification title or body.

## 13. Payload exclusions

Every field already excluded from an alert-event payload by
[Step 8A Section 5](alert-notification-engine-v1.md#explicitly-excluded-from-any-notification-payload)
remains excluded from every notification payload built from it. In
addition, no notification payload may ever contain:

- Raw transcript text or fragments.
- Raw audio bytes, audio URLs, or any reference resolvable to an audio file.
- Recording file paths or filenames.
- Credentials, tokens, connection strings, or session identifiers of any
  kind.
- Private dataset identifiers, `dataset_id`, or evaluation-pair identifiers.
- Unrestricted or free-form metadata of any kind. Only the fixed,
  contract-defined fields (`event_id`, `channel`, `state`, `display_summary`,
  a safe deep-link route) may appear in a push payload's data section.

## 14. Safe deep links

A notification may include a deep link, but only into an **authenticated**
application route:

- The link target must be a route that itself re-checks authentication and
  authorization on load (see [Section 16](#16-push-click-re-authorization)),
  never a route that trusts the notification click as proof of identity.
- The link target is an evidence-only view scoped to that specific alert
  event's `display_summary` and channel — for example, a future
  `/mobile/evidence`-style route already anticipated by
  [Step 8A Section 7](alert-notification-engine-v1.md#7-notification-relay)
  — never a raw transcript view, never a live-audio player, and never an
  incident-creation or unit-status-mutation screen, matching
  [mobile-phone-experience-v1's Evidence screen](mobile-phone-experience-v1.md#evidence)
  constraints unchanged.
- The link must never encode a credential, session token, or bypass
  parameter in its query string or fragment.

## 15. Service-worker caching prohibitions

The PWA service worker must **never** cache:

- Any authenticated API response (notification preferences, device list,
  alert history, or any Step 9-gated endpoint).
- Subscription credentials, the VAPID key pair, or any provider identifier.
- Notification payload history (received pushes are not written to Cache
  Storage or IndexedDB by the service worker itself).
- Private alert data of any kind.

This extends
[mobile-pwa-beta-v1's forbidden cache contents](mobile-pwa-beta-v1.md#ngsw-configjson)
unchanged; [`ngsw-config.json`](../web/ngsw-config.json) continues to define
only an `app-shell` asset group with no `dataGroups`, and this amendment
proposes no `dataGroups` addition. Push-related data flows through
`SwPush`'s in-memory `messages`/`notificationClicks` observables and
authenticated API calls, never through the precache.

## 16. Push-click re-authorization

Tapping a notification must **re-check authentication and authorization
server-side** before showing any evidence content:

- The deep-link route's data load is a fresh, authenticated API call; it
  never trusts client-side state assembled at notification-display time.
- If the session has expired, been revoked, or the account has been
  disabled since the notification was sent, the click lands on the normal
  Step 9 authentication flow, not on the evidence content.
- A stale or already-expired `alert_events` row (per [Section 11](#11-evidence-state-gating))
  must not render as if current; the route must reflect the event's own
  `expires_at` state.

## 17. Delivery audit

`notification_deliveries` (already named in
[Step 8A Section 9](alert-notification-engine-v1.md#9-storage-and-retention))
records one row per relay delivery attempt:

- Outcome (`sent`, `failed`, `expired`, `canceled`, `dead_letter`,
  `unauthorized`), attempt count, and a fixed, safe, allow-listed error
  code — mirroring `detection_audit.reason`'s
  `^[a-z][a-z0-9_]{0,63}$` convention.
- A reference back to `alert_events.event_id` and the device registration
  it targeted, never the raw subscription `endpoint` in plaintext audit
  text (see [Section 18](#18-rate-limits-abuse-controls-and-observability)).
- **Never** a provider secret, a webhook payload, a full notification
  payload, or transcript text.

Deletion of a `notification_deliveries` row must not be possible through an
ordinary UPDATE/DELETE path, consistent with the append-only convention
already used by `detection_audit` and `alert_events`.

## 18. Rate limits, abuse controls, and observability

- **Rate limits**: bounded per-user and per-device delivery rate limits
  (explicit configuration values, no assumed default, consistent with this
  amendment's [Section 10](#10-outboxrelay-model) bounded-retry convention)
  prevent a misconfigured tone/keyword list or a repeated cooldown-boundary
  edge case from flooding one device.
- **Abuse controls**: registration endpoints are rate-limited per
  authenticated user to prevent registration-spam; delivery attempts for a
  revoked or disabled device are rejected, not merely skipped, and repeated
  attempts against a revoked device are themselves rate-limited and logged.
- **Provider failure handling**: a provider outage or elevated error rate
  is detected and surfaced (see [Step 8A Section 11](alert-notification-engine-v1.md#11-failure-behavior)'s
  "never silently claim successful delivery" principle, unchanged and
  extended here); the outbox continues to record failed attempts and retry
  per [Section 10](#10-outboxrelay-model), never fabricating a `sent` outcome.
- **Duplicate suppression**: the outbox's idempotency key
  ([Section 10](#10-outboxrelay-model)) prevents a retried send from
  producing two visible notifications for one `(event, device)` pair, in
  addition to the existing `alert_events.dedup_key` suppression from
  [Step 8A Section 6](alert-notification-engine-v1.md#6-deduplication-and-delay).
- **Observability with redaction**: metrics and logs cover queue depth,
  attempt counts, latency, and outcome distribution; any log line that
  would otherwise include a subscription `endpoint`, provider identifier,
  or device token is redacted or truncated to a non-reversible prefix
  before it is written.

## 19. Synthetic test mode

A synthetic test mode must exist and must be structurally incapable of
reaching a real user or a production provider application:

- Test-mode delivery only ever targets devices explicitly flagged
  `test_mode = true` on their own registration row, created by an opted-in
  tester under Step 9 authentication, never inferred from environment alone.
- Test-mode delivery only ever calls the **development** or **preview**
  provider application credentials ([Section 20](#20-separate-provider-environments)),
  never the production application, regardless of feature flags — the
  production credential is not merely unused but not present in a
  non-production runtime configuration.
- Every automated test and manual verification step must explicitly confirm
  no notification reached a real device outside an opted-in, clearly
  labeled synthetic test audience, matching
  [Step 8A's ANE-15 acceptance test](alert-notification-engine-v1.md#13-acceptance-tests)
  unchanged.

## 20. Separate provider environments

Development, preview, and production each use **separate provider
applications and separate credentials** — never a shared app ID or REST key
across environments:

| Environment | Provider application | Credential source | May reach real users |
| --- | --- | --- | --- |
| Development | Dedicated dev provider app | Local-only `GFR_*` env values, never committed | No |
| Preview | Dedicated preview provider app | Preview-deployment secret store | No — opted-in testers only |
| Production | Dedicated production provider app | Production secret store, server-only | Only after the explicit approval gate in [Section 26](#26-prerequisite-order) |

A development or preview credential must never be capable of sending to the
production provider application's subscriber list, by construction (a
different app ID at the provider), not only by configuration discipline.

## 21. Platform coverage

| Platform/condition | Required documented behavior |
| --- | --- |
| iPhone/iPad installed PWA | iOS Safari's Web Push support is version-dependent; installed-PWA push support differs by iOS version. Any future implementation must document the exact minimum supported iOS/iPadOS version and degrade to "notifications unavailable on this device" below it — never silently pretend registration succeeded. |
| Android Chrome | Full Web Push support is expected; the acceptance device target, per [Step 8A's ANE-13](alert-notification-engine-v1.md#13-acceptance-tests) and [MPX-20](mobile-phone-experience-v1.md#acceptance-test-matrix) precedent. |
| Browser permission denial | Recorded client-side, per [Step 8A's failure-behavior table](alert-notification-engine-v1.md#11-failure-behavior) ("must not repeatedly re-prompt in a way that resembles a dark pattern, and must not claim alerts are active when they are not"), unchanged and restated here. |
| Revoked permission (after prior grant) | Detected on next attempted use of `SwPush` (a `PushManager` subscription can silently invalidate); the server-side outbox must treat a provider-reported invalid-subscription error as a signal to mark the device registration inactive, not as a transient failure to retry indefinitely. |
| Unsupported browser | The UI must state that push notifications are unavailable in the current browser, never silently omit the feature without explanation, consistent with the existing PWA-status messaging pattern. |

## 22. Sound is platform-controlled

Restating [Step 8A Section 7](alert-notification-engine-v1.md#7-notification-relay)
unchanged: **web push custom sounds cannot be promised.** Web Push on
Android Chrome and desktop browsers plays the platform's default
notification sound. There is no cross-browser API to specify a custom sound
file for a web push notification. A distinct sound per talkgroup or tone
set, if ever required, needs a native iOS/Android application with local
notification APIs — a separate, unscheduled, future decision, not
authorized or implied here.

## 23. "NOT LIVE CAD" and evidence-only warnings are preserved

Every warning already required by
[Step 8A Section 1](alert-notification-engine-v1.md#1-safety-boundary),
[mobile-pwa-beta-v1's required preservation table](mobile-pwa-beta-v1.md#existing-surface-that-must-be-preserved),
and
[mobile-phone-experience-v1's required preservation section](mobile-phone-experience-v1.md#required-preservation)
remains required, unchanged, and extends to every new notification-related
surface:

- The exact phrase `NOT LIVE CAD` appears in the notification text itself
  ([Section 12](#12-notification-text-and-lock-screen-minimalism)), in every
  deep-link destination, and in every settings/consent screen this
  amendment anticipates.
- `Synthetic replay preview. No operational authority.` remains visible on
  every notification-settings screen, matching the existing mobile-shell
  convention.
- This engine has **no operational authority**, restated from
  [Step 8A Section 1](alert-notification-engine-v1.md#1-safety-boundary)
  unchanged: no notification code path may call incident-creation,
  unit-status, or CAD-write logic.

## 24. Delivery is not proof of anything operational

Restating and extending
[Step 8A Section 1's connectivity-is-not-proof principle](alert-notification-engine-v1.md#1-safety-boundary):
**a successful push delivery proves only that the provider accepted the
message for that subscription at that moment.** It does not prove:

- CAD truth, or that the underlying detection reflects reality.
- Incident creation (this system creates none).
- Unit status (this system mutates none).
- That the notification was seen, read, or acted on by the recipient.
- That SDRTrunk is recording, the Go backend is processing, or the database
  is reachable at any time after the alert event was created.

Every future notification-consuming surface must repeat this caveat,
exactly as [Step 8A](alert-notification-engine-v1.md#1-safety-boundary)
already requires for browser connectivity generally.

## 25. Proposed future Go packages, tables, routes, and surfaces

Everything in this section is a **proposal for a separately authorized Step
8D-B**. None of it is created by Step 8D-A.

### 25.1 Go packages (under `backend/internal/`)

| Package | Purpose |
| --- | --- |
| `notifydevices` | Device/subscription registration, validation, lifecycle (replace/revoke), tied to authenticated identity. |
| `notifyoutbox` | Bounded-retry, backoff, idempotency, expiration, cancellation, dead-letter state machine consuming `alert_events`. |
| `notifyrelay` | Server-only provider REST client (already named as a proposed package in [Step 8A's naming-collision check](alert-notification-engine-v1.md#conflicts-found-during-review)); holds provider credentials exclusively. |
| `notifypreferences` | Server-side preference and quiet-hours evaluation against a user's current, non-cached settings. |
| `notifyaudit` | Redacted delivery-audit recording (`notification_deliveries` writes only, no secrets). |

None of these packages exists today; none is registered by
`backend/cmd/api` today or by this amendment.

### 25.2 Database tables (next free migration version: `000008`)

| Table | Purpose | Notes |
| --- | --- | --- |
| `notification_devices` | One row per registered device/subscription, owned by an authenticated user | Stores the Push API `endpoint`/`keys` (not a provider credential), platform hint, `test_mode`, `revoked_at`, `superseded_by`. |
| `notification_consents` | Append-only consent/revocation events per user/device | Mirrors the `transcript_reviews` "supersede, never delete" convention. |
| `alert_preferences` | Already named in [Step 8A Section 9](alert-notification-engine-v1.md#9-storage-and-retention); gated there on an approved auth contract | This amendment adds no new field beyond what Section 7 of this document already lists (user, role, talkgroup, tone-set, keyword-list, quiet-hours, enable/disable). |
| `notification_outbox` | One row per queued/attempted delivery per `(alert_events.event_id, notification_devices.id)` | Carries attempt count, next-attempt time, idempotency key, and terminal state (`sent`, `expired`, `canceled`, `dead_letter`). |
| `notification_deliveries` | Already named in [Step 8A Section 9](alert-notification-engine-v1.md#9-storage-and-retention) | Terminal, append-only audit record per [Section 17](#17-delivery-audit) of this document. |

No migration file is created by Step 8D-A. `000008` is recorded here only
because it is the next unused version number as of this amendment's
baseline; it does not reserve or commit to that number for whatever Step
8D-B or Step 9 actually implements first.

### 25.3 API routes (Go backend, all requiring Step 9 authentication)

| Route | Method | Purpose |
| --- | --- | --- |
| `/api/notifications/devices` | `POST` | Register a device/subscription for the authenticated user. |
| `/api/notifications/devices/{id}` | `DELETE` | Revoke a specific device registration. |
| `/api/notifications/preferences` | `GET`/`PUT` | Read/update the authenticated user's own preferences. |
| `/api/notifications/consent` | `POST`/`DELETE` | Record or revoke explicit consent. |

No public, unauthenticated, or configuration-mutation route is proposed,
consistent with [Step 8A Section 10](alert-notification-engine-v1.md#10-administration)'s
"no public configuration-mutation endpoint is authorized."

### 25.4 Angular/PWA surfaces

| Surface | Purpose |
| --- | --- |
| `web/src/app/mobile/mobile-settings/` (existing, proposed by [mobile-phone-experience-v1](mobile-phone-experience-v1.md#exact-proposed-component-and-file-surface)) | Extended, not replaced, with a notification enable/disable and consent control. |
| `web/src/app/notifications/notification-preferences/` (new, proposed) | Talkgroup/tone-set/keyword-list/quiet-hours preference editor, authenticated-only. |
| `SwPush` usage inside the above components | The only push-related client code; no new service-worker file. |

### 25.5 Environment variables (Go backend only, `GFR_*` convention)

| Variable | Purpose |
| --- | --- |
| `GFR_NOTIFY_ENV` | Selects which credential set (`dev`/`preview`/`production`) applies, per [Section 20](#20-separate-provider-environments). |
| `GFR_NOTIFY_PROVIDER_APP_ID` | Server-side provider application identifier for the selected environment. |
| `GFR_NOTIFY_PROVIDER_REST_API_KEY` | Server-side provider REST credential. Never logged, never defaulted. |
| `GFR_NOTIFY_VAPID_PUBLIC_KEY` / `GFR_NOTIFY_VAPID_PRIVATE_KEY` | Application-server key pair for native Push API subscriptions ([Section 4.1](#41-design-summary)); only the public half is ever served to the client. |
| `GFR_NOTIFY_RELAY_ENABLED` | Explicit boolean gate; defaults to disabled. Required by the [prerequisite order](#26-prerequisite-order)'s "disabled synthetic relay implementation" step. |
| `GFR_NOTIFY_OUTBOX_MAX_ATTEMPTS` | Bounded retry ceiling ([Section 10](#10-outboxrelay-model)). |
| `GFR_NOTIFY_OUTBOX_BACKOFF_BASE` / `GFR_NOTIFY_OUTBOX_BACKOFF_MAX` | Backoff bounds ([Section 10](#10-outboxrelay-model)). |
| `GFR_NOTIFY_RATE_LIMIT_PER_USER` / `GFR_NOTIFY_RATE_LIMIT_PER_DEVICE` | Rate-limit bounds ([Section 18](#18-rate-limits-abuse-controls-and-observability)). |

No dependency is added, no file above is created, and no environment
variable above is read by any running code as part of Step 8D-A.

## 26. Prerequisite order

1. **Step 8D-A** — this contract amendment. Documentation only.
2. **Step 9** — authentication/authorization contract, approved and
   **implemented** (not merely documented), per [Section 3](#3-authentication-prerequisite).
3. **Notification preference and device-registration schema** — a future
   migration for [`notification_devices`, `notification_consents`,
   `alert_preferences`, `notification_outbox`, and `notification_deliveries`](#252-database-tables-next-free-migration-version-000008),
   gated on step 2.
4. **Disabled synthetic relay implementation** — Step 8D-B code exists but
   is inert by default (`GFR_NOTIFY_RELAY_ENABLED` false), reachable only
   through synthetic test mode ([Section 19](#19-synthetic-test-mode)).
5. **Isolated provider sandbox testing** — development/preview credentials
   only ([Section 20](#20-separate-provider-environments)), opted-in
   testers only.
6. **Explicit approval before any real-user delivery** — a separate,
   future, explicitly named authorization milestone (not created, named, or
   scheduled by this document) before `GFR_NOTIFY_RELAY_ENABLED` may ever be
   true against the production provider application for a non-test-mode
   user.

Each step requires its own separate authorization to begin, exactly as
[Step 8A's phased plan](alert-notification-engine-v1.md#12-phased-implementation-plan)
already requires for Steps 8B–8F.

## 27. Acceptance criteria

All fixtures for every future phase below must be synthetic, mirroring
[Step 8A's acceptance-test conventions](alert-notification-engine-v1.md#13-acceptance-tests).
No test may require a real provider account, a real user, or live radio.

| ID | Case | Required pass condition |
| --- | --- | --- |
| NRA-01 | No implementation in Step 8D-A | `git status --short` and `git diff` show only `docs/notification-relay-amendment-v1.md` added; no code, schema, config, or dependency file changed. |
| NRA-02 | Clause superseding is narrow | A diff/text comparison of [Section 1](#1-clauses-superseded) against the two source contracts confirms only the four named terms are superseded; every other prohibition (including every authentication-related clause) is unchanged in the source documents and unmentioned as superseded here. |
| NRA-03 | Step 9 gate enforced | No Step 8D-B code path can construct a `notifydevices`, `notifyoutbox`, or `notifyrelay` value without an authenticated, server-verified identity; verified by dependency/API-surface inspection once those packages exist. |
| NRA-04 | Server-only credentials | No provider REST API key, app ID used for server calls, or webhook secret appears in any Angular/PWA source file, build artifact, or browser-reachable network response; verified by source and build-output scanning, extending [Step 8A's ANE-11](alert-notification-engine-v1.md#13-acceptance-tests). |
| NRA-05 | No provider identifier in the browser | Source scanning confirms the built PWA never references a provider app ID, SDK, or service-worker file of any kind. |
| NRA-06 | Evidence-state gating | Only `alert_events` rows with `state = 'matched'` ever produce a `notification_outbox` entry; a synthetic fixture set covering `ambiguous`, `partial`, `low_confidence`, and cooldown-suppressed duplicates produces zero outbox entries. |
| NRA-07 | Payload minimization | No notification payload, outbox row, or delivery-audit row contains transcript text, a file path, a credential, raw audio, a dataset ID, or unrestricted metadata; verified by an automated field-name and content scan, extending [Step 8A's ANE-10](alert-notification-engine-v1.md#13-acceptance-tests). |
| NRA-08 | `NOT LIVE CAD` in notification text | Every synthetic test notification's title/body includes the exact phrase `NOT LIVE CAD`. |
| NRA-09 | Deep-link re-authorization | A synthetic test confirms a notification click re-checks authentication/authorization server-side before rendering evidence content, and that an expired/revoked session redirects to the Step 9 auth flow instead. |
| NRA-10 | No authenticated data in service-worker cache | Generated `ngsw.json` and `ngsw-config.json` list no `dataGroups`, no `/api/notifications/*` URL, and no subscription/credential resource, extending [Step 8A's PWA-11/PWA-12 precedent](mobile-pwa-beta-v1.md#required-step-7b-verification). |
| NRA-11 | Idempotent, bounded outbox | A simulated provider timeout/5xx retries per the configured bounded policy and never produces two delivered notifications for the same `(event_id, device_id)` pair. |
| NRA-12 | Dead-letter visibility | An outbox entry that exhausts retries reaches a `dead_letter` state visible to observability/administration, never retried indefinitely. |
| NRA-13 | Environment isolation | Development/preview credentials cannot address the production provider application; verified by configuration inspection across all three environments. |
| NRA-14 | Synthetic test mode cannot reach real users | Every automated test and manual verification step explicitly confirms no notification reached a device outside an opted-in synthetic test audience, extending [Step 8A's ANE-15](alert-notification-engine-v1.md#13-acceptance-tests) unchanged. |
| NRA-15 | Revocation stops delivery | A synthetic test confirms a revoked device or disabled account produces zero further deliveries, including for entries already queued in the outbox. |
| NRA-16 | Cross-user registration rejected | A synthetic test confirms registering a subscription under a different user's identity than the authenticated caller is rejected and audited, never silently accepted or reassigned. |
| NRA-17 | No CAD/incident/unit-status mutation | End-to-end synthetic pipeline tests assert zero writes to `radio_transmissions`, `unit_status_decisions`, or any incident table caused by any notification-relay code, extending [Step 8A's ANE-14](alert-notification-engine-v1.md#13-acceptance-tests). |
| NRA-18 | iOS/Android platform behavior documented | A synthetic device test documents exact supported iOS/iPadOS and Android Chrome behavior, including permission-denial and revoked-permission handling, with no behavior assumed equal across platforms. |

## 28. Threat model

| Threat | Description | Required mitigation |
| --- | --- | --- |
| Stolen subscription object | A leaked Push API `endpoint`/`keys` value lets an attacker who also compromises the server (or a request in transit) address that browser's push channel. | HTTPS-only transport (already required by [mobile-pwa-beta-v1](mobile-pwa-beta-v1.md#https-beta-url)); the subscription object alone, without server-side credentials, cannot send a notification — only the Go backend's provider credential can, so a leaked subscription object is not sufficient to deliver anything by itself. |
| Forged device registration | An attacker submits a fabricated or replayed subscription object claiming to belong to another user. | Registration is bound to server-verified authenticated identity ([Section 3](#3-authentication-prerequisite), [Section 6](#6-device-and-subscription-registration)); the server never trusts a client-supplied user identifier. |
| Cross-user delivery | A bug or race condition delivers user A's alert to user B's device. | Delivery always joins through the device's own `notification_devices.user_id` at send time, re-checked per [Section 9](#9-per-userper-device-authorization-at-delivery), never cached from registration time; [NRA-16](#27-acceptance-criteria) tests the registration half explicitly. |
| Replay | An attacker replays a previously valid outbox/delivery request to cause duplicate or stale delivery. | Idempotency keys ([Section 10](#10-outboxrelay-model)) and `expires_at` enforcement ([Section 11](#11-evidence-state-gating)) prevent a replayed request from producing a second delivery or a late delivery of a stale event. |
| Privilege escalation | A user attempts to register for, or read preferences of, talkgroups/roles beyond their authorization. | Server-side role/preference enforcement at every delivery ([Section 7](#7-server-side-preference-enforcement), [Section 9](#9-per-userper-device-authorization-at-delivery)); no client-supplied scope is ever trusted. |
| Leaked provider credentials | A REST API key or webhook secret is exposed (misconfiguration, log leak, compromised host). | Server-only storage via `GFR_*` env vars ([Section 5](#5-credential-isolation)); redacted logging ([Section 18](#18-rate-limits-abuse-controls-and-observability)); separate per-environment credentials ([Section 20](#20-separate-provider-environments)) bound the blast radius of any one leak to that environment's app only. |
| Notification flooding | A misconfigured tone/keyword list, or a malicious/bugged caller, sends excessive notifications to one or many devices. | Per-user and per-device rate limits ([Section 18](#18-rate-limits-abuse-controls-and-observability)); cooldown/dedup at both the `alert_events` layer ([Step 8A Section 6](alert-notification-engine-v1.md#6-deduplication-and-delay)) and the outbox layer ([Section 10](#10-outboxrelay-model)). |
| Stale PWA caches | An old cached service-worker shell serves outdated notification-settings UI or misrepresents current consent/subscription state. | The existing stale-preview indicator and explicit-reload contract from [mobile-pwa-beta-v1](mobile-pwa-beta-v1.md#update-and-stale-version-handling) applies unchanged; preferences/consent are never cached client-side as the source of truth — every settings load is a fresh authenticated API call. |
| Malicious deep links | A crafted URL mimicking a notification deep link attempts to reach evidence content or trigger an action without a real notification. | The deep-link target itself re-authorizes server-side on every load ([Section 16](#16-push-click-re-authorization)), so a forged link grants nothing beyond what the visitor's own session already permits. |
| Lock-screen disclosure | Notification text visible to anyone near a locked device discloses sensitive information. | Minimal, non-sensitive, sanitized text only ([Section 12](#12-notification-text-and-lock-screen-minimalism)); the same exclusions already enforced on `display_summary` by [Step 8A Section 5](alert-notification-engine-v1.md#5-alert-event-contract) and the `alert_events` CHECK constraint in [migration `000007`](../database/migrations/000007_alert_persistence.sql). |

## 29. Unresolved decisions

None of these block this amendment, but each must be resolved before the
implementation step that depends on it proceeds. Several restate open
questions already recorded by [Step 8A](alert-notification-engine-v1.md#14-unresolved-questions)
and [mobile-phone-experience-v1](mobile-phone-experience-v1.md#unresolved-questions)
that this amendment does not attempt to close.

1. **Provider ownership**: who owns the OneSignal (or successor) account,
   and under what organizational or billing arrangement — restated from
   [Step 8A's unresolved question 5](alert-notification-engine-v1.md#14-unresolved-questions),
   still unresolved.
2. **Cost**: what per-notification or per-subscriber cost applies across
   development, preview, and production provider applications, and who
   bears it.
3. **Retention**: how long `notification_devices`, `notification_outbox`,
   and `notification_deliveries` rows are retained, and whether retention
   differs from `detection_audit`/`alert_events` retention (itself already
   unresolved per [Step 8A's unresolved question 4](alert-notification-engine-v1.md#14-unresolved-questions)).
4. **Authentication provider**: which identity provider and server-side
   authorization model Step 9 will use — restated from
   [Step 8A's unresolved question 6](alert-notification-engine-v1.md#14-unresolved-questions)
   and [mobile-phone-experience-v1's unresolved question 5](mobile-phone-experience-v1.md#unresolved-questions),
   still unresolved; not selected or implied here.
5. **Roles**: what role model (if any) governs eligibility beyond
   "authenticated user," and how roles map to talkgroup/tone-set/keyword-
   list eligibility.
6. **Eligible recipients**: which authenticated users are actually eligible
   to register for notifications at all — every authenticated user, or a
   restricted subset (for example, confirmed department volunteers)?
7. **Monitored channels**: which talkgroups are actually monitored for
   notification purposes in the first real deployment — restated from
   [Step 8A's unresolved question 2](alert-notification-engine-v1.md#14-unresolved-questions),
   still unresolved.
8. **Alert states eligible for delivery**: whether any state beyond
   `matched` (per [Section 11](#11-evidence-state-gating)) is ever approved
   for delivery, and under what labeling if so.
9. **Cooldowns**: the specific cooldown/expiration values for tone versus
   keyword alerts — restated from
   [Step 8A's unresolved question 3](alert-notification-engine-v1.md#14-unresolved-questions),
   still unresolved.
10. **Quiet hours**: exact default window (if any), per-user configurability
    bounds, and whether quiet-hours behavior suppresses or queues a
    matching alert.
11. **Wording**: approved notification title/body wording beyond the
    safety-required `NOT LIVE CAD` phrase — restated from
    [Step 8A's unresolved question 7](alert-notification-engine-v1.md#14-unresolved-questions),
    still unresolved.
12. **Escalation behavior**: whether an undelivered or dead-lettered
    critical alert ever escalates (for example, to a different channel or a
    supervisor), or whether dead-letter is always terminal with no
    escalation. Not designed or implied by this amendment.

## Step 8D-A validation checklist

- [ ] Only `docs/notification-relay-amendment-v1.md` is added.
- [ ] Baseline, clean-tree, and `origin/main` equality at `5d46b70` are
      recorded.
- [ ] `docs/alert-notification-engine-v1.md`,
      `docs/mobile-pwa-beta-v1.md`, `docs/mobile-phone-experience-v1.md`,
      the Step 8B/8C alert packages, migration `000007`, `web/ngsw-config.json`,
      and `web/public/manifest.webmanifest` are read and not edited.
- [ ] Exactly four clauses are identified and superseded, additively; every
      other prohibition in the two mobile contracts — including every
      authentication-related clause — remains unedited and unamended.
- [ ] This document explicitly does not authorize Step 8D-B.
- [ ] Notification implementation remains prohibited until Step 9
      authentication/authorization exists and is implemented.
- [ ] Server-side relay architecture, credential isolation, registration,
      preferences, consent/revocation, device lifecycle, per-delivery
      authorization, outbox model, evidence-state gating, payload
      minimization, deep links, service-worker caching limits, push-click
      re-authorization, delivery audit, rate limiting, synthetic test mode,
      and environment separation are all defined without authorizing
      implementation.
- [ ] iOS/Android platform limitations and web-push sound limitations are
      documented, not assumed.
- [ ] `NOT LIVE CAD`, evidence-only, and no-operational-authority warnings
      are preserved and extended to notification surfaces.
- [ ] Delivery-success-is-not-proof language is explicit.
- [ ] Prerequisite order, proposed files/routes/schema/env vars, acceptance
      criteria (`NRA-01`+), threat model, and unresolved decisions are all
      present.
- [ ] No implementation file, schema migration, dependency, provider
      account, credential, staging, commit, push, or deploy occurs.
