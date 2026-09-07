# Greenwich Fire Responder V3 backend

## Step 3A: local human transcript review

`cmd/transcript-review` provides `queue`, `show`, `submit`, `history`, `stats`, and
`export`. It starts no server, opens no applications, plays no audio, copies no
recordings, and sends no Whisper requests. Step 3A collects explicit human reference
evidence. Accuracy scoring/evaluation belongs to later Step 3B; prompt/vocabulary
tuning and the RID/status classifier are not part of this milestone.

Apply migration `000006` using [database/README.md](../database/README.md). Privately
set the existing `GFR_DATABASE_URL` in the process environment to the local
development database, with `sslmode=disable` for the existing local Compose setup.
The CLI accepts only loopback PostgreSQL URLs and rejects host overrides to other
machines. It does not load `.env`. `GFR_REVIEW_DB_TIMEOUT` defaults to `10s`, accepts
`1s` through `30s`, and bounds the whole command including connection acquisition;
individual connection attempts are capped at three seconds. Errors omit driver
details, credentials, and file paths.

From `backend/`, a real person performs this workflow:

```powershell
go run ./cmd/transcript-review queue --limit 25
$transmissionId = Read-Host 'Transmission ID from queue'
go run ./cmd/transcript-review show --id $transmissionId
$attemptId = Read-Host 'Exact transcription_attempt_id shown above'
$reviewer = Read-Host 'Public reviewer label (not a secret)'
```

Only explicit `show --id` returns the local source path and exact raw model text.
The person must manually listen to that recording in a trusted player and compare
the evidence. Nothing in the CLI verifies listening or infers corrections.
Never guess missing words from Whisper alone; use follow-up or exclusion when
the recording cannot support a reliable reference.

Create a private correction file without overwriting an existing one:

```powershell
$reviewDir = Join-Path $env:LOCALAPPDATA 'GreenwichFireResponder\review-data'
New-Item -ItemType Directory -Path $reviewDir -Force | Out-Null
$reference = Join-Path $reviewDir 'reference.txt'
New-Item -ItemType File -Path $reference -ErrorAction Stop | Out-Null
```

Manually edit that file as UTF-8 with the words actually heard, then save it. A
UTF-8 BOM is accepted and removed as encoding metadata; other text and whitespace
are retained exactly. Maximum reference size is 65,536 UTF-8 bytes. Invalid UTF-8,
controls other than tab/CR/LF, Unicode format controls, symlinks/reparse points,
linked ancestors, missing files, and unsafe paths are rejected. File arguments
must be clean absolute local paths with existing parent directories.

```powershell
go run ./cmd/transcript-review submit --id $transmissionId --attempt $attemptId --reviewer $reviewer --verdict accepted --text-file $reference --confirm-human
go run ./cmd/transcript-review history --id $transmissionId --limit 50
go run ./cmd/transcript-review stats
```

`--confirm-human` is an explicit acknowledgment, not identity verification. Do not
use it for automated or guessed corrections. Reviewer labels are mandatory,
nonblank, at most 80 UTF-8 bytes, and must not contain secrets. Optional `--notes`
allows 4,096 bytes; `--reason` allows 1,024 bytes. The correction itself is accepted
only through `--text-file`, never a command-line text argument.

| Verdict | Meaning |
| --- | --- |
| `accepted` | Person submitted a nonblank reference after reviewing the recording. Eligible for export only while this is the latest review. |
| `excluded` | Omit from the accepted dataset; requires a nonblank `--reason`. |
| `needs_followup` | Unresolved review, never accepted dataset truth; reference text is optional. |

To withdraw mistaken accepted truth, append a superseding review:

```powershell
go run ./cmd/transcript-review submit --id $transmissionId --attempt $attemptId --reviewer $reviewer --verdict needs_followup --notes 'Needs another listening review' --confirm-human
# Or exclude with a human-supplied reason:
go run ./cmd/transcript-review submit --id $transmissionId --attempt $attemptId --reviewer $reviewer --verdict excluded --reason 'Recording cannot support a reliable reference' --confirm-human
```

History is never deleted or rewritten. Highest review ID wins across **all**
verdicts; filtering accepted happens afterward. A later follow-up/exclusion removes
an earlier acceptance from exports. Queue includes eligible records whose latest
review is not accepted, including excluded records for deliberate re-review;
it shows latest verdict but no source path, filename, RID, or transcript. Queue
limits are 1–200 (default 25); history limits are 1–500 (default 50), newest first.
Stats counts latest accepted/excluded/follow-up/unreviewed eligible recordings,
plus accepted train/validation/test counts, not all historical review events.

