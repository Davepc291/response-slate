package alertstore

import (
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/alerts"
)

func baseEvent(t *testing.T) alerts.Event {
	t.Helper()
	b, err := alerts.NewBuilder(alerts.Limits{MaxExpiryWindow: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ev, err := b.New(alerts.Input{
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
		SourceKind:   alerts.SourceSynthetic,
		Channel:      alerts.ChannelCH1A,
		TGID:         57201,
		DetectorKind: alerts.DetectorTone,
		ToneSetID:    "quick-call-1",
		Evidence:     alerts.Evidence{AudioFingerprint: "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("a", 64)},
		State:        alerts.StateMatched,
		Label:        "quick-call alert",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func baseRecord(t *testing.T) Record {
	ev := baseEvent(t)
	return Record{
		CreatedAt:    ev.CreatedAt,
		SourceKind:   ev.SourceKind,
		Channel:      ev.Channel,
		TGID:         ev.TGID,
		DetectorKind: ev.DetectorKind,
		ConfigID:     ev.ToneSetID,
		State:        ev.State,
		Reason:       "matched",
		Evidence:     alerts.Evidence{AudioFingerprint: "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("a", 64)},
		Event:        &ev,
	}
}

func TestValidRecordAccepted(t *testing.T) {
	rec := baseRecord(t)
	if err := rec.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRejectedRecordRequiresNilEvent(t *testing.T) {
	rec := baseRecord(t)
	rec.State = StateRejected
	rec.Reason = "builder_validation_failed"
	// Event still set: invalid, a rejected record must carry no dedup_key/event_id.
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: rejected record must not carry an Event")
	}
	rec.Event = nil
	if err := rec.Validate(); err != nil {
		t.Fatalf("unexpected error for a properly-shaped rejected record: %v", err)
	}
}

func TestNonRejectedRecordRequiresEvent(t *testing.T) {
	rec := baseRecord(t)
	rec.Event = nil
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: non-rejected record must carry an Event")
	}
}

func TestChannelTGIDMismatchRejected(t *testing.T) {
	rec := baseRecord(t)
	rec.TGID = 57202 // CH1A requires 57201
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: channel/tgid mismatch must be rejected (contract Section 2)")
	}
}

func TestUnsupportedChannelRejected(t *testing.T) {
	rec := baseRecord(t)
	rec.Channel = alerts.Channel("CH9Z")
	rec.TGID = 0
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: unsupported channel rejected")
	}
}

func TestEventConsistencyEnforced(t *testing.T) {
	rec := baseRecord(t)
	rec.ConfigID = "different-tone-set"
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: config_id must match the Event it was built from")
	}
}

func TestMeasuredArrayBounds(t *testing.T) {
	rec := baseRecord(t)
	rec.MeasuredHz = make([]float64, MaxMeasuredSegments+1)
	rec.MeasuredDurationMS = make([]int64, MaxMeasuredSegments+1)
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: measured array exceeds bound")
	}
}

func TestMeasuredArrayLengthMismatch(t *testing.T) {
	rec := baseRecord(t)
	rec.MeasuredHz = []float64{600, 900}
	rec.MeasuredDurationMS = []int64{500}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: mismatched measured array lengths")
	}
}

func TestMeasuredFieldsOnlyForTone(t *testing.T) {
	rec := baseRecord(t)
	rec.DetectorKind = alerts.DetectorKeyword
	rec.ConfigID = "structure-fire"
	rec.Event.DetectorKind = alerts.DetectorKeyword
	rec.Event.ToneSetID = ""
	rec.Event.KeywordListID = "structure-fire"
	rec.MeasuredHz = []float64{600}
	rec.MeasuredDurationMS = []int64{500}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: measured_hz/duration only valid for tone detections")
	}
}

func TestKeywordOccurrencesBounds(t *testing.T) {
	rec := baseRecord(t)
	rec.DetectorKind = alerts.DetectorKeyword
	rec.ConfigID = "structure-fire"
	rec.Event.DetectorKind = alerts.DetectorKeyword
	rec.Event.ToneSetID = ""
	rec.Event.KeywordListID = "structure-fire"
	occ := MaxKeywordOccurrences + 1
	rec.KeywordOccurrences = &occ
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: keyword occurrences exceeds bound")
	}
	occ = -1
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: negative keyword occurrences rejected")
	}
}

func TestKeywordOccurrencesOnlyForKeyword(t *testing.T) {
	rec := baseRecord(t)
	occ := 3
	rec.KeywordOccurrences = &occ
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: keyword_occurrences only valid for keyword detections")
	}
}

func TestConfidenceOutOfRangeRejected(t *testing.T) {
	rec := baseRecord(t)
	bad := 1.5
	rec.Confidence = &bad
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: out-of-range confidence rejected")
	}
}

func TestConfigIDBoundedAndSQLInjectionShapedRejected(t *testing.T) {
	rec := baseRecord(t)
	for _, id := range []string{
		"",
		strings.Repeat("a", 129),
		"quick'; DROP TABLE alert_events; --",
		"quick call", // spaces not allowed in config_id
	} {
		rec.ConfigID = id
		rec.Event.ToneSetID = id
		if err := rec.Validate(); err == nil {
			t.Fatalf("expected rejection for config_id %q", id)
		}
	}
}

func TestReasonMustBeFixedShapeLowerSnakeCase(t *testing.T) {
	rec := baseRecord(t)
	for _, reason := range []string{"", "Matched", "matched!", strings.Repeat("a", 65), "has space"} {
		rec.Reason = reason
		if err := rec.Validate(); err == nil {
			t.Fatalf("expected rejection for reason %q", reason)
		}
	}
}

func TestEvidenceShapeValidated(t *testing.T) {
	rec := baseRecord(t)
	rec.Evidence = alerts.Evidence{}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: evidence required")
	}
	rec.Evidence = alerts.Evidence{AudioFingerprint: "not-a-real-fingerprint"}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: malformed audio_fingerprint rejected")
	}
	rec.Evidence = alerts.Evidence{
		AudioFingerprint: "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("a", 64),
		SourceIdentity:   strings.Repeat("b", 64),
	}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: exactly one evidence identity required")
	}
}

func TestZeroCreatedAtRejected(t *testing.T) {
	rec := baseRecord(t)
	rec.CreatedAt = time.Time{}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error: created_at required")
	}
}

func TestCooldownSecondsBounds(t *testing.T) {
	if _, err := cooldownSeconds(-time.Second); err == nil {
		t.Fatal("expected error: negative cooldown rejected")
	}
	if _, err := cooldownSeconds(MaxCooldown + time.Second); err == nil {
		t.Fatal("expected error: cooldown above the resource ceiling rejected")
	}
	secs, err := cooldownSeconds(5 * time.Minute)
	if err != nil || secs != 300 {
		t.Fatalf("expected 300 seconds, got %v err=%v", secs, err)
	}
	secs, err = cooldownSeconds(0)
	if err != nil || secs != 0 {
		t.Fatalf("zero cooldown must be an explicit, valid choice: got %v err=%v", secs, err)
	}
}

func TestResultDuplicate(t *testing.T) {
	if (Result{EventCreated: true}).Duplicate() {
		t.Fatal("a created event must not report as duplicate")
	}
	if (Result{Suppressed: true}).Duplicate() {
		t.Fatal("a cooldown-suppressed event must not report as duplicate")
	}
	if !(Result{}).Duplicate() {
		t.Fatal("neither created nor suppressed must report as duplicate (idempotent retry)")
	}
}
