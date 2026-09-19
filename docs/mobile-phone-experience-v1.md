# Mobile Phone Experience Contract v1 — Step 7C

Status: Design contract only. No Step 7C application implementation is
authorized by this document.

Contract identifier: `mobile-phone-experience-v1`.

Reviewed baseline: `2660b5c7a58afebc91fff50f57ed9fb6d17b1e6e`
(`2660b5c`). The working tree was clean, and HEAD and local `origin/main`
matched that commit when this contract was created.

## Purpose and milestone boundary

Step 7C defines a dedicated phone experience for iPhone and Android that is
visually and structurally different from the desktop board. It is a design for
a later synthetic Angular prototype, not permission to implement, deploy, or
connect an operational system.

The existing board at `/` remains the desktop route and visual baseline. Its
approximately 68% primary / 32% details layout, existing compact responsive
fallback, warnings, fixtures, roster, evidence language, and PWA packaging are
not changed by Step 7C.

The phone experience is a separate application surface under `/mobile`. It
uses the same existing synthetic fixture constants; it does not create a
second data model or reinterpret fixture meaning.

This document creates only
`docs/mobile-phone-experience-v1.md`. It does not authorize changes to Angular
source, routes, tests, styles, the manifest, service-worker configuration,
Vercel configuration, backend code, existing contracts, or deployment state.

## Existing-contract compatibility

No existing application API conflicts with this design because no application
API is proposed.

The [mobile/PWA beta contract](mobile-pwa-beta-v1.md) requires the root route
to render the current board and the manifest `start_url` to remain `/`. Step 7C
therefore defines explicit `/mobile/...` routes and forbids automatic
viewport, user-agent, or standalone-mode redirects away from `/`.

Under the current manifest, a newly installed PWA opens `/` and therefore the
existing board. A tester may navigate within the standalone app to
`/mobile/welcome`, because the manifest scope is `/`. Making the dedicated
phone experience the installed launch default would require a separately
approved amendment to the PWA contract and manifest; it is not approved here.

If a later implementation discovers that Angular routing, the current service
worker, or another approved contract cannot preserve these boundaries, work
must stop for contract review rather than changing the desktop route or PWA
behavior.

## Authoritative reviewed surfaces

- [Mobile/PWA beta contract](mobile-pwa-beta-v1.md)
- [Current application routes](../web/src/app/app.routes.ts)
- [Current board component](../web/src/app/board-preview/board-preview.ts)
- [Current board template](../web/src/app/board-preview/board-preview.html)
- [Current board fixtures](../web/src/app/board-preview/board-preview.fixtures.ts)
- [Current global board styles](../web/src/styles.scss)
- [Current PWA status component](../web/src/app/pwa-status/pwa-status.ts)
- [Current PWA status template](../web/src/app/pwa-status/pwa-status.html)
- [Current manifest](../web/public/manifest.webmanifest)
- [Current web README](../web/README.md)

## Required preservation

Every phone screen, including the welcome screen and all loading, offline,
unavailable, and stale states, keeps these exact visible warnings:

- `SHADOW / REPLAY — NOT LIVE CAD`
- `Synthetic replay preview. No operational authority.`

The following existing meanings remain unchanged:

- Header identity: `Greenwich Fire Responder V3`.
- Fixtures remain limited to the existing synthetic board fixtures, including
  `100 TEST STREET`, `SYN-PENDING-01`, and `SYN-INCIDENT-01`.
- Roster order remains `DC`, `E2`, `E3`, `E4`, `E5`, `SQ1`, `SQ8`, `T1`.
- `CH1A`, `CH2B`, `CH3B`, and `CH4C` remain evidence labels only.
- Channel and TGID do not authorize incidents or unit state.
- Proposed associations are not current apparatus state.
- Address resolution with unresolved call type is not proof of one incident.
- `ShadowOnly` and `PairingIntact` remain descriptive fixture indicators, not
  access-control or operational-safety properties.
