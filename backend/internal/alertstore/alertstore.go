// Package alertstore is the Step 8C PostgreSQL persistence layer for the
// Step 8B alert-event-v1 model (docs/alert-notification-engine-v1.md
// Section 5, 9). It stores exactly two tables: an append-only
// detection_audit row per detector evaluation attempt, and at most one
// alert_events row per attempt, subject to a caller-supplied cooldown. It
// never opens a recording, reads a transcript, or calls incident/unit-status/
// CAD-write logic, and it is never registered by the live server: a caller
// (a test, or a future authorized integration) must construct and invoke it
// explicitly.
package alertstore

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"greenwich-fire-responder/backend/internal/alerts"
)

var (
	// ErrInput marks a caller/programming error: malformed record, bounds
	// violation, or a channel/tgid mismatch. It is safe to display; it never
	// wraps a raw database error.
	ErrInput = errors.New("alertstore: invalid detection record")
	// ErrUnavailable marks a database failure. The caller must treat this as
	// "unknown state, fail closed": never a false claim of persistence.
	ErrUnavailable = errors.New("alertstore: database unavailable")
)

// StateRejected marks a detection whose alerts.Builder validation failed
// (Section 11: duplicate or malformed input). It is deliberately not one of
// the four values alerts.State itself produces; this package's own state
// column is a superset that also records rejections, so a failure is never
// silently dropped instead of audited.
const StateRejected alerts.State = "rejected"

// MaxMeasuredSegments bounds measured_hz/measured_duration_ms, matching
// tonedetection's own maxSegments ceiling: a resource bound, not a detection
// policy value.
const MaxMeasuredSegments = 8

// MaxKeywordOccurrences bounds keyword_occurrences against a pathological
// transcript; not a detection-sensitivity choice.
const MaxKeywordOccurrences = 100000

// MaxCooldown bounds the caller-supplied cooldown window to 30 days, a
// resource-safety ceiling against a pathological configuration value. It is
// not a chosen cooldown policy: Section 14 leaves the actual duration
// unresolved, and this package assumes no default of its own.
const MaxCooldown = 30 * 24 * time.Hour

