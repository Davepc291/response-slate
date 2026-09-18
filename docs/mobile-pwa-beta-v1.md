# Mobile / PWA Beta Contract v1 - Step 7A / Step 7B

Status: Approved minimal contract for a future installable synthetic board
preview.

Implementation status: Not started. Step 7A creates this document only.

Contract identifier: `mobile-pwa-beta-v1`.

Reviewed baseline: `c0622010b53d9f5220581bf10545fa06f2d265bc` (`c062201`).
The working tree was clean and HEAD and local `origin/main` pointed to that
commit. Green CI is supplied baseline context, not a claim of operational safety.

## Purpose and milestone boundary

Approve the smallest later-mobile milestone that can ship an **installable
Angular PWA** of the existing SHADOW / REPLAY board preview. The first beta is
frontend-only and synthetic. It is a packaged copy of the current board, not a
live CAD client, not a radio receiver, and not a Go API consumer.

Step 7A is this contract milestone. Step 7B is the future PWA implementation
milestone. A Vercel HTTPS preview is part of the approved plan and is **not**
executed by Step 7A. Deployment requires a separate later instruction after
Step 7B.

This is the product-requirements “later mobile milestone” (roadmap item 7),
narrowed to a synthetic beta. It does not authorize the roadmap’s earlier
“read-only live interface” (roadmap item 5), operational readiness (item 6),
authentication, notifications, or CAD authority. Historical AT-19, which
records that PWA installation is absent from the initial interface milestone,
is not edited. This document is the later exception; do not revise
[product requirements](product-requirements.md) to fit the beta.

The [project README](../README.md) remains future product direction, not
permission to add backend integrations here.

This document approves the design, not implementation in Step 7A. Step 7B
requires a separate instruction to begin. No implementation files,
existing-file changes, staging, commits, deploys, or pushes are part of
Step 7A.

## Non-goals

The first beta must not:

- Connect the Go API, PostgreSQL, WebSockets, SDRTrunk, Whisper, Icecast, live
  radio, or any recording store.
- Load, import, replay, or display private review-export JSONL, `Result.Pairs`,
  dataset IDs, fingerprints, or real evaluation summaries.
- Create incidents, mutate unit state, or present proposed associations as
  current apparatus state.
- Add authentication, authorization, user accounts, push notifications, SMS,
  email, geolocation, background sync, or share-target handlers.
- Add a map, police monitoring, or operational out-of-service controls.
- Cache future private transcripts, incident data, credentials, or radio audio.
- Change shadow processor, replay, import, or evaluation contracts.
- Claim official Greenwich Fire Department status or live CAD authority.

A later live board, if ever approved, needs its own contract. This beta is not
that board.

## Authoritative dependencies

- [Product requirements](product-requirements.md), FR-2, FR-7, AT-15, AT-16,
  AT-19, and roadmap item 7.
- [Shadow processor contract](shadow-processor-v1.md).
- [Shadow replay contract](shadow-replay-v1.md).
- [Shadow replay evaluation contract](shadow-replay-evaluation-v1.md).
- [Current board fixtures](../web/src/app/board-preview/board-preview.fixtures.ts).
- [Current board template](../web/src/app/board-preview/board-preview.html).
- [Current routes](../web/src/app/app.routes.ts).
- [Current application config](../web/src/app/app.config.ts).
- [Current index document](../web/src/index.html).
- [Current global styles](../web/src/styles.scss).
- [Angular project config](../web/angular.json).
- [Web package manifest](../web/package.json).

The completed board preview at `c062201` is the visual and fixture baseline.
Channel and TGID remain evidence labels only. `ShadowOnly` and `PairingIntact`
remain descriptive fixture indicators, not security boundaries. `both_resolved`
is still not proof of one incident; the current synthetic incident keeps
address resolved and call type unresolved.

Historical approved contracts may still say Phase 7 has not begun. That
wording is not a reason to edit them. This document is the Phase 7 beta
contract; it changes none of those files.

## Existing surface that must be preserved

Step 7B may add PWA packaging around the current preview. It must not change
the meaning of board data.