- The device-local clock is not a CAD or server clock.
- Out-of-service content remains a labeled synthetic, non-operational preview.

## Non-goals and prohibitions

A later Step 7C implementation must not:

- Display real incidents or load any live, private, or user-supplied data.
- Connect an HTTP/API client, WebSocket, Go API, PostgreSQL database, Icecast,
  SDRTrunk, Whisper, live radio, recording store, or any backend.
- Add notifications, push subscriptions, background sync, geolocation, SMS,
  email, analytics, telemetry, or third-party beacons.
- Load or cache private transcripts, JSONL, `Result.Pairs`, dataset IDs,
  fingerprints, credentials, authorization headers, incident payloads, unit
  payloads, or audio.
- Add authentication, authorization, user accounts, OAuth, identity tokens,
  sessions, cookies, credential storage, or password-reset behavior.
- Add a local PIN, hard-coded password, shared secret, security question, or
  client-side credential comparison.
- Add a map, police monitoring, operational out-of-service controls, CAD
  authority, or unit-state mutation.
- Change the existing desktop board, fixture meanings, shadow/replay
  contracts, PWA cache contract, production deployment configuration, or
  public-preview plan.

## Mobile visual model

The mobile surface must look like a purpose-designed phone application, not a
scaled or fully stacked copy of the desktop board.

Its visual hierarchy is:

1. A compact, sticky application header containing the product name and both
   warnings. Warning text remains readable without opening a menu.
2. A conditional connection/update banner using the approved PWA status copy.
3. One active screen at a time in a single-column scrolling content region.
4. A persistent bottom navigation bar on the five primary screens.

Cards use the existing dark visual family and status colors, but mobile
content is summarized and split across screens. The phone shell must not
render the desktop `.layout` grid, the 68% / 32% columns, or all desktop panels
in one long page. The desktop route continues to use those structures
unchanged.

No mobile screen may resemble an official dispatch terminal, use department
seal artwork, or hide the SHADOW / REPLAY identity in standalone mode.

## Welcome and login-screen prototype

`/mobile/welcome` is a welcome/login-screen **design prototype only**. It is
not authentication and must say so visibly.

Required visible content:

- `Greenwich Fire Responder V3`
- Both exact warning messages
- `Prototype access — not authentication.`
- `No credentials are collected or verified.`
- A primary button labeled `Enter synthetic preview`

The screen may use familiar sign-in visual composition—centered identity,
introductory panel, access explanation, and a primary continuation action—but
it must not contain username, email, password, PIN, passkey, biometric,
one-time-code, or security-question inputs. It must not use a `<form>`, browser
credential APIs, password-manager metadata, or autofill hints.

Selecting `Enter synthetic preview` performs only client-side navigation to
`/mobile/home`. It creates no session, token, cookie, local-storage entry,
session-storage entry, IndexedDB record, cache entry, log record, audit event,
or authorization state. Direct navigation to any mobile screen remains
possible; there is no route guard and no protected content.

Real authentication requires a later contract naming an approved identity
provider, threat model, server-side session or token validation, server-side
authorization rules, logout/revocation behavior, audit requirements, and
private-data boundary. A local-only substitute is forbidden.

## Exact proposed routes

These are proposals for a separately authorized implementation. Step 7C does
not create them.

| Route              | Component                             | Behavior                                                                                           |
| ------------------ | ------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `/`                | Existing `BoardPreview`               | Unchanged desktop board route at every viewport; no automatic redirect                             |
| `/mobile`          | Redirect                              | `pathMatch: 'full'` redirect to `/mobile/welcome`                                                  |
| `/mobile/welcome`  | `MobileWelcome` inside `MobileShell`  | Prototype access explanation and navigation-only entry action; bottom navigation hidden            |
| `/mobile/home`     | `MobileHome` inside `MobileShell`     | Compact Pending and Active summary cards                                                           |
| `/mobile/calls`    | `MobileCalls` inside `MobileShell`    | Existing synthetic pending/active call details only                                                |
| `/mobile/units`    | `MobileUnits` inside `MobileShell`    | Exact eight-unit roster in approved order                                                          |
| `/mobile/evidence` | `MobileEvidence` inside `MobileShell` | Channel/TGID evidence-only content and synthetic replay evidence                                   |
| `/mobile/settings` | `MobileSettings` inside `MobileShell` | Connection, build/version, update, and logout-prototype controls                                   |
| `/mobile/**`       | Redirect                              | Redirect unknown mobile paths to `/mobile/welcome`; do not add a global wildcard in this milestone |

