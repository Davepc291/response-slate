# Step 5A4: conservative roles from one transcript

`New()` loads `addressdata` and constructs an `addresscandidate` extractor.
`Interpret(string)` accepts one transcript; no data is retained between calls.
The interpreter is immutable and safe for concurrent calls. Neither dependency
nor either dictionary is changed. There is no external I/O or production wiring.

## Primary rules

Numeric evidence comes exclusively from the existing candidate extractor. A
primary requires an imperative `respond` cue at a clause start, followed by:

- the numeric candidate directly;
- `to` and the candidate;
- `to a residential alarm at` (or `to residential alarm at`) and the candidate.

`Engine <ASCII digits> respond ...` is also supported at clause start without a
comma; `Engine 4, respond ...` starts a new clause at the comma. This intentionally
small grammar does not interpret arbitrary call-type descriptions, reported
speech, or operational prepositions as dispatch commands. No intervening words
or clause boundaries may bridge the cue and address. Commas, periods, semicolons,
colons, exclamation/question marks, and line breaks delimit clauses. Case,
ordinary surrounding quotes/parentheses, and street-name hyphens are normalized
for matching, not written back to the original transcript.

Exact repeated identities (house-number string, canonical entry, dictionary kind)
are grouped, preserving every candidate mention and its cue. Distinct supported
identities produce `ambiguous`, all `Alternatives`, and a nil `Primary`. The
interpreter never chooses between alternatives. Operational numeric mentions
without a supported cue remain in `Unresolved`; they do not compete with a
supported primary or become primary by proximity. No number is carried forward
to another call. Access-road candidates retain their existing full entry and kind.

## Cross-street rules

Only these explicit patterns are supported:

- `cross street is <complete canonical street>`
- `cross streets are <street> and <street>`
- `runs between <street> and <street>`

A pattern starts a clause or immediately follows a complete numeric candidate
(allowing transcripts without punctuation between the address and cross cue).
Both names must match for a two-street pattern; incomplete/unknown pairs are
discarded. Names are matched by full tokens with longest-match behavior using
only canonical streets, not special access roads. No numbers, suffix expansions,
unknown names, or normalization-collision guesses are supplied.

Cross streets remain separate from primary addresses. Only identical canonical
street identities are grouped; first-mention order and every original occurrence
and cue are retained. Distinct similar street names are never fuzzy-deduplicated.

## Result and evidence

- `no evidence`: no supported primary, numeric candidate, recognized clause-start
  dispatch cue, or explicit cross pattern.
- `resolved`: exactly one distinct supported primary identity; cross streets and
  unresolved operational mentions may also be present.
- `ambiguous`: more than one supported primary identity, with an explicit reason
  and all alternatives; no primary selected.
- `unsupported`: invalid UTF-8/unsafe controls, or evidence/cues without a
  supported primary. A cross-only result therefore preserves cross streets while
  reporting `unsupported` and a nil primary. This does not invalidate the cross
  evidence; the state describes primary resolution.

All offsets are zero-based **original UTF-8 byte** offsets `[Start, End)`.
Evidence text equals the original substring at those offsets. Candidate evidence
and dispatch cue evidence are separate; cross-name and cross-cue evidence are
also separate. Surrounding punctuation is not part of a matched word span.
The input is never changed. Invalid UTF-8 or unsafe control/format characters
reject the entire transcript consistently with `addresscandidate`; tab, CR and
LF are allowed, with line breaks acting as clause boundaries.

These are syntactic roles, not verified dispatch truth. The small grammar can
miss legitimate phrasings and cannot authenticate a speaker, detect every quoted
or negated command, or establish that an address is an actual incident location.
There is no fuzzy/phonetic matching, correction, suffix inference, confidence,
geocoding, mapping, cross-call combination, database access, API, Whisper,
WebSocket, incident creation, or CAD-board update.

From `backend/`: `go test ./internal/addressrole` or `go test ./...`.
