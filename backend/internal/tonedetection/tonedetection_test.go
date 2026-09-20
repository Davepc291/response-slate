package tonedetection

import (
	"math"
	"math/cmplx"
	"math/rand"
	"reflect"
	"testing"

	"greenwich-fire-responder/backend/internal/alerts"
)

// tone appends durationMS of a pure sine wave at freqHz, amplitude in (0,1].
func tone(freqHz float64, durationMS int64, amplitude float64) []int16 {
	n := sampleRateHz * int(durationMS) / 1000
	out := make([]int16, n)
	for i := range out {
		v := amplitude * math.Sin(2*math.Pi*freqHz*float64(i)/sampleRateHz)
		out[i] = int16(v * 32767)
	}
	return out
}

func silence(durationMS int64) []int16 {
	return make([]int16, sampleRateHz*int(durationMS)/1000)
}

// noise appends durationMS of deterministic (seeded) white noise.
func noise(durationMS int64, amplitude float64, seed int64) []int16 {
	n := sampleRateHz * int(durationMS) / 1000
	out := make([]int16, n)
	r := rand.New(rand.NewSource(seed))
	for i := range out {
		out[i] = int16(amplitude * (r.Float64()*2 - 1) * 32767)
	}
	return out
}

func mix(a, b []int16) []int16 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(math.Max(-32768, math.Min(32767, float64(a[i])+float64(b[i]))))
	}
	return out
}

func concat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func pcm(samples []int16) PCM { return PCM{SampleRate: sampleRateHz, Samples: samples} }

// testWindowMS is the analysis window these tests explicitly choose. It is
// not a package default: NewRegistry requires every caller to supply one.
const testWindowMS = 100

func longTone() ToneSet {
	return ToneSet{
		ID:      "monitor-tone",
		Channel: alerts.ChannelCH1A,
		Segments: []Segment{
			{FrequencyHz: 1000, MinDurationMS: 800, MaxDurationMS: 3000},
		},
		ToleranceHz:       30,
		MaxGapMS:          500,
		PresenceThreshold: 0.15,
		MinConfidence:     0.6,
	}
}

func twoTone() ToneSet {
	return ToneSet{
		ID:      "quick-call",
		Channel: alerts.ChannelCH1A,
		Segments: []Segment{
			{FrequencyHz: 800, MinDurationMS: 800, MaxDurationMS: 1500},
			{FrequencyHz: 1200, MinDurationMS: 800, MaxDurationMS: 1500},
		},
		ToleranceHz:       30,
		MaxGapMS:          300,
		PresenceThreshold: 0.15,
		MinConfidence:     0.6,
	}
}

func registry(t *testing.T, sets ...ToneSet) *Registry {
	t.Helper()
	r, err := NewRegistry(sets, testWindowMS)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestLongToneMatches(t *testing.T) {
	r := registry(t, longTone())
	samples := concat(silence(200), tone(1000, 1000, 0.8), silence(200))
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(dets) != 1 {
		t.Fatalf("expected one detection, got %d", len(dets))
	}
	d := dets[0]
	if d.State != alerts.StateMatched {
		t.Fatalf("expected matched, got %+v", d)
	}
	if len(d.MeasuredDurationsMS) != 1 || d.MeasuredDurationsMS[0] < 800 {
		t.Fatalf("measured duration too short: %+v", d)
	}
}

func TestTwoToneSequenceMatches(t *testing.T) {
	r := registry(t, twoTone())
	samples := concat(silence(100), tone(800, 1000, 0.8), tone(1200, 1000, 0.8), silence(100))
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	d := dets[0]
	if d.State != alerts.StateMatched {
		t.Fatalf("expected matched, got %+v", d)
	}
	if len(d.MeasuredHz) != 2 {
		t.Fatalf("expected 2 measured segments, got %+v", d.MeasuredHz)
	}
}

func TestToleranceBoundaries(t *testing.T) {
	set := longTone() // ToleranceHz = 30
	r := registry(t, set)

	// Just inside tolerance: target + 25Hz.
	inside := concat(silence(100), tone(1025, 1000, 0.8), silence(100))
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(inside))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State != alerts.StateMatched {
		t.Fatalf("frequency inside tolerance must match: %+v", dets[0])
	}

	// Clearly outside tolerance: target + 150Hz (well beyond the ~10Hz
	// window resolution's coherence band around the tolerance edge).
	outside := concat(silence(100), tone(1150, 1000, 0.8), silence(100))
	dets, err = r.Detect(alerts.ChannelCH1A, pcm(outside))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State == alerts.StateMatched {
		t.Fatalf("frequency outside tolerance must not match: %+v", dets[0])
	}
}

