package alertpipeline

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/alerts"
	"greenwich-fire-responder/backend/internal/alertstore"
	"greenwich-fire-responder/backend/internal/keyworddetection"
	"greenwich-fire-responder/backend/internal/tonedetection"
)

// --- synthetic PCM fixtures (deterministic, matching tonedetection's own
// documented working values; no Greenwich-specific frequency is assumed) ---

const sampleRateHz = 16000
const testWindowMS = 100

func sineSamples(freqHz float64, durationMS int64, amplitude float64) []int16 {
	n := sampleRateHz * int(durationMS) / 1000
	out := make([]int16, n)
	for i := range out {
		v := amplitude * math.Sin(2*math.Pi*freqHz*float64(i)/sampleRateHz)
		out[i] = int16(v * 32767)
	}
	return out
}

func silenceSamples(durationMS int64) []int16 {
	return make([]int16, sampleRateHz*int(durationMS)/1000)
}

func concatSamples(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func longToneSet(id string, freqHz float64) tonedetection.ToneSet {
	return tonedetection.ToneSet{
		ID:                id,
		Channel:           alerts.ChannelCH1A,
		Segments:          []tonedetection.Segment{{FrequencyHz: freqHz, MinDurationMS: 800, MaxDurationMS: 3000}},
		ToleranceHz:       30,
		MaxGapMS:          500,
		PresenceThreshold: 0.15,
		MinConfidence:     0.6,
	}
}

func testLimits() Limits {
	return Limits{Alerts: alerts.Limits{MaxExpiryWindow: 10 * time.Minute}, Cooldown: 5 * time.Minute, MaxBatchSize: 10}
}

// --- fake store: records every call, in order, under a caller-suppliable result/error ---

type fakeStore struct {
	mu     sync.Mutex
	calls  []alertstore.Record
	result alertstore.Result
	err    error
	onCall func(rec alertstore.Record)
}

func (s *fakeStore) RecordDetection(ctx context.Context, cooldown time.Duration, rec alertstore.Record) (alertstore.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, rec)
	s.mu.Unlock()
	if s.onCall != nil {
		s.onCall(rec)
	}
	if s.err != nil {
		return alertstore.Result{}, s.err
	}
	return s.result, nil
}

func (s *fakeStore) configIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	for i, c := range s.calls {
		out[i] = c.ConfigID
	}
	return out
}

