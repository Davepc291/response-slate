# Windows production deployment (Step 9F-7)

Production identity:

- App origin: `https://app.gfrapp.com`
- WebAuthn RP ID: `gfrapp.com`
- RP display name: `Greenwich Fire Responder V3`

## Topology

```
Internet ──443/80──> Caddy (0.0.0.0:443/80, Windows, Task Scheduler)
                        │
                        ├─ /api/* ──> Go API  (127.0.0.1:8080, loopback only)
                        │                       │
                        │                       └─> Postgres (compose.prod.yaml,
                        │                                      127.0.0.1:5442, separate
                        │                                      from the 5432 dev instance)
                        └─ /*     ──> static files under the deployed Angular build,
                                       served by Caddy with SPA fallback to index.html
```

`web/vercel.json` is left in place and unused for now (9F-7C decision 3): this
deployment is the Windows + Caddy production path, not Vercel.

## Process management convention

Both the API and Caddy are managed the same way as the existing
[Windows Whisper runtime](../ops/windows/whisper/README.md): a per-user Task
Scheduler logon task, `Install-/Start-/Stop-/Status-/Uninstall-/Test-*.ps1`
scripts, `-WhatIf` support on every mutating script, strict ownership checks
before any scheduled task is touched, and a dedicated
`%LOCALAPPDATA%\GreenwichFireResponder\<Component>` data directory per
runtime. No NSSM or other third-party service wrapper is used, matching the
whisper precedent.

Two differences from the whisper runtime, and why:

- **No C# job-object host.** `RuntimeHost.cs` exists in the whisper runtime
  because `whisper-server.exe` spawns `ffmpeg.exe` children per conversion
  that must be reliably killed as a group. Neither `gfr-api.exe` nor
  `caddy.exe` spawns child processes, so `ops/windows/api/Run-Api.ps1` and
  `ops/windows/caddy/Run-Caddy.ps1` launch their target directly with
  `Start-Process -RedirectStandardOutput/-RedirectStandardError` and rely on
  `Stop-ScheduledTask` terminating that single action process.
- **Log rotation happens once per Start, not continuously mid-run.** The
  whisper runtime's C# `RotatingLog` caps a single run's output exactly at
  the configured byte limit as it's written. The simpler wrapper here checks
  size and rotates immediately before each `Start-Api.ps1` / `Start-Caddy.ps1`,
  so an unusually long run without a restart could grow past the configured
  limit before the next rotation. Both processes' own logging (Go's `slog`,
  Caddy's structured logs) is expected to stay well-bounded in normal
  operation; revisit if that turns out not to be true.
- **`Test-Api.ps1` / `Test-Caddy.ps1` are static-only.** They parse every
  script, reject a small forbidden-command list, and reject hardcoded
  private paths/addresses — the same static checks
  `ops/windows/whisper/Test-Whisper.ps1` runs before its added Pester suite.
  The Pester suite (mocked `ScheduledTasks` cmdlets, a job-contained test
  fixture) was not built for this step; that's a known gap versus the
  whisper runtime, not an oversight.
- **Task trigger is `AtLogOn`, inherited from the whisper convention as
  explicitly requested.** This means the API and Caddy only run while the
  chosen Windows account is logged on — worth revisiting for an
  always-on production box (e.g. an `AtStartup` trigger under a service
  account) if that turns out to matter more than convention consistency.

## Components

### Production PostgreSQL — `compose.prod.yaml`

Separate from the existing dev database (verified in 9F-7 to be
`greenwich-fire-responder-postgres-1` on `127.0.0.1:5432`, from the repo's
`compose.yaml`). `compose.prod.yaml` uses its own Compose project name
(`greenwich-fire-responder-prod`), its own named volume
(`postgres_prod_data`), and binds to `127.0.0.1:${POSTGRES_PROD_PORT:-5442}`
— never 5432. **Not started by this change.**

```powershell
# Validate the compose file only — does not start anything.
docker compose -f compose.prod.yaml config

# Later, once POSTGRES_PROD_* values exist in a local, non-committed env file:
docker compose -f compose.prod.yaml --env-file <local-prod-env-file> up -d
docker compose -f compose.prod.yaml --env-file <local-prod-env-file> down
```

### Go API — `ops/windows/api/`

