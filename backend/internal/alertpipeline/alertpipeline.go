// Package alertpipeline is the Step 8C synthetic-only pipeline service. It
// wires the Step 8B tonedetection and keyworddetection detectors, through the
// Step 8B alerts.Builder, into Step 8C persistence (alertstore). It accepts
// only already-prepared bounded PCM (for tone detection) and already-
// completed bounded transcript text (for keyword detection); it never
// discovers, opens, or decodes a recording, and it never calls dispatch-
// interpretation, unit-status, or CAD-write logic.
//
// This package is never registered by the live server. A caller (a test, or
// a future authorized integration) must construct a Service and invoke it
// explicitly.
package alertpipeline

import (
	"context"
	"errors"
	"time"

	"greenwich-fire-responder/backend/internal/alerts"
	"greenwich-fire-responder/backend/internal/alertstore"
	"greenwich-fire-responder/backend/internal/keyworddetection"
	"greenwich-fire-responder/backend/internal/tonedetection"
)

// ErrBatchTooLarge is returned, before anything is persisted, when a call
// would process more detection results than Limits.MaxBatchSize allows.
var ErrBatchTooLarge = errors.New("alertpipeline: detection batch exceeds the configured maximum size")

// ErrLimits marks an invalid, missing, or implicit Limits value: every bound
// here must be an explicit caller choice.
var ErrLimits = errors.New("alertpipeline: limits must be explicit and valid")

// Limits are the caller-supplied operational bounds this package will not
// invent for itself. Section 14 of the approved contract leaves cooldown,
// expiration, and confidence-threshold policy unresolved; this package
// assumes no default of its own for any of them.
type Limits struct {
	// Alerts is passed through to alerts.NewBuilder unchanged.
	Alerts alerts.Limits
	// Cooldown suppresses redelivery of a repeat match sharing a dedup_key
	// (Section 6). Zero is a valid, explicit choice ("no suppression");
	// negative or larger than alertstore.MaxCooldown is rejected.
	Cooldown time.Duration
	// MaxBatchSize bounds how many detector results one call may process, a
	// resource-safety ceiling, not a detection-sensitivity choice.
	MaxBatchSize int
}

// Store is the persistence dependency this package needs. alertstore.Postgres
// satisfies it; tests use a fake.
type Store interface {
	RecordDetection(ctx context.Context, cooldown time.Duration, rec alertstore.Record) (alertstore.Result, error)
}

// Outcome reports what happened to one detector result.
type Outcome struct {
	ConfigID string // tone_set_id or keyword_list_id
	State    alerts.State
	Rejected bool
	Result   alertstore.Result
}

// Service is stateless beyond its configuration; construct with NewService
// before use. A constructed Service supports concurrent calls.
type Service struct {
	builder *alerts.Builder
	store   Store
	limits  Limits
}

func NewService(store Store, limits Limits) (*Service, error) {
	if store == nil {
		return nil, ErrLimits
	}
	if limits.Cooldown < 0 || limits.Cooldown > alertstore.MaxCooldown {
		return nil, ErrLimits
	}
	if limits.MaxBatchSize <= 0 {
		return nil, ErrLimits
	}
	b, err := alerts.NewBuilder(limits.Alerts)
	if err != nil {
		return nil, err
	}
	return &Service{builder: b, store: store, limits: limits}, nil
}

// detectionResult is the common shape both detectors' outputs are normalized
// to before persistence, preserving each detector's own output ordering.
type detectionResult struct {
	ConfigID           string
	State              alerts.State
	Reason             string
	Confidence         *float64
	Label              string
	MeasuredHz         []float64
	MeasuredDurationMS []int64
	KeywordOccurrences *int
}

// ToneRequest carries already-decoded, already-bounded PCM (Section 2:
// "Decoded PCM already produced during audio analysis") and every tone-set
// configuration value explicitly: this package invents no frequency,
// duration, tolerance, or confidence threshold.
type ToneRequest struct {
	Registry   *tonedetection.Registry
	Channel    alerts.Channel
	TGID       int64
	SourceKind alerts.SourceKind
	Evidence   alerts.Evidence
	PCM        tonedetection.PCM
	Now        time.Time
	ExpiresAt  time.Time
	// Labels maps each configured tone_set_id to the human-readable label
	// alerts.Input.Label requires. A tone set with no entry here is recorded
	// as StateRejected (Section 11's "malformed input" path): this package
	// never invents a display label.
	Labels map[string]string
}

// toneReasonHasConfidence reports whether tonedetection actually computed a
// confidence value for this Detection. tonedetection.Detection leaves
// Confidence at its zero value (never explicitly set) for the two reasons
// that abort before any window is evaluated, so this package must not
// forward a fabricated 0.0 confidence for those.
func toneReasonHasConfidence(reason tonedetection.Reason) bool {
	switch reason {
	case tonedetection.ReasonMatched, tonedetection.ReasonLowConfidence, tonedetection.ReasonTooShort:
		return true
	default:
		return false
	}
}

