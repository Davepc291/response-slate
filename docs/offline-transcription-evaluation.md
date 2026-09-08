# Step 4A: offline transcription accuracy evaluation

Run from `backend/`, using an explicit absolute path to a private Step 3A
`transcript-review export` JSONL file:

```powershell
go run ./cmd/transcript-eval --dataset "$env:LOCALAPPDATA\GreenwichFireResponder\review-data\dataset.jsonl"
```

The standalone CLI reads only that dataset and writes aggregate JSON to stdout.
It does not load `.env`, read recordings, use a database, contact Whisper or any
network service, or change the export. No default dataset is selected. Exit codes
are 0 for a report, 1 for invalid/unsafe input or output failure, and 2 for usage.
Errors omit file contents and paths. No partial metrics are printed on invalid
input. The report includes the SHA-256 of the exact input bytes and explicit
metric and vocabulary versions. JSON property and vocabulary ordering is stable.

## Metrics: greenwich-evaluation-v1

Compare `raw_model_transcript` against `human_reference_transcript`. Both are
lowercased using Go's Unicode lowercase mapping, then split into maximal runs of
Unicode letters or numbers. Every other character separates tokens. Whitespace,
capitalization, and punctuation therefore do not affect normalized exact match.
For example, `Engine 2, on-scene!` matches `engine 2 on scene`; `10-4` produces
two tokens, `10` and `4`. There is no stemming, spelling correction, alias or
number expansion, Unicode canonical normalization, or domain-specific repair.

Overall and independently for train, validation, and test:

- **Records:** number of valid records in the group.
- **Exact matches:** records whose normalized token sequences are identical.
  Exact-match rate is exact matches divided by records, not byte-for-byte equality.
- **Substitutions, deletions, insertions:** unit-cost word Levenshtein operations
  turning the reference into the model hypothesis. Minimum-cost ties prefer the
  diagonal (match/substitution), then deletion, then insertion. Counts can differ
  from tools with another tie policy even when total edit distance agrees.
- **Reference words:** sum of normalized reference token counts.
- **WER:** `(substitutions + deletions + insertions) / reference_words`, a pooled
  word-weighted rate, not the average of per-record WERs. It can exceed 1 (100%).
- Empty groups have zero counts and JSON `null` rates, never an invented zero WER.

The implementation uses rolling rows for bounded Levenshtein memory and caps
total dynamic-programming cells. Input order cannot affect metrics; changing
the byte order of records changes the dataset SHA-256.

## Critical terms: greenwich-critical-terms-v1

The embedded, versioned source file
`backend/internal/transcripteval/vocabulary-v1.txt` contains only the confirmed
terms supplied for this milestone: Engine 2, Engine 5, GEMS, Chief, medic,
dispatch, 10-4, good copy, on scene, clear, canceled, transported one,
last unit on scene, and disregard that last. It contains no private transcripts.
Vocabulary or metric changes require a new version for meaningful comparisons.

Phrases use the same normalization and match contiguous whole-token sequences:
`clear` does not match `unclear` or `cleared`; `Engine 2` does not match `Engine 20`.
For each phrase **within each record**, count reference occurrences R and model
occurrences H. Correct recognitions are `min(R,H)`; misses are `R-min(R,H)`.
Sum these counts across records, then compute recall as correct/reference
occurrences. Zero reference occurrences produce `null` recall. An occurrence in
another record cannot compensate for a miss. Additional model occurrences do
not earn credit. Each starting token is considered, including overlapping
occurrences. Nested vocabulary entries count independently (e.g. `on scene`
inside `last unit on scene`). `critical_totals` sums these possibly overlapping
phrase counts and reports their pooled recall; it is not a count of unique words.

This is occurrence-count recall, not timestamp- or alignment-based recognition.
It does not establish that a phrase was in the correct sentence position or
context, and does not measure critical-term precision or hallucination frequency.

## Input contract and bounds

Input must be UTF-8 without a BOM, with one JSON object per line. LF and CRLF,
and a final line without a newline, are supported. Empty datasets and blank
lines are rejected. Required fields match the Step 3A export exactly:

`dataset_id`, `audio_fingerprint`, `transcription_attempt_id`, `channel`, `tgid`,
`duration_ms`, `raw_model_transcript`, `human_reference_transcript`, `model`,
`split`, and `reviewed_at`.

Reject missing, null, unknown, duplicate, or incorrectly cased keys; malformed
JSON/UTF-8, unpaired surrogate escapes, trailing JSON, invalid types, unknown
splits, duplicate dataset IDs or audio fingerprints, and references with no
normalized words. Empty hypotheses are allowed and score as deletions. IDs must
match the export's `gfr-audio-v1:` plus lowercase MD5 of the fingerprint (MD5 is
an identifier convention, not an integrity check). Fingerprints require the
canonical PCM prefix plus 64 lowercase SHA-256 hex digits. Require positive
attempt IDs, nonnegative durations, matching Greenwich TGID/channel pairs,
nonzero RFC3339 review timestamps, and bounded model identifiers. Transcript
control/format characters are rejected except tab, CR, and LF.

Limits are deliberately stricter than the maximum potential review export:

| Bound | Limit |
| --- | ---: |
| Dataset bytes | 64 MiB |
| JSON line bytes, excluding line ending | 1 MiB |
| Records | 1,000 |
| Each decoded transcript | 65,536 UTF-8 bytes |
| Tokens in each transcript | 2,048 |
| Sum of reference-token × hypothesis-token counts | 20,000,000 |

Over-limit input fails; nothing is silently truncated or sampled. Re-export a
smaller bounded dataset if needed, retaining its provenance and split policy.

Paths must be clean, absolute, local paths; UNC paths, alternate data streams,
relative traversal, nonregular files, symlinks, Windows reparse points, and linked
ancestors are rejected. Reads use an anchored directory handle and verify file
and ancestor identities, size, and modification time before/after evaluation.
Keep the dataset in an access-controlled directory and do not concurrently edit
it: these checks are not a filesystem snapshot or protection against an attacker
who can rewrite data and restore metadata. No read-only claim covers OS access
time bookkeeping. The evaluator itself never opens the dataset for writing.

## Interpretation and privacy

The current dataset has **14 train, 2 test, and 0 validation records**. Validation
is **unavailable**. The report makes no promotion-readiness decision. Training
metrics are descriptive, the test sample is tiny, and the vocabulary was informed
by this reviewed dataset; none supports an independent generalization claim.
This tool trusts the supplied references and split labels and cannot authenticate
human review or detect leakage beyond duplicate identities within this file.
It does not listen to audio or infer operational truth. There is no model tuning,
classification, incident, unit-status, WebSocket, or CAD action in Step 4A.

Do not commit datasets, references, recordings, or private reports. Keep exports
outside the repository. Existing `.gitignore` rules cover `*.jsonl`,
`review-data/`, and `dataset-exports/`; they cannot prevent deliberate forced
additions or disclosure through another filename. Unit tests contain synthetic
data only and use automatically cleaned temporary directories. Evaluation never
copies dataset content into tests, source control, or PostgreSQL.

## Verification

```powershell
# From backend/; these tests require no services or real dataset.
go vet ./...
go test ./...
```

Tests cover edit operations and ties, normalization, per-split aggregation,
undefined rates, critical repetition/boundaries/nesting, deterministic output,
schema and Unicode rejection, duplicate identities, resource bounds, source
integrity, safe paths, and CLI errors. Actual symlink tests may skip when Windows
denies unprivileged creation; Windows reparse attributes are tested independently.