The five bottom-navigation destinations are Home, Calls, Units, Evidence, and
Settings, in that order. Each item uses an icon plus a visible text label, and
the active route is exposed with `aria-current="page"`. Browser Back and
Forward navigation must work normally.

## Screen requirements

### Shared shell

`MobileShell` owns the persistent warnings, screen heading region, PWA status
indicator, content outlet, and bottom navigation. The bottom navigation is
hidden only on `/mobile/welcome`. Warning content is never hidden.

Each route renders one `<main>` landmark with one level-one heading. Navigation
changes move focus to that heading without trapping focus or unexpectedly
scrolling the user.

### Home

Home shows exactly two compact summary cards:

- Pending: `SYN-PENDING-01`, `100 TEST STREET (PENDING)`, unresolved call type,
  `CH1A`, and TGID `57201`, with evidence-only labeling.
- Active: `SYN-INCIDENT-01`, `100 TEST STREET`, resolved address evidence,
  unresolved call type, `CH1A`, and TGID `57201`, with the existing
  not-one-incident warning.

The cards may link to `/mobile/calls`. They do not refresh, poll, animate as
new dispatches, show notification badges, or imply recency.

### Calls

Calls shows the existing pending and active fixture details only. It preserves
unresolved states and the synthetic/non-operational notes. It must not add a
call list history, timestamps presented as dispatch time, search, filters,
real addresses, action controls, or API loading behavior.

### Units

Units renders one compact accessible list in this exact order:

1. `DC`
2. `E2`
3. `E3`
4. `E4`
5. `E5`
6. `SQ1`
7. `SQ8`
8. `T1`

Each row preserves the existing station, synthetic status label, and proposed
or synthetic-only qualifier. Color is supplementary; text communicates every
status. There are no edit, acknowledge, dispatch, availability, or
out-of-service controls.

### Evidence

Evidence displays `CH1A`, `CH2B`, `CH3B`, and `CH4C` with the visible phrase
`evidence labels only`. Wherever a TGID appears, the page also shows:

`Channel and TGID are evidence only. They do not authorize incidents or unit state.`

The page may show the existing `ShadowOnly`, `PairingIntact`, synthetic
raw-model phrase, synthetic human-reference phrase, ambiguous item, rejected
item, and unbound association. It must retain their existing caveats and must
not load private replay exports or portray evidence as operational truth.

### Settings

Settings contains:

- Connection state derived only from `navigator.onLine`, labeled as a browser
  connectivity hint and not proof of CAD, radio, API, or server connectivity.
- Build/version information from compile-time static constants. It may show an
  application version, short build identifier, and build date, but it must not
  fetch a version endpoint, expose environment secrets, or call an API.
- The existing stale-build message and explicit
  `Reload synthetic preview` action when an Angular service-worker update is
  ready.
- A button labeled `Log out of prototype` followed by
  `Prototype only — no authenticated session exists.`

`Log out of prototype` navigates to `/mobile/welcome` only. It clears nothing
because no identity, credential, token, or session exists. Browser Back may
return to the prior screen, and direct URLs remain accessible; tests must make
this non-security behavior explicit.

## Phone layout and breakpoints

The dedicated mobile route targets portrait and landscape phones from 320 CSS
pixels wide. Its own component styles use these bands without changing the
existing board breakpoint:

