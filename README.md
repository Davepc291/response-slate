# Response Slate

[![Response Slate CI](https://github.com/Davepc291/response-slate/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Davepc291/response-slate/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

Response Slate is an **in-development, radio-assisted fire incident and
unit-status board**. Its current implementation provides recording-processing,
transcription-review, and deterministic address-processing foundations. The
incident engine, unit-status engine, and responder board are still planned.

> **Experimental development software.** Response Slate is not affiliated with
> or endorsed by any fire department. It is not currently suitable for emergency
> response or life-safety decisions.

## Overview

The project explores how radio recordings can support a reviewable information
board while keeping source evidence separate from operational decisions. Current
work focuses on ingestion, audio analysis, transcription quality, and conservative
interpretation of individual transcripts. Development follows a shadow/read-only
boundary with no authority over an existing computer-aided dispatch (CAD) system.

Response Slate aims to follow professional open-source practices similar in
spirit to projects such as ThinLine Radio: clear documentation, reviewable changes,
automated checks, and explicit limitations. It is an independent project; this
comparison does not imply affiliation, endorsement, or shared implementation.

Some internal documentation and identifiers retain the earlier name
**Greenwich Fire Responder V3**. The initial address dictionary and radio metadata
rules are Greenwich-specific; general support for other jurisdictions is not yet
implemented.

## Current features

- Go API foundation with liveness, database readiness, graceful shutdown, and
  transcription operations monitoring.
- SDRTrunk filename metadata ingestion with file-stability checks, startup
  protection against historical replay, and duplicate protection.
- FFmpeg/FFprobe audio analysis, normalized audio fingerprints, and persisted
  processing evidence in PostgreSQL.
- Optional Whisper-compatible transcription with bounded requests, recorded
  attempts, and retry/lease handling.
- Human transcript-review CLI with append-only review history and private dataset
  exports; offline accuracy evaluation and a separate controlled A/B experiment
  harness.
- Deterministic offline address processing, described below.
- Windows Task Scheduler scripts for managing a local Whisper runtime.
- Angular application scaffold with unit tests and a production build check.

These foundations do not yet provide automatic incident creation, live unit-state
tracking, a completed operational wallboard, or CAD updates. The experiment harness
is optional tooling, not a production transcription policy.

## Architecture

| Component            | Current responsibility                                                                   |
| -------------------- | ---------------------------------------------------------------------------------------- |
| **Go**               | API, recording-processing workers, review/evaluation tools, and offline address packages |
| **Angular**          | Frontend scaffold for the planned desktop and phone interface                            |
| **PostgreSQL**       | Recording metadata, processing/transcription evidence, and review history                |
| **FFmpeg / FFprobe** | Audio normalization and measurements                                                     |
| **Whisper**          | Optional speech-to-text provider through a compatible HTTP endpoint                      |
| **Docker Compose**   | Local development PostgreSQL service; no application deployment stack                    |

The implemented recording path is ingestion  ->  audio analysis  ->  optional
transcription  ->  persisted evidence. Review, evaluation, and address-processing
tools build on that foundation. The address pipeline is standalone and is not
wired into the API, production transcription worker, or a board.

## Completed address pipeline stages

| Stage                                                          | Scope                                                                                     |
| -------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| [5A2: Dictionary](backend/internal/addressdata/README.md)      | 1,211 canonical street entries and 2 separate access-road entries, embedded and validated |
| [5A3: Candidates](backend/internal/addresscandidate/README.md) | Exact street-name and supported numeric address evidence from one transcript              |
| [5A4: Roles](backend/internal/addressrole/README.md)           | Conservative dispatch-cue primary interpretation and explicit cross-street patterns       |
| [5A5: Composition](backend/internal/addresspipeline/README.md) | One unchanged transcript  ->  candidates  ->  role interpretation  ->  structured result           |

The pipeline retains original evidence and UTF-8 byte offsets, keeps candidate
mentions and cross-street roles separate, and preserves unresolved or ambiguous
results for later handling. It does not combine transmissions, correct addresses,
perform fuzzy matching, or geocode. A resolved syntactic role does not establish
dispatch truth.

## Testing and reliability

[Response Slate CI](.github/workflows/ci.yml) runs for pushes to `main`, pull
requests targeting `main`, and manual dispatch. Separate jobs check Go formatting,
run `go vet` and Go tests, install locked npm dependencies, run Angular tests in
CI mode, and build the frontend for production.

Tests use synthetic fixtures and small test interfaces where practical. Address
tests cover dictionary integrity across LF/CRLF checkouts, exact matching,
ambiguity, original byte offsets, deterministic results, and concurrent calls.
Database schema tests and explicitly enabled integration checks are documented
separately; ordinary CI does not start database or transcription services.

Passing tests are development evidence, not certification for operational use.
Transcription errors, incomplete evidence, recovery limitations, and ambiguous
language still require careful review. Credentials, recordings, private transcripts,
and generated review datasets must not be committed to Git.

## Development status and roadmap

Response Slate is being built in small, testable milestones. Current work provides
the processing and offline interpretation foundations; it does not constitute a
deployable emergency-response product.

Planned work includes:

1. Expand reviewed evaluation evidence and test conservative interpretation
   against more representative inputs.
2. Design and validate shadow-only incident and unit-status logic with explicit
   audit trails and ambiguity handling.
3. Build the read-only board interface and connect reviewed backend capabilities
   through defined API and update mechanisms.
4. Strengthen recovery, access controls, backup/restore verification, and extended
   shadow testing before considering any operational-readiness claims.

The [product requirements](docs/product-requirements.md) describe intended
behavior and open questions. Requirements and roadmap items are not a list of
features already implemented.

## Development setup

Use the existing component documentation for prerequisites and commands:

- [Development environment and local operations](docs/development.md)
- [Backend configuration, running, tests, and review tools](backend/README.md)
- [Local database migrations and schema tests](database/README.md)
- [Angular development and build instructions](web/README.md)
- [Offline transcription accuracy evaluation](docs/offline-transcription-evaluation.md)
- [Controlled transcription experiment workflow](docs/transcription-experiments.md)
- [Windows local Whisper runtime management](ops/windows/whisper/README.md)

Keep local credentials and private data outside source control. Follow the
documented opt-in procedures for any live integration or transcription experiment.

## License

Response Slate is licensed under the **GNU General Public License, version 3
(GPL-3.0)**. See [LICENSE](LICENSE) for the full terms, including the warranty
disclaimer. Dependencies retain their respective licenses.
