# Local development database

This directory contains PostgreSQL migrations and SQL tests for the local Docker
development database in this repository's `greenwich-fire-responder` Compose
project. This milestone stores radio metadata and immutable shadow unit-status
decisions, plus audio-analysis measurements. Migration `000002` adds recording
ingestion; `000003` adds analysis bookkeeping and content deduplication.
The Go API inserts metadata and analyzes audio; these migrations create no incidents or
current board state.

## Schema

- `schema_migrations`: applied version strings (including `000001`) and timestamps.
- `talkgroups`: four confirmed channels; only 57201 can create incidents. All four
  accept unit status and start active.
- `units`: eight ordered primary board units and eight additional trusted units.
  Unknown stations and non-primary display positions are NULL. SQ8 is STN8;
  there is no volunteer classification in this schema.
- `radio_identities`: 15 confirmed trusted mappings. RID 577811 is prohibited in
  this reference table, but remains recordable in transmissions.
- `radio_transmissions`: unique audio fingerprints or path-based source identities,
  source metadata, optional raw
  TGID/RID values, recording time, audio measurements, transcript/model metadata,
  workflow status, and errors. Unknown metadata may be NULL; TGID/RID have no
  reference-table foreign keys. `audio_fingerprint` remains reserved for nonblank
  audio-content hashes and is NULL for metadata-only ingestion. No audio is stored
  in this table.
- `unit_status_decisions`: append-only decisions referencing transmissions, with
  detected unit, prior/proposed normalized status, phrase, confidence, acceptance
  or rejection reason, classifier version, and creation time. Multiple decisions
  for one transmission are permitted so retries and rejections remain auditable.

All timestamps use `timestamptz`. Reference tables use natural primary keys;
transmissions and decisions use generated bigint identities. Mutable tables have
automatic `updated_at` triggers. Processing and chronological indexes support
work queues and retrieval by talkgroup, RID, unit, and transmission.

Workflow statuses are `pending`, `processing`, `completed`, `failed`, and `skipped`.
Duration is nonnegative milliseconds. RMS and peak use linear absolute amplitude
relative to full scale, from 0 to 1, with RMS no greater than peak; they are not
decibel measurements. Missing measurements remain NULL. Confidence is NULL when
unknown, otherwise between 0 and 1.

Decision statuses are `QUARTERS`, `ENROUTE`, `ONSCENE`, and `ONAIR`. Normalize aliases
before insertion. Prior status can be unknown; rejected decisions may have no
detected unit or proposed status. Accepted decisions require both. Detected units
are evidence strings, not foreign keys, so unrecognized classifications can still
be audited. A rejection requires a nonblank reason. The future engine remains
responsible for trusted-RID checks, transition validation, and duplicate decisions;
this migration does not implement that engine.

`shadow_mode` defaults to true and is constrained to true for this milestone.
UPDATE, DELETE, and TRUNCATE of decisions are rejected by an always-enabled
trigger. Corrections must append a decision. Referenced transmissions cannot be
deleted. Database owners/superusers can alter schema protections, so this is not
tamper-proof storage against administrators; future application roles must not
own tables or have DDL privileges. Transmission processing fields remain mutable;
the decision row preserves its own classification evidence.

## Apply migration to the running local container

Run from the repository root in PowerShell. The commands use the container's
configured database and username without displaying credentials or reading a
password into the command line. They do not start containers. Configure the local
environment as described in `docs/development.md` beforehand.

First confirm the local Docker context and the expected running service:

```powershell
docker context show
docker ps --filter label=com.docker.compose.project=greenwich-fire-responder --filter label=com.docker.compose.service=postgres --format '{{.Names}} {{.Image}} {{.Ports}}'
```

Proceed only with the local `greenwich-fire-responder-postgres-1` service using
`postgres:16-alpine`, bound to `127.0.0.1` on the configured host port. If it is not
running or the target differs, stop and verify the local setup first.

```powershell
Get-Content -Raw -LiteralPath database/migrations/000001_radio_status_foundation.sql | docker exec -i greenwich-fire-responder-postgres-1 sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Migration failed; inspect the error before continuing.' }
```

The migration uses BEGIN/COMMIT; a SQL error with `ON_ERROR_STOP` aborts execution
and connection closure rolls back its transaction. Apply version 000001 once to
an unmigrated local database. It intentionally fails on existing tables instead
of masking a mismatched schema. A successful run ends with `INSERT 0 1` and
`COMMIT`, recording version `000001` atomically with the schema and seeds.

## Migration 000002: recording ingestion

Apply `000002_recording_ingestion.sql` once, after `000001`, to the same verified
local development container. Do not reapply a migration already recorded in
`schema_migrations`. This migration is transactional and leaves migration `000001`
unchanged.

```powershell
Get-Content -Raw -LiteralPath database/migrations/000002_recording_ingestion.sql | docker exec -i greenwich-fire-responder-postgres-1 sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Migration 000002 failed; inspect the error before continuing.' }
```

Success ends with `INSERT 0 1` and `COMMIT`. The migration adds these columns to
`radio_transmissions`:

- `source_identity`: unique SHA-256 hex identity for metadata-only ingestion.
- `source_path`: cleaned absolute source path.
- `system_site_label`, `alias_channel_label`, and `channel_label`: parsed native
  labels and the canonical Greenwich channel.
- `extension` and `recording_timezone`: normalized audio extension and IANA zone.
- `source_size_bytes` and `source_modified_at`: observed stable file metadata.

The path identity hashes `sdrtrunk-path-v1`, a NUL separator, and the cleaned
absolute path; Windows paths are lowercased. The filename already includes the
recording timestamp and radio identifiers. Size and modification time are excluded
so retries or file growth do not generate new identities. A unique constraint and
`ON CONFLICT (source_identity) DO NOTHING` make repeated/concurrent ingestion
idempotent, including retries after an uncertain database response.