Export accepted latest references to private JSONL:

```powershell
$export = Join-Path $reviewDir 'dataset.jsonl'
go run ./cmd/transcript-review export --output $export --limit 100
# Deliberately replace an existing export only when intended:
go run ./cmd/transcript-review export --output $export --limit 100 --overwrite
```

Export streams in canonical-fingerprint order, default 100 and maximum 1,000
records. The reported limit may truncate a larger accepted dataset; increase it
explicitly within the bound. A same-directory temporary file is flushed before
publication. Windows uses a no-replace rename unless `--overwrite` is supplied;
Unix uses atomic hard-link publication for no-replace and rename for overwrite.
Failures before publication preserve the old target and remove temporary files.
Existing exports, directories, reparse/symlink targets and linked ancestors are
checked; only `.jsonl` targets are allowed. Use a private local directory protected
from concurrent hostile filesystem changes. Power loss or forced termination may
leave an uncommitted temporary file; this is not a crash-proof archival system.

Each JSONL record contains only `dataset_id`, `audio_fingerprint`,
`transcription_attempt_id`, `channel`, `tgid`, `duration_ms`,
`raw_model_transcript`, `human_reference_transcript`, `model`, `split`, and
`reviewed_at` (UTC). Model identifiers are bounded tokens, not URLs or binary
version attestations. No path, filename, RID, reviewer label, notes, reason,
provider URL, settings, token, database error, or audio bytes are exported.
`dataset_id` is `gfr-audio-v1:` plus MD5 of the canonical fingerprint; the full
canonical PCM SHA-256 fingerprint is also retained for integrity and deduplication.

Split policy v1 assigns `first_32_bits(MD5(fingerprint)) % 100`: 0–79 train,
80–89 validation, 90–99 test, approximately **80/10/10**. MD5 is a deterministic
distributor, not a security hash. One immutable dataset item exists per canonical
audio fingerprint; aliases cannot enter the dataset or cross splits. Small
datasets need not match the ratio exactly. Keep the existing canonical decoding
policy consistent; a future fingerprint/split policy needs a separate migration.

Operational raw and corrected transcript text can contain sensitive locations,
names, or other information despite metadata minimization. The export preserves
evidence rather than redacting text automatically. Review its contents before
sharing; never use reviewer labels or notes for secrets. Live transcripts,
corrections, recordings, and generated exports must never be committed. Root
`.gitignore` excludes JSONL, private review directories, and designated correction
suffixes; it cannot stop `git add -f` or content saved under unrelated extensions.
Append-only storage retains withdrawn references; privacy deletion/retention and
authenticated reviewer identities require a separately authorized future policy.
Raw and corrected transcripts are evidence, **never CAD commands**.

## Transcription operations monitoring

Apply migration `000005` before running this version; see
[database instructions](../database/README.md). No automatic migrations or `.env`
loading occurs. `GET /api/operations/transcription` returns fixed-shape operational
JSON, `Cache-Control: no-store`, and a UTC `observed_at`. It never returns raw
transcripts, filenames, paths, RIDs, model/user labels, credentials, or provider
URLs. Keep the default loopback binding; authentication is still a later milestone.

Metrics cover only direct children of `GFR_RECORDINGS_DIR`, using the same exact
path scope as transcription selection. With no directory configured, this is an
empty scope. Monitoring still runs when transcription is disabled. Queries have
a two-second deadline. Query failure returns HTTP 503 with
`status=database_unavailable`, `metrics=null`, and no internal error detail.
All other monitoring states return HTTP 200. `/api/health` and `/api/ready` retain
their existing contracts: backlog or provider failure never makes database
readiness fail.

| Field | Meaning |
| --- | --- |
| `waiting_jobs` | Analyzed canonical jobs with pending/failed/expired-processing status, retryable, and no future retry/lease deadline. Includes expired final claims awaiting exhaustion bookkeeping, just like worker selection. |
| `oldest_waiting_age_ms` | Database observation time minus oldest eligible job's metadata `created_at`, clamped at zero; NULL for an empty queue. Includes analysis and any prior retry delay, not radio recording age. |
| `processing_jobs` | Claims with processing status and an unexpired lease. A crashed worker may remain counted until lease expiry. |
| `retry_waiting_jobs` | Retryable failed jobs whose next attempt is not yet due. Excluded from `waiting_jobs`. |
| `completed_jobs`, `failed_jobs`, `skipped_jobs` | Current transcription row states in scope; failed includes both retryable and permanent failures. Skipped includes content aliases. |
| `attempt_count` | All persisted claim attempts in scope, including interrupted attempts and source failures before HTTP. |
| `retry_count` | Attempts numbered greater than one, not retries merely scheduled for the future. |
| `timeout_count` | Attempts whose safe error code is `provider_timeout`; not distinct jobs. |
| `last_success_at`, `last_provider_failure_at` | Latest persisted finished attempt timestamps; provider failures include unavailable, timeout, rejected, and invalid-response outcomes. NULL if none. |

