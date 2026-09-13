# Step 5B3: call-type evidence candidates

`New()` builds an immutable extractor exclusively from
[calltypedata](../calltypedata/README.md). No second runtime vocabulary exists.
`Extract(transcript string)` takes exactly one transcript, retains no transcript
state, and supports concurrent calls.

Each candidate embeds the unchanged catalog `Phrase`: `Exact`, `Kind`,
`CanonicalValue`, and `RequiresDispatchContext`. It adds `Evidence` and
zero-based UTF-8 byte offsets `[Start, End)`. The original string is never
modified and `transcript[Start:End] == Evidence`. Surrounding punctuation is
outside the evidence; punctuation between matched words remains inside it.
Returned slices and records do not expose the matcher.

Matching lowercases complete tokens without accent folding or correction.
Ordinary separators are spaces, tabs, CR/LF, commas, periods, semicolons, colons,
question/exclamation marks, straight/curly quotation marks, parentheses, and
ASCII hyphens. Repeated separators collapse for comparison only. Other
characters, including underscores, symbols, combining marks, nonbreaking spaces,
and non-ASCII dashes, remain part of tokens and are not erased. A match may span
ordinary punctuation or line breaks; this stage does not interpret clauses.

At each starting token, only the longest matching phrase is emitted. Matches
starting at different tokens remain separate, even when their spans overlap:
`natural gas leak` yields that phrase at byte 0 and `gas leak` at byte 8.
This preserves evidence rather than silently deduplicating distinct starts.
Repeated mentions, different call types, alarm levels, and qualifiers remain
separate in starting-byte order. A normalized catalog collision fails construction.

Invalid UTF-8, control characters other than tab/CR/LF, Unicode format characters,
and Unicode line/paragraph separators reject the entire input with no evidence.
No partial results are returned for invalid text.

All 42 catalog phrases are exercised by tests, covering 12 call types, 4 alarm
levels, and 2 qualifiers. Generic alarm, bare wires/CO alarm/investigate/investigation,
I-95, Merritt Parkway, and unknown/partial words do not match.

**Evidence is not dispatch truth.** Context flags are copied, not evaluated.
Quoted, negated, and operational mentions can be returned for later handling.
There is no primary-type selection, conflict resolution, ambiguous/unresolved
classification, inference, fuzzy/phonetic matching, or cross-transmission state.
There is no database, API, Whisper, unit/status, incident/CAD, or production wiring.

Run from `backend/`:

```sh
go test ./internal/calltypecandidate
go vet ./...
go test ./...
```