This is a source-path identity, not a content hash. Copying or moving a recording to
another path produces another identity; reusing the same path produces a duplicate.
Windows case-sensitive paths differing only in case are treated as the same identity.
The watcher never moves recordings itself.

`audio_fingerprint` is now nullable and remains reserved for audio-content hashing.
It stays NULL until the audio-analysis stage inspects audio content. The schema
requires at least one of `audio_fingerprint` or `source_identity`, and requires
complete source metadata when `source_identity` is supplied. Existing content-hash
rows and their uniqueness constraint remain valid. No audio bytes, transcript,
duration, RMS, or peak are populated by this ingestion milestone. The two processing
status columns retain their `pending` defaults. RID values are stored without
inferring a unit; no status decisions, incidents, or CAD actions are created.

## Migration 000003: audio analysis

Apply once after `000002`, only to the verified local development container:

```powershell
Get-Content -Raw -LiteralPath database/migrations/000003_audio_analysis.sql | docker exec -i greenwich-fire-responder-postgres-1 sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Migration 000003 failed.' }
```

The transaction adds `audio_probe` (allowlisted JSON technical metadata),
`analysis_attempts`, `analysis_retryable`, and `audio_duplicate_of` (a foreign key
to the canonical transmission). It records version `000003`; it does not rewrite
earlier migrations or existing fingerprints.

Analysis claims atomically change eligible `pending`/retryable `failed` rows to
`processing` and increment the persisted attempt counter. Already claimed or final
rows cannot be claimed again. Successful results are committed through
`complete_audio_analysis(...)`, a function invoked in a single transaction:

1. Validate the versioned fingerprint and measurement ranges.
2. Acquire a transaction-scoped advisory lock derived from the fingerprint, then
   lock the source transmission row. Concurrent calls for identical fingerprints
   serialize; unrelated hash collisions only cause extra serialization.
3. If there is no fingerprint owner, atomically set duration, RMS, peak, probe,
   fingerprint, and `completed` state on the source row.
4. If a canonical owner exists, retain the second path as a metadata alias, set
   `audio_duplicate_of` to that owner, store its measurements/probe, and mark it
   `skipped`. Its own fingerprint stays NULL to preserve the original unique
   constraint. Repeated completion calls return `already-processed`.

Count logical transmissions using `audio_duplicate_of IS NULL`; alias rows preserve
source-path idempotency and must not be published as additional transmissions.
The canonical owner cannot be deleted while aliases reference it. This stage never
deletes source rows or changes the immutable status-decision audit. The function
is the application write path; direct writers must also honor this protocol.

Fingerprint format is `pcm-s16le-16000-mono-v1:sha256:` followed by 64 lowercase hex
characters for SHA-256 over raw signed 16-bit little-endian, mono, 16 kHz decoded
PCM. Duration is decoded sample count / 16,000 rounded to nearest millisecond, ties
up. RMS/peak are linear values normalized by 32768. `audio_probe` contains source
format/codec/rate/channels and optional reported duration, not raw probe output,
filenames, tags, or credentials. Decoder changes may alter exact bytes; keep builds
consistent and introduce a new prefix when changing canonical rules.

Invalid audio uses `failed` with `analysis_retryable=false`. Retryable tool/source
failures use `failed` with a safe code and bounded attempts. A failed database
commit may have an uncertain result: failure updates target only `processing` rows,
so they cannot overwrite committed success. Retry claims and fingerprint uniqueness
preserve idempotency. A lost claim response or process crash may leave a `processing`
row requiring review; there is no automatic historical replay or lease recovery.
Transcription status remains `pending`, and no classification is performed.

## Run schema tests

```powershell
Get-Content -Raw -LiteralPath database/tests/000001_schema_test.sql | docker exec -i greenwich-fire-responder-postgres-1 sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Schema tests failed.' }
```

Tests verify seeds, permissions, primary order/stations, key RID mappings,
unknown metadata, unique fingerprints, normalized statuses, rejection reasons,
shadow defaults/enforcement, confidence, and audit immutability. Success prints
`ALL SCHEMA TESTS PASSED` and ends with `ROLLBACK`. Test rows and helper functions
are rolled back. Identity sequences may advance despite rollback, which is normal;
tests do not assume contiguous IDs. Fixtures are synthetic text, not recordings.

After migration `000002`, run its transactional schema test as well:

```powershell
Get-Content -Raw -LiteralPath database/tests/000002_schema_test.sql | docker exec -i greenwich-fire-responder-postgres-1 sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Migration 000002 schema tests failed.' }
```

This checks the version record, native metadata-only insertion, idempotent conflict
handling, source identity validation, required metadata, positive file size,
supported extensions, and compatibility with content-fingerprint inserts. Success
prints `ALL INGESTION SCHEMA TESTS PASSED` and ends with `ROLLBACK`. Its synthetic
rows do not remain in the database. The original `000001` test remains applicable.

After `000003`, also run its transactional test:

```powershell
Get-Content -Raw -LiteralPath database/tests/000003_schema_test.sql | docker exec -i greenwich-fire-responder-postgres-1 sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
if ($LASTEXITCODE -ne 0) { throw 'Audio-analysis schema tests failed.' }
```

Success prints `ALL AUDIO ANALYSIS SCHEMA TESTS PASSED` and ends with `ROLLBACK`.
The tests cover atomic completion, claim limits, retry safety, duplicate aliases,
canonical uniqueness, and measurement constraints. All fixtures are synthetic.

Secrets, recordings, and database backups must never be committed to Git.