func TestMinDurationBoundary(t *testing.T) {
	set := longTone() // MinDurationMS = 800
	r := registry(t, set)

	tooShort := concat(silence(100), tone(1000, 300, 0.8), silence(500))
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(tooShort))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State != alerts.StatePartial {
		t.Fatalf("too-short tone must be partial, got %+v", dets[0])
	}

	longEnough := concat(silence(100), tone(1000, 900, 0.8), silence(100))
	dets, err = r.Detect(alerts.ChannelCH1A, pcm(longEnough))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State != alerts.StateMatched {
		t.Fatalf("sufficient duration must match, got %+v", dets[0])
	}
}

func TestMaxGapExceededIsPartial(t *testing.T) {
	set := twoTone() // MaxGapMS = 300
	r := registry(t, set)

	samples := concat(silence(100), tone(800, 1000, 0.8), silence(600), tone(1200, 1000, 0.8))
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State != alerts.StatePartial || dets[0].Reason != ReasonGapExceeded {
		t.Fatalf("gap beyond max_gap_ms must be partial/gap_exceeded, got %+v", dets[0])
	}
}

func TestSegmentNeverFoundIsPartial(t *testing.T) {
	r := registry(t, longTone())
	samples := silence(1000)
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State != alerts.StatePartial || dets[0].Reason != ReasonSegmentAbsent {
		t.Fatalf("silence must be partial/segment_not_found, got %+v", dets[0])
	}
}

func TestNoisyAudioIsLowConfidence(t *testing.T) {
	set := longTone() // MinConfidence = 0.6
	r := registry(t, set)

	clean := tone(1000, 1000, 0.5)
	loud := noise(1000, 0.9, 42)
	noisy := concat(silence(100), mix(clean, loud), silence(100))

	dets, err := r.Detect(alerts.ChannelCH1A, pcm(noisy))
	if err != nil {
		t.Fatal(err)
	}
	d := dets[0]
	if d.State != alerts.StateLowConfidence {
		t.Fatalf("heavily noised tone must be low_confidence (frequency/duration can still align), got %+v", d)
	}
	if d.Confidence <= 0 || d.Confidence >= set.MinConfidence {
		t.Fatalf("expected a measured confidence below the acceptance threshold: %+v", d)
	}
	if len(d.MeasuredDurationsMS) != 1 || d.MeasuredDurationsMS[0] < set.Segments[0].MinDurationMS {
		t.Fatalf("noisy handling must still record measured duration: %+v", d)
	}
}

func TestUnsupportedToneSetIsRejected(t *testing.T) {
	bad := []ToneSet{
		{ID: "x", Channel: alerts.ChannelCH1A, Segments: nil, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 0.5},
		{ID: "x", Channel: alerts.ChannelCH1A, Segments: []Segment{{FrequencyHz: 9000, MinDurationMS: 500, MaxDurationMS: 1000}}, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 0.5},
		{ID: "x", Channel: alerts.ChannelCH1A, Segments: []Segment{{FrequencyHz: 1000, MinDurationMS: 0, MaxDurationMS: 1000}}, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 0.5},
		{ID: "x", Channel: alerts.ChannelCH1A, Segments: []Segment{{FrequencyHz: 1000, MinDurationMS: 1000, MaxDurationMS: 500}}, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 0.5},
		{ID: "x", Channel: alerts.Channel("CHX"), Segments: []Segment{{FrequencyHz: 1000, MinDurationMS: 500, MaxDurationMS: 1000}}, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 0.5},
		{ID: "x", Channel: alerts.ChannelCH1A, Segments: []Segment{{FrequencyHz: 1000, MinDurationMS: 500, MaxDurationMS: 1000}}, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 1.5},
		{ID: "", Channel: alerts.ChannelCH1A, Segments: []Segment{{FrequencyHz: 1000, MinDurationMS: 500, MaxDurationMS: 1000}}, ToleranceHz: 10, MaxGapMS: 100, MinConfidence: 0.5},
	}
	for i, s := range bad {
		if _, err := NewRegistry([]ToneSet{s}, testWindowMS); err == nil {
			t.Fatalf("case %d: expected rejection of unsupported tone set %+v", i, s)
		}
	}
}

