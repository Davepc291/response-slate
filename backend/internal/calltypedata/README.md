# Offline call-type vocabulary data v1

This package contains only the approved data in
[Step 5B1](../../../../docs/call-type-vocabulary-v1.md):
12 call types, 4 alarm levels, 2 alarm qualifiers, and 42 exact phrases
(36 call-type, 4 level, 2 qualifier phrases). Original case and punctuation are
preserved, including CO, MVA, and the approved comma in the accident phrase.
Qualifier canonical values use the exact lowercase qualifier phrases from the
catalog; no new uppercase labels are introduced.

Use `Load()` to construct and validate a catalog. `Values(kind)` and `Phrases()`
return independent copies in documented catalog order. `Lookup(exact)` is
case-sensitive and accepts only the entire exact phrase; it does not normalize
or search a transcript. Returned records are values. The catalog can be shared
for concurrent reads; its zero value is empty, not a validated v1 catalog.

Each phrase records its exact text, concept kind, canonical value, and
`RequiresDispatchContext`. All 36 call-type records require explicit
dispatch/response context. Alarm-level and qualifier records have that flag
false because Step 5B1 imposes this requirement on call types. This is metadata,
not permission to recognize a quoted/negated level or to treat a qualifier as a
standalone type. Qualifiers remain associated with alarm calls.

Construction rejects empty/unsafe text, invalid kinds, values outside the correct
set, duplicate exact or normalized phrases (including conflicting mappings),
wrong context flags, missing value coverage, and incorrect total/per-concept
counts. Private duplicate-validation keys lowercase Unicode characters, replace
Unicode punctuation/spacing with spaces, and collapse spaces. This conservative
collision check never changes stored text and is never used by lookup; it does
not define a future transcript normalization algorithm.

There is no generic "alarm" fallback, bare "wires", bare "CO alarm", bare
"investigate", bare "investigation", I-95, or Merritt Parkway entry.
There is no transcript scanning, recognition, fuzzy/phonetic matching, inference,
address correction, or cross-call state. Context, negation, ambiguity, and other
recognition rules remain future work. No database, API, Whisper, unit/status,
incident/CAD, or production integration is added.

From `backend/`:

```sh
go test ./internal/calltypedata
go vet ./...
go test ./...
```
