package alerts

import (
	"math"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/audioanalysis"
)

func validFingerprint() string {
	return audioanalysis.FingerprintPrefix + strings.Repeat("a", 64)
}

// testLimits is an explicit, test-chosen ceiling. It is not a value this
// package assumes on its own: every caller (including this test) must pick
// one, per Section 14 items 3-4 leaving the actual policy unresolved.
func testLimits() Limits { return Limits{MaxExpiryWindow: 10 * time.Minute} }

func builder(t *testing.T) *Builder {
	t.Helper()
	b, err := NewBuilder(testLimits())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return b
}

func baseInput() Input {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return Input{
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
		SourceKind:   SourceSynthetic,
		Channel:      ChannelCH1A,
		TGID:         57201,
		DetectorKind: DetectorTone,
		ToneSetID:    "quick-call-1",
		Evidence:     Evidence{AudioFingerprint: validFingerprint()},
		State:        StateMatched,
		Label:        "quick-call alert",
	}
}

func TestNewBuilderRequiresExplicitPositiveLimit(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		if _, err := NewBuilder(Limits{MaxExpiryWindow: d}); err == nil {
			t.Fatalf("expected NewBuilder to reject max_expiry_window=%v: this package must not silently assume a policy value", d)
		}
	}
}

func TestNewValidTone(t *testing.T) {
	b := builder(t)
	in := baseInput()
	conf := 0.9
	in.Confidence = &conf
	ev, err := b.New(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q", ev.SchemaVersion)
	}
	if !ev.Synthetic || !ev.ShadowOnly {
		t.Fatalf("synthetic/shadow_only must always be true: %+v", ev)
	}
	if ev.EventID == "" || !strings.HasPrefix(ev.EventID, "evt_") {
		t.Fatalf("event id: %q", ev.EventID)
	}
	if ev.DedupKey == "" {
		t.Fatalf("dedup key must be set")
	}
	want := "CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD."
	if ev.DisplaySummary != want {
		t.Fatalf("display summary = %q, want %q", ev.DisplaySummary, want)
	}
	if !strings.Contains(ev.DisplaySummary, "NOT LIVE CAD") {
		t.Fatalf("display summary missing NOT LIVE CAD: %q", ev.DisplaySummary)
	}
}