| Surface | Required preservation |
| --- | --- |
| Warning banner | Exact text `SHADOW / REPLAY — NOT LIVE CAD` remains visible on browser, installed standalone, online, and offline shells |
| Synthetic warning | Exact text `Synthetic replay preview. No operational authority.` remains visible in those same states |
| Header | `Greenwich Fire Responder V3` |
| Desktop layout | Approximately 68% primary area and 32% details area; no map; no page scrolling at about 1440×900 and larger |
| Phone layout | Compact stacked panels; roster rows stay compact |
| Roster order | `DC`, `E2`, `E3`, `E4`, `E5`, `SQ1`, `SQ8`, `T1` |
| Channel labels | `CH1A`, `CH2B`, `CH3B`, `CH4C` as evidence labels only |
| Fixture data | Only the existing clearly synthetic board fixtures (`100 TEST STREET`, `SYN-PENDING-01`, `SYN-INCIDENT-01`, synthetic raw-model and human-reference phrases) |
| Clock | Device-local clock under the roster; not a CAD or server clock |
| Out-of-service section | Remains a labeled synthetic, non-operational preview |

Root route `''` continues to render the board preview. There is still no health
page. Do not add API-backed routes.

## Approved future files

The Step 7B implementation is limited to the smallest Angular PWA packaging
set plus a Vercel static-preview config. Expected files, created only when
Step 7B is separately authorized:

```text
web/ngsw-config.json
web/public/manifest.webmanifest
web/public/icons/icon-192.png
web/public/icons/icon-512.png
web/public/icons/icon-512-maskable.png
web/public/icons/apple-touch-icon.png
web/src/app/pwa-status/pwa-status.ts
web/src/app/pwa-status/pwa-status.html
web/src/app/pwa-status/pwa-status.scss
web/src/app/pwa-status/pwa-status.spec.ts
vercel.json
```

Minimal edits of existing web files are allowed only where required to
register the manifest, theme color, Apple home-screen metadata, service
worker, and the offline/update indicator:

- `web/src/index.html`
- `web/src/app/app.config.ts`
- `web/src/app/app.html` or the board-preview host template, solely to mount
  the status indicator
- `web/angular.json` if the application builder requires an explicit
  `serviceWorker` / `ngswConfig` path
- `web/package.json` / lockfile only for `@angular/service-worker` aligned to
  the existing Angular 22.1.x line

No other dependency is approved. If `@angular/service-worker` cannot be added
without a broader upgrade or a second package, stop and report. Do not add
Workbox, Firebase, Capacitor, Cordova, Ionic, OneSignal, or a custom service
worker runtime.

Step 7A creates none of those files. Step 7B must not modify backend,
database, contracts, migrations, Docker, audio, or operations files. If an
incompatibility is discovered, stop and report it instead of changing shadow
contracts or inventing live data loading.

## Proposed API and configuration surface

No HTTP application API is approved. The only new runtime surface is the
browser PWA configuration below.

### Web app manifest

`web/public/manifest.webmanifest` is the install metadata. Required fields:

| Field | Approved value |
| --- | --- |
| `name` | `Greenwich Fire Responder V3 — SHADOW / REPLAY` |
| `short_name` | `GFR V3 SHADOW` |
| `description` | `Synthetic replay preview. No operational authority. NOT LIVE CAD.` |
| `start_url` | `/` |
| `scope` | `/` |
| `id` | `/shadow-replay-preview` |
| `display` | `standalone` |
| `background_color` | `#0b0f14` |
| `theme_color` | `#0b0f14` |
| `orientation` | `any` |
| `lang` | `en` |
| `icons` | 192 PNG, 512 PNG, and 512 maskable PNG; purpose `any` or `maskable` as appropriate |

`short_name` must keep `SHADOW`. Do not use `CAD`, `Live`, `Dispatch`,
`Greenwich Fire Department`, or official-seal artwork. Icons must be original
synthetic marks, not GFD badges.

`display_override`, `share_target`, `file_handlers`, `protocol_handlers`,
`protocol`, and `prefer_related_applications` are not approved.
`related_applications` must not point at an operational CAD.

### HTML install metadata

`index.html` may add, without changing the visible board copy:

