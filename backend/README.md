# Greenwich Fire Responder V3 backend

HTTP API foundation using the Go standard library and `github.com/jackc/pgx/v5/pgxpool`.
The temporary
module name is `greenwich-fire-responder/backend`. Requires Go 1.26 or later.

## Run

From the repository root:

```sh
cd backend
go run ./cmd/api
```

The server listens on `127.0.0.1:8080` by default, accepting connections only
from this computer. Set `GFR_HTTP_ADDR` to override the listening address.
For example, in PowerShell, from `backend/`:

```powershell
$env:GFR_HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

For future Docker or production deployments, set `GFR_HTTP_ADDR` to `:8080`
to listen on all available interfaces.

An unset or empty `GFR_HTTP_ADDR` uses `127.0.0.1:8080`. Press Ctrl+C to shut down.
The server also handles SIGTERM and allows up to 10 seconds for active requests
to finish before closing remaining connections. The PostgreSQL pool is closed
after HTTP shutdown, including on server startup failure.

## PostgreSQL configuration

The API reads process environment variables only. It does **not** automatically
load `.env`; Docker Compose's `.env` settings are not passed to a Go process
running on Windows. Use the local PostgreSQL setup in `../docs/development.md`
and the separate migration commands in `../database/README.md`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `GFR_HTTP_ADDR` | `127.0.0.1:8080` | API listening address |
| `GFR_DATABASE_URL` | Empty | PostgreSQL connection string |
| `GFR_DATABASE_REQUIRED` | `false` | Require a successful startup database ping |

An empty URL leaves the database unconfigured. Optional mode keeps the API running
when PostgreSQL is unavailable. A valid pool is retained and readiness can recover
when PostgreSQL becomes reachable. An invalid connection string in optional mode
leaves readiness unavailable until configuration is corrected and the API restarted.

When `GFR_DATABASE_REQUIRED=true`, a missing URL, invalid connection configuration,
or failed startup ping stops startup with a safe error. Invalid boolean values also
fail configuration validation. A later database outage makes readiness return 503;
it does not stop the API or affect liveness.

Connection attempts are capped at 5 seconds; startup and readiness pings have a
2-second deadline, including pool acquisition. Database errors and connection
strings are never logged or included in readiness responses. Pool construction is
followed by an explicit ping because [pgxpool creates connections lazily](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#hdr-Creating_a_Pool).

From `backend/`, set the variables explicitly in PowerShell. This URL contains only
a placeholder; substitute your local credentials and configured host port privately
and percent-encode special characters in URL credentials:

```powershell
$env:GFR_DATABASE_URL = "postgres://gfr_dev:replace_me@127.0.0.1:5432/greenwich_fire_responder_dev?sslmode=disable"
$env:GFR_DATABASE_REQUIRED = "true"
go run ./cmd/api
```

`sslmode=disable` is for the local loopback development database. Use appropriate
TLS settings for remote connections. Never commit credentials or recordings.
The API checks connectivity and, when explicitly configured, ingests recording
metadata. It does not run migrations or write incident, unit-status, or board
state. No Docker API service is included.

## SDRTrunk recording ingestion

Apply migration `000002` after `000001` using the local commands in
`../database/README.md` before enabling ingestion. Configure the database through
the process environment as described above. Then, from `backend/`, explicitly set:

```powershell
$env:GFR_RECORDINGS_DIR = 'C:\Users\User\SDRTrunk\recordings'
$env:GFR_RECORDING_TIMEZONE = 'America/New_York'
go run ./cmd/api
```

The directory must already exist. Leaving `GFR_RECORDINGS_DIR` unset or empty
disables ingestion without preventing API startup. The API never automatically
loads `.env`. The environment template documents defaults; it does not enable the
watcher by itself.

| Setting | Default | Behavior |
| --- | --- | --- |
| `GFR_RECORDINGS_DIR` | Empty | Single directory to observe; no recursive scan |
| `GFR_RECORDING_TIMEZONE` | `America/New_York` | IANA timezone for filename timestamps |
| `GFR_RECORDING_POLL_INTERVAL` | `1s` | Directory reconciliation interval |
| `GFR_RECORDING_STABLE_FOR` | `3s` | Unchanged size and modification time before acceptance |
| `GFR_RECORDING_MAX_WAIT` | `2m` | Maximum pending observation lifetime, including retries |
| `GFR_RECORDING_RETRY_INTERVAL` | `5s` | Delay between failed database writes |
| `GFR_RECORDING_MAX_ATTEMPTS` | `3` | Maximum database write attempts per observation |

Durations use Go syntax such as `1s` or `2m`. Polling must be between 10ms and 1m;
stability must be at least the polling interval and at most 10m. Maximum wait must
exceed stability and be at most 1h. Retry interval must be at least the polling
interval and at most 10m; attempts must be between 1 and 20. Invalid settings fail
configuration validation with safe errors. Timezone data is embedded for Windows.

The watcher takes a synchronous startup snapshot and ignores all names in that
snapshot, even if those files are subsequently modified. Only names first observed
after this boundary are candidates. Periodic polling is also reconciliation; it
does not depend on filesystem notifications. It never reads audio bytes or modifies,
renames, moves, or deletes recordings. It rejects directories, nonregular files,
symlinks, and Windows reparse points, including linked directory path components.
Directory access is anchored through `os.Root`.

The native example
`20260905_081609Greenwich_Fairfield_T-NEW_GFD1__TO_57201_FROM_578060.mp3`
parses as follows:

| Field | Value |
| --- | --- |
| Recording time | `2026-09-05T08:16:09-04:00` (`12:16:09Z`) |
| System/site label | `Greenwich_Fairfield` |
| Alias/channel label | `T-NEW_GFD1` |
| Talkgroup / canonical channel | `57201` / `CH1A` |
| Source RID | `578060` (no unit inference) |
| Extension | `.mp3` |
| Original filename | Preserved exactly |

The parser supports the supplied format and an optional underscore immediately
after the timestamp. It treats the first two underscore-delimited label components
as system/site and the remainder as channel. This is the supported local convention;
underscores inside system/site names cannot be disambiguated from the filename.
The [upstream SDRTrunk filename builder](https://github.com/DSheirer/sdrtrunk/blob/master/src/main/java/io/github/dsheirer/record/AudioRecordingManager.java)
also has naming variants; tone/version suffixes and missing-label variants are not
supported by this initial parser and are ignored.

Only TGIDs 57201/CH1A, 57202/CH2B, 57203/CH3B, and 57204/CH4C are accepted.
`.mp3` and `.wav` extensions are case-insensitive. Malformed names, unsupported
talkgroups/extensions, and case-insensitive `TEST_`, `REPLAY_`, or `demo_` prefixes
are ignored with fixed reason codes. Unknown positive RIDs remain metadata and
never imply trusted units. Invalid dates, nonexistent local times, and repeated
one-hour daylight-saving times are rejected rather than assigned an invented time.

### Identity, persistence, and operation

`source_identity` is the SHA-256 hex digest of `sdrtrunk-path-v1`, a NUL separator,
and the cleaned absolute source path. Windows paths are lowercased before hashing.
The path includes the native filename and its timestamp/identifiers; mutable size
and modification time are deliberately excluded so growth or retries do not create
new identities. PostgreSQL enforces uniqueness, and inserts use
`ON CONFLICT (source_identity) DO NOTHING`.

`audio_fingerprint` remains reserved for audio-content hashing and is NULL for
these metadata-only rows. Parsed fields, source path, observed size, and modification
time are persisted. Audio measurements, duration, and transcript remain unset;
processing and transcription statuses remain `pending` for future milestones.

Structured JSON logs use `recording_ingestion`, a hashed recording ID, an outcome
(`accepted`, `ignored`, `duplicate`, or `failed`), and a fixed reason. Startup also
reports the count of ignored existing entries. Logs omit raw filenames, paths,
driver errors, credentials, and database URLs. Database writes have a 2-second
deadline. Failures retry within the configured limits and do not crash the API.
An unavailable/missing directory disables that watcher instance while HTTP remains
available; correct the directory and restart the API to enable it. Shutdown cancels
and joins the watcher before closing the connection pool.

### Initial limitations

- Stability is a size/mtime heuristic, not a recorder completion signal. A writer
  paused longer than the stability window may still append later. Empty files are
  not accepted and eventually reach the observation deadline.
- Files must remain present long enough to be discovered and stabilized. Files
  created and removed between polls cannot be recovered. Scans and writes are
  serial; large directories or database outages increase processing delay.
- This is not a durable ingestion queue. Failed observations stop retrying at the
  attempt/deadline limit, and restarting does not replay files already present.
  Operators must review failures; automatic historical recovery is not included.
- Moving/copying a recording to another path creates a different source identity.
  Reusing the same path collides intentionally; content-level deduplication is
  deferred. Windows case-sensitive directories are not distinguished by case.
- Completed/ignored names are remembered while present. If a name disappears and
  later reappears, it is reconsidered; database identity still prevents reinsertion
  of a previously stored path. Memory use scales with directory entries.

This remains shadow mode with no CAD authority. The watcher runs no FFmpeg,
Whisper, classification, incident creation, status updates, WebSockets, or CAD
actions. Secrets and recordings must never be committed to Git.

## Health endpoint

`GET http://127.0.0.1:8080/api/health` returns HTTP 200 with
`Content-Type: application/json` and the following body:

