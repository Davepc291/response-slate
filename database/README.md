# Local development database

This directory contains PostgreSQL migrations and SQL tests for the local Docker
development database in this repository's `greenwich-fire-responder` Compose
project. This milestone stores radio metadata and immutable shadow unit-status
decisions only. It creates no incidents, current board state, or application
database integration.

## Schema

- `schema_migrations`: applied version strings (including `000001`) and timestamps.
- `talkgroups`: four confirmed channels; only 57201 can create incidents. All four
  accept unit status and start active.
- `units`: eight ordered primary board units and eight additional trusted units.
  Unknown stations and non-primary display positions are NULL. SQ8 is STN8;
  there is no volunteer classification in this schema.
- `radio_identities`: 15 confirmed trusted mappings. RID 577811 is prohibited in
  this reference table, but remains recordable in transmissions.
- `radio_transmissions`: unique audio fingerprints, source metadata, optional raw
  TGID/RID values, recording time, audio measurements, transcript/model metadata,
  workflow status, and errors. Unknown metadata may be NULL; TGID/RID have no
  reference-table foreign keys. Fingerprints must be nonblank content-derived
  identifiers; the ingestion milestone must choose and consistently apply the
  fingerprint algorithm. No audio is stored in this table.
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

Secrets, recordings, and database backups must never be committed to Git.