- `link rel="manifest"` to `/manifest.webmanifest`
- `meta name="theme-color"` content `#0b0f14`
- `meta name="apple-mobile-web-app-capable"` content `yes`
- `meta name="apple-mobile-web-app-title"` content `GFR V3 SHADOW`
- `link rel="apple-touch-icon"` to the 180×180 (or 192) PNG
- `meta name="apple-mobile-web-app-status-bar-style"` content `black-translucent`

Do not set a title that omits SHADOW / REPLAY. The existing document title
`Greenwich Fire Responder V3 — SHADOW / REPLAY` stays.

### Service worker registration

Future `app.config.ts` may add Angular’s service-worker provider only:

```ts
provideServiceWorker('ngsw-worker.js', {
  enabled: !isDevMode(),
  registrationStrategy: 'registerWhenStable:30000',
})
```

Registration is production/preview-build only. `ng serve` development remains
unregistered so local board work does not cache aggressively. No
`provideHttpClient`, interceptor, WebSocket factory, or environment API URL is
approved.

The optional `pwa-status` component may inject Angular `SwUpdate` and read
`navigator.onLine`. It must not fetch application data. Approved user-visible
strings:

| State | Exact indicator text |
| --- | --- |
| Offline shell | `Offline. Synthetic replay preview only. NOT LIVE CAD.` |
| Preview unavailable (first visit with no cache, or failed HTTPS fetch of the shell) | `Preview unavailable. Synthetic board is not loaded. NOT LIVE CAD.` |
| Newer hashed build detected | `Stale preview. A newer synthetic build is available. NOT LIVE CAD.` |

The stale-preview control may include a button labeled `Reload synthetic preview`.
Reload happens only after that explicit action. Silent auto-reload is
forbidden; it would resemble a live CAD refresh.

### `ngsw-config.json`

Only an application-shell asset group is approved. No `dataGroups`.

```json
{
  "index": "/index.html",
  "assetGroups": [
    {
      "name": "app-shell",
      "installMode": "prefetch",
      "updateMode": "prefetch",
      "resources": {
        "files": [
          "/favicon.ico",
          "/index.html",
          "/manifest.webmanifest",
          "/icons/**",
          "/*.css",
          "/*.js"
        ]
      }
    }
  ]
}
```

Hashed JS/CSS produced by the current production `outputHashing: all` setting
are the intended precache targets. `navigationUrls` must not be expanded to
external origins. Do not add `externalUrls` or runtime caching of any URL
outside the Vercel deployment origin.

Forbidden cache contents, including if a later developer adds routes:

- Transcript text, JSONL, evaluation `Result.Pairs`, dataset IDs, fingerprints
- Incident or unit-state API payloads
- Credentials, cookies intended for API auth, authorization headers
- Radio audio, Icecast, Whisper, SDRTrunk, or recording files
- `blob:`, `data:` audio, and any `/api` path

Absence of those resources in v1 is not permission to cache them later. A
future live board needs a new caching contract.

### Angular CLI / builder

Current [angular.json](../web/angular.json) copies `public/` assets and has no
service-worker entry. Step 7B may set the application-builder service-worker
option to `ngsw-config.json` on the production configuration only. Do not
enable the worker on the development serve target.

Current [package.json](../web/package.json) has Angular 22.1.x, no
`@angular/service-worker`, no HTTP client usage in application code, and no
PWA scripts. The only new npm package approved is `@angular/service-worker` at
the same major/minor as `@angular/core`.

### Vercel static preview

Approved root `vercel.json` shape:

```json
{
  "buildCommand": "npm ci --prefix web && npm run build --prefix web",
  "outputDirectory": "web/dist/web/browser",
  "rewrites": [{ "source": "/((?!ngsw\\.json|ngsw-worker\\.js|manifest\\.webmanifest).*)", "destination": "/index.html" }],
  "headers": [
    {
      "source": "/index.html",
      "headers": [{ "key": "Cache-Control", "value": "no-cache" }]
    },
    {
      "source": "/ngsw.json",
      "headers": [{ "key": "Cache-Control", "value": "no-cache" }]
    },
    {
      "source": "/:file(.*)\\.(js|css)",
      "headers": [{ "key": "Cache-Control", "value": "public, max-age=31536000, immutable" }]
    }
  ]
}
```