| Width             | Required behavior                                                                                                                   |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `320px–479px`     | Single column, compact spacing, full-width cards, five-item bottom navigation with short labels                                     |
| `480px–767px`     | Single column with slightly larger spacing and type; cards remain vertically ordered                                                |
| `768px–899px`     | Phone surface centered with a maximum content width of `600px`; bottom navigation remains the mobile navigation                     |
| `900px` and wider | Explicit `/mobile/...` routes remain a centered phone-style surface; `/` independently retains the existing 68% / 32% desktop board |

No route decision may depend on `window.innerWidth`, user-agent sniffing, or
display mode. CSS controls presentation; URLs control which experience is
shown.

The shell uses `min-height: 100vh` with `100dvh` enhancement and accounts for:

- `env(safe-area-inset-top)` in the sticky header
- `env(safe-area-inset-right)` and `env(safe-area-inset-left)` in all edge
  padding
- `env(safe-area-inset-bottom)` below the bottom navigation
- Content bottom padding large enough that the navigation never covers the
  last focusable control
- Landscape widths and mobile browser chrome without horizontal scrolling

The document may scroll vertically on mobile. Cards must not introduce nested
scroll regions for ordinary content.

## Install and standalone behavior

The existing manifest name, `GFR V3 SHADOW` short name, icon set, scope,
display mode, colors, orientation, and `start_url: "/"` remain unchanged.
Step 7C adds no install prompt and no manifest changes.

When `/mobile/...` is opened inside the installed standalone PWA:

- The same routes, warnings, synthetic content, and bottom navigation render.
- Safe-area padding prevents status-bar, notch, home-indicator, and navigation
  overlap.
- The absence of browser chrome makes the in-app warnings especially
  important; they stay visible on every screen.
- Refresh preserves the current mobile URL through existing SPA routing.
- There is no native capability, push notification, background data refresh,
  or app-store implication.

iPhone Safari and Android Chrome require later physical-device acceptance.
Older iOS service-worker limitations remain those documented by the PWA
contract.

## Loading, offline, unavailable, and stale states

The phone shell uses the existing `PwaStatus` behavior and exact approved
messages:

| State                   | Required presentation                                                                                                                    |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Initial route rendering | `Loading synthetic mobile preview. NOT LIVE CAD.` as text if rendering is not immediate; no live-data skeleton or network progress claim |
| Offline cached shell    | `Offline. Synthetic replay preview only. NOT LIVE CAD.`                                                                                  |
| Shell unavailable       | `Preview unavailable. Synthetic board is not loaded. NOT LIVE CAD.`                                                                      |
| New build ready         | `Stale preview. A newer synthetic build is available. NOT LIVE CAD.` plus `Reload synthetic preview`                                     |

Both primary warnings remain visible with every state. Offline mode continues
to show only cached synthetic fixtures. It must not infer a last incident,
last radio transmission, server timestamp, or data-freshness claim from the
device clock.

The shell must not fetch, retry, poll, queue actions, or add `dataGroups`.
Update activation and reload occur only after the user selects
`Reload synthetic preview`; silent reload remains forbidden.

## Accessibility requirements

A later implementation must meet these minimum requirements:

- Semantic `header`, `nav`, `main`, section headings, lists, and buttons.
- One descriptive page `<h1>` per route and a stable product label in the
  shell.
- Visible keyboard focus with logical order; no focus trap or positive
  `tabindex`.
- Touch targets at least 44×44 CSS pixels with separation sufficient to avoid
  accidental activation.
- Text and meaningful icons meet WCAG 2.2 AA contrast; status is never
  communicated by color alone.
- Content remains usable at 200% text zoom and with browser text scaling.
- Navigation labels remain visible; icons have appropriate accessible names
  or are hidden when redundant.
- Status updates use a non-interruptive live region; the two static warnings
  must not be repeatedly announced on every clock tick or navigation event.
- Reduced-motion preference disables nonessential transitions; no flashing,
  pulsing, countdown, siren, or attention-grabbing live-dispatch animation.
