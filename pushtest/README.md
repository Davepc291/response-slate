# iPhone Push Test preparation (Part 2A, corrected in Part 2B)

Local, non-deployed preparation for exactly one controlled, synthetic Web
Push notification, per the isolated-origin design (`pushtest.gfrapp.com`,
Option B) agreed in Part 1. Nothing in this directory is wired into
production. DNS, Caddy, and deployment changes are explicit future steps,
not part of this preparation.

## What's here

- `web/` — the minimal, static, iPhone-installable test page: a manifest,
  a service worker (`push` + `notificationclick` only), and `index.html`
  with an explicit Subscribe button and a password-type token input. No
  build step; served as plain files.
- `Caddyfile.pushtest.template` — the site block this origin would need,
  additive alongside (never replacing) the existing `app.gfrapp.com` block.
  Not applied anywhere.
- The subscription receiver lives in the Go backend, not here: see
  `backend/cmd/pushtest-receiver`.

## Why the subscription is never shown on screen

`index.html` `fetch()`s the subscription straight to `/submit` (same-origin
once deployed, so no CORS or mixed-content handling is needed), and the
receiver writes it directly to one local file. The endpoint, `p256dh`, and
`auth` values never appear in the page, a log, a console, or any report at
any point.

## The receiver token is never stored in any served file (Part 2B correction)

Part 2A's own security review found that `config.js` — a plain static file
Caddy would serve to any visitor, unauthenticated, once deployed — is not
a safe place for a secret: anyone who requests it (or just views page
source) could read the token. `config.js` now holds only the VAPID
**public** key, which is designed to be public. The receiver token is
instead typed by hand into a password-type `<input>` on the page itself,
at test time, on the phone, each time. It is:

- held in a page-memory variable only for the duration of one submit
  attempt;
- cleared (`tokenEl.value = ''`) on every exit path — success, every
  rejection, and any exception;
- never written to `localStorage`, `sessionStorage`, a cookie, or a URL;
- never logged to the console or included in any on-page status string.

The input also sits outside any `<form>` and sets `autocomplete="off"`, to
reduce (not guarantee — browser autofill policy isn't fully under a page's
control) the chance a browser offers to save it.

## The receiver authenticates before checking single-use state (Part 2B correction)

`pushtest-receiver`'s `/submit` handler now checks the bearer token first.
An unauthenticated request gets an identical 401 whether or not the
receiver has already been used — it can no longer distinguish those two
states without first proving it holds the token.

## Local dry run (no deployment, no real reachability from a phone yet)

This can be exercised entirely on one machine today, without DNS or Caddy,
to prove the pieces fit together — a real phone can't reach `127.0.0.1`,
so this is a mechanics check, not the end-to-end test:

1. Copy `web/config.example.js` to `web/config.js` and fill in the VAPID
   public key (not generated yet — see below). Do not add a token field.
2. `cd backend && GFR_PUSHTEST_RECEIVER_TOKEN=<a long random value> go run ./cmd/pushtest-receiver -out ../../pushtest-local-test.json -confirm`
   — see `backend/cmd/pushtest-receiver`'s own `-h` output for Windows ACL
   guidance on where to point `-out`.
3. Serve `web/` locally over HTTPS on this machine (for example, the
   existing `npm run local-auth` pattern adapted to this directory, or any
   other local static HTTPS server) and open it in a desktop browser.
   Type the *same* token into the page's own input field when prompted.

## What's still needed before a real iPhone test (not done in Part 2A/2B)

1. Decide to proceed with DNS + Caddy for `pushtest.gfrapp.com` (explicit,
   separate authorization — this part only prepares the template).
2. Generate the dev-only VAPID key pair (`cmd/vapid-keygen`) — not done
   yet.
3. Generate a receiver token (any long random value) and set it as
   `GFR_PUSHTEST_RECEIVER_TOKEN` when starting the receiver. It is never
   written to any file in `pushtest/web/` — only typed into the page.
4. Apply the Caddy template, including setting
   `$env:GFR_PUSHTEST_FRONTEND_ROOT` in the same session that starts/
   reloads Caddy (see the template's own corrected comments — this was
   previously undocumented), and create the DNS record.
5. Run `pushtest-receiver`, open the installed PWA on the iPhone, tap
   Subscribe, and type the token in when prompted.
6. Import with `sandbox-device` (guarded by the dev-database identity
   check — see `backend/cmd/sandbox-device`), then send with
   `sandbox-push`, using the *same* VAPID key pair as step 2.