If Vercel’s Angular preset conflicts with this output path, stop and report
rather than adding serverless functions. No Vercel Serverless, Edge, KV,
Postgres, Blob, Cron, or environment secrets are approved.

Framework: static output of `ng build` default production configuration.
Output directory follows the current builder: `web/dist/web/browser`.

## Platform support

The beta is a **browser PWA**, not a native App Store or Play Store app.

| Platform | Required support |
| --- | --- |
| Android Chrome | Install / Add to Home Screen from the HTTPS preview; standalone display; service worker controls the origin after first successful load |
| iPhone Safari | Add to Home Screen using the share sheet; standalone display; `apple-touch-icon` and `apple-mobile-web-app-*` metadata present |
| iPhone Chrome / Android Safari-class browsers | Best-effort: the same HTTPS URL and manifest must remain usable as a browser tab even if A2HS UI differs |

Documented limitations, not silent failures:

- iOS versions before Safari 16.4 may install a home-screen bookmark without
  full service-worker offline behavior. The offline indicator must still tell
  the user the preview is unavailable or not cached. That limitation is not a
  pass for “offline iPhone on old iOS.”
- Desktop Chromium install is allowed but not the acceptance target.
- Firefox Android installability is best-effort.

Add to Home Screen must open the synthetic board with both required warnings
visible without extra navigation. The installed name must remain
`GFR V3 SHADOW` / `Greenwich Fire Responder V3 — SHADOW / REPLAY`.

Do not implement `beforeinstallprompt` flows that hide warnings, auto-prompt
on first paint, or style the install UI as an operational CAD download.

## HTTPS beta URL

The approved first beta URL is a **normal Vercel HTTPS preview**
(`https://*.vercel.app` or the project’s Vercel preview hostname). It must
use HTTPS. Local `http://localhost` is for development only and is not the
beta.

The URL and page metadata must not impersonate Greenwich Fire Department,
live CAD, or an internal dispatch terminal. No custom vanity domain is
approved in this contract. If a later instruction adds a custom domain, it
must still contain a non-operational label and requires a separate review.

The preview is public synthetic content. It still must not include private
transcripts or credentials. Product-requirements language about authentication
before shared deployment of recordings and audit data does not apply to this
synthetic-only frontend; that is not permission to publish private evidence.

## Update and stale-version handling

`ngsw.json` must be served with `Cache-Control: no-cache` so a new Vercel
deployment can be discovered.

On `SwUpdate.versionUpdates` of type `VERSION_READY` or the Angular 22
equivalent, show the stale-preview indicator. The previously cached synthetic
shell may remain visible until the user reloads. That cached shell is still
synthetic fixtures, not live CAD, but it is **stale packaging** and must be
labeled as such.

After reload, the board still shows the same fixture meaning unless a later
authorized change updates fixtures. Step 7B must not use an update cycle to
swap in private or live data.

If `versionInstallationFailed`, keep the last good synthetic shell and show
preview unavailable or stale text. Do not retry against a backend.

Service-worker `skipWaiting` / `activateUpdate` may run only as part of the
explicit reload action.

## Offline and unavailable indicators

After a successful first HTTPS load, the application shell and hashed assets
may be available offline. Offline mode is **cached synthetic UI**, not an
operations mode.

Rules:

- Offline and unavailable banners must include `NOT LIVE CAD`.
- The local clock may continue; that is not proof of connectivity or CAD
  freshness.
- First visit while offline, with no precache, shows preview unavailable, not
  an empty operational board.
- Do not invent a “last heard radio” or “last incident time” from cache.
- Do not queue user actions for later sync; there are no user mutations.

`navigator.onLine` is an indicator input only. A false online value must not
be used to fetch incidents.

## Vercel HTTPS preview deployment plan

This plan is approved documentation. Step 7A deploys nothing. A later
instruction may execute it after Step 7B exists.

1. Import the GitHub repository into Vercel as a static project.
2. Root directory: repository root, using the `vercel.json` above.
3. Install/build: `npm ci --prefix web` then `npm run build --prefix web`.
4. Output: `web/dist/web/browser`.
5. Environment variables: none.
6. Serverless functions: none.
7. Deployment protection: optional Vercel standard protection is allowed only
   if it does not require application-level auth code. The contract prefers an
   ordinary preview URL so iPhone Safari Add to Home Screen can be tested
   without a native login page. If protection is enabled, document the
   limitation; do not add Angular login.
