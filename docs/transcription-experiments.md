# Step 4B1: controlled local Whisper A/B harness

This is a separate manual CLI, not a production worker feature. This milestone
implements and tests the harness only; it does **not** run the live experiment.
All ordinary tests use synthetic files and in-process fake HTTP/database boundaries.
The managed Whisper task, production configuration, and database schema are unchanged.

## Safe future manual workflow

Run only after separately authorizing a live experiment and confirming the local
PostgreSQL and managed Windows Whisper services are already available. The CLI
does not start, stop, reconfigure, or health-probe either service. Reserve the
provider for the experiment if timing comparisons matter. Do not run the API
watcher on the experiment output directory.

From the repository root in PowerShell, establish explicit private paths:

```powershell
$repo = (Get-Location).Path
$dataset = Join-Path $env:LOCALAPPDATA 'GreenwichFireResponder\review-data\dataset.jsonl'
$recordings = Join-Path $env:USERPROFILE 'SDRTrunk\recordings'
$results = Join-Path $env:LOCALAPPDATA 'GreenwichFireResponder\experiment-results'
$prompt = Join-Path $repo 'backend\internal\transcriptexperiment\prompts\greenwich-v1.txt'
New-Item -ItemType Directory -Path $results -Force | Out-Null
$baseline = Join-Path $results 'greenwich-v1-baseline-train.jsonl'
$candidate = Join-Path $results 'greenwich-v1-candidate-train.jsonl'
```

The recordings example resolves to `C:\Users\User\SDRTrunk\recordings` for that
Windows account. The directory must contain the canonical paths already stored
in PostgreSQL; files are never guessed or located by recursive scanning.

Supply the local development connection URL through the process environment,
using your existing secure credential workflow. The CLI reads only
`GFR_DATABASE_URL`, never `.env` or a URL flag. For an interactive private entry:

```powershell
$privateURL = Read-Host 'Local PostgreSQL connection URL (hidden)' -AsSecureString
$env:GFR_DATABASE_URL = [System.Net.NetworkCredential]::new('', $privateURL).Password
```

Use a least-privileged SELECT-only database account where available. Use an
explicit local URL with `127.0.0.1` or `::1`, database, username/password, and
appropriate `sslmode` (local Compose commonly uses `sslmode=disable`). Never
paste the URL into a command argument, log, source file, or report.

Run two separate experiments, using the same original reviewed dataset and split:

```powershell
Push-Location (Join-Path $repo 'backend')
try {
    go run ./cmd/transcript-experiment --dataset "$dataset" --recordings-dir "$recordings" --output "$baseline" --name greenwich-v1-baseline --split train --request-timeout 60s --command-timeout 30m
    if ($LASTEXITCODE -ne 0) { throw 'Baseline failed; inspect the safe summary before continuing.' }

    go run ./cmd/transcript-experiment --dataset "$dataset" --recordings-dir "$recordings" --output "$candidate" --name greenwich-v1-candidate --split train --prompt-file "$prompt" --request-timeout 60s --command-timeout 30m
    if ($LASTEXITCODE -ne 0) { throw 'Candidate failed; inspect the safe summary before continuing.' }

    go run ./cmd/transcript-eval --dataset "$baseline"
    if ($LASTEXITCODE -ne 0) { throw 'Baseline evaluation failed.' }
    go run ./cmd/transcript-eval --dataset "$candidate"
    if ($LASTEXITCODE -ne 0) { throw 'Candidate evaluation failed.' }
}
finally {
    Pop-Location
    Remove-Item Env:GFR_DATABASE_URL -ErrorAction SilentlyContinue
    $privateURL = $null
}
```

No `--prompt-file` means the prompt multipart field is omitted entirely. The
candidate sends the exact UTF-8 prompt bytes, including its final newline, and
records their SHA-256. The candidate contains only the requested confirmed terms;
it adds no addresses. This text is model context, not a claim of affiliation.

The stored no-prompt transcripts remain in the original export. The fresh
no-prompt run is a timing/runtime-matched experimental baseline. Never use a
candidate result as the next input dataset: its modified raw text intentionally
does not match the historical attempt evidence. References, identities, split,
model, and review timestamp are retained unchanged in both result exports.

## Held-out protection

`--split` is mandatory. Normal experiments select exactly `train` or `validation`.
Selecting `test` additionally requires `--allow-test`; the latter is rejected
for other splits. There is no implicit split, fallback, or `all` option. The
whole input is parsed for structural validity and duplicate identities, but
nonselected records are not scored, resolved in PostgreSQL, read from recordings,
sent to Whisper, or included in output. The harness itself calculates no accuracy
metrics; use Step 4A separately on a deliberately selected result export.

The current 14 train / 2 test / 0 validation dataset has **no validation evidence**.
A validation run fails without making a request. Do not repeatedly inspect test
results while selecting prompts. No promotion decision or operational action is
authorized by this harness.

## Evidence and network boundaries

Each selected `transcription_attempt_id` must join directly to its canonical
transmission. A parameterized SELECT requires a completed attempt with finish
timestamp, completed analysis and transcription, non-alias transmission, probe
and fingerprint evidence, and consistent current/attempt model. The returned
attempt ID, fingerprint, channel, TGID, duration, model, and exact baseline raw
text must match the dataset. Missing or mismatched evidence stops the run before
requests. All SELECTs occur in one PostgreSQL repeatable-read, read-only
transaction with read-only startup defaults. It is rolled back and closed
before HTTP begins. No INSERT/UPDATE/DELETE or SQL functions that mutate data are
used; BEGIN/ROLLBACK are transaction-control operations only.