`recent_timings` covers the **latest 100 finished attempts** in scope. Each of its
five fields contains `samples`, `mean_ms`, `p95_ms` (continuous percentile), and
`max_ms`; absent measurements have zero samples and NULL summary values:

- `ingestion_to_claim`: attempt `started_at` minus metadata `created_at`, including
  analysis time and retries. Each retry is a separate sample.
- `claim_to_request`: client request-start UTC timestamp minus PostgreSQL attempt
  `started_at`. Negative clock skew is clamped at zero.
- `provider_request`: monotonic client duration immediately before `Client.Do`
  through response read/validation. Includes connection, upload, network, and
  server execution; excludes source opening and multipart setup. This is not
  server-only inference time.
- `claim_to_completion`: successful attempt `finished_at` minus `started_at`.
- `ingestion_to_completion`: successful attempt `finished_at` minus metadata
  `created_at`. Both completion summaries exclude unsuccessful attempts.

Request timings are nullable for historical records, unissued requests, and
crashes before completion persistence. They are never backfilled with estimates.
The existing claim fence and the new wrapper persist outcome and timing in one
transaction. Late/repeated completion cannot replace either. No transaction is
held across HTTP. An uncertain commit/crash can still cause a repeated remote
request, as before.

`worker_state` is process-local: disabled, idle, claiming, preparing, requesting,
persisting, database_unavailable, or stopped. `provider_state` is unknown until an
actual request outcome, then recent_success or recent_failure, and stale after
60 seconds. No provider health probes are issued by monitoring. `healthy_idle`
and `healthy_backlog` mean recent successful request evidence, **not guaranteed
current availability**. Other top-level states are provider_unknown,
provider_unavailable, worker_disabled, and database_unavailable. Worker disabled
takes precedence over provider evidence; database-query failure takes precedence
over both. `consecutive_provider_failures` counts completed provider-failure
outcomes since the last success in this API process, not across restarts.

Warnings are observational only and never change retries, readiness, unit state,
incidents, or CAD. Set these process-environment values (invalid values fail
startup with a safe error):

| Variable | Default | Allowed |
| --- | --- | --- |
| `GFR_MONITOR_WARN_QUEUE_DEPTH` | `10` | 1–100000 jobs |
| `GFR_MONITOR_WARN_OLDEST_AGE` | `30s` | 1s–24h |
| `GFR_MONITOR_WARN_REQUEST_DURATION` | `15s` | 1ms–5m |
| `GFR_MONITOR_WARN_CONSECUTIVE_FAILURES` | `3` | 1–100 outcomes |

Thresholds trigger at **greater than or equal to** the configured value. A
background monitor samples every five seconds; endpoint reads also refresh queue
warnings. Fixed-key structured `transcription_monitor` logs report backlog,
oldest_job_age, slow_provider_request, provider_timeout, retry, provider_failures,
and metrics_unavailable transitions. Repeated active conditions do not repeat
warnings; clearing the condition logs `active=false`. One provider_recovery event
is emitted on success after a failure streak. Existing per-attempt audit logs
remain; database claim polling errors now log once per failure episode. A brief
queue peak may not be seen between samples. Aggregates scan historical scoped
evidence and may time out on a very large database; the response remains bounded.

Example (excerpt; timing objects omitted here for brevity):

```json
{"status":"healthy_idle","database":"ok","worker_state":"idle","provider_state":"recent_success","consecutive_provider_failures":0,"observed_at":"2026-09-07T02:00:00Z","metrics":{"waiting_jobs":0,"oldest_waiting_age_ms":null,"processing_jobs":0,"retry_waiting_jobs":0,"completed_jobs":1,"failed_jobs":0,"skipped_jobs":0,"attempt_count":1,"retry_count":0,"timeout_count":0}}
```

