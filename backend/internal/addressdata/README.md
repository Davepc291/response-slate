# Greenwich address dictionary v1 — Step 5A2

`Load()` embeds and validates two independent lowercase dictionaries:
1,211 canonical streets and 2 special North Street access roads. No external
file, database, network, or legacy-project access occurs at runtime.

`HasStreet` and `HasAccessRoad` are case-sensitive exact lookups. For example,
`doubling road` matches; `Doubling Road`, `doubling rd`, and padded input do not.
There is no normalization, fuzzy correction, address extraction, or production
wiring. `Streets()` and `AccessRoads()` return sorted copies; callers cannot
mutate the dictionaries. Concurrent reads are safe after successful loading.

Validation rejects invalid UTF-8, BOMs (including embedded BOMs), empty entries,
leading/trailing whitespace, uppercase/titlecase letters, control/format
characters, Unicode line/paragraph separators, duplicates within either set or
across sets, and incorrect counts. LF and CRLF are accepted, with one optional
final line ending. Failure returns no partial dictionary.

## Provenance

The supplied legacy source was `C:\gfd-cad\greenwich_streets.txt`: 1,213
JSON-fragment entries. Two special North Street access-road entries were
separated; no entries were discarded. This provenance is supplied with the
inputs; the legacy project was not accessed or modified for this loader.

| File | Entries | SHA-256 of exact file bytes |
| --- | ---: | --- |
| Legacy `greenwich_streets.txt` | 1,213 | `DAEEED4EAA02281FA87FE093793D5CEFF9E79926456735B008A60003291D7989` |
| `greenwich-streets-v1.txt` | 1,211 | `909DBB45B763F29826F520126D89263461E8C74D8198BCA8CC0788258FCADB68` |
| `greenwich-access-roads-v1.txt` | 2 | `68DA81578887B4C37785401928477418B768C4DE246AD982868E567CACB83CCB` |

Access-road entries are `55 north st driveway` and
`55 north turning loop extension`; neither is in the canonical street set.
Tests pin both embedded hashes, counts, separation, validation, and confirmed
street lookups. Preserve the supplied file bytes, including line endings.
These are legacy dictionary entries, not independently verified current GIS
or dispatch authority. No map, API, Whisper, classification, or CAD changes
are included.

From `backend/`: `go test ./internal/addressdata` (or `go test ./...`).