func TestNewValidKeyword(t *testing.T) {
	b := builder(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	in := Input{
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Minute),
		SourceKind:    SourceShadowReplay,
		Channel:       ChannelCH2B,
		TGID:          57202,
		DetectorKind:  DetectorKeyword,
		KeywordListID: "structure-fire",
		Evidence:      Evidence{SourceIdentity: strings.Repeat("b", 64)},
		State:         StateAmbiguous,
		Label:         "structure fire",
	}
	ev, err := b.New(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Confidence != nil {
		t.Fatalf("confidence should be nil when detector defines none")
	}
	want := "CH2B keyword ambiguous match: structure fire (synthetic). NOT LIVE CAD."
	if ev.DisplaySummary != want {
		t.Fatalf("display summary = %q, want %q", ev.DisplaySummary, want)
	}
}

// TestDeterministicEventID is the core fix for the reviewed event_id
// contradiction: the contract (Section 5) only requires event_id be
// "server-generated" and "never derived from private evidence" - it does not
// require randomness. The Step 8B task's own test requirements explicitly
// ask for "deterministic event IDs/deduplication", so identical Input must
// produce an identical EventID on every call, not a fresh random one.
//
// Property 1: identical safe event fields produce the same event_id.
func TestDeterministicEventID(t *testing.T) {
	b := builder(t)
	in := baseInput()

	a, err := b.New(in)
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.New(in)
	if err != nil {
		t.Fatal(err)
	}
	if a.EventID != c.EventID {
		t.Fatalf("identical input must produce identical event_id: %q vs %q", a.EventID, c.EventID)
	}
}

// TestEventIDIndependentOfEvidence is the fix for the reviewed private-
// evidence leak: eventID(in) never receives AudioFingerprint, SourceIdentity,
// evidenceKey, or DedupKey at all (see the eventID doc comment) - not merely
// "doesn't happen to use them" but structurally cannot, since eventID takes
// only the Input's non-evidence fields. This proves that behaviorally:
// changing only the evidence identity, in either representation, must never
// change EventID, while it must still change DedupKey (Section 6 requires
// dedup_key be evidence-derived).
//
// Property 2: changing only AudioFingerprint or SourceIdentity does not
// change event_id. Property 5: no private evidence value influences
// event_id (demonstrated behaviorally here; enforced structurally by
// eventID's signature, which accepts no evidence-derived parameter).
func TestEventIDIndependentOfEvidence(t *testing.T) {
	b := builder(t)
	base := baseInput() // Evidence: AudioFingerprint = validFingerprint()
	baseEv, err := b.New(base)
	if err != nil {
		t.Fatal(err)
	}

	// Different AudioFingerprint value.
	diffFingerprint := baseInput()
	diffFingerprint.Evidence = Evidence{AudioFingerprint: audioanalysis.FingerprintPrefix + strings.Repeat("f", 64)}
	fpEv, err := b.New(diffFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if fpEv.EventID != baseEv.EventID {
		t.Fatalf("changing audio_fingerprint must not change event_id: %q vs %q", fpEv.EventID, baseEv.EventID)
	}
	if fpEv.DedupKey == baseEv.DedupKey {
		t.Fatalf("changing audio_fingerprint must still change dedup_key (Section 6 is evidence-derived)")
	}

	// SourceIdentity instead of AudioFingerprint entirely (a different
	// evidence *shape*, not just a different value of the same field).
	diffShape := baseInput()
	diffShape.Evidence = Evidence{SourceIdentity: strings.Repeat("9", 64)}
	shapeEv, err := b.New(diffShape)
	if err != nil {
		t.Fatal(err)
	}
	if shapeEv.EventID != baseEv.EventID {
		t.Fatalf("switching to source_identity evidence must not change event_id: %q vs %q", shapeEv.EventID, baseEv.EventID)
	}
	if shapeEv.DedupKey == baseEv.DedupKey {
		t.Fatalf("switching to source_identity evidence must still change dedup_key")
	}
}

// TestEventIDChangesWithPermittedFields covers property 3: changing a
// permitted, non-evidence event field changes event_id. Each case starts
// from a fresh, otherwise-valid baseInput() and changes exactly one
// contract-approved field.
func TestEventIDChangesWithPermittedFields(t *testing.T) {
	b := builder(t)
	base := baseInput()
	baseEv, err := b.New(base)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]Input{
		"created_at": func() Input {
			in := baseInput()
			in.CreatedAt = in.CreatedAt.Add(time.Minute)
			in.ExpiresAt = in.ExpiresAt.Add(time.Minute)
			return in
		}(),
		"state": func() Input {
			in := baseInput()
			in.State = StatePartial
			return in
		}(),
		"channel": func() Input {
			in := baseInput()
			in.Channel = ChannelCH2B
			in.TGID = 57202
			return in
		}(),
		"detector_kind": func() Input {
			in := baseInput()
			in.DetectorKind = DetectorKeyword
			in.ToneSetID = ""
			in.KeywordListID = "structure-fire"
			return in
		}(),
		"configuration_id": func() Input {
			in := baseInput()
			in.ToneSetID = "other-tone-set"
			return in
		}(),
	}

	for name, in := range cases {
		ev, err := b.New(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ev.EventID == baseEv.EventID {
			t.Fatalf("%s: expected changing this permitted field to change event_id, got the same %q", name, ev.EventID)
		}
	}

	// dedup_key is scoped to (evidence, detector_kind, set id) only, per
	// Section 6: a state-only change must leave it untouched even though it
	// changes event_id.
	stateOnly, err := b.New(cases["state"])
	if err != nil {
		t.Fatal(err)
	}
	if stateOnly.DedupKey != baseEv.DedupKey {
		t.Fatalf("dedup_key must depend only on (evidence, detector_kind, set id), per Section 6: got %q vs %q", stateOnly.DedupKey, baseEv.DedupKey)
	}
}

// TestEventIDDistinctFromDedupKey covers property 4: event_id and dedup_key
// are always distinct, domain-separated values, never equal by construction.
func TestEventIDDistinctFromDedupKey(t *testing.T) {
	b := builder(t)
	ev, err := b.New(baseInput())
	if err != nil {
		t.Fatal(err)
	}
	if ev.EventID == ev.DedupKey {
		t.Fatalf("event_id must never equal dedup_key: both were %q", ev.EventID)
	}
	if strings.TrimPrefix(ev.EventID, "evt_") == ev.DedupKey {
		t.Fatalf("event_id's hash payload must not equal dedup_key's hash payload")
	}
}

func TestEventIDStableAcrossTimestampLocation(t *testing.T) {
	b := builder(t)
	utc := baseInput()

	loc := time.FixedZone("UTC-5", -5*3600)
	shifted := baseInput()
	shifted.CreatedAt = shifted.CreatedAt.In(loc)
	shifted.ExpiresAt = shifted.ExpiresAt.In(loc)

	a, err := b.New(utc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.New(shifted)
	if err != nil {
		t.Fatal(err)
	}
	if a.EventID != c.EventID {
		t.Fatalf("the same instant in a different Location must hash to the same event_id: %q vs %q", a.EventID, c.EventID)
	}
}

func TestDeterministicDedupKeyAcrossTimestamps(t *testing.T) {
	b := builder(t)
	in1 := baseInput()
	in2 := baseInput()
	in2.CreatedAt = in2.CreatedAt.Add(time.Minute)
	in2.ExpiresAt = in2.ExpiresAt.Add(time.Minute)

	ev1, err := b.New(in1)
	if err != nil {
		t.Fatal(err)
	}
	ev2, err := b.New(in2)
	if err != nil {
		t.Fatal(err)
	}
	if ev1.DedupKey != ev2.DedupKey {
		t.Fatalf("dedup keys should match for identical evidence/detector/set: %q vs %q", ev1.DedupKey, ev2.DedupKey)
	}
	if ev1.EventID == ev2.EventID {
		t.Fatalf("event ids should differ when created_at/expires_at differ")
	}
}

func TestDedupKeyDiffersByDetectorOrSet(t *testing.T) {
	b := builder(t)
	in := baseInput()
	base, err := b.New(in)
	if err != nil {
		t.Fatal(err)
	}

	diffSet := baseInput()
	diffSet.ToneSetID = "other-set"
	other, err := b.New(diffSet)
	if err != nil {
		t.Fatal(err)
	}
	if base.DedupKey == other.DedupKey {
		t.Fatalf("different tone_set_id must produce a different dedup key")
	}

	diffEvidence := baseInput()
	diffEvidence.Evidence = Evidence{AudioFingerprint: audioanalysis.FingerprintPrefix + strings.Repeat("c", 64)}
	other2, err := b.New(diffEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if base.DedupKey == other2.DedupKey {
		t.Fatalf("different evidence must produce a different dedup key")
	}
}

func TestRejectsMismatchedTGID(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.TGID = 57202 // does not match CH1A
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error for mismatched tgid/channel")
	}
}

func TestRejectsInvalidSourceKind(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.SourceKind = SourceKind("live")
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error for unsupported source_kind (never live)")
	}
}

func TestRejectsInvalidState(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.State = State("resolved")
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error for unsupported state")
	}
}

func TestRejectsOutOfBoundsConfidence(t *testing.T) {
	b := builder(t)
	for _, c := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		in := baseInput()
		in.Confidence = &c
		if _, err := b.New(in); err == nil {
			t.Fatalf("expected error for confidence %v", c)
		}
	}
}