- Screen-reader names include `NOT LIVE CAD` where a status could otherwise be
  mistaken for operational information.
- Portrait and landscape orientations remain usable with no horizontal
  scrolling at 320 CSS pixels.

## Exact proposed component and file surface

These files are the maximum proposed frontend surface for a later,
separately authorized implementation. They are not created by Step 7C.

Existing file proposed for minimal modification:

```text
web/src/app/app.routes.ts
```

Proposed new files:

```text
web/src/app/mobile/mobile.routes.ts
web/src/app/mobile/mobile.routes.spec.ts
web/src/app/mobile/mobile-build-info.ts
web/src/app/mobile/mobile-build-info.spec.ts
web/src/app/mobile/mobile-shell/mobile-shell.ts
web/src/app/mobile/mobile-shell/mobile-shell.html
web/src/app/mobile/mobile-shell/mobile-shell.scss
web/src/app/mobile/mobile-shell/mobile-shell.spec.ts
web/src/app/mobile/mobile-welcome/mobile-welcome.ts
web/src/app/mobile/mobile-welcome/mobile-welcome.html
web/src/app/mobile/mobile-welcome/mobile-welcome.scss
web/src/app/mobile/mobile-welcome/mobile-welcome.spec.ts
web/src/app/mobile/mobile-home/mobile-home.ts
web/src/app/mobile/mobile-home/mobile-home.html
web/src/app/mobile/mobile-home/mobile-home.scss
web/src/app/mobile/mobile-home/mobile-home.spec.ts
web/src/app/mobile/mobile-calls/mobile-calls.ts
web/src/app/mobile/mobile-calls/mobile-calls.html
web/src/app/mobile/mobile-calls/mobile-calls.scss
web/src/app/mobile/mobile-calls/mobile-calls.spec.ts
web/src/app/mobile/mobile-units/mobile-units.ts
web/src/app/mobile/mobile-units/mobile-units.html
web/src/app/mobile/mobile-units/mobile-units.scss
web/src/app/mobile/mobile-units/mobile-units.spec.ts
web/src/app/mobile/mobile-evidence/mobile-evidence.ts
web/src/app/mobile/mobile-evidence/mobile-evidence.html
web/src/app/mobile/mobile-evidence/mobile-evidence.scss
web/src/app/mobile/mobile-evidence/mobile-evidence.spec.ts
web/src/app/mobile/mobile-settings/mobile-settings.ts
web/src/app/mobile/mobile-settings/mobile-settings.html
web/src/app/mobile/mobile-settings/mobile-settings.scss
web/src/app/mobile/mobile-settings/mobile-settings.spec.ts
```

The mobile components import the existing constants from
`board-preview.fixtures.ts`; no copied fixture file is proposed. `MobileShell`
reuses the existing `PwaStatus`. Navigation may be implemented inside
`MobileShell`; a separate navigation component is not approved by this
surface.

The following remain unchanged unless a later contract explicitly says
otherwise:

```text
web/src/app/board-preview/**
web/src/app/pwa-status/**
web/src/styles.scss
web/public/manifest.webmanifest
web/ngsw-config.json
vercel.json
backend/**
database/**
ops/**
```

No new package, environment file, API service, HTTP provider, state-management
library, authentication library, icon package, serverless function, or
backend endpoint is proposed.

## Acceptance-test matrix