func TestNaNAndInfRejected(t *testing.T) {
	base := func() ToneSet {
		s := longTone()
		s.ID = "x"
		return s
	}
	cases := []func(*ToneSet){
		func(s *ToneSet) { s.Segments[0].FrequencyHz = math.NaN() },
		func(s *ToneSet) { s.Segments[0].FrequencyHz = math.Inf(1) },
		func(s *ToneSet) { s.Segments[0].FrequencyHz = math.Inf(-1) },
		func(s *ToneSet) { s.ToleranceHz = math.NaN() },
		func(s *ToneSet) { s.MinConfidence = math.NaN() },
		func(s *ToneSet) { s.PresenceThreshold = math.NaN() },
	}
	for i, mutate := range cases {
		s := base()
		s.Segments = append([]Segment(nil), s.Segments...) // avoid mutating the shared literal's backing array across cases
		mutate(&s)
		if _, err := NewRegistry([]ToneSet{s}, testWindowMS); err == nil {
			t.Fatalf("case %d: expected NaN/Inf tone set field to be rejected: %+v", i, s)
		}
	}
}

func TestPresenceThresholdMustNotExceedMinConfidence(t *testing.T) {
	s := longTone()
	s.ID = "x"
	s.PresenceThreshold = s.MinConfidence + 0.1
	if _, err := NewRegistry([]ToneSet{s}, testWindowMS); err == nil {
		t.Fatal("expected error: presence_threshold must not exceed min_confidence")
	}
}

func TestWindowMSBounded(t *testing.T) {
	set := longTone()
	for _, ms := range []int64{0, -1, minWindowMS - 1, maxWindowMS + 1} {
		if _, err := NewRegistry([]ToneSet{set}, ms); err == nil {
			t.Fatalf("expected window_ms=%d to be rejected", ms)
		}
	}
	if _, err := NewRegistry([]ToneSet{set}, minWindowMS); err != nil {
		t.Fatalf("expected window_ms=%d (minimum) to be accepted: %v", minWindowMS, err)
	}
	if _, err := NewRegistry([]ToneSet{set}, maxWindowMS); err != nil {
		t.Fatalf("expected window_ms=%d (maximum) to be accepted: %v", maxWindowMS, err)
	}
}

// TestSegmentsSliceNotAliased is the fix for the reviewed mutable-slice
// aliasing gap: NewRegistry must defensively copy Segments so a caller
// mutating the slice they passed in cannot silently change an
// already-validated Registry's configuration.
func TestSegmentsSliceNotAliased(t *testing.T) {
	set := longTone()
	set.Segments = []Segment{{FrequencyHz: 1000, MinDurationMS: 800, MaxDurationMS: 3000}}
	r, err := NewRegistry([]ToneSet{set}, testWindowMS)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the caller's own backing array after construction.
	set.Segments[0].FrequencyHz = 3999

	samples := concat(silence(100), tone(1000, 1000, 0.8), silence(100))
	dets, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if dets[0].State != alerts.StateMatched {
		t.Fatalf("Registry must be unaffected by the caller's post-construction mutation: %+v", dets[0])
	}
}