The only live inference URL is hardcoded to
`http://127.0.0.1:8001/v1/audio/transcriptions`. There is no endpoint override,
bearer token, proxy use, redirect following, or automatic retry. Requests are
sequential, once per selected record on success; the first failure aborts the
remaining records without publishing a partial dataset. HTTP status zero denotes
a request attempt without an HTTP response. There is no health request.

Fields are fixed to model `small.en`, language `en`, response format `json`,
temperature `0`, beam size `5`, best-of `5`, and empty word boost, with only the
optional prompt differing. Source filenames are replaced with `recording.mp3`
or `recording.wav` in multipart uploads. Responses must have one valid string
`text`; raw whitespace is retained. No transcript or database error is logged.

## File, time, and publication bounds

- Dataset: Step 4A's unchanged strict UTF-8 JSONL schema and resource limits,
  including duplicate IDs/fingerprints, unknown keys/splits, and line/token bounds.
- Prompt: at most 4,096 UTF-8 bytes, nonblank, no BOM or unsafe control/format characters.
- Each recording: regular `.mp3`/`.wav`, nonempty, at most 32 MiB; only canonical
  paths beneath the explicitly supplied recordings directory are eligible.
- Response: at most 128 KiB; transcript at most 65,536 decoded bytes and must
  pass Step 4A token/resource validation before output publication.
- Output: at most 64 MiB, strict evaluator JSONL with no extra fields.
- Sidecar summary: at most 1 MiB at `<output>.experiment-summary.json`.
- Request deadline: default 60 seconds, range 1 second–2 minutes. Command deadline:
  default 30 minutes, at most 1 hour and not shorter than the request deadline.
  Database connect timeout is 3 seconds. Cancellation is checked between file
  operations; synchronous local filesystem calls and mandatory final integrity
  checks can extend cleanup beyond the command deadline.

All paths must be clean absolute local paths. Symlinks/reparse points and linked
ancestors, unsafe alternate-stream/UNC paths, duplicate source paths or file
identities, directories as files, and input/output aliases are rejected. Outputs
must be outside the recordings directory. Existing outputs or sidecars require
`--overwrite`; prefer a new experiment name/path instead.

Every selected source has path/file identity, regular-file status, size, mtime,
and full source-byte SHA-256 captured before requests and checked again after the
run, including failures. A source is also rechecked before upload. Source files
are opened only for reading. Dataset and prompt are similarly checked. No audio
decoding or FFmpeg occurs: the stored PCM fingerprint is verified against database
evidence, while the source-byte SHA-256 verifies stability during this run. It
cannot prove the source still matches its historical decoded PCM fingerprint.

Outputs use exclusive same-directory staging and atomic publication per file.
Staging files are removed on normal error/return paths. The sidecar is published
first with `status: prepared` and `published: false`; the final stdout summary
reports completion only after the JSONL is published. The pair is **not atomic**:
a crash or publication failure can leave only a sidecar, or a new sidecar beside
an older output when overwriting. Check the sidecar's output SHA-256 against the
actual JSONL and require a successful final CLI status before interpreting it.
Final post-publication integrity failure makes the CLI fail; discard that run's
artifacts. A force-kill may leave a `.gfr-experiment-*.tmp` file.

Summary metadata includes experiment name, prompt-present flag and SHA-256 (the
empty-byte hash for no prompt), input/output SHA-256, model, split, request count,
per-record source SHA-256, HTTP outcomes, and request/total timing. Timing is
client elapsed time, including upload and response reading, not server-only
inference. Median averages the two middle values for even counts; p95 uses
nearest rank `ceil(0.95*n)`. Records run in sorted dataset-ID order. The sidecar
captures timings immediately before publication; stdout includes final cleanup.

## Cleanup and limitations

Keep all exports, summaries, and captured reports in private user-local storage.
Existing JSONL ignore rules plus the experiment-summary rule are a backstop,
not access control and not protection against `git add -f`. Source control holds
only code, synthetic tests, documentation, and the versioned candidate prompt.

After reviewing results, remove only the exact experiment artifacts you created:

```powershell
# Use the explicit paths established above; never target $recordings or $dataset.
Remove-Item -LiteralPath $baseline, "$baseline.experiment-summary.json", $candidate, "$candidate.experiment-summary.json"
```

Do not delete legitimate database rows or source recordings. There are no
experimental database rows to clean up. Do not stop managed Whisper or change
production settings as cleanup. The CLI performs no service or task management.

Protect local directories against concurrent hostile writes: identity/hash
checks are not a filesystem snapshot, and cannot prevent an attacker who can
rewrite files and restore their metadata. No guarantee is made about OS access
time bookkeeping. HTTP model fields cannot attest the loaded model binary,
server version, decoding implementation, CPU load, or process identity. Timing
and transcripts may vary across runs. Capture the separately verified runtime
version/hash in your private experiment notes if needed. No classification,
incident, WebSocket, unit-state, CAD, or model-promotion logic is implemented.

## Implementation verification

From `backend/`: `go vet ./...` and `go test ./...`. Tests use synthetic files and
fake RoundTrippers/SELECT boundaries; no actual database or Whisper connection is
needed. A real Windows symlink test may skip without privileges; reparse attribute
checks run independently. Database transaction settings and eligibility SQL are
tested at the boundary; real PostgreSQL enforcement and live inference remain
unverified until a separately authorized experiment.