var (
	configIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	reasonPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	fingerprintHex  = regexp.MustCompile(`^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$`)
	sourceIDHex     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// channelTGID mirrors the FR-1 mapping already established by
// docs/alert-notification-engine-v1.md Section 2 and repeated by the
// Step 8B alerts/tonedetection/keyworddetection packages. It is a
// correctness constraint from the existing approved contract, not a new
// policy value.
var channelTGID = map[alerts.Channel]int64{
	alerts.ChannelCH1A: 57201,
	alerts.ChannelCH2B: 57202,
	alerts.ChannelCH3B: 57203,
	alerts.ChannelCH4C: 57204,
}

// Record is exactly what this package will persist for one detector
// evaluation attempt. Every field is bounded and safe to store: there is no
// transcript, file path, credential, raw audio, or free-form metadata field,
// and none can be added without editing this struct and its validation.
//
// When State is StateRejected, Event must be nil: alerts.Builder rejected
// the input, so no dedup_key/event_id exists. For every other state, Event
// must be non-nil and consistent with the top-level fields.
type Record struct {
	CreatedAt          time.Time
	SourceKind         alerts.SourceKind
	Channel            alerts.Channel
	TGID               int64
	DetectorKind       alerts.DetectorKind
	ConfigID           string // tone_set_id or keyword_list_id
	State              alerts.State
	Reason             string // fixed, lowercase_snake_case reason code
	Confidence         *float64
	MeasuredHz         []float64 // tone only
	MeasuredDurationMS []int64   // tone only, same length as MeasuredHz
	KeywordOccurrences *int      // keyword only
	Evidence           alerts.Evidence
	Event              *alerts.Event
}

// Result reports what RecordDetection actually did, never more than it can
// prove: EventCreated is true only when a new alert_events row was inserted.
type Result struct {
	AuditID      int64
	EventCreated bool
	Suppressed   bool // a duplicate dedup_key within the caller's cooldown
}

// Duplicate reports whether this call was an idempotent retry of an
// already-recorded event: neither newly created nor cooldown-suppressed.
func (r Result) Duplicate() bool { return !r.EventCreated && !r.Suppressed }

// Store persists one detector evaluation attempt. cooldown is a required,
// explicit, caller-supplied policy value (Section 14 leaves the actual
// duration unresolved); implementations must not assume a default.
type Store interface {
	RecordDetection(ctx context.Context, cooldown time.Duration, rec Record) (Result, error)
}

func channelMatchesTGID(ch alerts.Channel, tgid int64) bool {
	want, ok := channelTGID[ch]
	return ok && want == tgid
}

// Validate checks rec against every bound this package enforces, independent
// of and in addition to whatever alerts.Builder already validated for
// rec.Event. It never trusts the caller to have validated first.
func (rec Record) Validate() error {
	if rec.CreatedAt.IsZero() {
		return ErrInput
	}
	switch rec.SourceKind {
	case alerts.SourceSynthetic, alerts.SourceShadowReplay:
	default:
		return ErrInput
	}
	if !channelMatchesTGID(rec.Channel, rec.TGID) {
		return ErrInput
	}
	switch rec.DetectorKind {
	case alerts.DetectorTone, alerts.DetectorKeyword:
	default:
		return ErrInput
	}
	if !configIDPattern.MatchString(rec.ConfigID) {
		return ErrInput
	}
	if !reasonPattern.MatchString(rec.Reason) {
		return ErrInput
	}
	if rec.Confidence != nil {
		c := *rec.Confidence
		if math.IsNaN(c) || c < 0 || c > 1 {
			return ErrInput
		}
	}
	if len(rec.MeasuredHz) > MaxMeasuredSegments || len(rec.MeasuredDurationMS) > MaxMeasuredSegments {
		return ErrInput
	}
	if (rec.MeasuredHz == nil) != (rec.MeasuredDurationMS == nil) || len(rec.MeasuredHz) != len(rec.MeasuredDurationMS) {
		return ErrInput
	}
	if rec.DetectorKind != alerts.DetectorTone && (rec.MeasuredHz != nil || rec.MeasuredDurationMS != nil) {
		return ErrInput
	}
	for _, hz := range rec.MeasuredHz {
		if math.IsNaN(hz) || hz < 0 || hz > 20000 {
			return ErrInput
		}
	}
	for _, ms := range rec.MeasuredDurationMS {
		if ms < 0 || ms > 30000 {
			return ErrInput
		}
	}
	if rec.KeywordOccurrences != nil {
		if rec.DetectorKind != alerts.DetectorKeyword {
			return ErrInput
		}
		if *rec.KeywordOccurrences < 0 || *rec.KeywordOccurrences > MaxKeywordOccurrences {
			return ErrInput
		}
	}
	fp, sid := rec.Evidence.AudioFingerprint != "", rec.Evidence.SourceIdentity != ""
	if fp == sid {
		return ErrInput
	}
	if fp && !fingerprintHex.MatchString(rec.Evidence.AudioFingerprint) {
		return ErrInput
	}
	if sid && !sourceIDHex.MatchString(rec.Evidence.SourceIdentity) {
		return ErrInput
	}

	if rec.State == StateRejected {
		if rec.Event != nil {
			return ErrInput
		}
		return nil
	}
	switch rec.State {
	case alerts.StateMatched, alerts.StateAmbiguous, alerts.StatePartial, alerts.StateLowConfidence:
	default:
		return ErrInput
	}
	ev := rec.Event
	if ev == nil {
		return ErrInput
	}
	if ev.SourceKind != rec.SourceKind || ev.Channel != rec.Channel || ev.TGID != rec.TGID ||
		ev.DetectorKind != rec.DetectorKind || ev.State != rec.State {
		return ErrInput
	}
	switch rec.DetectorKind {
	case alerts.DetectorTone:
		if ev.ToneSetID != rec.ConfigID || ev.KeywordListID != "" {
			return ErrInput
		}
	case alerts.DetectorKeyword:
		if ev.KeywordListID != rec.ConfigID || ev.ToneSetID != "" {
			return ErrInput
		}
	}
	if ev.EventID == "" || ev.DedupKey == "" || ev.DisplaySummary == "" || ev.ExpiresAt.IsZero() {
		return ErrInput
	}
	return nil
}

func cooldownSeconds(cooldown time.Duration) (int64, error) {
	if cooldown < 0 || cooldown > MaxCooldown {
		return 0, ErrInput
	}
	return int64(cooldown / time.Second), nil
}