```json
{"status":"ok","service":"greenwich-fire-responder-api"}
```

This endpoint reports that the API is running; it does not check external services.

## Readiness endpoint

`GET http://127.0.0.1:8080/api/ready` pings PostgreSQL. It returns HTTP 200 when
reachable:

```json
{"status":"ready","database":"ok"}
```

When unavailable or unconfigured, it returns HTTP 503 with the same safe body for
both conditions:

```json
{"status":"not_ready","database":"unavailable"}
```

Both responses use `Content-Type: application/json` and `Cache-Control: no-store`.
Readiness checks connectivity, not migration version or application data.

## Format, vet, and test

Run from `backend/`:

```sh
gofmt -w cmd/api internal/config internal/database internal/httpapi internal/recordings
go vet ./...
go test ./...
```

Tests cover liveness independence, ready/unavailable/unconfigured responses,
configuration defaults and validation, explicit environment-only loading, optional
recovery, required startup checks, safe errors, deadlines, and pool cleanup. They
use small fake interfaces and do not require a running PostgreSQL database.
Recording tests also cover the exact native filename, supported channels and
extensions, exclusions, stability, startup snapshots, duplicate/retry handling,
cancellation, and file safety with temporary synthetic fixtures. Creating real
symlinks requires OS permission; that test reports a skip when unavailable, while
the Windows reparse-attribute test remains independent of symlink privileges.

## Layout

- `cmd/api/main.go`: configuration, HTTP server lifecycle, and graceful shutdown.
- `internal/config/`: environment configuration and validation tests.
- `internal/database/`: pgxpool lifecycle, safe ping checks, and tests.
- `internal/httpapi/`: routing, liveness/readiness handlers, and HTTP tests.
- `internal/recordings/`: metadata parser, polling watcher, path identity, platform
  safety checks, and tests behind a small `Store` interface.

The Angular application remains in `../web/` and runs separately.
