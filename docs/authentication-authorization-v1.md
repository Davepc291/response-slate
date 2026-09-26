# Authentication and Authorization Contract v1 — Step 9A

Status: Approved design contract for a future phased implementation. No code,
schema, dependency, provider account, credential, or environment variable is
authorized by this document.

Implementation status: Not started. Step 9A creates this document only.

Contract identifier: `authentication-authorization-v1`.

Reviewed baseline: `c53878a717d1359776fe42701ce32bed70e5a8d1` (`c53878a`). The
working tree was clean, and HEAD and local `origin/main` matched that commit
when this contract was created. Green CI is supplied baseline context, not a
claim of operational safety.

## Purpose and milestone boundary

Several existing approved contracts name a future "Step 9" authentication and
authorization contract as their own explicit prerequisite, without resolving
it:

- [Mobile phone experience contract v1](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype)
  requires "a later contract naming an approved identity provider, threat
  model, server-side session or token validation, server-side authorization
  rules, logout/revocation behavior, audit requirements, and private-data
  boundary" before any real authentication replaces its welcome-screen
  prototype, and explicitly forbids "a local-only substitute."
- [Alert and notification engine contract v1 § 8](alert-notification-engine-v1.md#8-user-preferences)
  gates authenticated per-user notification preferences on "real
  authentication and server-side authorization" having "their own separately
  approved contracts."
- [Notification relay amendment v1 § 3](notification-relay-amendment-v1.md#3-authentication-prerequisite)
  names this document "Step 9" by placeholder, states that "no notification
  implementation of any kind" is authorized until it "is approved and
  implemented," and lists the minimum coverage it must provide.

Step 9A is that contract. It defines an account model, first-time-login flow,
login-screen behavior, password/recovery policy, MFA/passkey posture, roles
and authorization rules, an administrator user-management flow, session
handling, protected-call-information rules, an audit-event catalog, a
proposed technical surface, and the interaction with the still-blocked Step
8D notification relay.

This document approves a design. It does not implement authentication,
modify the login screen, modify any existing file or contract, create a
database migration, add a dependency, add an API endpoint, add an
environment variable, or implement notifications. Every phase named below
requires its own separate future authorization, exactly as every existing
Step 6, 7, and 8 contract in this repository already requires for its own
phases.

## Authoritative dependencies

- [Mobile phone experience contract v1](mobile-phone-experience-v1.md),
  especially
  [Welcome and login-screen prototype](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype),
  [Non-goals and prohibitions](mobile-phone-experience-v1.md#non-goals-and-prohibitions),
  [Security limitations](mobile-phone-experience-v1.md#security-limitations),
  and [Unresolved questions](mobile-phone-experience-v1.md#unresolved-questions).
- [Mobile/PWA beta contract v1](mobile-pwa-beta-v1.md), especially
  [Non-goals](mobile-pwa-beta-v1.md#non-goals),
  [`ngsw-config.json`](mobile-pwa-beta-v1.md#ngsw-configjson), and
  [Security limitations](mobile-pwa-beta-v1.md#security-limitations).
- [Alert and notification engine contract v1](alert-notification-engine-v1.md),
  especially
  [Section 8 (User preferences)](alert-notification-engine-v1.md#8-user-preferences)
  and [Section 10 (Administration)](alert-notification-engine-v1.md#10-administration).
- [Notification relay amendment v1](notification-relay-amendment-v1.md),
  especially
  [Section 3 (Authentication prerequisite)](notification-relay-amendment-v1.md#3-authentication-prerequisite),
  [Section 26 (Prerequisite order)](notification-relay-amendment-v1.md#26-prerequisite-order),
  and [Section 29 (Unresolved decisions)](notification-relay-amendment-v1.md#29-unresolved-decisions).
- [Product requirements](product-requirements.md), Security section (lines
  172-182: "define authentication and authorization before shared
  deployment") and the open question "Who may access the application,
  recordings, transcripts, and audit records? What authentication,
  authorization, network exposure, and transport policies apply?"
- [Current mobile welcome route](../web/src/app/mobile/mobile-welcome/mobile-welcome.html)
  and [component](../web/src/app/mobile/mobile-welcome/mobile-welcome.ts).
- [Current mobile settings route](../web/src/app/mobile/mobile-settings/mobile-settings.html)
  and [component](../web/src/app/mobile/mobile-settings/mobile-settings.ts).
- [Current mobile routes](../web/src/app/mobile/mobile.routes.ts) and
  [application routes](../web/src/app/app.routes.ts).
- [Database README](../database/README.md), migrations `000001`-`000007`,
  and their append-only/`ENABLE ALWAYS` audit convention.
- [Config package](../backend/internal/config/config.go), for the existing
  `GFR_*` environment-variable convention.

Historical approved contracts may still describe their own implementation as
not started. That wording is not a reason to edit them. This document
changes none of those files.

## Non-goals and prohibitions

Step 9A does not, and a future implementation phase built from it must not
begin without its own separate authorization to:

- Implement any authentication code, session code, password-hashing code, or
  authorization-check code.
- Modify `mobile-welcome.html`, `mobile-welcome.ts`, `mobile-settings.html`,
  `mobile-settings.ts`, or any other existing Angular, Go, or SQL file.
- Create a database migration file. The next unused migration version
  remains `000008`, reserved here only as a number, not as a commitment to
  its contents.
- Add an npm or Go dependency, an identity-provider SDK, or a password
  library.
- Add an HTTP API endpoint, route, or Go package.
- Add an environment variable to any running configuration.
- Implement any part of the Step 8D/8D-B notification relay, which
  [remains explicitly blocked](#12-interaction-with-notifications) on this
  contract's later implementation, not its approval.
- Stage, commit, push, or deploy anything.

## 1. Account model

### No public self-registration

There is no sign-up form and no public account-creation endpoint. Every
account is created by an administrator through the flow in
[Section 7](#7-admin-user-management-flow). A person who is not already
invited has no path to obtain access on their own.

### Identity and normalization

- Every account has a server-generated, immutable, opaque `user_id`
  (matching the generated-bigint-identity convention already used for
  `radio_transmissions` and `unit_status_decisions` in
  [migration `000001`](../database/README.md#schema)), never a
  client-supplied or email-derived identifier.
- Every account has exactly one **normalized email address** as its primary
  identifier: lowercased, Unicode-NFC-normalized, and validated against a
  standard email-address grammar. It is unique across all accounts,
  including disabled and expired ones — a normalized email is never reused
  by a second account, so audit history and revocation always resolve to
  exactly one identity, past or present.
- A separate, optional, administrator-assigned **display name** (for
  example, a first name and last initial, or a department call sign) may
  appear in UI and audit displays instead of the raw email address, but the
  normalized email remains the unique account key.
- No username/email change is self-service in this contract generation. An
  email correction is an administrator action, audited like any other
  administrative account change (see [Section 10](#10-audit-and-security-events)).

### Account states

Every account has exactly one of these states at any time:

| State | Meaning |
| --- | --- |
| `invited` | Administrator has created the account and issued a temporary credential; the user has not yet completed first-time login. |
| `password-change-required` | The user has redeemed the invitation (or an administrator-issued reset) but has not yet established a permanent password; the account cannot reach protected information until this state clears. |
| `active` | The user has a permanent password (and MFA enrolled, if required for the role) and may authenticate normally. |
| `suspended` | An administrator has temporarily blocked authentication and session use without discarding the account; reversible by an administrator restore action. |
| `disabled` | An administrator has permanently blocked authentication and session use; distinct from `suspended` in that this contract treats `disabled` as the terminal state for departed personnel, while `suspended` is the terminal state for temporary holds. Both block access identically; the distinction is administrative record-keeping, not a technical difference in enforcement. |
| `expired` | An `invited` or `password-change-required` account whose temporary credential's expiration passed before redemption or completion; the account cannot authenticate until an administrator resends or resets it, per [Section 2](#2-first-time-login). |

State transitions are administrator-driven except the automatic
`invited`/`password-change-required` → `expired` transition on timeout, and
the user-driven `invited` → `password-change-required` → `active`
progression during first-time login. No account reaches `active` without
passing through `password-change-required` at least once.

### Administrator distinguishability

Every account has an explicit **role** (see [Section 6](#6-roles-and-authorization)).
Administrator roles (`system administrator`, `department administrator`) are
a disjoint, clearly labeled subset of all roles; the account model never
infers administrator status from a naming convention, an email domain, or
any other implicit signal. Role is a first-class, audited field, changed
only through the flow in [Section 7](#7-admin-user-management-flow).

### Password storage

**Plaintext passwords are never stored, logged, or transmitted in any form
after the single request that sets them.** Only a salted, computationally
expensive password hash (see [Section 4](#4-password-and-recovery-policy))
is persisted. This applies identically to permanent passwords, temporary
credentials, and any administrator-set fallback value; there is no
"recoverable" or "administrator-visible" password anywhere in this design.

## 2. First-time login

1. An administrator creates the user through [Section 7](#7-admin-user-management-flow)'s
   flow, supplying at minimum a normalized email address, a display name,
   and a role.
2. The system generates a **single-use invitation**: a high-entropy,
   server-generated, random token, stored only as a hash (never in
   plaintext) with a short, configurable expiration window and a status of
   `pending`.
3. The temporary secret is delivered to the user through a channel outside
   this application (see [delivery](#delivery-of-the-invitation) below). It
   is never displayed again by the application after creation — not in the
   admin UI, not in an API response, not in a log — after the one moment the
   administrator triggers its creation and initial delivery.
4. The user follows the invitation to the login screen
   ([Section 3](#3-login-screen-behavior)) and authenticates using the
   invitation token. A successful redemption:
   - Immediately marks the invitation `redeemed` (single-use; a second
     redemption attempt with the same token fails and is audited).
   - Moves the account to `password-change-required`.
   - Forces the user through a **mandatory permanent-password
     establishment** step (and MFA enrollment, if the role requires it, per
     [Section 5](#5-mfapasskeys)) before any protected route, protected
     API response, or protected call information is reachable. There is no
     "skip for now" path.
5. Once a permanent password (and required MFA) is established, the account
   moves to `active` and normal login applies going forward.

### Temporary credentials cannot be reused

A redeemed, expired, or superseded (see resend/reset below) invitation token
is permanently invalid. The server validates a presented token against the
stored hash and current status (`pending` and unexpired) on every use; any
other status returns the same generic failure used for an unknown token, per
[account-enumeration resistance](#account-enumeration-resistance).

### Expiration behavior

If the invitation's expiration passes before redemption, the account
(if still `invited`) or the in-progress password establishment (if already
`password-change-required` but not yet completed) automatically transitions
to `expired`. An expired account cannot authenticate with the expired token.
Recovery requires an administrator resend or reset, per below; there is no
user-initiated "extend my own invitation" action.

### Administrator resend and reset behavior

- **Resend** (for an `invited` or `expired` account that never completed
  first-time login): an administrator action that invalidates any prior
  invitation token for that account and issues a new one with a fresh
  expiration, following the same single-use, hash-only-storage rule as
  initial issuance. The prior token is invalidated even if it was still
  technically unexpired, so only the newest issued invitation is ever valid.
- **Reset** (for an `active`, `suspended`, or `password-change-required`
  account that needs a new temporary credential, for example after a
  suspected compromise): an administrator action that immediately
  invalidates all of that account's existing sessions
  ([Section 8](#8-sessions)) and any outstanding password-reset token
  ([Section 4](#4-password-and-recovery-policy)), moves the account to
  `password-change-required`, and issues a new single-use temporary
  credential following the same rules as an invitation.
- Both actions are audited administrative events
  ([Section 10](#10-audit-and-security-events)) that record which
  administrator acted, when, and on which account, never the token value
  itself.

### Delivery of the invitation

**Permanent passwords are never sent through email or a notification.**
Only the initial single-use invitation token (or, for a reset, the new
single-use credential) is delivered outside the application, and only that
one time. This contract does not select a specific delivery channel or
provider — see [Unresolved questions](#15-unresolved-questions) — but
whatever channel is chosen must treat the token with the same sensitivity
as a password: no channel that indefinitely retains a plaintext copy the
user can revisit (for example, a channel with permanent unencrypted message
history that the application does not control) is an acceptable default
without an explicit, separately reviewed decision.

## 3. Login screen behavior

### Milestone boundary

[Mobile phone experience contract v1](mobile-phone-experience-v1.md#welcome-and-login-screen-prototype)
already establishes that `/mobile/welcome` is "a welcome/login-screen design
prototype only" and "not authentication," with "no username, email,
password, PIN, passkey, biometric, one-time-code, or security-question
inputs." **This contract does not change that file.** The welcome screen
becomes a real login screen only during a later, separately authorized
implementation phase that explicitly supersedes the current prototype
prohibitions, exactly as
[notification-relay-amendment-v1](notification-relay-amendment-v1.md#1-clauses-superseded)
superseded four narrow notification-related clauses without touching any
other prohibition. This section proposes that future screen's exact text
and behavior; it creates none of it now.

### Proposed exact screen text and controls

**Amendment (2026-09-25):** the production login screen at `/mobile/auth/**`
does **not** render the `SHADOW / REPLAY — NOT LIVE CAD` /
`Synthetic replay preview. No operational authority.` warnings, and does not
carry a second, duplicate product-identity mark in its header — the original
proposal below is superseded on those two points only. Rationale: those
warnings and the doubled branding described the non-authenticating
`/mobile/welcome` prototype and the still-synthetic `/preview` and `/mobile`
board/mobile experiences; once `/mobile/auth/**` performs real authentication
against the live API, continuing to badge that specific screen as shadow/
replay risked a user reading "NOT LIVE CAD" on their actual sign-in screen and
concluding the credential check itself was fake. The warnings are otherwise
unchanged and **must** continue to appear, exactly as specified, on
`/preview` ([BoardPreview](../web/src/app/board-preview)) and on every
`/mobile/**` synthetic-preview route served by
[MobileShell](../web/src/app/mobile/mobile-shell) — this amendment narrows
only the production auth shell
([MobileAuthShell](../web/src/app/mobile-auth/mobile-auth-shell)), exactly as
[notification-relay-amendment-v1](notification-relay-amendment-v1.md#1-clauses-superseded)
superseded four narrow clauses without touching any other prohibition.

| Element | Proposed exact text / control |
| --- | --- |
| Product identity | `Greenwich Fire Responder V3` (shown once, via the large sign-in logo — not repeated in a header) |
| Warning one | Not shown on `/mobile/auth/**` (see amendment above); unchanged elsewhere: `SHADOW / REPLAY — NOT LIVE CAD` |
| Warning two | Not shown on `/mobile/auth/**` (see amendment above); unchanged elsewhere: `Synthetic replay preview. No operational authority.` |
| Heading | `Sign in` |
| Username/email field label | `Email` — a single labeled text input, `type="email"`, `autocomplete="username"`, no placeholder-only label. |
| Password field label | `Password` — `type="password"` by default, `autocomplete="current-password"`. |
| Show-password control | A toggle button labeled `Show password` / `Hide password` (icon plus visible text or an accessible name matching the visible text) that toggles the field's `type` between `password` and `text`; it never logs, stores, or transmits the revealed value differently than the hidden state. |
| Sign-in control | A primary button labeled `Sign in`, disabled while a request is in flight, replaced by a labeled loading state (`Signing in…`) rather than a spinner with no text. |
| Forgot-password control | A text link labeled `Forgot password?` beneath the password field, leading to a dedicated request screen (not an inline reveal), per [Section 4](#4-password-and-recovery-policy). |
| Generic error region | A single, non-field-specific error message region above the form, announced via an ARIA live region, used for every authentication failure per [account-enumeration resistance](#account-enumeration-resistance) below. |

The form is a real `<form>` with real `autocomplete` hints and no
credential-manager-blocking attributes, so browser and OS password managers
work normally — the opposite of the current prototype's explicit prohibition
on `<form>` and autofill hints, which applies only to the non-authenticating
prototype.

### Account-enumeration resistance

Every failure that could otherwise distinguish "no such account,"
"account exists but wrong password," "account not yet activated," and
"account locked" **must render the same generic message and take
approximately the same server time to respond**, with two narrow,
deliberate exceptions defined below (expired-invite and suspended-account
states) that are unavoidable because the user has just followed a link
naming their own invitation:

- **Default failure text**: `That email or password is incorrect.` for any
  combination of unknown email, correct email with wrong password, or
  wrong email with any password. The system never reveals whether the
  email portion matched a real account.
- **Timing**: a failed lookup for a non-existent account performs an
  equivalent-cost dummy hash comparison so response time does not leak
  account existence, matching the timing-safe-compare principle already
  implied by [password hashing requirements](#4-password-and-recovery-policy).
- **Forgot-password flow**: always responds with the same
  `If that email has an account, a reset link has been sent.` message
  whether or not the email is registered, per
  [Section 4](#4-password-and-recovery-policy).

### Defined states

| State | Required presentation |
| --- | --- |
| Loading (initial screen render, or a request in flight) | `Signing in…` on the button; no skeleton that implies cached protected data. (The SHADOW/REPLAY and synthetic-preview warnings do not appear on this screen at all — see the 2026-09-25 amendment above — so there is nothing to keep visible.) |
| Offline | `Offline. Sign-in requires a network connection. NOT LIVE CAD.`; the sign-in control is disabled rather than allowed to fail silently. |
| Locked (rate-limited; see [Section 4](#4-password-and-recovery-policy)) | The same generic failure text as any other failure, plus a non-specific `Too many attempts. Try again later.` that does not reveal the exact lockout duration or whether the account itself exists, distinct from but adjacent to the enumeration-resistant default. |
| Expired invite (user follows an expired invitation link) | `This invitation has expired. Ask your administrator to resend it.` — this is a deliberate, narrow exception to generic error text because the user already possesses a link naming their own pending account; it reveals nothing about any other account. |
| Suspended account | `This account is suspended. Contact your administrator.` — the same narrow exception rationale: the user has already authenticated with a correct password, so confirming account state to that specific person leaks nothing to an outside attacker who does not have the password. |
| Service unavailable (backend/database unreachable) | `Sign-in is temporarily unavailable. NOT LIVE CAD.` — never a raw error, stack trace, or database message. |

The `locked`, `expired invite`, and `suspended account` distinctions are
made **after** a correct password has already been supplied where relevant
(suspended) or from an invitation-specific token the user already holds
(expired invite); they never leak information to a caller who has supplied
only a guessed email address.

### Direct protected routes

Every direct navigation to a protected route (any route requiring
`active` status and role authorization) must be **rejected server-side**
before any protected content, protected API response, or protected data is
returned — a client-side route guard is UX only, never the authorization
boundary. This restates and narrows, for a real authentication phase, the
same principle the mobile phone experience contract already states about
its own non-authenticating prototype: "hidden links... would not fix that
limitation," extended here to mean a real implementation's Angular route
guard is equally insufficient without the matching server check on every
request.

## 4. Password and recovery policy

### Password requirements

Following modern guidance (for example, NIST SP 800-63B) rather than
arbitrary forced complexity:

- **Minimum length**: a configurable minimum, proposed at 12 characters,
  with no forced uppercase/lowercase/digit/symbol composition rule.
- **Maximum length**: a generous maximum (proposed: at least 256 characters)
  so passphrases are not penalized.
- **Breached-password checking**: a submitted password is checked against a
  known-breached-password corpus (for example, a k-anonymity range query
  against a breach corpus, so the full password or its full hash is never
  sent to a third party) at both invitation-redemption and password-change
  time; a breached match is rejected with a specific, safe message
  (`This password has appeared in a data breach. Choose a different one.`)
  distinct from the generic sign-in failure text, because the user is
  already authenticated to the account at that point.
- **No forced periodic rotation** absent a specific compromise signal;
  forced rotation is a known anti-pattern that encourages weaker,
  incrementally-varied passwords.
- **No password hints, security questions, or knowledge-based recovery.**

### Hashing requirements

- A memory-hard, deliberately slow algorithm (Argon2id, or bcrypt/scrypt if
  a specific platform constraint requires it) with a per-password random
  salt and a configurable work factor reviewed periodically as hardware
  improves.
- Hashing parameters (algorithm identifier, salt, work factor) are stored
  alongside each hash so the work factor can be upgraded for future logins
  without forcing an immediate mass reset (rehash-on-next-successful-login).

### Password reset

- A reset request always responds with the same
  `If that email has an account, a reset link has been sent.` message
  regardless of whether the email matches an account, per
  [account-enumeration resistance](#account-enumeration-resistance).
- The reset token is **short-lived** (a small number of minutes to a few
  hours, configurable — see [Unresolved questions](#15-unresolved-questions)
  for the exact value) and **single-use**, stored only as a hash, following
  the same pattern as the invitation token in
  [Section 2](#2-first-time-login).
- Successful password reset **immediately revokes every existing session**
  for that account ([Section 8](#8-sessions)), not only the session that
  requested the reset, on the assumption that a reset may follow a
  suspected compromise.
- An administrator-initiated reset (for example, on a report of a lost or
  compromised device) follows the same session-revocation rule and the same
  single-use, hash-only token issuance as [Section 2](#2-first-time-login)'s
  reset action.

### Throttling, lockout, and rate limiting

- Failed sign-in attempts are rate-limited **per account** and **per source
  IP/network**, with bounded, configurable thresholds and a backoff window,
  never an unbounded lockout that becomes its own denial-of-service vector
  against a known email address.
- Rate-limit state itself must not be usable to enumerate accounts: the
  generic `locked` message in [Section 3](#3-login-screen-behavior) applies
  identically whether or not the targeted email exists.
- Every rate-limit trigger is an audited security event
  ([Section 10](#10-audit-and-security-events)).

### Logging prohibition

**Passwords, password-reset tokens, invitation tokens, and session tokens
must never be logged**, in any log level, error message, audit row, or
diagnostic output, matching the existing repository-wide convention that
`GFR_DATABASE_URL` and other secrets never reach a log line
([config.go](../backend/internal/config/config.go)) and the notification
relay amendment's identical rule for push subscription endpoints
([notification-relay-amendment-v1 § 18](notification-relay-amendment-v1.md#18-rate-limits-abuse-controls-and-observability)).
A logged authentication event records outcome, account identifier, and
safe metadata only — never the credential or token value itself, whether
it succeeded or failed.

## 5. MFA/passkeys

- **Administrators (`system administrator`, `department administrator`)
  must enroll strong MFA** before their account can leave
  `password-change-required`, and cannot access any administrative function
  ([Section 6](#6-roles-and-authorization)) without an MFA-verified current
  session.
- **Recommended method**: passkeys/WebAuthn (platform or roaming
  authenticator) as the preferred, phishing-resistant option; a TOTP
  authenticator app is an acceptable fallback where passkey hardware is
  unavailable. SMS-based one-time codes are discouraged as a primary
  factor because they are not phishing-resistant, and are not proposed
  here as an administrator option.
- **Normal users** (`dispatcher/operator`, `responder`,
  `read-only/auditor`): whether MFA is required or merely offered is an
  [unresolved question](#15-unresolved-questions). This contract does not
  default normal-user MFA to either mandatory or optional; that decision
  needs department input on device availability and volunteer workflow
  before a future implementation phase.
- **Recovery methods must not silently bypass MFA.** A lost-authenticator
  recovery path (for example, an administrator-issued reset) must still
  require the administrator to re-verify the user's identity out of band
  and must re-enroll MFA before the account leaves
  `password-change-required` again for an MFA-required role; recovery is
  never a quiet fallback to password-only access for an account whose role
  requires MFA.
- **Provider selection is left unresolved.** No existing contract names an
  identity provider, WebAuthn relying-party library, or TOTP library; this
  document does not select one. See
  [Unresolved questions](#15-unresolved-questions).

## 6. Roles and authorization

### Proposed least-privilege roles

| Role | Summary |
| --- | --- |
| `system administrator` | Full account and role management across the deployment, including managing other administrators; the smallest role that may create a `system administrator`. |
| `department administrator` | Manages users and roles within their own department/scope; cannot elevate anyone to `system administrator`. |
| `dispatcher/operator` | Operational read access to protected call information, unit status, and evidence within their assigned scope; cannot manage users. |
| `responder` | Read access scoped to what an individual responder needs (their own assignments/notifications); narrower than `dispatcher/operator`. |
| `read-only/auditor` | Read-only access to protected call information and audit history for oversight purposes; cannot mutate anything, including their own notification-device state on another user's behalf. |

Exact department-scope boundaries and whether additional roles are needed
are [unresolved](#15-unresolved-questions); the table above is a proposed
starting set, not a final enumeration.

### Permission matrix (proposed)

| Operation | `system administrator` | `department administrator` | `dispatcher/operator` | `responder` | `read-only/auditor` |
| --- | --- | --- | --- | --- | --- |
| View protected call information | Yes | Yes (own scope) | Yes (own scope) | Yes (own assignments) | Yes (read-only) |
| View unit status | Yes | Yes (own scope) | Yes (own scope) | Yes (own scope) | Yes (read-only) |
| View transcripts/evidence | Yes | Yes (own scope) | Yes (own scope) | Limited/no | Yes (read-only) |
| Manage users (create/edit) | Yes | Yes (own scope) | No | No | No |
| Change roles | Yes (any role) | Yes (non-administrator roles, own scope) | No | No | No |
| Enable/disable accounts | Yes | Yes (own scope) | No | No | No |
| Reset invitations/password access | Yes | Yes (own scope) | No | No | No |
| Register notification devices | Yes (own device) | Yes (own device) | Yes (own device) | Yes (own device) | Yes (own device) |
| Change notification preferences | Yes (own) | Yes (own) | Yes (own) | Yes (own) | Yes (own) |
| Review audit history | Yes | Yes (own scope) | No | No | Yes |

"Own scope" is a placeholder for whatever department/talkgroup scoping model
a future phase defines; this contract does not define department boundaries
(see [Unresolved questions](#15-unresolved-questions)). Notification-device
registration and preference changes are always self-service for one's own
identity only, never on another user's behalf by any role in this
generation — an administrator disabling a user revokes that user's devices
([Section 12](#12-interaction-with-notifications)) but does not register or
configure devices for them.

### Authorization enforcement principle

**Every protected operation must be authorized by the backend on every
request. Hiding a button, route, or menu item in Angular is not
authorization** — it is UX guidance for an already-authorized user, exactly
as [Section 3](#3-login-screen-behavior) states for direct route access and
exactly as [mobile-phone-experience-v1](mobile-phone-experience-v1.md#security-limitations)
already states about its own non-authenticating prototype ("Client-side
route guards, hidden links... would not fix that limitation"). A future
implementation must reject an unauthorized request with a safe error
regardless of what the client UI displayed or attempted to hide.

## 7. Admin user-management flow

### Proposed complete flow

1. An administrator (role-checked server-side) opens **Users**.
2. Selects **Add user**.
3. Enters the approved minimum information: normalized email address,
   display name, and role (plus department/scope, once that model exists).
   No password field exists on this screen — the system generates the
   invitation, never an administrator-chosen password.
4. Confirms the role and permitted scope from a constrained selector (never
   free text), so a typo cannot silently grant an unintended role.
5. Selects **Create invitation**; the system creates the account in
   `invited` state and issues the single-use invitation token per
   [Section 2](#2-first-time-login).
6. The invitation is securely delivered outside the application (see
   [Section 2](#2-first-time-login)'s delivery note); the admin UI confirms
   only that an invitation was created, never displaying the token itself.
7. The user completes first-time login: redeems the invitation, establishes
   a permanent password, and enrolls MFA if their role requires it
   ([Section 5](#5-mfapasskeys)).
8. From that point, the administrator can **suspend**, **disable**,
   **restore**, or **revoke** the account at any time, each a distinct,
   audited action:
   - **Suspend**: temporary block, reversible by **Restore**.
   - **Disable**: permanent block for departed personnel; also reversible
     only by an explicit **Restore** action, but treated administratively
     as the "this person should not come back without a new decision"
     state.
   - **Restore**: returns a `suspended` or `disabled` account to `active`
     (if it already completed first-time login) or `invited`/`expired`
     handling otherwise; itself an audited action requiring the same role
     check as the original creation.
   - **Revoke**: immediately invalidates every session and notification
     device for the account ([Section 12](#12-interaction-with-notifications))
     without necessarily changing the account's enabled/disabled state — a
     narrower, faster action than disable, for "kill this person's current
     access right now, decide the account's fate after."

### Proposed screens, fields, and messages

| Screen | Fields | Validation messages (proposed) | Confirmation messages (proposed) |
| --- | --- | --- | --- |
| Users (list) | Search/filter by name, email, role, status | — | — |
| Add user | Email, display name, role, scope | `Enter a valid email address.` / `This email already has an account.` / `Choose a role.` | `Invitation created and sent to {email}.` |
| User detail | Email, display name, role, scope, status, last sign-in, MFA enrollment state | `Choose a role before saving.` | `Changes saved.` |
| Suspend user (dangerous) | Confirmation dialog naming the exact user | — | Dialog: `Suspend access for {display name} ({email})? They will not be able to sign in until restored.` Buttons: `Cancel` / `Suspend`. Post-action: `{display name} is suspended.` |
| Disable user (dangerous) | Confirmation dialog naming the exact user | — | Dialog: `Disable {display name} ({email})? This blocks sign-in and revokes all active sessions and notification devices.` Buttons: `Cancel` / `Disable`. Post-action: `{display name} is disabled.` |
| Restore user | Confirmation dialog naming the exact user | — | Dialog: `Restore access for {display name} ({email})?` Buttons: `Cancel` / `Restore`. Post-action: `{display name} is restored.` |
| Revoke sessions (dangerous) | Confirmation dialog naming the exact user | — | Dialog: `Sign {display name} out everywhere and revoke their notification devices? They will need to sign in again.` Buttons: `Cancel` / `Revoke`. Post-action: `All sessions and devices revoked for {display name}.` |
| Resend invitation | Confirmation dialog naming the exact user | — | Dialog: `Resend invitation to {email}? The previous invitation link will stop working.` Buttons: `Cancel` / `Resend`. Post-action: `A new invitation was sent to {email}.` |
| Reset password access | Confirmation dialog naming the exact user | — | Dialog: `Send a new sign-in credential to {display name} ({email})? This signs them out everywhere.` Buttons: `Cancel` / `Reset`. Post-action: `A new credential was sent to {email}. All sessions were revoked.` |

Every "dangerous" action (suspend, disable, revoke) requires an explicit
confirmation dialog naming the specific affected user by display name and
email, never a bare "Are you sure?" with no target named, so an
administrator cannot mis-click their way into the wrong account's
suspension.

## 8. Sessions

- **Cookies**: session identifiers are carried in a **secure, HttpOnly,
  SameSite** cookie by default. No authentication or session token is ever
  placed in `localStorage`, `sessionStorage`, or a non-HttpOnly cookie,
  unless a later implementation contract explicitly establishes a safer
  alternative for a specific documented reason (for example, a native-app
  bridge) — no such alternative is proposed here.
- **CSRF protection**: state-changing requests require a CSRF defense
  appropriate to a cookie-based session (for example, a double-submit
  token or `SameSite=Strict`/`Lax` combined with an explicit anti-CSRF
  header check on mutating routes); this contract does not select the
  exact mechanism, only requires that one exists before any mutating
  authenticated endpoint ships.
- **Expiration and idle timeout**: sessions have both an absolute maximum
  lifetime and an idle timeout that ends a session after a period of
  inactivity; exact values are an [unresolved question](#15-unresolved-questions).
- **Device/session listing and revocation**: an authenticated user can view
  their own active sessions/devices and revoke any one of them individually
  (for example, "sign out this device"), distinct from the administrator
  actions in [Section 7](#7-admin-user-management-flow).
- **Server-side authorization on every protected request**: restated from
  [Section 6](#6-roles-and-authorization) — a valid session proves identity,
  not authorization for a specific operation; every protected request
  re-checks role/scope server-side.
- **Logout**: ends the current session only; the client-side state is
  cleared and the server invalidates that session's server-side record
  (not merely lets the cookie expire client-side).
- **Logout of all devices**: an explicit, separate action available to the
  user (and, per [Section 7](#7-admin-user-management-flow), to an
  administrator on the user's behalf) that invalidates every session for
  that account at once.
- **Installed PWA sessions**: a session created inside the installed
  standalone PWA behaves identically to a browser-tab session for
  expiration, revocation, and CSRF purposes — there is no separate,
  longer-lived, or less-protected session class for standalone mode. Because
  standalone mode hides the browser chrome (already noted for the existing
  non-authenticating shell in
  [mobile-phone-experience-v1](mobile-phone-experience-v1.md#install-and-standalone-behavior)),
  a future implementation must ensure session-expiration and
  forced-re-authentication prompts remain visible and reachable without a
  URL bar, and that the installed app's own service worker never treats an
  authenticated route as long-term cacheable content (see
  [Section 9](#9-protected-call-information)).

## 9. Protected call information

- **Anonymous users must never receive protected call data.** Every
  endpoint serving protected call information, unit status, transcripts, or
  evidence requires a valid, non-expired, authorized session, checked
  server-side per [Section 6](#6-roles-and-authorization).
- **No protected/private call data in the Angular bundle or public fixture
  files.** The existing synthetic board fixtures
  (`board-preview.fixtures.ts`) remain public, synthetic, and unchanged;
  this contract does not authorize adding real call data to any bundled or
  statically served file. A future protected surface must be served only
  from an authenticated API response, never compiled into the frontend
  build.
- **Service workers must not cache protected API responses** unless a
  later, separately approved encrypted/offline-data contract explicitly
  allows it, extending the existing rule that
  [`ngsw-config.json` defines only an application-shell asset group with no
  `dataGroups`](mobile-pwa-beta-v1.md#ngsw-configjson) and the identical
  rule already stated for notification data in
  [notification-relay-amendment-v1 § 15](notification-relay-amendment-v1.md#15-service-worker-caching-prohibitions).
- **Notifications on locked devices use minimized/redacted content by
  default**, restating
  [notification-relay-amendment-v1 § 12](notification-relay-amendment-v1.md#12-notification-text-and-lock-screen-minimalism)
  unchanged: only a sanitized `display_summary` (or a further-truncated
  subset), never transcript text, appears in a lock-screen-visible
  notification.
- **Role/scope checks and audit records**: every view of protected call
  information is both authorized (per [Section 6](#6-roles-and-authorization)'s
  matrix) and recorded as a `protected_call_access` audit event
  ([Section 10](#10-audit-and-security-events)), so "who viewed which call
  and when" is reconstructible.

### Honest limitation: screenshots and photography

**A web/PWA application cannot reliably prevent a user from taking a
screenshot, screen-recording, or photographing the screen with a second
device.** No claim in this contract, or in any future implementation built
from it, may state or imply that protected call information is protected
against a determined viewer with a camera. Proposed deterrence and response
controls, none of which make photography or screenshotting impossible:

- A visible **user/time watermark** overlaid on protected call screens (for
  example, the signed-in user's display name and a timestamp), so a leaked
  photo or screenshot is traceable to who viewed it and when.
- A **privacy mode** toggle or automatic behavior (for example, blurring
  protected content when the app is not the foreground/focused window,
  where the platform exposes that signal) that a user can also invoke
  manually before others might view their screen.
- **Automatic hiding when backgrounded**: protected content is not visible
  in an OS app-switcher preview or a backgrounded browser tab thumbnail
  where the platform allows the application to suppress it.
- **Short sessions and idle timeout** ([Section 8](#8-sessions)) limit the
  window during which a stolen or borrowed device shows protected content.
- **Audit logging** ([Section 10](#10-audit-and-security-events)) creates a
  record of every protected-content view, supporting after-the-fact
  investigation rather than prevention.
- **Training and policy**: a non-technical control — department policy on
  acceptable use, device handling, and consequences for unauthorized
  disclosure — is necessary because no client-side technical control fully
  substitutes for it.
- **Managed-device controls**: where the department issues and manages
  devices (MDM), device-level policy (screen-lock enforcement, restricted
  screenshot capability where the platform supports it, remote wipe) is a
  complementary, separately scoped control, not a web-application feature.

**Native Android `FLAG_SECURE`, iOS screen-capture detection APIs, and MDM
screenshot restriction are native-device capabilities, not currently
available to a web/PWA application.** They may be discussed only as
future considerations for a possible later native-application milestone
(already named as unscheduled in
[alert-notification-engine-v1's phased plan](alert-notification-engine-v1.md#12-phased-implementation-plan)
and
[notification-relay-amendment-v1's sound limitation](notification-relay-amendment-v1.md#22-sound-is-platform-controlled)),
never presented as something the current or proposed web/PWA surface can
do.

## 10. Audit and security events

Every event below is an **immutable, append-only** record, following the
existing `ENABLE ALWAYS`-trigger convention already used for
`unit_status_decisions`, `transcript_reviews`, `detection_audit`, and
`alert_events` ([database README](../database/README.md)). No audit table
in this design supports UPDATE, DELETE, or TRUNCATE through an ordinary
application path; corrections are new rows, never edits to history.

| Event | Recorded at minimum |
| --- | --- |
| Login success | account, timestamp, session identifier (not the raw token), source network hint |
| Login failure | attempted email (normalized; not proof an account exists), timestamp, safe failure reason code, source network hint |
| Invitation created | target account, issuing administrator, timestamp |
| Invitation redeemed | account, timestamp |
| Invitation expired | account, timestamp of expiration transition |
| Password reset requested | account (if matched; the response to the user is identical either way per [Section 3](#3-login-screen-behavior)), timestamp |
| Password reset completed | account, timestamp, resulting session revocation |
| MFA enrollment | account, method (passkey/TOTP), timestamp |
| MFA recovery | account, administrator (if administrator-assisted), timestamp |
| Role or scope change | account, prior role/scope, new role/scope, administrator, timestamp |
| Account suspension/disable/restore | account, administrator, action, timestamp |
| Protected call access | account, resource identifier, timestamp |
| Notification-device registration/revocation | account, device identifier (not the raw push endpoint), action, timestamp |
| Session creation/revocation | account, session identifier (not the raw token), action, timestamp |
| Administrative actions (any Section 7 action not already listed) | acting administrator, target account, action, timestamp |

**Audit records must never contain passwords, tokens (session, invitation,
password-reset, or push-subscription), full notification payloads, or
unnecessary private call content.** This restates
[Section 4's logging prohibition](#4-password-and-recovery-policy) and
[notification-relay-amendment-v1 § 17](notification-relay-amendment-v1.md#17-delivery-audit)'s
identical rule for delivery audit rows, applied consistently to every
authentication and authorization event.

## 11. Proposed technical surface

Everything in this section is a **proposal for a separately authorized
future implementation phase**. Step 9A creates none of it.

### 11.1 Angular routes/components (proposed)

| Route/component | Purpose |
| --- | --- |
| `/mobile/welcome` (existing route, proposed for later modification) | Becomes the real login screen per [Section 3](#3-login-screen-behavior), superseding the current prototype's no-credential-input rule only after this contract's implementation phase is separately authorized. |
| `web/src/app/auth/forgot-password/` (new, proposed) | Password-reset request screen. |
| `web/src/app/auth/reset-password/` (new, proposed) | Password-reset completion screen (token-carrying route). |
| `web/src/app/auth/first-time-login/` (new, proposed) | Mandatory permanent-password (and MFA enrollment) establishment screen following invitation redemption. |
| `web/src/app/admin/users/` (new, proposed) | Administrator Users list/detail per [Section 7](#7-admin-user-management-flow). |
| `web/src/app/admin/audit/` (new, proposed) | Administrator/auditor audit-history view per [Section 10](#10-audit-and-security-events). |
| `web/src/app/mobile/mobile-settings/` (existing, proposed for later extension) | Extended with a "Sessions and devices" panel per [Section 8](#8-sessions), alongside the notification panel already proposed by [notification-relay-amendment-v1 § 25.4](notification-relay-amendment-v1.md#254-angularpwa-surfaces). |

### 11.2 Go packages/interfaces (proposed, under `backend/internal/`)

| Package | Purpose |
| --- | --- |
| `identity` | Account model, state machine, invitation/reset token issuance and validation. |
| `passwordpolicy` | Hashing, breach checking, length/complexity-free validation. |
| `sessions` | Session issuance, validation, listing, and revocation. |
| `mfa` | WebAuthn/passkey and TOTP enrollment/verification. |
| `authorization` | Role/scope permission checks, consumed by every protected HTTP handler. |
| `identityaudit` | Append-only audit-event recording for every event in [Section 10](#10-audit-and-security-events). |

None of these packages exists today; none is registered by
`backend/cmd/api` today or by this contract.

### 11.3 HTTP endpoints (proposed, all requiring the session/authorization model above once it exists)

| Route | Method | Purpose |
| --- | --- | --- |
| `/api/auth/login` | `POST` | Authenticate with email/password (or invitation/reset token context). |
| `/api/auth/logout` | `POST` | End the current session. |
| `/api/auth/logout-all` | `POST` | End every session for the current account. |
| `/api/auth/sessions` | `GET` | List the current account's active sessions/devices. |
| `/api/auth/sessions/{id}` | `DELETE` | Revoke one session/device. |
| `/api/auth/password-reset` | `POST` | Request a password-reset token (always the same generic response). |
| `/api/auth/password-reset/{token}` | `POST` | Complete a password reset. |
| `/api/auth/first-time-login/{token}` | `POST` | Redeem an invitation and establish a permanent password. |
| `/api/auth/mfa/enroll` | `POST` | Begin/complete MFA enrollment. |
| `/api/admin/users` | `GET`/`POST` | List users / create an invitation, administrator-only. |
| `/api/admin/users/{id}` | `GET`/`PATCH` | View/update a user (role, scope, display name), administrator-only. |
| `/api/admin/users/{id}/suspend` | `POST` | Suspend, administrator-only. |
| `/api/admin/users/{id}/disable` | `POST` | Disable, administrator-only. |
| `/api/admin/users/{id}/restore` | `POST` | Restore, administrator-only. |
| `/api/admin/users/{id}/revoke` | `POST` | Revoke all sessions/devices, administrator-only. |
| `/api/admin/users/{id}/resend-invitation` | `POST` | Resend invitation, administrator-only. |
| `/api/admin/users/{id}/reset-credential` | `POST` | Administrator-initiated credential reset, administrator-only. |
| `/api/admin/audit` | `GET` | Query audit events, administrator/auditor-only. |

No public, unauthenticated route other than `/api/auth/login`,
`/api/auth/password-reset`, and `/api/auth/first-time-login/{token}` is
proposed, and each of those three is rate-limited and enumeration-resistant
per [Sections 3-4](#3-login-screen-behavior).

### 11.4 Database tables (proposed; next free migration version `000008`)

| Table | Purpose |
| --- | --- |
| `users` | One row per account: normalized email, display name, role, scope, status, password hash + hashing parameters, MFA enrollment state, timestamps. |
| `invitations` | Append-only-per-issuance token records (hash only), status, expiration, issuing administrator. |
| `password_resets` | Append-only-per-issuance token records (hash only), status, expiration. |
| `sessions` | Active/revoked session records, device hint, creation/expiration/revocation timestamps. |
| `mfa_credentials` | Enrolled passkey/TOTP credential records per user (public key material only; no secret key material for passkeys, encrypted/hashed TOTP secret if TOTP is used). |
| `identity_audit_log` | Append-only audit rows for every event in [Section 10](#10-audit-and-security-events); `ENABLE ALWAYS` triggers reject UPDATE/DELETE/TRUNCATE, matching `detection_audit`/`alert_events`. |

No migration file is created by Step 9A. `000008` is recorded here, as it
already was in
[notification-relay-amendment-v1 § 25.2](notification-relay-amendment-v1.md#252-database-tables-next-free-migration-version-000008),
only as the next unused version number as of this contract's baseline; it
does not reserve or commit that number to whichever future migration
(this one, the notification schema, or another) actually lands first.

### 11.5 Environment variables (proposed, `GFR_*` convention)

| Variable | Purpose |
| --- | --- |
| `GFR_AUTH_SESSION_SECRET` | Server-side session-signing/encryption key. Never logged, never defaulted. |
| `GFR_AUTH_SESSION_IDLE_TIMEOUT` | Idle-timeout duration ([Section 8](#8-sessions)); exact value unresolved. |
| `GFR_AUTH_SESSION_MAX_LIFETIME` | Absolute session lifetime; exact value unresolved. |
| `GFR_AUTH_INVITATION_TTL` | Invitation-token expiration window ([Section 2](#2-first-time-login)). |
| `GFR_AUTH_PASSWORD_RESET_TTL` | Password-reset-token expiration window ([Section 4](#4-password-and-recovery-policy)). |
| `GFR_AUTH_RATE_LIMIT_PER_ACCOUNT` / `GFR_AUTH_RATE_LIMIT_PER_IP` | Login-throttling bounds ([Section 4](#4-password-and-recovery-policy)). |
| `GFR_AUTH_BREACH_CHECK_ENABLED` | Explicit boolean gate for breached-password checking. |
| `GFR_AUTH_MFA_PROVIDER` | Selects the MFA/passkey relying-party configuration, once a provider is chosen ([Section 5](#5-mfapasskeys); unresolved). |

No dependency is added, no file above is created, and no environment
variable above is read by any running code as part of Step 9A.

### 11.6 Identity-provider boundary

This contract does not select a specific identity-provider model (self-hosted
credential storage versus a third-party identity platform); see
[Unresolved questions](#15-unresolved-questions). Whichever is chosen, the
boundary is the same: the Go backend is the only component that validates
credentials and issues sessions; the Angular/PWA client never receives,
stores, or validates a credential directly, mirroring the "server-mediated,
nothing privileged in the browser" boundary already established for the
notification relay in
[notification-relay-amendment-v1 § 4.1](notification-relay-amendment-v1.md#41-design-summary).

### 11.7 Notification-authorization boundary

Restated from [Section 12](#12-interaction-with-notifications) below: the
`authorization` package proposed in [Section 11.2](#112-go-packagesinterfaces-proposed-under-backendinternal)
is the single point every future `notifydevices`/`notifyoutbox`/`notifyrelay`
call ([notification-relay-amendment-v1 § 25.1](notification-relay-amendment-v1.md#251-go-packages-under-backendinternal))
must pass through; no notification package independently re-implements
identity or role checking.

## 12. Interaction with notifications

- **Step 8D notification delivery remains blocked.** Nothing in this
  document authorizes Step 8D-B or any notification code; it only satisfies
  the prerequisite [notification-relay-amendment-v1 § 3](notification-relay-amendment-v1.md#3-authentication-prerequisite)
  named. Step 8D-B still requires its own separate future authorization
  after this contract's own implementation phase is complete, per
  [notification-relay-amendment-v1 § 26](notification-relay-amendment-v1.md#26-prerequisite-order).
- **A user may register a notification device only after authenticated
  authorization exists**, restating
  [notification-relay-amendment-v1 § 6](notification-relay-amendment-v1.md#6-device-and-subscription-registration)
  unchanged: there is no anonymous or pre-authentication registration path.
- **Device subscriptions belong to a user and a permitted scope**: a
  registration is always tied to the server-verified authenticated
  identity, never a client-supplied user identifier, and delivery is gated
  by that user's current role/scope per [Section 7 of the amendment](notification-relay-amendment-v1.md#7-server-side-preference-enforcement).
- **Disabling a user revokes sessions and notification subscriptions
  together.** [Section 7](#7-admin-user-management-flow)'s **Disable**
  action, and the narrower **Revoke** action, both immediately invalidate
  every session and every registered notification device for that account,
  consistent with
  [notification-relay-amendment-v1 § 8](notification-relay-amendment-v1.md#8-consent-revocation-and-device-lifecycle)'s
  "disabled account... immediately stops all delivery" rule and
  [§ 9](notification-relay-amendment-v1.md#9-per-userper-device-authorization-at-delivery)'s
  per-delivery re-check.
- **Notification permissions never grant access to call information.** A
  registered device and a granted browser notification permission are
  orthogonal to [Section 6](#6-roles-and-authorization)'s role/scope
  authorization; holding a notification subscription proves nothing about
  what protected call information that user may view, and a future
  implementation must not conflate "has a device registered" with "is
  authorized to view protected content."
- **Push endpoints and subscription identifiers are sensitive data**,
  restating
  [notification-relay-amendment-v1 § 5](notification-relay-amendment-v1.md#5-credential-isolation)
  and [§ 18](notification-relay-amendment-v1.md#18-rate-limits-abuse-controls-and-observability)
  unchanged: never logged in plaintext, never exposed to another user, and
  covered by the same audit-minimization rule as every other event in
  [Section 10](#10-audit-and-security-events).

## 13. Acceptance-test matrix

All fixtures for every future phase below must be synthetic. No test may
require a real user's credentials, a real identity-provider account, or
live radio, mirroring the synthetic-only convention already required by
[alert-notification-engine-v1's acceptance tests](alert-notification-engine-v1.md#13-acceptance-tests)
and
[notification-relay-amendment-v1's acceptance criteria](notification-relay-amendment-v1.md#27-acceptance-criteria).

| ID | Case | Required pass condition |
| --- | --- | --- |
| AAX-01 | No public registration | No route or endpoint allows account creation without an authenticated administrator session. |
| AAX-02 | Invitation issuance | Creating a user issues a single-use, hash-stored token with a configured expiration; the plaintext token is never persisted and never appears in a second API response. |
| AAX-03 | First-time login | Redeeming a valid invitation moves the account to `password-change-required` and blocks every protected route/response until a permanent password (and MFA, if required) is established. |
| AAX-04 | Forced password establishment | No protected content is reachable for an account still in `password-change-required`, verified by an authenticated request attempt in that state. |
| AAX-05 | Expired invitation | An invitation redeemed after its expiration is rejected with the account moved to `expired`, and shows the exact expired-invite message from [Section 3](#3-login-screen-behavior). |
| AAX-06 | Reused invitation | A second redemption attempt of an already-redeemed token fails and is audited as a `invitation redeemed`-adjacent failure event, never a silent success. |
| AAX-07 | Administrator MFA | An administrator role account cannot leave `password-change-required`, and cannot reach any `/api/admin/*` route, without a completed MFA enrollment. |
| AAX-08 | Role enforcement | Each role in [Section 6](#6-roles-and-authorization)'s matrix can perform only its listed operations; every disallowed combination is rejected server-side, tested independently of any Angular UI state. |
| AAX-09 | Backend authorization, not UI hiding | A direct API request for a disallowed operation is rejected even when made with developer tools bypassing the Angular UI entirely. |
| AAX-10 | Account-enumeration resistance | Login failure for a nonexistent email, a wrong password, and (outside the two named exceptions) every other failure case return the identical generic message and statistically indistinguishable response timing. |
| AAX-11 | Password reset issues single-use short-lived token | A reset token is rejected after use or after expiration; the response to the reset-request endpoint is identical whether or not the email matches an account. |
| AAX-12 | Session revocation on reset | Completing a password reset invalidates every other existing session for that account. |
| AAX-13 | PWA session handling | A session created inside the installed standalone PWA expires, revokes, and re-prompts identically to a browser-tab session; no separate long-lived standalone session class exists. |
| AAX-14 | Protected-data caching restrictions | Generated `ngsw.json`/`ngsw-config.json` list no `dataGroups` and no authenticated-API URL; a protected API response is never present in Cache Storage. |
| AAX-15 | Notification-device authorization | Device registration is rejected for an unauthenticated request and for a request presenting a different user's identity than the authenticated caller. |
| AAX-16 | Disabled account revokes everything | Disabling an account immediately invalidates its sessions and cancels/revokes its registered notification devices, verified end-to-end. |
| AAX-17 | Audit completeness | Every event category in [Section 10](#10-audit-and-security-events) produces exactly one audit row per occurrence, containing no password, token, full notification payload, or unnecessary private call content, verified by an automated field-name and content scan. |
| AAX-18 | Screenshot/privacy limitations documented, not solved | Documentation and any implemented privacy-mode/watermark feature explicitly state that photography and screenshotting cannot be prevented; no test may assert prevention. |
| AAX-19 | Accessibility | Login, first-time-login, forgot-password, and admin-user-management screens meet the same accessibility minimums already required by [mobile-phone-experience-v1](mobile-phone-experience-v1.md#accessibility-requirements) (landmarks, focus order, contrast, zoom, reduced motion, screen-reader labeling). |
| AAX-20 | Security failure states | Offline, locked, expired-invite, suspended-account, and service-unavailable states each render their exact defined text from [Section 3](#3-login-screen-behavior) and never a raw error, stack trace, or database message. |

## 14. Threat model and limitations

| Threat | Description | Required mitigation / honest limitation |
| --- | --- | --- |
| Credential stuffing | An attacker tries breached email/password pairs from another service against this application. | Breached-password checking at password-set time ([Section 4](#4-password-and-recovery-policy)), per-account and per-IP rate limiting, MFA for administrators; normal-user MFA is [unresolved](#15-unresolved-questions) and this is not fully mitigated for normal-user accounts without it. |
| Phishing | An attacker tricks a user into entering credentials on a fake login page. | Passkeys/WebAuthn ([Section 5](#5-mfapasskeys)) are phishing-resistant by design (origin-bound); TOTP and password alone are not. This contract cannot force phishing-resistant MFA on normal users without resolving the open MFA-for-normal-users question. |
| Session theft | A stolen session cookie or token is replayed by an attacker. | HttpOnly/secure/SameSite cookies prevent script-based theft ([Section 8](#8-sessions)); idle/absolute timeout bounds the exposure window; device/session listing lets a user or administrator revoke a suspicious session. A cookie stolen through a compromised device's OS or a network without TLS is not mitigated by this contract alone. |
| CSRF | A malicious site induces a signed-in user's browser to make an unwanted authenticated request. | Explicit CSRF defense on every mutating route ([Section 8](#8-sessions)); mechanism not yet selected. |
| XSS/token theft | Injected script reads or exfiltrates a session token. | HttpOnly cookies prevent direct script access to the session token; this contract does not eliminate the underlying need for output encoding and CSP in the Angular application, which remain a future implementation's responsibility, not created here. |
| Privilege escalation | A lower-privileged user attempts to reach an administrator-only operation or another user's data. | Server-side role/scope checks on every request ([Section 6](#6-roles-and-authorization), [Section 9](#9-protected-call-information)); AAX-08/AAX-09 test this directly. |
| Insecure direct object references | A user substitutes another user's or another department's identifier in a request to view data outside their scope. | Every protected read/write re-checks the requester's own scope against the target resource server-side, never trusting a client-supplied scope; this is a [Section 6](#6-roles-and-authorization) enforcement requirement, not a separate mechanism. |
| Account enumeration | An attacker distinguishes real from fake emails via login or reset responses. | Generic, timing-safe responses per [Section 3](#3-login-screen-behavior) and [Section 4](#4-password-and-recovery-policy); the two narrow, justified exceptions are documented, not hidden. |
| Malicious administrators | An administrator abuses their own legitimate access (for example, creating unauthorized accounts, viewing protected data outside a real need, or covering their tracks). | Every administrative action is audited ([Section 10](#10-audit-and-security-events)) with an append-only, trigger-protected log; this contract does not claim the log is tamper-proof against a database owner/superuser, restating the existing honest limitation already stated for `transcript_reviews` and `unit_status_decisions` in the [database README](../database/README.md#migration-000006-append-only-transcript-review): "these are database protections, not cryptographic attestation... A table owner/superuser can change schema, disable triggers, or restore altered data." Least-privilege application database roles (not table owners) are a future implementation requirement, not created here. |
| Lost or shared devices | A device with an active session is lost, stolen, or used by someone other than the account holder. | Idle timeout, session listing/revocation ([Section 8](#8-sessions)), and administrator-initiated revoke ([Section 7](#7-admin-user-management-flow)) bound and end unauthorized access; none of this prevents misuse during the window before revocation. |
| Screenshots and external cameras | Restated from [Section 9](#9-protected-call-information): not preventable by a web/PWA application. Deterrence (watermarking, privacy mode, short sessions) and response (audit, policy) controls only. |
| Notification leakage | Sensitive content appears in a lock-screen-visible notification or is intercepted in transit. | Minimized/redacted notification text ([Section 9](#9-protected-call-information),
[notification-relay-amendment-v1 § 12](notification-relay-amendment-v1.md#12-notification-text-and-lock-screen-minimalism)); HTTPS-only transport. |
| Service-worker/cache leakage | Protected data is retained in Cache Storage or IndexedDB by the service worker, outliving the session it was fetched under. | No `dataGroups` for authenticated routes ([Section 9](#9-protected-call-information)); restated from [notification-relay-amendment-v1 § 15](notification-relay-amendment-v1.md#15-service-worker-caching-prohibitions). |
| Audit tampering | An attacker or insider alters or deletes audit history to hide an action. | `ENABLE ALWAYS` triggers reject UPDATE/DELETE/TRUNCATE at the database level ([Section 10](#10-audit-and-security-events)); the same owner/superuser caveat as "malicious administrators" above applies unchanged. |
| Denial of service | An attacker floods login, reset, or invitation-redemption endpoints to exhaust resources or lock out legitimate users. | Rate limiting ([Section 4](#4-password-and-recovery-policy)) bounds attack cost per account/IP; this contract does not include a network-layer DoS mitigation (for example, upstream WAF/CDN throttling), which remains outside this document's scope. |

## 15. Unresolved questions

These do not block this documentation milestone, but each must be resolved
before the implementation phase that depends on it proceeds. None is
silently chosen by this contract.

1. **Identity provider**: self-hosted credential storage (the Go backend
   validates passwords/passkeys directly) versus a third-party identity
   platform (for example, an OIDC provider). Neither is selected; see
   [Section 11.6](#116-identity-provider-boundary).
2. **Invitation/reset delivery provider**: which email (or other) delivery
   channel carries the invitation and reset tokens, and who owns that
   account/billing relationship — the same open question the notification
   relay amendment already left unresolved for push-provider ownership
   ([notification-relay-amendment-v1 § 29, item 1](notification-relay-amendment-v1.md#29-unresolved-decisions)),
   now restated for a different delivery channel.
3. **Final roles and department scopes**: the exact set of roles beyond
   [Section 6](#6-roles-and-authorization)'s proposed five, and how
   department/talkgroup scope boundaries are defined and assigned.
4. **User eligibility/approval authority**: who decides which people may be
   invited at all (for example, confirmed department volunteers only),
   restating
   [notification-relay-amendment-v1 § 29, item 6](notification-relay-amendment-v1.md#29-unresolved-decisions)'s
   "eligible recipients" question at the account-creation layer instead of
   the notification layer.
5. **MFA requirement for normal users**: mandatory, optional, or
   role-dependent; not decided in [Section 5](#5-mfapasskeys).
6. **Session timeout values**: exact idle-timeout and absolute-lifetime
   durations ([Section 8](#8-sessions)), and whether they differ by role.
7. **Retention periods**: how long `identity_audit_log`, `sessions`,
   `invitations`, and `password_resets` rows are retained, echoing the
   still-open retention questions already recorded in
   [alert-notification-engine-v1 § 14, item 4](alert-notification-engine-v1.md#14-unresolved-questions)
   and
   [notification-relay-amendment-v1 § 29, item 3](notification-relay-amendment-v1.md#29-unresolved-decisions).
8. **Notification content policy**: restated from
   [notification-relay-amendment-v1 § 29, item 11](notification-relay-amendment-v1.md#29-unresolved-decisions),
   still unresolved and unaffected by this contract.
9. **Managed-device/MDM requirements**: whether the department will issue
   and manage devices under MDM, and what policy that implies for
   [Section 9](#9-protected-call-information)'s managed-device controls.
10. **Native-app schedule**: whether and when a native iOS/Android
    application is ever built, restating the still-unscheduled question
    from
    [alert-notification-engine-v1's phased plan](alert-notification-engine-v1.md#12-phased-implementation-plan)
    and
    [notification-relay-amendment-v1 § 22](notification-relay-amendment-v1.md#22-sound-is-platform-controlled).
11. **Legal/privacy policy and incident-response ownership**: who owns the
    department's privacy policy, breach-notification obligations, and
    incident-response process for this system, a question this contract
    surfaces but does not answer.

## Step 9A validation checklist

- [ ] Only `docs/authentication-authorization-v1.md` is added.
- [ ] Baseline, clean-tree, and `origin/main` equality at `c53878a` are
      recorded.
- [ ] Existing mobile, PWA, alert, and notification-relay contracts, and the
      current mobile welcome/settings routes, are read and not edited.
- [ ] No public self-registration; account states, identity normalization,
      administrator distinguishability, and no-plaintext-password storage
      are explicit.
- [ ] First-time login defines invitation issuance, single-use/expiry
      behavior, forced password establishment, and administrator
      resend/reset without sending permanent passwords through email or
      notifications.
- [ ] Login-screen section gives exact proposed text/controls, defines every
      required state, and states enumeration resistance without changing
      the existing non-authenticating welcome-screen file.
- [ ] Password/recovery policy uses length- and breach-based requirements,
      not arbitrary complexity, and defines hashing, reset, throttling, and
      no-logging rules.
- [ ] MFA/passkey section requires strong administrator MFA, recommends a
      phishing-resistant method, and leaves normal-user MFA and provider
      selection unresolved rather than silently decided.
- [ ] Roles/authorization define least-privilege roles, a permission
      matrix, and the backend-authorization-not-UI-hiding principle.
- [ ] Admin user-management flow defines the complete flow with exact
      proposed screens, fields, and dangerous-action confirmations.
- [ ] Sessions section covers cookies, CSRF, expiration/idle timeout,
      device listing/revocation, server-side authorization, logout/logout-
      all, and installed-PWA session handling.
- [ ] Protected-call-information section states the no-anonymous-access,
      no-bundled-data, and no-protected-caching rules, and gives an honest,
      unembellished statement of screenshot/photography limitations with
      deterrence/response controls that do not claim prevention.
- [ ] Audit/security-event catalog is immutable, append-only, and excludes
      passwords, tokens, full notification payloads, and unnecessary
      private call content.
- [ ] Proposed technical surface (routes, Go packages, endpoints, tables,
      env vars, identity-provider boundary, notification-authorization
      boundary) is marked proposed, not implemented.
- [ ] Interaction with notifications keeps Step 8D blocked and ties device
      registration, disable, and revocation to this contract's account
      model.
- [ ] Acceptance-test matrix and threat model cover every required area
      without asserting prevention where none exists.
- [ ] Unresolved questions are listed and none are silently chosen.
- [ ] No implementation file, schema migration, dependency, environment
      variable, API endpoint, login-screen change, staging, commit, push,
      or deploy occurs.