| ID     | Case                     | Required pass condition                                                                                                                  | Verification                                                                      |
| ------ | ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| MPX-01 | Desktop root             | `/` still renders the existing `BoardPreview` at all widths                                                                              | Automated route/component test                                                    |
| MPX-02 | Desktop layout           | Existing 68% / 32% desktop grid, responsive fallback, fixture meaning, and board tests are unchanged                                     | Diff inspection plus existing tests; desktop visual check                         |
| MPX-03 | Dedicated visual design  | `/mobile/...` uses the mobile shell, single active screen, and bottom navigation rather than the desktop board grid                      | Component test plus phone screenshots                                             |
| MPX-04 | Warning one              | Exact `SHADOW / REPLAY — NOT LIVE CAD` is visible on every mobile route and state                                                        | Parameterized component tests; device check                                       |
| MPX-05 | Warning two              | Exact `Synthetic replay preview. No operational authority.` is visible on every mobile route and state                                   | Parameterized component tests; device check                                       |
| MPX-06 | Welcome copy             | Welcome shows product identity, `Prototype access — not authentication.`, and `No credentials are collected or verified.`                | Component test                                                                    |
| MPX-07 | No credential collection | Welcome contains no form or credential/PIN/passkey/OTP inputs and writes no browser storage                                              | DOM test, source scan, storage spy                                                |
| MPX-08 | Entry behavior           | `Enter synthetic preview` only navigates to `/mobile/home`; it creates no session or access decision                                     | Router test and storage/network spies                                             |
| MPX-09 | Route openness           | Direct mobile URLs work without guards; tests state this is not authorization                                                            | Router test                                                                       |
| MPX-10 | Navigation               | Home, Calls, Units, Evidence, Settings appear in order with labels, 44×44 targets, and `aria-current`                                    | Component/a11y test; device check                                                 |
| MPX-11 | Home                     | Compact Pending and Active cards show only existing synthetic fixtures and evidence labels                                               | Component test                                                                    |
| MPX-12 | Calls                    | Calls shows synthetic pending/active detail, unresolved call type, and no history/actions/real data                                      | Component test and source scan                                                    |
| MPX-13 | Units                    | Unit order is exactly `DC`, `E2`, `E3`, `E4`, `E5`, `SQ1`, `SQ8`, `T1` with text statuses                                                | Component test                                                                    |
| MPX-14 | Evidence                 | All four channel labels and Channel/TGID evidence-only warning are visible; replay caveats remain intact                                 | Component test                                                                    |
| MPX-15 | Settings connection      | Connection state is based only on `navigator.onLine` and is labeled as a browser hint, not CAD/server state                              | Unit test and source scan                                                         |
| MPX-16 | Settings build           | Static build/version fields render without a version endpoint or secrets                                                                 | Unit test, source scan, production-build inspection                               |
| MPX-17 | Prototype logout         | `Log out of prototype` navigates only to welcome, clears nothing, and does not prevent Back/direct navigation                            | Router and storage tests                                                          |
| MPX-18 | Breakpoints              | 320–767 widths are phone-first; 768+ mobile routes are centered; `/` retains its independent desktop behavior                            | Responsive visual tests at 320, 375, 390, 412, 480, 768, 900, and 1440 CSS pixels |
| MPX-19 | Safe areas               | Header, content, and bottom navigation honor all four safe-area insets in portrait and landscape                                         | CSS inspection and physical-device check                                          |
| MPX-20 | Standalone               | Mobile routes stay within manifest scope, keep warnings, and remain usable without browser chrome                                        | Android Chrome and iPhone Safari installed-PWA checks                             |
| MPX-21 | Loading                  | Any delayed route render shows `Loading synthetic mobile preview. NOT LIVE CAD.` and no live-data implication                            | Component test                                                                    |
| MPX-22 | Offline                  | Cached shell shows exact offline copy, both warnings, and only synthetic fixtures                                                        | Unit/integration test plus installed-device offline check                         |
| MPX-23 | Unavailable              | Unavailable state shows exact approved copy and no empty operational-looking screen                                                      | Unit/integration test plus cold-offline check where platform permits              |
| MPX-24 | Stale update             | Exact stale copy appears; no activation/reload occurs before explicit button selection                                                   | `SwUpdate` unit test and HTTPS preview update check                               |
| MPX-25 | Accessibility            | Landmarks, headings, focus order, live regions, contrast, text zoom, reduced motion, and screen-reader labels meet this contract         | Automated a11y checks plus keyboard, VoiceOver, and TalkBack review               |
| MPX-26 | No integrations          | No `HttpClient`, `fetch`, WebSocket, API URL, notifications, auth library, credential store, database, audio, telemetry, or private data | Dependency/source/cache/secret scans                                              |
| MPX-27 | Shell-only cache         | Existing application-shell asset group remains; no `dataGroups` or external URLs are added                                               | Source and generated `ngsw.json` inspection                                       |
| MPX-28 | Scope                    | Implementation diff, if later authorized, stays within the exact proposed web surface and does not edit contracts or deployment config   | Git diff and status inspection                                                    |