For verification and recovery, see [development operations](../docs/development.md#transcription-monitoring-and-recovery).

## Optional remote speech-to-text

For the verified Windows whisper.cpp runtime, optional per-user Task Scheduler
management is provided under [ops/windows/whisper](../ops/windows/whisper/README.md).
It binds only `127.0.0.1:8001` and uses small.en, English, CPU-only inference with
8 threads and one processor. Installation is manual and separate from API startup;
transcription remains disabled by default. Set `GFR_TRANSCRIPTION_BASE_URL` to
`http://127.0.0.1:8001` in the API process environment when opting in. The guide
covers validation, bounded logs/restarts, exact task targeting, status, and rollback.

Apply migration `000004` before enabling transcription. A separate serial worker
polls successfully analyzed canonical transmissions in `GFR_RECORDINGS_DIR` once
per second. It never scans other directories or transcribes audio aliases. The
Windows directory example is `C:\Users\User\SDRTrunk\recordings`. Existing filenames
are still seeded without ingestion replay; the transcription worker can resume
already-ingested pending canonical work in this explicitly configured directory.

The provider is configurable shared HTTP infrastructure, not ThinLine application
code. Integration uses only its HTTP API, with no dependency on its source code
or X-TLR headers. This application must not administer or restart that service.
An unauthenticated plain-HTTP endpoint is suitable only on a trusted private LAN
until a dedicated authenticated service is built. Prefer HTTPS and configure the
optional bearer token privately when supported. Audio leaves this computer when
transcription is enabled; recordings and transcripts remain sensitive evidence.

Set these **process environment** variables explicitly; `.env` is never loaded by
the API. Do not put service addresses with credentials, tokens, or recordings in Git.

| Variable suffix after `GFR_TRANSCRIPTION_` | Default | Bounds / behavior |
| --- | --- | --- |
| `ENABLED` | `false` | Boolean; no provider calls when disabled |
| `BASE_URL` | Empty | Required when enabled; HTTP(S), no userinfo, query, fragment |
| `MODEL` | `small.en` | Nonblank, at most 128 bytes |
| `LANGUAGE` | `en` | Nonblank, at most 32 bytes |
| `TIMEOUT` | `60s` | 1 second to 5 minutes, including upload and response |
| `MAX_RESPONSE_BYTES` | `1048576` | 1024 to 4194304 bytes |
| `MAX_AUDIO_BYTES` | `33554432` | 1024 to 134217728 bytes |
| `MAX_ATTEMPTS` | `3` | 1 to 5 total attempts, including interrupted claims |
| `RETRY_DELAY` | `5s` | 1 second to 1 minute; exponential backoff capped at 1 minute |
| `BEARER_TOKEN` | Empty | Optional, environment only, at most 4096 bytes |
| `PROMPT`, `WORD_BOOST` | Empty | Optional, each at most 2048 bytes; stored as evaluation settings |

Configuration text rejects control characters. Never put secrets in a prompt or
word boost. Requests stream the verified original MP3/WAV file from disk using
multipart `file`, `model`, `language`, `prompt`, `response_format=json`,
`temperature=0`, `beam_size=5`, `best_of=5`, and `word_boost`. Analysis has already
validated and fingerprinted the audio; uploading the original preserves the
verified service's input contract. The upload filename is a generic basename.
No redirect is followed, including redirects to another URL on the same host.
Error response reads are capped at 4096 bytes and never logged or persisted.

Only HTTP 200 with a valid nonempty JSON string `text` succeeds. Trailing JSON,
oversized bodies, invalid text, and unsafe control/format characters are rejected.
`transcription_raw_text` stores the exact decoded provider string; `transcript`
only collapses whitespace. Neither is interpreted as instructions. Whisper can
hallucinate; the observed baseline **"Thank you for reporting on the distraction."**
is preserved as evidence without correcting its words or inferring any apparatus.
Provider, model, language, prompt, fixed decoding options, timestamps, and attempt
outcomes allow later evaluation. There is no classification, status change,
incident creation, WebSocket, or CAD action; the application remains unofficial
and has no CAD authority.

Lifecycle: pending waits for an enabled worker; processing owns a database lease;
completed preserves successful evidence; failed with `transcription_retryable=true`
waits for its retry time; failed with false is permanent/exhausted; duplicate
aliases are skipped. Disabled is an operational worker state, logged explicitly,
and leaves database work unchanged rather than discarding it. HTTP 408/429/5xx,
transport failures, and timeouts retry within the configured limit. Other HTTP
errors, malformed responses, oversized input, and unavailable/changed source files
are permanent. Cancellation records a safe canceled attempt and permits a bounded
retry if attempts remain.

Claims use short PostgreSQL transactions and unique tokens; no transaction stays
open during HTTP. A lease expires after the request timeout plus 30 seconds.
Restart recovers expired claims, retaining interrupted attempts. Successful text
cannot be overwritten by a late result or ordinary UPDATE. The provider does not
offer an idempotency contract: a crash after remote processing but before database
completion can require another HTTP request. Exactly-once remote execution across
that failure boundary is not promised; completed database results and normal
duplicate observations are idempotent. A lost source file or interrupted audio
analysis still requires operator review; transcription does not recover analysis.

Structured logs contain safe outcomes and transmission IDs, never raw provider
errors, transcript text, tokens, URLs, or authorization headers. `/api/health`
remains independent of both services; `/api/ready` still checks PostgreSQL only.
Ctrl+C cancels in-flight requests, joins workers, closes idle HTTP connections,
and then closes PostgreSQL. No continuous Whisper health polling is performed.

Normal `go test ./...` uses fakes/httptest and skips the opt-in live check. See
[development instructions](../docs/development.md#remote-transcription-verification)
for its prerequisites and cleanup. `internal/transcription` owns the provider and
worker; `internal/database/transcription.go` implements the small persistence
interface. No additional Go dependencies are required.

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
metadata and performs audio analysis. It does not run migrations or write incident, unit-status, or board
state. No Docker API service is included.

## SDRTrunk recording ingestion

Apply migrations `000001`, `000002`, and `000003` in order using the local commands in
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
does not depend on filesystem notifications. Metadata ingestion does not read audio;
the analysis stage receives a verified read-only handle afterward. Neither stage
modifies, renames, moves, or deletes recordings. It rejects directories, nonregular files,
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

`audio_fingerprint` remains reserved for audio-content hashing and is NULL at initial
metadata insertion. Parsed fields, source path, observed size, and modification time
are persisted first. The analysis stage then fills duration and audio measurements.
Transcription stays `pending`, and transcript remains unset.

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
  Reusing the same path collides intentionally. Audio analysis links identical
  canonical content to one transmission while preserving the additional path as
  an alias row. Windows case-sensitive directories are not distinguished by case.
- Completed/ignored names are remembered while present. If a name disappears and
  later reappears, it is reconsidered; database identity still prevents reinsertion
  of a previously stored path. Memory use scales with directory entries.

This remains shadow mode with no CAD authority. Optional transcription follows
analysis as described above. It runs no classification,
incident creation, status updates, WebSockets, or CAD actions. Secrets and recordings
must never be committed to Git.

## Audio-analysis foundation

FFprobe validates accepted stable recordings, then FFmpeg decodes them for streaming
measurement. Install both tools or configure their explicit executable paths.
They are invoked directly with `exec.CommandContext`, never through a shell.
Neither tool runs when `GFR_RECORDINGS_DIR` is empty.

| Variable | Default | Allowed range / purpose |
| --- | --- | --- |
| `GFR_FFPROBE_PATH` | `ffprobe` | Executable name on PATH or explicit path |
| `GFR_FFMPEG_PATH` | `ffmpeg` | Executable name on PATH or explicit path |
| `GFR_AUDIO_TIMEOUT` | `30s` | 1sâ€“5m; shared deadline for probe and decode per attempt |
| `GFR_AUDIO_MAX_DURATION` | `10m` | 1sâ€“1h; upper bound on reported duration and decoded samples |
| `GFR_AUDIO_MAX_ATTEMPTS` | `3` | 1â€“10; bounded attempts, also counted in PostgreSQL |
| `GFR_AUDIO_RETRY_INTERVAL` | `2s` | 10msâ€“1m; cancellation-aware delay between attempts |

FFprobe JSON must describe exactly one usable audio stream, with no additional
video or other streams. Actual detected formats must be MP3 with MP3 audio, or WAV
with supported PCM (`pcm_u8`, `pcm_s16le`, `pcm_s24le`, `pcm_s32le`, `pcm_f32le`,
or `pcm_f64le`). Sample rates must be 8â€“192 kHz and channel counts 1â€“8. Extension
alone is not proof of valid audio. Corrupt, empty, unsupported, multiple-stream,
video-only, and excessive-duration inputs fail safely.

Only allowlisted probe fields are stored in `audio_probe`: format, codec, source
sample rate/channel count, and reported duration when available. Pipe input may
not report duration. Stored `duration_ms` is authoritative decoded sample count
divided by 16,000, rounded to the nearest millisecond with half milliseconds rounded
up; it may differ from a container's duration estimate or encoder padding.

Canonical output is raw signed 16-bit little-endian PCM, mono, 16,000 Hz, without a
container header. `audio_fingerprint` uses this versioned format:

```text
pcm-s16le-16000-mono-v1:sha256:<64 lowercase hex characters>
```

The digest covers every decoded PCM byte, in order. Peak is the maximum absolute
sample amplitude divided by 32768; RMS is the square root of the mean squared
normalized amplitudes. Thus -32768 is exactly 1.0, while +32767 is 32767/32768.
Hashing and measurement work across arbitrary output chunks without retaining PCM.
Decoded bytes are limited to the configured duration at 32,000 bytes/second.

The fingerprint represents exact decoded bytes, not perceptual similarity. Changes
to resampling/downmix behavior or FFmpeg versions can change those bytes. Keep the
decoder build consistent across producers; changing the canonical rules requires
a new version prefix and an explicit compatibility/reanalysis decision. No existing
fingerprints are silently rewritten.

### Execution safety and state

After metadata insertion, the watcher opens the file read-only beneath its anchored
directory, checks identity/size/mtime and link safety, and passes that handle to the
processor. The source is checked again around analysis. FFprobe/FFmpeg read `pipe:0`;
they receive neither a source pathname nor database credentials. Protocol access is
restricted to `pipe`, and input demuxers to MP3/WAV. Decoded stdout is streamed, never
written beside the recording. FFprobe stdout is bounded to 64 KiB; stderr is capped
at 16 KiB and excess is drained/discarded. Raw stderr is never logged or stored.
Child environment contains only platform execution variables, excluding database
settings and `FFREPORT`. Context cancellation kills/reaps the direct tool process;
Windows child windows are hidden. Individual FFmpeg allocations are capped at 64 MiB.

| Outcome | Database state | Logging / retry behavior |
| --- | --- | --- |
| Claimed | `processing`, increment `analysis_attempts` | One worker may claim an eligible row |
| Analyzed | `completed`, measurements/probe/fingerprint committed atomically | `analyzed`; no retry |
| Invalid audio | `failed`, `analysis_retryable=false`, safe error code | `invalid`; no retry |
| Tool unavailable, timeout, source changed, transient failure | `failed` with safe error code | `failed`; retry within attempt cap, then stop |
| Identical content at another path | `skipped`, `audio_duplicate_of` points to canonical row | `duplicate-content`; no retry |

Duplicate path rows remain metadata aliases, not another logical transmission.
Their fingerprint stays NULL to preserve uniqueness; measurements and probe fields
are retained. Use `audio_duplicate_of IS NULL` when counting logical transmissions.
See `../database/README.md` for the transactional fingerprint-locking strategy.

Analysis is serial within the watcher, so a long recording delays later scans but
does not block HTTP handlers. Shutdown cancels analysis, records a safe failure when
possible, joins the watcher, and then closes the database pool. If PostgreSQL cannot
persist a failure, only a safe failure log is possible. A process crash or ambiguous
claim response may leave `processing` rows requiring operator review; this is not a
durable background queue and does not replay historical recordings after restart.
Size/mtime checks remain a stability heuristic, not protection against all concurrent
writer behavior. Native decoder memory is not isolated by an OS sandbox.

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
gofmt -w cmd/api internal/config internal/database internal/httpapi internal/recordings internal/audioanalysis internal/transcription
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

Audio unit tests use fake tools and the Go test executable as a helper process;
they do not require installed FFmpeg. They cover probe validation, canonical SHA-256,
PCM edge cases, chunk boundaries, bounded output, timeouts/cancellation, missing
executables, sanitized environment/errors, persistence outcomes, and shutdown.
Real-tool verification is a separate controlled local integration exercise described
in `../docs/development.md`.

## Layout

- `cmd/api/main.go`: configuration, HTTP server lifecycle, and graceful shutdown.
- `internal/config/`: environment configuration and validation tests.
- `internal/database/`: pgxpool lifecycle, safe ping checks, and tests.
- `internal/httpapi/`: routing, liveness/readiness handlers, and HTTP tests.
- `internal/recordings/`: metadata parser, polling watcher, path identity, platform
  safety checks, and tests behind a small `Store` interface.
- `internal/audioanalysis/`: tool execution, probe parsing, streaming PCM analysis,
  bounded retry orchestration, and unit tests behind small tool/store interfaces.

The Angular application remains in `../web/` and runs separately.