8. Production alias is optional and not required for the first beta. Preview
   deployments are sufficient.
9. Confirm the preview serves `manifest.webmanifest`, `ngsw-worker.js`, and
   `ngsw.json` as static files, not the SPA `index.html` fallback.
10. Confirm response is HTTPS and the visible board still shows both required
    warnings, the 68% / 32% desktop split, stacked phone layout, roster order,
    and channel evidence labels.

Do not attach Icecast, Whisper, API, or database add-ons. Do not upload
recordings. Do not store secrets in Vercel.

## Rollback and removal procedure

Rollback and removal must leave testers with either an older **synthetic**
preview or no app, never a live-looking remnant.

### Rollback

1. In Vercel, Instant Rollback to the previous successful preview/production
   deployment of this same synthetic frontend, or redeploy the last known
   good git SHA that contains only the synthetic PWA.
2. Confirm `ngsw.json` of the rolled-back build is reachable with `no-cache`.
3. Open the HTTPS URL on a device that had the stale worker; use `Reload
   synthetic preview` or an equivalent documented hard refresh so the worker
   activates the rolled-back shell.
4. Confirm both required warnings and synthetic fixture labels are present.
5. Do not roll back onto a build that talks to the Go API, even if one exists
   on another branch.

### Removal

1. Deploy, if needed, a one-page removal shell whose only job is to show
   `This SHADOW / REPLAY preview has been removed. NOT LIVE CAD.` and to
   unregister the service worker. That tombstone is a separately authorized
   tiny frontend; it is not a CAD status page.
2. Disable or delete the Vercel project so the HTTPS hostname stops serving
   the board.
3. Instruct testers to delete the home-screen icon and clear site data for
   the Vercel origin (Safari: remove from Home Screen, then clear website
   data; Chrome Android: Site settings → Clear & reset).
4. Confirm `navigator.serviceWorker.getRegistrations()` is empty on a test
   device after that procedure.
5. Remove any Vercel domain assignment. Do not leave an unmaintained
   standalone icon that still opens a cached board without a removal notice
   if testers skip step 3; the tombstone in step 1 exists for that case.

Cached synthetic fixtures are not private evidence, but an abandoned
installed icon is still a misrepresentation risk. Removal is complete only
when the hostname is gone and the worker is unregistered or serving the
tombstone.

## Security limitations

The PWA packaging does not create a security boundary.

- `ShadowOnly` in fixtures, warning banners, and manifest names are
  descriptive. Package structure (no API client, no `dataGroups`, no secrets)
  must keep the beta synthetic.
- Service-worker caching can retain UI after the origin is updated; stale
  labeling and the removal procedure are mandatory because cache is not
  access control.
- A public Vercel URL can be shared. That is acceptable only because the
  payload is synthetic. It is not acceptable for private JSONL, recordings,
  or credentials.
- Installed standalone mode can hide the browser URL bar. Warnings in the
  page chrome must therefore remain in the application layout, not only in
  the tab title.
- HTTPS on Vercel authenticates Vercel’s certificate, not GFD, and not CAD.
- Add to Home Screen is not an app-store review and is not authorization
  for emergency use.
- `.gitignore` and Vercel build exclusion are not encryption.

## Prohibitions

Step 7B and any later preview deploy must not:

- Execute live audio, FFmpeg, playback, Whisper, Icecast, or SDRTrunk.
- Open WebSockets or HTTP clients to the Go API or any incident source.
- Read or write PostgreSQL or any store.
- Register push notifications or notification permission prompts.
- Add authentication, OAuth, or API keys.
- Cache or display private transcripts, `Result.Pairs`, dataset IDs, or
  fingerprints.
- Represent `both_resolved` as one-incident proof or proposed unit/status
  association as current apparatus state.
- Add maps, police monitoring, or operational OOS controls.
- Ship a second service worker, Workbox config, or CDN cache of audio.
- Treat Vercel analytics, speed insights, or web beacons as required; they
  are not part of this contract and must not be added unless separately
  approved without sending board fixture text.

## Required Step 7B verification

All fixtures remain the existing synthetic board fixtures. Private datasets
and live radio are not used.

