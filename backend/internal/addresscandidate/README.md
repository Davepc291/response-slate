# Step 5A3: address candidates from one transcript

`New()` builds an immutable matcher from the existing `addressdata` dictionaries.
`Extract(transcript string)` accepts exactly one transcript and returns supported
mentions in transcript order. Calls retain no transcript state and are safe for
concurrent use. No external I/O or production integration is involved.

Each candidate contains the digit-only house number as a string (no numeric
conversion), full canonical dictionary entry, dictionary kind (`street` or
`access road`), original evidence text, and zero-based UTF-8 byte offsets
`[Start, End)`. `transcript[Start:End]` equals `Evidence`. Case and punctuation
in the original string are never changed. The evidence ends at the final matched
word; surrounding quotes, parentheses, and sentence punctuation are excluded.

Matching lowercases word tokens, requires whole tokens and complete dictionary
names, and takes the longest matching name at each supported house number. A
normalization collision is rejected rather than resolved arbitrarily. Multiple
mentions, including repeated identical addresses, remain separate; none is marked
primary. For example, `12 Edgewood Avenue Connector` matches the longer entry,
not `edgewood avenue`.

Canonical streets require an immediately preceding ASCII digit-only token.
Spaces, tabs, commas, quotes, and parentheses can separate the number and name
or street words. ASCII hyphens may separate street-name words (`Doubling-Road`),
but not the house number from the street. Sentence punctuation, line breaks,
symbols, and intervening words are barriers. Invalid UTF-8 and unsafe control or
format characters reject the whole transcript. Alphanumeric numbers, spelled-out
numbers, ranges, fractions, decimals, signed numbers, and grouped/consecutive
numeric tokens are unsupported; they are not converted into an address.

The two special access-road entries match only as complete phrases:
`55 north st driveway` and `55 north turning loop extension`. Their embedded
`55` is the supported house-number token; `CanonicalStreet` retains the entire
dictionary entry including `55`. Another number cannot be substituted. Ordinary
`55 north street` remains a canonical street candidate, not an access road.

Candidate extraction is **not primary-address selection** or a statement of
dispatch truth. The matcher cannot determine whether a number refers to a house,
a unit, or some other entity from wider context. It requires the documented
local syntax and makes no decision about competing mentions. It does not perform
fuzzy/phonetic matching, suffix inference, correction, geocoding, cross-street
classification, confidence scoring, or combination of separate transmissions.
There are no database, API, Whisper, mapping, unit-state, or CAD changes.

Run from `backend/`: `go test ./internal/addresscandidate` or `go test ./...`.