func TestRejectsCrossedDetectorFields(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.KeywordListID = "should-not-be-set"
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error: tone detection must not carry a keyword_list_id")
	}

	in2 := baseInput()
	in2.DetectorKind = DetectorKeyword
	in2.KeywordListID = "list-1"
	// ToneSetID left set from baseInput -> both set, must be rejected.
	if _, err := b.New(in2); err == nil {
		t.Fatal("expected error: keyword detection must not carry a tone_set_id")
	}
}

func TestRejectsBadTimestamps(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.ExpiresAt = in.CreatedAt // not strictly after
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error: expires_at must be after created_at")
	}

	in2 := baseInput()
	in2.ExpiresAt = in2.CreatedAt.Add(testLimits().MaxExpiryWindow + time.Second)
	if _, err := b.New(in2); err == nil {
		t.Fatal("expected error: expires_at exceeds the caller-configured maximum window")
	}

	in3 := baseInput()
	in3.CreatedAt = time.Time{}
	if _, err := b.New(in3); err == nil {
		t.Fatal("expected error: created_at required")
	}
}

func TestDifferentLimitsProduceDifferentAcceptance(t *testing.T) {
	// The bound is genuinely caller-supplied: two Builders with different
	// Limits accept/reject the same Input differently. If the package had a
	// hidden internal default, this would be impossible to demonstrate.
	tight, err := NewBuilder(Limits{MaxExpiryWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	loose, err := NewBuilder(Limits{MaxExpiryWindow: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	in := baseInput()
	in.ExpiresAt = in.CreatedAt.Add(10 * time.Minute)

	if _, err := tight.New(in); err == nil {
		t.Fatal("expected the tight limit to reject a 10-minute window")
	}
	if _, err := loose.New(in); err != nil {
		t.Fatalf("expected the loose limit to accept a 10-minute window: %v", err)
	}
}

func TestRejectsEvidenceShapes(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.Evidence = Evidence{}
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error: evidence required")
	}

	in2 := baseInput()
	in2.Evidence = Evidence{AudioFingerprint: validFingerprint(), SourceIdentity: strings.Repeat("a", 64)}
	if _, err := b.New(in2); err == nil {
		t.Fatal("expected error: exactly one evidence identity")
	}

	in3 := baseInput()
	in3.Evidence = Evidence{AudioFingerprint: "not-a-real-fingerprint"}
	if _, err := b.New(in3); err == nil {
		t.Fatal("expected error: malformed audio_fingerprint")
	}

	in4 := baseInput()
	in4.Evidence = Evidence{SourceIdentity: "not-hex"}
	if _, err := b.New(in4); err == nil {
		t.Fatal("expected error: malformed source_identity")
	}
}

func TestRejectsUnboundedOrUnsafeLabel(t *testing.T) {
	b := builder(t)
	cases := []string{
		"",
		strings.Repeat("a", 81),
		"contains a \"transcript\" quote",
		"path/like/value",
		"line\nbreak",
		"credential=sk-live-abc123",
	}
	for _, label := range cases {
		in := baseInput()
		in.Label = label
		if _, err := b.New(in); err == nil {
			t.Fatalf("expected rejection for label %q", label)
		}
	}
}

func TestRejectsUnsupportedDetectorKind(t *testing.T) {
	b := builder(t)
	in := baseInput()
	in.DetectorKind = DetectorKind("transcription")
	if _, err := b.New(in); err == nil {
		t.Fatal("expected error for unsupported detector_kind")
	}
}

func TestConfidencePointerNotAliased(t *testing.T) {
	// A caller mutating the float64 behind their own Confidence pointer
	// after construction must never retroactively change an already-built,
	// supposedly-immutable Event.
	b := builder(t)
	in := baseInput()
	conf := 0.75
	in.Confidence = &conf

	ev, err := b.New(in)
	if err != nil {
		t.Fatal(err)
	}
	if *ev.Confidence != 0.75 {
		t.Fatalf("expected 0.75, got %v", *ev.Confidence)
	}
	conf = 0.01 // mutate the caller's own variable after construction
	if *ev.Confidence != 0.75 {
		t.Fatalf("Event.Confidence must not alias the caller's pointer: got %v after caller mutation", *ev.Confidence)
	}
}

func TestNoSideEffects(t *testing.T) {
	// Constructing an Event must be a pure, fully deterministic computation:
	// calling New twice with the same input produces an identical Event,
	// event_id included.
	b := builder(t)
	in := baseInput()
	a, err := b.New(in)
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.New(in)
	if err != nil {
		t.Fatal(err)
	}
	if a.EventID != c.EventID || a.DedupKey != c.DedupKey || a.DisplaySummary != c.DisplaySummary {
		t.Fatalf("repeated construction must be fully deterministic: %+v vs %+v", a, c)
	}
}
