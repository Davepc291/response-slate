# PostgreSQL development

## Step 3A human-reference dataset

Step 3A adds only the local review workflow and immutable dataset foundation;
Step 3B accuracy evaluation, later prompt tuning, and classification are separate.
See [the complete human procedure](../backend/README.md#step-3a-local-human-transcript-review)
and [migration 000006](../database/README.md#migration-000006-append-only-transcript-review).
No HTTP service, player, editor, classifier, WebSocket, incident, or CAD component
is started by the review CLI. It neither reads audio nor contacts Whisper.

A real person must use `show` to select one canonical recording, listen manually,
and write a UTF-8 correction based on the audio. `submit` requires the exact
transmission/attempt IDs, reviewer label, verdict and `--confirm-human`; acceptance
also requires a nonblank `--text-file`. Use `needs_followup` or `excluded` when
truth is uncertain. Never generate a reference from model output alone. Append a
superseding review to withdraw a mistake; inspect `history` to retain the audit.
Only a latest accepted review enters export, with its immutable content-derived
split. Private transcript text and exports must never enter Git or be interpreted
as status/CAD commands. Ignored files are not encrypted or access-controlled.

Ordinary tests use fakes and temporary synthetic files:

```powershell
# From backend/:
go vet ./...
go test ./...
```

The safe opt-in integration test uses an existing local database URL supplied
privately in the process environment. It expects the verified 21-transmission,
23-attempt, zero-decision baseline and no existing dataset items/reviews:

```powershell
$env:GFR_REVIEW_LIVE_TEST = 'true'
go test ./internal/transcriptreview -run '^TestControlledReviewCLI$' -count=1 -v -timeout 60s
Remove-Item Env:GFR_REVIEW_LIVE_TEST
```

This exercises the production command dispatcher and PostgreSQL store for queue,
show, submit, history, stats and export in one **rolled-back transaction**. Every
submission is explicitly labeled synthetic and is not human-reviewed truth.
It verifies bounded output, exact raw text, four superseding reviews, accepted-only
exports, stable ordering/splits, metadata privacy, and unchanged baseline content
digests. Only its exact temporary correction/export files and directory are
removed. No source recording or Whisper endpoint is accessed. Ordinary runs skip
this opt-in test. Forced termination still rolls back an uncommitted connection,
but temporary files may need inspection. Never disable audit triggers to clean up
a real review; supersede it instead.

## Transcription monitoring and recovery

Apply migration `000005` and run all five transactional SQL suites in
[database/README.md](../database/README.md). The API does not load `.env` or migrate
automatically. See [the backend monitoring reference](../backend/README.md#transcription-operations-monitoring)
for all measurement definitions, units, thresholds, JSON fields, and limitations.

```powershell
Invoke-RestMethod http://127.0.0.1:8080/api/operations/transcription
```

Backlog is eligible waiting work, not proof of a failed provider. Compare queue
depth and age with worker activity, recent HTTP durations, timeout/retry counts,
and provider evidence. A recent success expires to stale after 60 seconds; an old
successful timestamp never guarantees current health. Requesting plus an active
claim indicates work underway. Worker disabled is intentional if transcription
or the recordings directory is unset. Historical metrics remain available for a
configured directory when disabled. Provider errors do not affect database-only
readiness. Database-query failure gives safe HTTP 503 with no metrics.

When backlog grows, inspect recent durations and the managed local status script;
do not restart services merely because a queue exists. When provider failures
appear, verify `http://127.0.0.1:8001` and the local runtime logs separately, then
allow the existing bounded retry policy to run. Recovery emits a single event
when a request succeeds. Failed/exhausted jobs require review; monitoring never
resets attempts, replays files, changes unit state, or creates incidents. If metrics
are unavailable, check PostgreSQL health, migration version, directory scope, and
query capacity. Do not print database URLs or credentials in troubleshooting.

Controlled managed-provider verification is explicitly opt-in. Privately supply
the local `GFR_DATABASE_URL`, enable required database and transcription, use the
loopback provider with small.en/English, and set:

```powershell
$env:GFR_LIVE_MONITORING_TEST = 'true'
$env:GFR_LIVE_SOURCE = 'C:\Users\User\SDRTrunk\recordings\<eligible-native-filename>.mp3'
# From backend/, with FFmpeg and FFprobe in PATH:
go test ./cmd/api -run '^TestControlledLiveMonitoring$' -count=1 -v -timeout 240s
Remove-Item Env:GFR_LIVE_MONITORING_TEST
Remove-Item Env:GFR_LIVE_SOURCE
```

The test refuses an existing canonical audio fingerprint. It copies only after
the empty startup snapshot, observes monitoring before/during/after, counts one
request with an audited transport, re-observes the temporary path, and gracefully
cancels the API. Deferred cleanup removes only its exact temporary rows and copy,
checks original hash/size/mtime, and leaves managed Whisper running. It never
contacts Linux. Normal tests skip this test and use fakes. Provider timing includes
network/upload/response overhead; queue samples are not a durable time series.
The endpoint has no sensitive labels or raw transcripts. Warnings are observations
only: no classifier, board, incident, WebSocket, or CAD actions are implemented.

## Optional Windows local Whisper management

See [the Windows runtime guide](../ops/windows/whisper/README.md) for the exact
manual installation, start, status, stop, and rollback procedures. Registration
uses Windows Task Scheduler under the current user and does not start the server.
No third-party service wrapper is required. Run its preview/tests before any later
installation; no task is registered by tests.

The managed runtime uses `127.0.0.1:8001` only, small.en/English, CPU-only, eight
threads, one processor, and FFmpeg conversion in a dedicated user-local directory.
Paths derive from the current profile or validated absolute parameters. The model's
exact SHA-1 is checked before registration. An occupied port causes refusal; the
scripts never stop the pre-existing manual server or the Linux service.

Runtime stdout/stderr have bounded rotation outside the repository. Uninstall
retains logs and runtime files unless its separate cleanup switch is requested.
The task requires a logged-on user; it does not provide a pre-logon system service.
Configure the API's process environment explicitly and keep its existing database
configuration private. Neither these scripts nor the API automatically loads `.env`.

## Remote transcription verification

The optional speech provider is configurable shared infrastructure, not part of
ThinLine application code. Use only its HTTP API. Never modify, copy, restart, or
stop the Linux service. Unauthenticated plain HTTP is intended only for a trusted
private LAN until a dedicated authenticated service exists; prefer HTTPS and an
environment-only bearer token when supported. Never commit a private service
address, token, database password, or recording.

Apply migration 000004 and run all four rollback-only SQL suites using
[database/README.md](../database/README.md). See
[backend/README.md](../backend/README.md#optional-remote-speech-to-text) for every
transcription variable, default, bound, lifecycle state, and retry limitation.
Configure the private `GFR_DATABASE_URL` and `GFR_TRANSCRIPTION_BASE_URL` directly
in the process environment; the API does not load `.env`. Set
`GFR_TRANSCRIPTION_ENABLED=true`, `GFR_DATABASE_REQUIRED=true`, model `small.en`,
and language `en`. With transcription disabled (the default), HTTP and ingestion
continue normally; readiness still depends only on PostgreSQL.

For a controlled local check, confirm the Compose PostgreSQL service is healthy
and that the provider's `GET /health` returns status ok and `GET /v1/models`
includes small.en. The opt-in test repeats bounded provider preflight requests and
then runs the actual API lifecycle on localhost:8080 with an audited HTTP transport.
It creates a new empty temporary watched directory and copies the source only
after watcher startup. It never uses the live recordings directory as a watcher
test fixture. Windows source-directory example: `C:\Users\User\SDRTrunk\recordings`.

After privately setting the required database and provider process settings, run
from `backend/` with FFmpeg and FFprobe installed/on PATH:

```powershell
$env:GFR_HTTP_ADDR = '127.0.0.1:8080'
$env:GFR_DATABASE_REQUIRED = 'true'
$env:GFR_TRANSCRIPTION_ENABLED = 'true'
$env:GFR_TRANSCRIPTION_MODEL = 'small.en'
$env:GFR_TRANSCRIPTION_LANGUAGE = 'en'
$env:GFR_LIVE_TRANSCRIPTION_TEST = 'true'
$env:GFR_LIVE_SOURCE = 'C:\Users\User\SDRTrunk\recordings\20260905_081609Greenwich_Fairfield_T-NEW_GFD1__TO_57201_FROM_578060.mp3'
go test ./cmd/api -run '^TestControlledLiveTranscription$' -count=1 -v -timeout 240s
Remove-Item Env:GFR_LIVE_TRANSCRIPTION_TEST
Remove-Item Env:GFR_LIVE_SOURCE
```

Do not run the live check against a database already containing the source audio's
canonical fingerprint: it must not delete or replace a pre-existing canonical row.
The check counts exactly one multipart POST, compares stored raw text to the actual
response in memory, checks provider/model/attempt timestamps, repeats observation,
and adds a content alias without another transcription. The baseline result is
"Thank you for reporting on the distraction." Preserve that evidence; Whisper
output is untrusted, can hallucinate, and must not become a status command.

The check cancels and joins the API, verifies port 8080 can be bound again, removes
only its temporary copies/directory and exact test rows, and verifies the original
SHA-256, size, and modification time are unchanged. Verbose live output includes
the raw evidence; keep it private. Normal tests do not log transcripts or contact
the service. Logs use safe outcome codes, not remote errors or credentials.
If test execution is forcibly killed, review the reported test artifacts manually;
normal failures run deferred cleanup. No permanent process is started.

The worker resumes only persisted eligible canonical work in the configured
directory. This does not replay historical filenames into ingestion. Path-based
`source_identity` and content-based `audio_fingerprint` remain separate. Normal
duplicates are idempotent; remote exactly-once execution after an uncertain crash
is impossible without provider support. No classifications, incidents, status
changes, WebSockets, or CAD writes are implemented.

This Compose project provides PostgreSQL 16 for local development. The Go API runs
separately on Windows and uses explicitly configured environment variables to
connect to it. Run the commands below in PowerShell
from the repository root, with Docker Desktop configured for Linux containers.

## Configure

Create your local configuration only if `.env` does not already exist:

```powershell
if (-not (Test-Path -LiteralPath .env)) {
    Copy-Item -LiteralPath .env.example -Destination .env
}
```

Edit `.env` and replace the example password with a unique local password before
starting PostgreSQL. Keep this file private; Git ignores it. The example values
are placeholders, not production credentials.

| Variable | Purpose |
| --- | --- |
| `POSTGRES_DB` | Initial database name (required) |
| `POSTGRES_USER` | Initial database administrator username (required) |
| `POSTGRES_PASSWORD` | Initial password (required and non-empty) |
| `POSTGRES_PORT` | Windows host port; defaults to `5432` |

PostgreSQL binds to `127.0.0.1` on the host. If port 5432 is already occupied,
change `POSTGRES_PORT`, for example to `5433`. The container port stays 5432.
Variables already set in your shell take precedence over `.env` values.

Database initialization settings apply when the data volume is first initialized.
Editing `.env` later does not change an existing database's name, user, or password.

## Validate without starting containers

Validate the checked-in template:

```powershell
docker compose --env-file .env.example config
```

For your actual local configuration, suppress the rendered values so the password
is not printed:

```powershell
docker compose --env-file .env config --quiet
```

Compose rejects missing or empty required values using its
[required-variable interpolation syntax](https://docs.docker.com/reference/compose-file/interpolation/).

## Start

```powershell
docker compose --env-file .env up -d postgres
```

This may download `postgres:16-alpine` and creates a project-scoped Docker volume
for `postgres_data`, named `greenwich-fire-responder_postgres_data` with the
default project name. That volume stores the database across container restarts.

## Status and logs

```powershell
docker compose --env-file .env ps postgres
docker compose --env-file .env logs --tail 100 -f postgres
```

Wait for the service to report `healthy` before connecting. The health check uses
`pg_isready` to check whether PostgreSQL accepts connections. Press Ctrl+C to
stop following logs; the database keeps running.

## Connect

In a PostgreSQL client on Windows, use:

- Host: `127.0.0.1`
- Port: your `POSTGRES_PORT` value, or `5432` by default
- Database, username, and password: the values configured in your local `.env`

With `psql` installed on Windows, the template database and username can be used
as follows; adjust them and the port if you changed `.env`. `-W` prompts for the
password rather than putting it in the command line:

```powershell
psql -h 127.0.0.1 -p 5432 -U gfr_dev -d greenwich_fire_responder_dev -W
```

Alternatively, use the container's client, with the same adjustments:

```powershell
docker compose --env-file .env exec postgres psql -h 127.0.0.1 -U gfr_dev -d greenwich_fire_responder_dev -W
```

Type `\q` to leave `psql`.

## Stop

```powershell
docker compose --env-file .env stop postgres
```

This preserves the container and database volume. Run the start command again
to resume. The service uses `restart: unless-stopped`.

## Recording-ingestion development

Use `C:\Users\User\SDRTrunk\recordings` as the Windows recordings-directory example.
The API must be able to list that existing directory and inspect file metadata.
It must not be a symlink, junction, or another reparse point, and its path must not
pass through one. Analysis reads a verified read-only handle after ingestion; it
never changes source recordings.

Before enabling ingestion:

1. Confirm the local PostgreSQL service is healthy using the status command above.
2. Apply migrations `000001`, `000002`, and `000003` in order if not already applied, and run
   their rollback-only SQL tests using [database/README.md](../database/README.md).
   The API does not apply migrations automatically.
3. Configure `GFR_DATABASE_URL` privately in the API process environment using the
   local database credentials and host port. See [backend/README.md](../backend/README.md)
   for connection setup. The API does not automatically load `.env`; Compose
   configuration does not configure a Go process running on Windows.
4. From the repository root, start the API explicitly:

```powershell
$env:GFR_DATABASE_REQUIRED = 'true'
$env:GFR_RECORDINGS_DIR = 'C:\Users\User\SDRTrunk\recordings'
$env:GFR_RECORDING_TIMEZONE = 'America/New_York'
Set-Location backend
go run ./cmd/api
```

`GFR_DATABASE_REQUIRED=true` ensures startup checks the configured database.
Press Ctrl+C to stop the API and its watcher; the PostgreSQL service remains
running. Leaving `GFR_RECORDINGS_DIR` unset or empty disables ingestion while
keeping the HTTP API available.

Safe defaults in `.env.example` are a 1-second reconciliation interval, 3 seconds
of stable size/mtime, a 2-minute observation limit, 5 seconds between failed
database writes, and at most 3 write attempts. The corresponding variables are
`GFR_RECORDING_POLL_INTERVAL`, `GFR_RECORDING_STABLE_FOR`, `GFR_RECORDING_MAX_WAIT`,
`GFR_RECORDING_RETRY_INTERVAL`, and `GFR_RECORDING_MAX_ATTEMPTS`. Set overrides
explicitly in the process environment. The backend guide documents valid bounds.

The watcher snapshots existing filenames at startup and does not replay them,
including files modified after that snapshot. New files are found through periodic,
nonrecursive scans of this directory only; filesystem notifications are not needed.
Only native `.mp3`/`.wav` names for TGIDs 57201 through 57204 are eligible. Extensions
are case-insensitive; malformed names and `TEST_`, `REPLAY_`, or `demo_` prefixes
are ignored. The backend guide shows the exact native filename and parsed fields.

New metadata rows are deduplicated by `source_identity`: SHA-256 of the versioned,
cleaned absolute path, case-folded on Windows. Size/mtime changes do not change
this identity. `audio_fingerprint` is populated only after canonical PCM analysis.
A copied recording at a different path has a different source identity, but
identical decoded audio is linked to one canonical transmission. The same path is
not inserted twice. Source files are never renamed or moved by ingestion.

Watch the API terminal for structured `recording_ingestion` logs with accepted,
ignored, duplicate, or failed outcomes and safe reason codes. Logs contain hashed
recording identifiers instead of raw filenames, paths, or database errors. A
database write failure does not crash the API. Retries stop at the configured
limits, and restarting does not replay the directory; review failed observations
before relying on ingestion. Polling cannot capture files removed between scans,
and size/mtime stability is a heuristic rather than a recorder completion signal.

This foundation stores metadata and audio measurements in shadow mode with no CAD
authority, with optional remote transcription as described above. It performs no classification, incident creation,
unit-status updates, or WebSocket/CAD publication. Secrets and recordings must
never be committed to Git. Automated watcher tests use temporary synthetic files;
do not use a live recordings directory as a test fixture.

From `backend/`, verify the code with:

```powershell
gofmt -w cmd/api internal/config internal/database internal/httpapi internal/recordings internal/audioanalysis internal/transcription
go vet ./...
go test ./...
```

## Audio tools and controlled integration verification

The audio stage needs FFprobe and FFmpeg. Defaults are `ffprobe` and `ffmpeg` on
PATH. If an already-running terminal has a stale PATH, restart it or set
`GFR_FFPROBE_PATH` and `GFR_FFMPEG_PATH` to the installed executables explicitly.
No shell interprets media-tool arguments, and the API does not automatically load
`.env` or pass database settings to those subprocesses.

Defaults: `GFR_AUDIO_TIMEOUT=30s` per probe/decode attempt,
`GFR_AUDIO_MAX_DURATION=10m`, `GFR_AUDIO_MAX_ATTEMPTS=3`, and
`GFR_AUDIO_RETRY_INTERVAL=2s`. See the backend guide for validation bounds and
supported codecs. Missing tools fail analysis safely without taking down HTTP.

Output PCM is streamed as signed 16-bit little-endian, mono, 16 kHz; no normalized
file is created. The fingerprint is `pcm-s16le-16000-mono-v1:sha256:<hex>` over those
raw bytes. Duration is rounded from decoded sample count to nearest millisecond,
ties up. RMS/peak use a 32768 full-scale divisor. Pin consistent decoder builds:
changing canonical decoding can change hashes even when a recording sounds alike.

The normal Go suite does not need installed media tools. Keep real-tool verification
separate and confined to the local development database:

1. Confirm PostgreSQL health and migrations; record the source file's SHA-256,
   size, and modification time without altering it.
2. Create a new empty temporary directory and point `GFR_RECORDINGS_DIR` there.
   Set database credentials privately in memory/process environment and start the
   API temporarily. Confirm health/readiness before adding a recording.
3. Copy
   `C:\Users\User\SDRTrunk\recordings\20260905_081609Greenwich_Fairfield_T-NEW_GFD1__TO_57201_FROM_578060.mp3`
   into that temporary directory. Never modify or move the original.
4. Wait for `completed` analysis and verify `audio_probe`, `duration_ms`, `rms`,
   `peak`, and the canonical fingerprint. For an independent check, decode only
   the temporary copy with the same canonical settings and measure/hash its stream.
5. Add a second temporary copy with a distinct valid native filename. Verify a
   `skipped` alias points to the first canonical row and only one fingerprint owner
   exists. Verify safe `analyzed` and `duplicate-content` logs.
6. Stop the temporary API and confirm its listener and media children are gone.
   Remove only that test's alias row before its canonical row, each selected by the
   captured ID and exact source identity/path. Never delete a pre-existing canonical
   row. Remove only the temporary copies and their empty directory.
7. Confirm the original hash, size, and modification time are unchanged; run the Go
   tests again. Never commit recordings, temporary PCM, credentials, or backups.

Analysis claims and retries are persisted, but no durable lease recovery or startup
replay is implemented. A crash or database outage can leave work requiring review.
The worker is serial, so slow analysis delays subsequent directory scans while the
HTTP API remains responsive. Content aliases must not be counted as extra logical
transmissions. Optional transcription uses its own durable lease worker. No classification, status changes, incidents, or CAD writes
are performed.

## Step 4A: private offline accuracy evaluation

Use the independent `backend/cmd/transcript-eval` CLI with an explicit `--dataset`
path to a private review export. No PostgreSQL, Whisper, API, recordings, or
network access is required. Keep datasets outside the repository; evaluation
prints only aggregate metrics and the input SHA-256 and does not persist data.
See [offline evaluation instructions and exact metric definitions](offline-transcription-evaluation.md).
Validation is unavailable for the current export with zero validation records;
this milestone does not authorize promotion or operational decisions.

## Step 4B1: local experiment harness

The separate `transcript-experiment` CLI supports a future controlled comparison
using read-only database evidence and the fixed local Windows Whisper endpoint.
It is not part of the API or production worker. Follow the explicit split,
private output, and credential workflow in
[transcription experiments](transcription-experiments.md). Do not run a live
experiment as part of installation or ordinary tests.

## Step 9F: local authentication end-to-end testing

This section documents the **local-only** procedure for exercising the Step
9C/9D/9E authentication and administration surface end to end: sign-in,
first-time password establishment, sessions, admin user management,
suspension, role restrictions, logout, and audit records. Nothing here
changes production configuration, authorization rules, or deployment
behavior. See
[docs/authentication-authorization-v1.md](authentication-authorization-v1.md)
for the approved contract these screens and endpoints implement.

### 1. Prerequisite checks

Confirm Docker and the local PostgreSQL service are healthy before doing
anything else (see [Configure](#configure) and
[Status and logs](#status-and-logs) above if `.env` does not exist yet or the
service is not running):

```powershell
docker context show
docker compose --env-file .env ps postgres
```

Wait for `postgres` to report `healthy`. Do not proceed against a database
you do not recognize as your own local instance.

### 2. Apply migration 000008 and verify its schema

Apply migrations `000001`-`000007` first if not already applied (see
[database/README.md](../database/README.md)), then apply `000008` and its
schema test:

```powershell
Get-Content -Raw database/migrations/000008_identity_foundation.sql |
    docker compose --env-file .env exec -T postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Migration 000008 failed.' }
Get-Content -Raw database/tests/000008_schema_test.sql |
    docker compose --env-file .env exec -T postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Identity schema tests failed.' }
```

Confirm the version is recorded and `users` is empty (a prerequisite for
`bootstrap-admin` below):

```powershell
docker compose --env-file .env exec postgres psql -U gfr_dev -d greenwich_fire_responder_dev -c "SELECT version FROM schema_migrations ORDER BY version;"
docker compose --env-file .env exec postgres psql -U gfr_dev -d greenwich_fire_responder_dev -c "SELECT count(*) FROM users;"
```

### 3. Required `GFR_AUTH_*` environment variables

Set these in the process environment before starting the API (never in
`.env`: the API does not load it automatically). `GFR_AUTH_ENABLED=true`
independently requires `GFR_DATABASE_URL` to be set, regardless of
`GFR_DATABASE_REQUIRED`.

```powershell
$env:GFR_DATABASE_URL = 'postgres://gfr_dev:<your local password>@127.0.0.1:5432/greenwich_fire_responder_dev?sslmode=disable'
$env:GFR_DATABASE_REQUIRED = 'true'
$env:GFR_AUTH_ENABLED = 'true'
$env:GFR_AUTH_SESSION_IDLE_TIMEOUT = '15m'
$env:GFR_AUTH_SESSION_MAX_LIFETIME = '12h'
$env:GFR_AUTH_PASSWORD_RESET_TTL = '1h'
# Local testing only: the approved contract leaves this duration an
# unresolved production decision (Section 15, item 6/7). 1 hour is short
# enough that a stale local invitation does not linger, and must never be
# treated as a production default.
$env:GFR_AUTH_INVITATION_TTL = '1h'
$env:GFR_AUTH_ALLOWED_ORIGINS = 'https://localhost:4200'
$env:GFR_AUTH_RATE_LIMIT_PER_ACCOUNT = '5/15m'
$env:GFR_AUTH_RATE_LIMIT_PER_IP = '20/15m'
$env:GFR_AUTH_RATE_LIMIT_INVITATION = '10/1h'
$env:GFR_AUTH_RATE_LIMIT_PASSWORD_RESET = '5/1h'
$env:GFR_AUTH_BREACH_CHECK_ENABLED = 'false'
```

`GFR_AUTH_ALLOWED_ORIGINS` must exactly match the origin the browser actually
shows for the Angular app — `https://localhost:4200`, the origin
`npm run local-auth` (below) serves. This is an allowlist for the CSRF
origin check, not a CORS relaxation; it does not, by itself, allow a
cross-origin request to succeed.

Generate `GFR_AUTH_SESSION_SECRET` fresh for each local database you
bootstrap; it never has a default and must never be committed:

```powershell
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$env:GFR_AUTH_SESSION_SECRET = [Convert]::ToBase64String($bytes)
$bytes = $null
```

(Equivalently, on a machine with OpenSSL available: `openssl rand -base64 32`.)
Never `Write-Output`, log, or paste this value anywhere outside setting the
environment variable itself.

### 4. Bootstrap the first administrator

`backend/cmd/bootstrap-admin` is a standalone, local-only CLI. It is never
built into, imported by, or reachable from the live API (`backend/cmd/api`);
it adds no HTTP endpoint and changes no authorization rule. It exists only
because migration 000008 intentionally inserts zero rows and every
`/api/admin/users*` route requires an already-authenticated administrator —
there is otherwise no way to reach the first one.

It refuses to run against any database host other than `127.0.0.1`,
`localhost`, or `::1`; refuses to run unless the `users` table is completely
empty; requires the explicit `-confirm` flag; and always collects the new
password interactively with hidden terminal input, confirmed a second time.
It never accepts a password as a command-line argument, and never prints or
logs a password, password hash, token, or database credential.

```powershell
Set-Location backend
go run ./cmd/bootstrap-admin -email "admin@example.test" -display-name "Local Test Admin" -confirm
Set-Location ..
```

You will be prompted twice, with no characters echoed to the terminal.
Choose any password meeting the length policy (12-256 characters; no
composition rules) and remember it — it is never stored anywhere by this
tool and cannot be shown again. On success it prints the new account's id,
email, display name, role (`system_administrator`), and status (`active`)
only — never the password or its hash.

Run this exactly once per freshly migrated local database. A second run
against a database that already has any user refuses with an explicit
"users table is not empty" error, by design.

### 5. Start the authenticated Go API

With the environment variables from step 3 still set in the same shell:

```powershell
Set-Location backend
go run ./cmd/api
Set-Location ..
```

Watch for `authentication` `outcome=enabled` in the startup log. The API now
serves `/api/auth/*` and `/api/admin/*` in addition to its existing routes,
unchanged for every route outside those two prefixes.

### 6. Start Angular through the local HTTPS proxy

In a separate shell, from `web/`:

```powershell
npm run local-auth
```

This runs `ng serve --ssl --proxy-config proxy.conf.json`: it serves the
Angular app over HTTPS at `https://localhost:4200` (satisfying the `Secure`
attribute the `__Host-` session/CSRF cookies require — plain HTTP has no
fallback, in production or locally) and proxies every `/api/*` request to
`http://127.0.0.1:8080`, so the browser sees one same-origin HTTPS
application instead of two different origins. `proxy.conf.json` adds no CORS
header and changes nothing about the backend; it only makes the dev server
forward requests. `npm start` (plain `ng serve`, no proxy, no HTTPS) and
`npm run build` are unchanged and unaffected by this script's existence.

### 7. Browser certificate warning

`ng serve --ssl` with no `--ssl-cert`/`--ssl-key` generates a local
self-signed certificate for `localhost`. The browser will show a private-
connection/certificate warning the first time you open
`https://localhost:4200` — this is expected for a self-signed local
certificate, not a sign of a compromised connection, and applies only to
this local development server. Accept/proceed past the warning once per
browser profile (or install a local certificate authority such as mkcert and
pass it via `--ssl-cert`/`--ssl-key` if you prefer to avoid the warning).
Never do this against a non-local hostname.

### 8. Complete local end-to-end checklist

With the API (step 5) and Angular (step 6) both running, using the
administrator created in step 4:

1. **Sign-in**: open `https://localhost:4200/mobile/auth/sign-in`, sign in as
   the bootstrapped administrator. Confirm `__Host-gfr_session` and
   `__Host-gfr_csrf` cookies are set (DevTools → Application → Cookies).
2. **Admin user creation**: as the administrator, use **Add user** to create
   a second, non-administrator account (for example `dispatcher_operator`).
   Capture the one-time invitation code shown — it is never shown again.
3. **First-time access / password creation**: sign out, open
   `/mobile/auth/first-time-access`, redeem the invitation code, and
   establish a permanent password for the new account. Confirm the account
   moves `invited` → `password_change_required` → `active`.
4. **Sessions**: sign in as the new user, open the sessions screen (or
   `GET /api/auth/sessions`), and revoke one session; confirm it can no
   longer be used.
5. **Suspension**: as the administrator, suspend the second user; confirm
   their existing session is rejected and sign-in now shows the
   suspended-account message.
6. **Role restrictions**: while signed in as the non-administrator account,
   attempt an `/api/admin/users*` request directly (for example with
   DevTools or `Invoke-RestMethod`, not only through the hidden UI) and
   confirm a server-side rejection, not merely a hidden button.
7. **Logout**: confirm `/api/auth/logout` ends the session server-side (a
   second use of the same cookie value fails), not merely that the cookie is
   cleared client-side.
8. **Audit records**: read-only, confirm rows were recorded for each action
   above and that none contain a password, hash, or token value:

```powershell
docker compose --env-file .env exec postgres psql -U gfr_dev -d greenwich_fire_responder_dev -c "SELECT event_type, account_id, actor_id, created_at FROM identity_audit_log ORDER BY created_at;"
```

### 9. Cleanup: removing local test accounts and sessions

Stop the API and Angular dev server first (Ctrl+C in each shell), then
decide which cleanup applies.

**Sessions, invitations, password resets, and MFA credentials** are ordinary
mutable rows and can always be removed for the exact test account ids you
created:

```sql
-- Review before deleting.
SELECT id, normalized_email, role, status FROM users ORDER BY id;

DELETE FROM sessions WHERE user_id IN (<ids>);
DELETE FROM invitations WHERE user_id IN (<ids>);
DELETE FROM password_resets WHERE user_id IN (<ids>);
DELETE FROM mfa_credentials WHERE user_id IN (<ids>);
```

**The `users` rows themselves usually cannot be deleted**, and this is
intentional, not a bug: `identity_audit_log.account_id`/`actor_id` reference
`users.id` with `ON DELETE RESTRICT`, and `identity_audit_log` itself is
append-only (`ENABLE ALWAYS` triggers reject UPDATE/DELETE/TRUNCATE through
the ordinary application role, matching the existing
`unit_status_decisions`/`transcript_reviews` convention). Once a test
account has signed in, been created, or been acted on by an administrator —
true for essentially every account exercised by the checklist above — its
audit rows make `DELETE FROM users WHERE id = ...` fail with a foreign-key
violation. Do not disable or bypass the audit trigger to force it through.

In practice this means local test accounts are not surgically removable
once exercised. Two real options:

1. **Leave them.** They are synthetic, clearly test-labeled local rows with
   no real personal data; suspending or disabling one instead of deleting it
   is sufficient if you want it inert.
2. **Reset the whole local database** if you want a completely clean slate:

```powershell
docker compose --env-file .env down -v postgres
```

This deletes every table's data, not just the test accounts; you will need
to reapply every migration from `000001` again afterward.