func evidence() alerts.Evidence {
	return alerts.Evidence{AudioFingerprint: "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("a", 64)}
}

func TestNewServiceRequiresExplicitValidLimits(t *testing.T) {
	if _, err := NewService(nil, testLimits()); err == nil {
		t.Fatal("expected error for nil store")
	}
	store := &fakeStore{}
	bad := testLimits()
	bad.Cooldown = -time.Second
	if _, err := NewService(store, bad); err == nil {
		t.Fatal("expected error for negative cooldown")
	}
	bad = testLimits()
	bad.Cooldown = alertstore.MaxCooldown + time.Second
	if _, err := NewService(store, bad); err == nil {
		t.Fatal("expected error for cooldown above the resource ceiling")
	}
	bad = testLimits()
	bad.MaxBatchSize = 0
	if _, err := NewService(store, bad); err == nil {
		t.Fatal("expected error for zero max batch size")
	}
	bad = testLimits()
	bad.Alerts.MaxExpiryWindow = 0
	if _, err := NewService(store, bad); err == nil {
		t.Fatal("expected error propagated from alerts.NewBuilder for an unset expiry window")
	}
}

func TestProcessToneSuccess(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{longToneSet("quick-call-1", 1000)}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{result: alertstore.Result{AuditID: 1, EventCreated: true}}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := concatSamples(silenceSamples(200), sineSamples(1000, 1000, 0.8), silenceSamples(200))
	outcomes, err := svc.ProcessTone(context.Background(), ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		Labels:     map[string]string{"quick-call-1": "quick-call alert"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Rejected || outcomes[0].State != alerts.StateMatched {
		t.Fatalf("unexpected outcomes: %+v", outcomes)
	}
	if len(store.calls) != 1 {
		t.Fatalf("expected exactly one store call, got %d", len(store.calls))
	}
	rec := store.calls[0]
	if rec.Event == nil || rec.Event.ToneSetID != "quick-call-1" || rec.Event.DetectorKind != alerts.DetectorTone {
		t.Fatalf("unexpected persisted record: %+v", rec)
	}
	if !strings.Contains(rec.Event.DisplaySummary, "NOT LIVE CAD") {
		t.Fatalf("display summary missing safety phrase: %q", rec.Event.DisplaySummary)
	}
}

func TestProcessKeywordSuccess(t *testing.T) {
	list := keyworddetection.List{
		ID:   "structure-fire",
		Name: "structure fire",
		Keywords: []keyworddetection.Keyword{
			{Phrase: "structure fire"},
		},
		Channels: []alerts.Channel{alerts.ChannelCH1A},
	}
	store := &fakeStore{result: alertstore.Result{AuditID: 2, EventCreated: true}}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	outcomes, err := svc.ProcessKeyword(context.Background(), KeywordRequest{
		Detector:   keyworddetection.New(),
		List:       list,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		Transcript: keyworddetection.Transcript{Text: "Engine 2 responding, structure fire reported."},
		Now:        now,
		ExpiresAt:  now.Add(time.Minute),
		Labels:     map[string]string{"structure fire": "structure fire"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].State != alerts.StateMatched {
		t.Fatalf("unexpected outcomes: %+v", outcomes)
	}
	if len(store.calls) != 1 || store.calls[0].DetectorKind != alerts.DetectorKeyword {
		t.Fatalf("unexpected persisted record: %+v", store.calls)
	}
	if store.calls[0].KeywordOccurrences == nil || *store.calls[0].KeywordOccurrences != 1 {
		t.Fatalf("expected one recorded occurrence, got %+v", store.calls[0].KeywordOccurrences)
	}
}

func TestProcessKeywordChannelNotConfiguredNoError(t *testing.T) {
	list := keyworddetection.List{
		ID:       "structure-fire",
		Name:     "structure fire",
		Keywords: []keyworddetection.Keyword{{Phrase: "structure fire"}},
		Channels: []alerts.Channel{alerts.ChannelCH2B}, // not CH1A
	}
	store := &fakeStore{}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	outcomes, err := svc.ProcessKeyword(context.Background(), KeywordRequest{
		Detector:   keyworddetection.New(),
		List:       list,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		Transcript: keyworddetection.Transcript{Text: "structure fire"},
		Now:        now,
		ExpiresAt:  now.Add(time.Minute),
	})
	if err != nil || len(outcomes) != 0 {
		t.Fatalf("expected no outcomes and no error, got %+v, %v", outcomes, err)
	}
	if len(store.calls) != 0 {
		t.Fatal("an unconfigured channel must never reach the store")
	}
}

func TestMissingLabelIsRejectedNotFabricated(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{longToneSet("quick-call-1", 1000)}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{result: alertstore.Result{AuditID: 1, EventCreated: true}}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := concatSamples(silenceSamples(200), sineSamples(1000, 1000, 0.8), silenceSamples(200))
	outcomes, err := svc.ProcessTone(context.Background(), ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		// Labels intentionally omitted.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Rejected {
		t.Fatalf("expected a rejected outcome when no label is supplied, got %+v", outcomes)
	}
	if len(store.calls) != 1 || store.calls[0].State != alertstore.StateRejected || store.calls[0].Event != nil {
		t.Fatalf("expected a rejected record with no Event: %+v", store.calls)
	}
}

func TestOrderingPreserved(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{
		longToneSet("set-a", 1000),
		longToneSet("set-b", 1400),
	}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{result: alertstore.Result{AuditID: 1, EventCreated: true}}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := silenceSamples(2000) // both sets fail to find their segment: state=partial
	_, err = svc.ProcessTone(context.Background(), ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		Labels:     map[string]string{"set-a": "tone a", "set-b": "tone b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := store.configIDs()
	if len(ids) != 2 || ids[0] != "set-a" || ids[1] != "set-b" {
		t.Fatalf("expected detector output ordering preserved (set-a, set-b), got %v", ids)
	}
}

func TestCancellationStopsBatchPromptly(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{
		longToneSet("set-a", 1000),
		longToneSet("set-b", 1400),
		longToneSet("set-c", 1800),
	}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{
		result: alertstore.Result{AuditID: 1, EventCreated: true},
		onCall: func(rec alertstore.Record) {
			if rec.ConfigID == "set-a" {
				cancel() // cancel after the first item is persisted
			}
		},
	}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := silenceSamples(2000)
	outcomes, err := svc.ProcessTone(ctx, ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		Labels:     map[string]string{"set-a": "tone a", "set-b": "tone b", "set-c": "tone c"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("expected exactly one outcome before cancellation stopped the batch, got %d", len(outcomes))
	}
	if len(store.calls) != 1 {
		t.Fatalf("expected the batch to stop promptly instead of silently completing: %d calls", len(store.calls))
	}
}

func TestBatchSizeBoundRejectsBeforeAnyPersistence(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{
		longToneSet("set-a", 1000),
		longToneSet("set-b", 1400),
	}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{result: alertstore.Result{AuditID: 1, EventCreated: true}}
	limits := testLimits()
	limits.MaxBatchSize = 1
	svc, err := NewService(store, limits)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := silenceSamples(2000)
	_, err = svc.ProcessTone(context.Background(), ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		Labels:     map[string]string{"set-a": "tone a", "set-b": "tone b"},
	})
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("expected ErrBatchTooLarge, got %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatal("an oversized batch must never partially persist")
	}
}

func TestStoreErrorStopsProcessingWithoutFabricatingSuccess(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{
		longToneSet("set-a", 1000),
		longToneSet("set-b", 1400),
	}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{err: alertstore.ErrUnavailable}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := silenceSamples(2000)
	outcomes, err := svc.ProcessTone(context.Background(), ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57201,
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		Labels:     map[string]string{"set-a": "tone a", "set-b": "tone b"},
	})
	if !errors.Is(err, alertstore.ErrUnavailable) {
		t.Fatalf("expected the store error to propagate, got %v", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("a failed persistence must never report a fabricated successful outcome: %+v", outcomes)
	}
}

func TestContractChannelTGIDRestrictionEnforced(t *testing.T) {
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{longToneSet("quick-call-1", 1000)}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{result: alertstore.Result{AuditID: 1, EventCreated: true}}
	svc, err := NewService(store, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	samples := concatSamples(silenceSamples(200), sineSamples(1000, 1000, 0.8), silenceSamples(200))
	outcomes, err := svc.ProcessTone(context.Background(), ToneRequest{
		Registry:   registry,
		Channel:    alerts.ChannelCH1A,
		TGID:       57202, // wrong TGID for CH1A
		SourceKind: alerts.SourceSynthetic,
		Evidence:   evidence(),
		PCM:        tonedetection.PCM{SampleRate: sampleRateHz, Samples: samples},
		Now:        now,
		ExpiresAt:  now.Add(5 * time.Minute),
		Labels:     map[string]string{"quick-call-1": "quick-call alert"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Rejected {
		t.Fatalf("expected a mismatched channel/tgid to be rejected, not silently accepted: %+v", outcomes)
	}
}