- `Build-Api.ps1 [-Output <path>]` — `go build`s `./cmd/api` to
  `%LOCALAPPDATA%\GreenwichFireResponder\ApiBin\gfr-api.exe` by default.
- `Install-Api.ps1 [-Executable <path>] [-HttpAddr 127.0.0.1:8080]` —
  registers the `GFR-Api-<sid>` logon task. Refuses any `HttpAddr` other
  than `127.0.0.1:8080`; the API is never reachable except through Caddy.
- `Start-Api.ps1` / `Stop-Api.ps1` / `Status-Api.ps1` / `Uninstall-Api.ps1
  [-CleanupRuntimeData]` — same shape as the whisper scripts.
- `Test-Api.ps1` — static checks (see above).

`Start-Api.ps1` fails closed if
`%LOCALAPPDATA%\GreenwichFireResponder\Api\api.env` doesn't exist yet — that
file is where real production secrets/config go (`GFR_AUTH_SESSION_SECRET`,
`GFR_DATABASE_URL`, etc., see `.env.production.example`), and this step
creates none of them. `Uninstall-Api.ps1 -CleanupRuntimeData` deliberately
refuses to run while `api.env` is present, so cleanup can never silently
discard secrets.

### Caddy — `ops/windows/caddy/`

- `Caddyfile.production` — the reviewed production config template.
  Reverse-proxies `/api/*` to `127.0.0.1:8080` and serves the Angular build
  for everything else with SPA fallback. **Caddy is not installed by any
  script here.**
- `Install-Caddy.ps1 [-CaddyExecutable <path>] [-CaddyfilePath <path>]
  [-FrontendRoot <path>]` — registers the `GFR-Caddy-<sid>` logon task.
  Refuses any `Domain` other than `app.gfrapp.com` or `ApiUpstream` other
  than `127.0.0.1:8080`. Validation requires a real `caddy.exe` to already
  exist at `-CaddyExecutable` (default `%ProgramFiles%\Caddy\caddy.exe`), so
  running this before Caddy is installed fails closed rather than fetching
  or exposing anything.
- `Start-Caddy.ps1` / `Stop-Caddy.ps1` / `Status-Caddy.ps1` /
  `Uninstall-Caddy.ps1 [-CleanupRuntimeData]`.
- `Test-Caddy.ps1` — static checks.

Automatic HTTPS for `app.gfrapp.com` additionally needs, before
`Start-Caddy.ps1` can obtain a certificate: DNS for `app.gfrapp.com` pointed
at this host, and 80/443 forwarded to it. Neither is done by this step —
those remain explicit follow-ups (Cloudflare DNS, router port forwarding,
Windows Firewall) requiring separate approval.

Caddy's own ACME account/certificates live under
`%LOCALAPPDATA%\GreenwichFireResponder\Caddy\caddy-data`, fully inside the
managed data directory. `Uninstall-Caddy.ps1 -CleanupRuntimeData` refuses to
run while that directory is present, so cleanup can never silently discard
live TLS material.

### Angular frontend — `ops/windows/frontend/`

- `Build-Frontend.ps1 [-Destination <path>]` — `npm ci`, `ng build`
  (production is the default Angular configuration), then copies
  `web/dist/web/browser` to `%LOCALAPPDATA%\GreenwichFireResponder\Frontend`
  by default. `Caddyfile.production`'s `{$GFR_FRONTEND_ROOT}` is set to this
  path by `Run-Caddy.ps1` at start time — it is never hardcoded in the
  Caddyfile.

## What this step does NOT do

Nothing has been installed, started, or exposed. Specifically, not done:

- Caddy is not installed.
- Production Postgres (`compose.prod.yaml`) has not been started.
- No production secrets exist anywhere (`api.env`, `GFR_AUTH_SESSION_SECRET`,
  database passwords, etc.).
- Cloudflare DNS, Windows Firewall, and router port forwarding are
  unchanged.
- Ports 80/443 are not exposed.
- Nothing has been deployed, staged, committed, or pushed.
- Step 8D-B has not been started.
- The dev Postgres container/compose file (`compose.yaml`,
  `127.0.0.1:5432`) and unrelated services (`thinline-*`) are untouched.

## Next step after approval

Provision real values (production database credentials, a generated
`GFR_AUTH_SESSION_SECRET`, `api.env`) and install Caddy itself — both
explicit follow-ups, not part of this step.