// TestFFTProducesCorrectPeakBin exercises the real FFT (fft/fftWindow)
// directly: a pure sinusoid placed exactly on a bin frequency must produce
// its spectral peak at that bin, with the expected unit amplitude after
// normalization. This is what makes the detector's "FFT-based frequency
// analysis" (Section 3) an actual Fast Fourier Transform rather than a
// single-frequency shortcut.
func TestFFTProducesCorrectPeakBin(t *testing.T) {
	const n = 1024
	const binIndex = 64 // freq = 64 * 16000 / 1024 = 1000Hz exactly
	freq := float64(binIndex) * float64(sampleRateHz) / float64(n)

	samples := make([]float64, n)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * freq * float64(i) / sampleRateHz)
	}
	spectrum := fftWindow(samples)
	if len(spectrum) != n {
		t.Fatalf("expected fft length %d, got %d", n, len(spectrum))
	}

	peakBin, peakMag := -1, -1.0
	for k := 0; k <= n/2; k++ {
		m := cmplx.Abs(spectrum[k])
		if m > peakMag {
			peakMag, peakBin = m, k
		}
	}
	if peakBin != binIndex {
		t.Fatalf("expected peak at bin %d, got bin %d", binIndex, peakBin)
	}
	amp := 2 * peakMag / float64(n)
	if math.Abs(amp-1.0) > 0.02 {
		t.Fatalf("expected amplitude ~1.0 at the peak bin, got %v", amp)
	}
}

func TestFFTOfSilenceIsZero(t *testing.T) {
	samples := make([]float64, 256)
	spectrum := fftWindow(samples)
	for k, c := range spectrum {
		if cmplx.Abs(c) > 1e-9 {
			t.Fatalf("bin %d: expected ~0 magnitude for an all-zero window, got %v", k, cmplx.Abs(c))
		}
	}
}

func TestRejectedInputPCM(t *testing.T) {
	r := registry(t, longTone())

	if _, err := r.Detect(alerts.ChannelCH1A, PCM{SampleRate: 44100, Samples: tone(1000, 1000, 0.8)}); err == nil {
		t.Fatal("expected error for unsupported sample rate")
	}
	if _, err := r.Detect(alerts.ChannelCH1A, PCM{SampleRate: sampleRateHz, Samples: nil}); err == nil {
		t.Fatal("expected error for empty pcm")
	}
	if _, err := r.Detect(alerts.ChannelCH1A, PCM{SampleRate: sampleRateHz, Samples: make([]int16, maxSamples+1)}); err == nil {
		t.Fatal("expected error for pcm exceeding bounded duration")
	}
}

func TestDuplicateToneSetIDRejected(t *testing.T) {
	set := longTone()
	if _, err := NewRegistry([]ToneSet{set, set}, testWindowMS); err == nil {
		t.Fatal("expected error for duplicate tone set id on the same channel")
	}
}

func TestTalkgroupFiltering(t *testing.T) {
	ch1Only := longTone()
	r := registry(t, ch1Only)

	samples := concat(silence(100), tone(1000, 1000, 0.8), silence(100))
	dets, err := r.Detect(alerts.ChannelCH2B, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(dets) != 0 {
		t.Fatalf("tone set configured only for CH1A must not run on CH2B: %+v", dets)
	}

	dets, err = r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(dets) != 1 {
		t.Fatalf("tone set configured for CH1A must run on CH1A: %+v", dets)
	}
}

func TestDuplicateInputIsDeterministic(t *testing.T) {
	r := registry(t, longTone())
	samples := concat(silence(100), tone(1000, 1000, 0.8), silence(100))

	d1, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	d2, err := r.Detect(alerts.ChannelCH1A, pcm(samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(d1) != 1 || len(d2) != 1 || !reflect.DeepEqual(d1[0], d2[0]) {
		t.Fatalf("identical input must produce identical output: %+v vs %+v", d1, d2)
	}
}

func TestNoSideEffects(t *testing.T) {
	r := registry(t, longTone())
	samples := concat(silence(100), tone(1000, 1000, 0.8), silence(100))
	input := pcm(samples)
	original := append([]int16(nil), input.Samples...)
	if _, err := r.Detect(alerts.ChannelCH1A, input); err != nil {
		t.Fatal(err)
	}
	for i := range original {
		if input.Samples[i] != original[i] {
			t.Fatalf("input samples must not be mutated")
		}
	}
}