| ID | Case | Pass |
| --- | --- | --- |
| PWA-01 | Shadow/replay warning | Visible exact text `SHADOW / REPLAY — NOT LIVE CAD` in browser and standalone |
| PWA-02 | Non-operational warning | Visible exact text `Synthetic replay preview. No operational authority.` |
| PWA-03 | Roster order | `DC`, `E2`, `E3`, `E4`, `E5`, `SQ1`, `SQ8`, `T1` |
| PWA-04 | Channel evidence | `CH1A`, `CH2B`, `CH3B`, `CH4C` labeled evidence only |
| PWA-05 | Desktop layout | About 68% / 32% split; no map; no page scroll at 1440×900 |
| PWA-06 | Phone layout | Compact stacked panels on an iPhone-width viewport |
| PWA-07 | Synthetic fixtures | `100 TEST STREET`, `SYN-PENDING-01`, `SYN-INCIDENT-01`; no private transcript text |
| PWA-08 | Manifest | Required name, short_name containing `SHADOW`, colors, start_url `/`, standalone display, icons |
| PWA-09 | No live claims | No “official CAD”, “live incident”, or “current apparatus state” except the existing negations |
| PWA-10 | No application HTTP/WebSocket | Production source has no `HttpClient` API calls, no WebSocket, no environment backend URL |
| PWA-11 | Shell cache only | `ngsw-config.json` has assetGroups for the shell and no dataGroups |
| PWA-12 | Forbidden cache | Config and generated `ngsw.json` list no `/api`, audio, JSONL, or credential URLs |
| PWA-13 | Update | A new hashed build surfaces the stale-preview indicator; reload is explicit |
| PWA-14 | Offline after first load | Cached shell loads with the offline indicator and both CAD warnings |
| PWA-15 | Unavailable | First visit offline, or missing shell, shows preview-unavailable text, not a blank operational board |
| PWA-16 | Android Chrome A2HS | Installs; standalone opens the synthetic board |
| PWA-17 | iPhone Safari A2HS | Add to Home Screen; standalone opens the synthetic board; Apple metadata present |
| PWA-18 | HTTPS preview plan | `vercel.json` matches this contract; output is `web/dist/web/browser`; no functions or secrets |
| PWA-19 | Existing board tests | Current board-preview unit tests still pass unchanged in meaning |
| PWA-20 | Scope inspection | Diff stays under `web/` plus root `vercel.json`; no backend/contract edits |

PWA-16 and PWA-17 are human device checks on the HTTPS preview when a later
deploy instruction exists. Step 7B without deploy must still prove PWA-01
through PWA-15, PWA-18 through PWA-20 with unit tests, config inspection, and
local production-build serving over HTTPS if a preview URL is not yet
authorized.

Do not add tests that require private review export, live audio, network
credentials, or the Go API.

## Read-only review checklist

- [ ] Step 7B not started from this Step 7A document alone.
- [ ] Both required warnings remain exact and visible in standalone.
- [ ] Desktop 68% / 32% and phone stacking preserved.
- [ ] Roster order and channel evidence labels preserved.
- [ ] Fixtures remain synthetic; no private evidence.
- [ ] Manifest short name contains `SHADOW`; icons are not official seals.
- [ ] Service worker precaches only the application shell.
- [ ] No dataGroups, API, WebSocket, audio, or credential caching.
- [ ] Offline, unavailable, and stale indicators include `NOT LIVE CAD`.
- [ ] Reload of a new version is explicit, not silent.
- [ ] Vercel plan is static HTTPS only; no secrets or functions.
- [ ] Rollback/removal procedure reviewed before any public preview.
- [ ] No Go API, PostgreSQL, SDRTrunk, Whisper, Icecast, auth, notifications,
      or CAD authority.

## Unresolved questions

These do not block this contract:

1. Whether Vercel Deployment Protection is used on the first preview URL.
2. Whether a later tombstone deployment is a second tiny app or a board-route
   replacement; either must unregister the worker and keep `NOT LIVE CAD`.
3. iOS versions older than Safari 16.4 remain a documented service-worker
   limitation.
4. A custom domain, app-store wrap, push notifications, or live API board
   each require a new contract.