// ProcessTone runs every tone set configured for req.Channel against req.PCM
// and persists one record per result, in detector order.
func (s *Service) ProcessTone(ctx context.Context, req ToneRequest) ([]Outcome, error) {
	detections, err := req.Registry.Detect(req.Channel, req.PCM)
	if err != nil {
		return nil, err
	}
	results := make([]detectionResult, len(detections))
	for i, d := range detections {
		var confidence *float64
		if toneReasonHasConfidence(d.Reason) {
			c := d.Confidence
			confidence = &c
		}
		results[i] = detectionResult{
			ConfigID:           d.ToneSetID,
			State:              d.State,
			Reason:             string(d.Reason),
			Confidence:         confidence,
			Label:              req.Labels[d.ToneSetID],
			MeasuredHz:         d.MeasuredHz,
			MeasuredDurationMS: d.MeasuredDurationsMS,
		}
	}
	return s.process(ctx, req.SourceKind, req.Channel, req.TGID, alerts.DetectorTone, req.Evidence, req.Now, req.ExpiresAt, results)
}

// KeywordRequest carries already-completed transcript text (Section 2:
// "Transcript text already produced by the existing transcription worker")
// and every keyword-list configuration value explicitly.
type KeywordRequest struct {
	Detector   *keyworddetection.Detector
	List       keyworddetection.List
	Channel    alerts.Channel
	TGID       int64
	SourceKind alerts.SourceKind
	Evidence   alerts.Evidence
	Transcript keyworddetection.Transcript
	Now        time.Time
	ExpiresAt  time.Time
	// Labels maps each keyword phrase to the human-readable label
	// alerts.Input.Label requires. A matched phrase with no entry here is
	// recorded as StateRejected: this package never invents a display label.
	Labels map[string]string
}

func keywordReason(state alerts.State) string {
	if state == alerts.StateMatched {
		return "keyword_matched"
	}
	return "keyword_ambiguous_context"
}

// ProcessKeyword runs list against req.Transcript for req.Channel and
// persists one record per match, in detector order. A list not configured
// for req.Channel yields no matches and no error, matching
// keyworddetection.Detector.Detect.
func (s *Service) ProcessKeyword(ctx context.Context, req KeywordRequest) ([]Outcome, error) {
	matches, err := req.Detector.Detect(req.List, req.Channel, req.Transcript)
	if err != nil {
		return nil, err
	}
	results := make([]detectionResult, len(matches))
	for i, m := range matches {
		occ := m.Occurrences
		results[i] = detectionResult{
			ConfigID:           m.ListID,
			State:              m.State,
			Reason:             keywordReason(m.State),
			Label:              req.Labels[m.Phrase],
			KeywordOccurrences: &occ,
		}
	}
	return s.process(ctx, req.SourceKind, req.Channel, req.TGID, alerts.DetectorKeyword, req.Evidence, req.Now, req.ExpiresAt, results)
}

// process persists results in order, stopping (without fabricating success
// for the remainder) if the context is canceled or the store fails.
func (s *Service) process(ctx context.Context, sourceKind alerts.SourceKind, channel alerts.Channel, tgid int64,
	detectorKind alerts.DetectorKind, evidence alerts.Evidence, now, expiresAt time.Time, results []detectionResult) ([]Outcome, error) {
	if len(results) > s.limits.MaxBatchSize {
		return nil, ErrBatchTooLarge
	}
	outcomes := make([]Outcome, 0, len(results))
	for _, r := range results {
		select {
		case <-ctx.Done():
			return outcomes, ctx.Err()
		default:
		}

		in := alerts.Input{
			CreatedAt:    now,
			ExpiresAt:    expiresAt,
			SourceKind:   sourceKind,
			Channel:      channel,
			TGID:         tgid,
			DetectorKind: detectorKind,
			Confidence:   r.Confidence,
			State:        r.State,
			Evidence:     evidence,
			Label:        r.Label,
		}
		if detectorKind == alerts.DetectorTone {
			in.ToneSetID = r.ConfigID
		} else {
			in.KeywordListID = r.ConfigID
		}

		rec := alertstore.Record{
			CreatedAt:          now,
			SourceKind:         sourceKind,
			Channel:            channel,
			TGID:               tgid,
			DetectorKind:       detectorKind,
			ConfigID:           r.ConfigID,
			State:              r.State,
			Reason:             r.Reason,
			Confidence:         r.Confidence,
			MeasuredHz:         r.MeasuredHz,
			MeasuredDurationMS: r.MeasuredDurationMS,
			KeywordOccurrences: r.KeywordOccurrences,
			Evidence:           evidence,
		}

		rejected := false
		if ev, err := s.builder.New(in); err != nil {
			rejected = true
			rec.State = alertstore.StateRejected
			// A fixed, safe reason code: the underlying alerts.Builder error
			// text is never forwarded, so it can never carry anything
			// unbounded or unexpected into the audit log.
			rec.Reason = "builder_validation_failed"
			rec.Confidence = nil
			rec.MeasuredHz = nil
			rec.MeasuredDurationMS = nil
			rec.KeywordOccurrences = nil
		} else {
			rec.Event = &ev
		}

		result, err := s.store.RecordDetection(ctx, s.limits.Cooldown, rec)
		if err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, Outcome{ConfigID: r.ConfigID, State: rec.State, Rejected: rejected, Result: result})
	}
	return outcomes, nil
}
