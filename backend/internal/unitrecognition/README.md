# Step 5D2: offline unit recognition

`unitrecognition` follows the repository's focused internal-package naming
convention. It implements the [approved v1 model](../../../docs/unit-vocabulary-v1.md)
without changing the existing pipelines.

## API and catalog

- `New() (*Matcher, error)` validates and constructs an immutable matcher.
- `Matcher.Catalog() []Unit` returns an independent catalog copy.
- `Matcher.Recognize(transcript string) Result` processes exactly one unchanged
  transcript. Use New; the zero value is not a validated matcher.

The catalog has exactly 26 units and 26 phrases: eight career units in approved
DC/E2/E3/E4/E5/SQ1/SQ8/T1 order, followed by 18 volunteer units in catalog order.
The volunteer list is incomplete. Volunteer stations are explicitly `unknown`.
Display labels equal canonical IDs; the V suffix is metadata, not spoken.
Construction checks counts and duplicates, including normalized collisions, then
checks every field and ordering against the fixed approved v1 catalog. Excluded
identities, wrong classes/kinds/stations, or changed phrase assignments cannot
enter through construction.

Unit metadata contains `CanonicalID`, `DisplayLabel`, `RosterKind`,
`ApparatusClass`, `Station`, and `Phrase`. No class or identity is inferred from
a prefix. In particular, car 4 maps only to DC; engine 62 maps only to E62V.

## Result contract

Result contains `Transcript`, `State`, `Reason`, and ordered `Mentions`.
A mention embeds Unit plus original `Evidence`, zero-based UTF-8 byte offsets
`Start` and exclusive `End`, `Accepted`, `Reason`, and conflict `Alternatives`.
The original substring `transcript[Start:End]` exactly equals Evidence.
Repeated and different-unit mentions remain separate.

- `resolved`: at least one accepted mention and no supported identity conflict.
  Different units are independent valid mentions, not a conflict.
- `ambiguous`: a supported evidence span maps to conflicting identities. Its Unit
  is empty, Alternatives retains metadata, and no identity is arbitrarily selected.
  Validated v1 construction prevents such collisions; an internal synthetic test
  seam exercises the defensive result behavior without adding catalog aliases.
- `unresolved`: no accepted evidence and no identity conflict. Unknown words do
  not invent a candidate; rejected supported phrases remain auditable.

Result reasons are empty on resolution, `conflicting_identities`,
`no_supported_evidence`, `all_supported_evidence_rejected`, or
`invalid_utf8_or_unsafe_controls`. Mention reasons are empty when accepted,
`conflicting_identities`, `quoted_or_apostrophe_text`, or `unsupported_context`.
Context rejection takes precedence over a synthetic conflict.

## Matching and conservative context limits

Complete tokens are compared using deterministic lowercase text. Only spaces,
tabs, commas, periods, colons, semicolons, and ASCII hyphens may separate tokens
within a phrase. Line breaks and parentheses cannot connect phrase fragments.
Symbols, extra words, larger numeric tokens, combining marks, and unsupported
punctuation cannot be erased to manufacture a match. Longest match is selected
at each starting token. Engine 2/21, engine 5/51, tanker 7/17, and rescue 5/51 stay
distinct.

Context accepts a line containing only complete supported mentions separated by
spaces, tabs, commas, periods, colons, or semicolons. Balanced parentheses may
surround one complete mention; nested or list-wide parentheses are unsupported.
Hyphens are permitted inside phrases, not between separate mentions.

Any other text on that line rejects its supported mentions with
`unsupported_context`. This closed grammar rejects negated, historical,
hypothetical, and operational discussion without claiming to understand intent.
It also intentionally rejects broader dispatch or self-identification wording
such as a response cue or a status after a unit name. No special role or status
is inferred. Context is local to a line; historical wording on one line does
not qualify or reject a different line.

Any straight/curly quotation mark or apostrophe anywhere in the transcript
conservatively rejects all supported mentions, including contractions and
unmatched quotes. Quote characters may delimit evidence for audit purposes but
never permit an accepted quoted mention.

Invalid UTF-8, Unicode format characters, line/paragraph separators, and controls
other than tab/CR/LF reject the entire input with no mentions. The original input
is still retained in memory. No serialization or byte conversion is performed.

## Meaning, exclusions, and ownership

An accepted phrase establishes only a unit mention in text, not dispatch,
assignment, response, speaker identity, status, station inference, or incident
association. Temporary volunteer display and role/status interpretation remain
later work. This package does not update a board.

MINI11, E12, E62, E71, E11, TANKER2, CAR5, CAR1, and CAR4 are excluded as separate
recognition identities. Existing database records remain unchanged. Chief, GEMS,
medic, engine 20, generic engine/truck/squad/tanker/ladder/rescue, and canonical
codes are not spoken inputs. No abbreviation expansion, number-word conversion,
fuzzy/phonetic matching, transcription correction, or prefix inference is added.

There is no RID/TGID/channel input, mapping, or lookup. Channel conflicts remain
deferred. No address, call-type, alarm-level, timing, prior-transmission, or
expected-assignment data is accepted. No database, network, filesystem, logging,
configuration, environment, unit-state, incident, CAD, or production integration
is added.

The matcher holds no transcript state and supports concurrent reuse. Returned
catalogs, mentions, and ambiguity alternatives belong to their caller; modifying
them cannot alter another call or internal state. Callers sharing one returned
result must synchronize their mutations.

Run from backend:

```sh
go test ./internal/unitrecognition -v
go vet ./...
go test ./...
```

Synthetic tests cover all metadata and phrases, exclusions, punctuation and
context boundaries, invalid catalogs/input, ambiguity and longest-match seams,
byte offsets, ownership, isolation, and 1,000 concurrent calls on one matcher.