Physical iPhone Safari and Android Chrome checks are mandatory for MPX-10,
MPX-18 through MPX-20, and the device portions of MPX-22 through MPX-25. They
cannot be replaced by desktop emulation alone.

## Security limitations

- The welcome/login-screen styling is not authentication. It offers no
  confidentiality, identity proof, authorization, access control, or audit
  trail.
- Direct routes, browser Back, source inspection, and developer tools can
  bypass the welcome screen by design because there is nothing to protect in
  this synthetic prototype.
- Client-side route guards, hidden links, local PINs, hard-coded passwords,
  browser storage, and obfuscated JavaScript would not fix that limitation and
  are forbidden substitutes for server-side authorization.
- The PWA service worker caches a public synthetic shell. Cache availability
  is not authorization, and cached UI may be stale.
- `navigator.onLine` describes browser connectivity only. It does not prove
  availability or freshness of CAD, radio, an API, or any server.
- Standalone mode can hide the URL bar, so both warnings must remain in the
  application chrome.
- HTTPS authenticates the hosting origin, not GFD, CAD, dispatch, or the
  correctness of displayed fixture content.
- Build identifiers must not include secrets, private dataset identifiers,
  internal hostnames, or credentials.
- This public synthetic design cannot be reused for private transcripts,
  recordings, real incidents, or operational unit state without new data,
  identity, authorization, retention, caching, and threat-model contracts.

## Unresolved questions

These questions do not block this documentation milestone, but they must be
resolved before any affected implementation behavior is approved:

1. Should a future PWA-contract amendment change the installed launch from
   `/` to `/mobile/welcome`, or should the installed app continue opening the
   desktop board with a separate explicit mobile entry? Step 7C preserves `/`.
2. How should users discover `/mobile/welcome` without adding a link to the
   existing desktop board? No board change is approved here.
3. What compile-time version format should Settings show: package version,
   short commit identifier, build date, or a reviewed combination? It must not
   require a runtime endpoint.
4. Should widths from 768px through 899px use the phone shell or a future
   tablet design? This contract centers the phone shell until a tablet
   contract exists.
5. Should the navigation-only action continue to be labeled `Log out of
prototype`, or should implementation use the less auth-like `Exit
prototype`? The acceptance copy currently requires the former plus an
   explicit no-session explanation.
6. Which identity provider, account lifecycle, roles, server-side
   authorization policy, revocation model, and audit requirements would apply
   to a future real authentication contract? None is selected or implied.

## Step 7C validation checklist

- [ ] Only this documentation file is added.
- [ ] Baseline, clean-tree, and `origin/main` equality are recorded.
- [ ] Existing PWA and board contracts are not edited.
- [ ] Root/manifest constraints and the explicit mobile-route consequence are
      documented.
- [ ] Desktop layout and route are preserved.
- [ ] Both warnings are exact and mandatory on every phone screen/state.
- [ ] Welcome/login design is visibly a prototype and has no credential
      controls or security claim.
- [ ] Five navigation destinations and all screen contents are defined.
- [ ] Fixtures, roster order, channels, and evidence meaning remain exact.
- [ ] Breakpoints, safe areas, standalone mode, states, and accessibility are
      defined.
- [ ] Proposed routes and file surface are exact.
- [ ] Acceptance tests distinguish automated and physical-device checks.
- [ ] Security limitations and unresolved decisions are explicit.
- [ ] No deployment, staging, commit, push, or implementation occurs.
