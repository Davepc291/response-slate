# PostgreSQL development

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
pass through one. The watcher never reads audio bytes or changes source recordings.

Before enabling ingestion:

1. Confirm the local PostgreSQL service is healthy using the status command above.
2. Apply migrations `000001` and `000002` in order if not already applied, and run
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
this identity. `audio_fingerprint` remains reserved for audio-content hashing and
is NULL for this milestone. A copied recording at a different path has a different
identity; the same path is not inserted twice. Source files are never renamed or
moved by ingestion.

Watch the API terminal for structured `recording_ingestion` logs with accepted,
ignored, duplicate, or failed outcomes and safe reason codes. Logs contain hashed
recording identifiers instead of raw filenames, paths, or database errors. A
database write failure does not crash the API. Retries stop at the configured
limits, and restarting does not replay the directory; review failed observations
before relying on ingestion. Polling cannot capture files removed between scans,
and size/mtime stability is a heuristic rather than a recorder completion signal.

This foundation stores metadata only in shadow mode with no CAD authority. It
performs no audio normalization, transcription, classification, incident creation,
unit-status updates, or WebSocket/CAD publication. Secrets and recordings must
never be committed to Git. Automated watcher tests use temporary synthetic files;
do not use a live recordings directory as a test fixture.

From `backend/`, verify the code with:

```powershell
gofmt -w cmd/api internal/config internal/database internal/httpapi internal/recordings
go vet ./...
go test ./...
```
