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

## Where the dev-only credentials live, and how tools load them (Part 3D)

The dev-only VAPID key pair and the receiver token are generated locally
and stored as `KEY=VALUE` lines in one file under an operator-controlled,
ACL-restricted directory outside this repository entirely:

```
%LOCALAPPDATA%\GreenwichFireResponder\pushtest-secrets\pushtest-dev-credentials.env
```

That directory's ACLs are set with `icacls <dir> /inheritance:r
/grant:r "<account>:(OI)(CI)F"` — inherited permissions removed, access
granted only to the one account that created it.

Neither `sandbox-push` nor `pushtest-receiver` accepts any of these values
as a command-line flag (both structurally refuse a `-token`/`-vapid-*`
flag — see each tool's own `TestParseFlagsRejects*Flag` test). Both read
them only from the process environment. The safe way to load them into one
PowerShell session, immediately before running either tool, without ever
printing a value to the screen or writing it into shell history, is to
loop over the file's own `KEY=VALUE` lines and set each as an environment
variable directly:

```powershell
Get-Content "$env:LOCALAPPDATA\GreenwichFireResponder\pushtest-secrets\pushtest-dev-credentials.env" |
    Where-Object { $_ -match '^[A-Z_]+=' } |
    ForEach-Object {
        $name, $value = $_ -split '=', 2
        Set-Item -Path "Env:$name" -Value $value
    }
```

This sets `GFR_NOTIFY_VAPID_PUBLIC_KEY`, `GFR_NOTIFY_VAPID_PRIVATE_KEY`,
and `GFR_PUSHTEST_RECEIVER_TOKEN` in that shell's own environment only —
never echoed, never persisted to a user/system environment variable,
never written anywhere else. Close the shell (or `Remove-Item Env:GFR_*`)
when done for the session.

### `GFR_NOTIFY_VAPID_SUBJECT` — required by `sandbox-push`, not a secret, not generated for you

`sandbox-push` also requires `GFR_NOTIFY_VAPID_SUBJECT` (RFC 8292's "sub"
claim). This is **not a credential** — it identifies a real contact point
the push service can reach if it needs to reach the sender — so it was
deliberately not generated or invented as part of Part 3D. `internal/
notifywebpush.validSubject` accepts exactly one of two shapes:

- a `mailto:` address matching `mailto:local@domain.tld` (simple
  format-only check, no DNS/MX lookup); or
- an absolute `https://` URL with a non-empty host.

Max length 320 bytes. **The operator must supply their own real value**
(for example an address or page they actually control) when setting this
variable before running `sandbox-push` — it is not stored in the
credentials file above and has no placeholder here.

## Local dry run (no deployment, no real reachability from a phone yet)

This can be exercised entirely on one machine today, without DNS or Caddy,
to prove the pieces fit together — a real phone can't reach `127.0.0.1`,
so this is a mechanics check, not the end-to-end test:

1. `web/config.js` already exists locally (generated in Part 3D) with only
   the dev VAPID public key. Do not add a token field to it.
2. Load `GFR_PUSHTEST_RECEIVER_TOKEN` into the current shell using the
   loop above, then, from `backend/`:
   ```
   go run ./cmd/pushtest-receiver -out "$env:LOCALAPPDATA\GreenwichFireResponder\pushtest-secrets\received\subscription.json" -confirm
   ```
   **Never use a repository-relative `-out` path** (an earlier version of
   this doc incorrectly suggested `../../pushtest-local-test.json`, which
   from `backend/` resolves to the repository root — inside Git). The path
   above reuses the same ACL-restricted, outside-the-repo directory Part 3D
   already created for credentials; create the `received\` subdirectory
   first if it doesn't exist yet (it inherits that directory's restricted
   ACL). See `backend/cmd/pushtest-receiver`'s own `-h` output for the full
   Windows ACL guidance.
3. Serve `web/` locally over HTTPS on this machine (for example, the
   existing `npm run local-auth` pattern adapted to this directory, or any
   other local static HTTPS server) and open it in a desktop browser.
   Type the *same* token into the page's own input field when prompted.

## What's still needed before a real iPhone test (updated after Part 3D)

1. Decide to proceed with DNS + Caddy for `pushtest.gfrapp.com` (explicit,
   separate authorization — a validated candidate exists, not yet applied;
   see the Part 3B/3C reports).
2. ~~Generate the dev-only VAPID key pair~~ — done (Part 3D): stored in
   `%LOCALAPPDATA%\GreenwichFireResponder\pushtest-secrets\
   pushtest-dev-credentials.env`; the public half is already in
   `web/config.js` (gitignored, local only).
3. ~~Generate a receiver token~~ — done (Part 3D), stored in the same
   credentials file. Load it into a shell with the loop above before
   starting the receiver each time; it is never written to any file in
   `pushtest/web/` — only typed into the page.
4. Apply the Caddy template, including setting
   `$env:GFR_PUSHTEST_FRONTEND_ROOT` in the same session that starts/
   reloads Caddy (see the template's own corrected comments), and create
   the DNS record.
5. Run `pushtest-receiver`, open the installed PWA on the iPhone, tap
   Subscribe, and type the token in when prompted.
6. Import with `sandbox-device` (guarded by the dev-database identity
   check — see `backend/cmd/sandbox-device`), then send with
   `sandbox-push`, using the *same* VAPID key pair from step 2.
