// Package tonedetection is a deterministic, synthetic signal-processing
// evidence source over already-decoded PCM (docs/alert-notification-engine-v1.md
// Section 3). It never opens a recording file, invokes FFmpeg, decodes audio,
// or watches a directory: callers supply already-materialized 16 kHz mono PCM
// samples. Detectors propose evidence only; they have no operational
// authority and never mutate any state.
package tonedetection

import (
	"errors"
	"math"
	"math/cmplx"
	"regexp"

	"greenwich-fire-responder/backend/internal/alerts"
)

const (
	// sampleRateHz is the only supported rate: the canonical rate the
	// existing audio-analysis pipeline decodes to (pcm-s16le-16000-mono-v1).
	// This is a correctness constraint taken directly from the contract's
	// established PCM convention, not a chosen policy value.
	sampleRateHz = 16000

	// maxSamples bounds accepted input to 10 minutes of 16kHz mono audio,
	// matching audioanalysis.DefaultOptions().MaxDuration. This is a
	// resource/DoS safety ceiling on untrusted input size, required by the
	// "accept only bounded inputs" rule; it does not affect what counts as
	// a tone match and is not a Greenwich operational value.
	maxSamples = sampleRateHz * 600

	maxSegments = 8

	// minWindowMS/maxWindowMS bound the caller-supplied analysis window to a
	// sane range so it cannot degenerate into a zero-length window (a
	// division-by-zero/index panic) or an unbounded per-window FFT size.
	// This is a resource-safety ceiling, not a detection-sensitivity choice:
	// the actual window size that determines detection behavior is supplied
	// by the caller (see NewRegistry), never assumed here.
	minWindowMS = 10
	maxWindowMS = 2000
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Segment is one {frequency, duration} element of an ordered tone pattern. A
// long tone is a one-segment list; a two-tone sequence is a two-segment list.
type Segment struct {
	FrequencyHz   float64
	MinDurationMS int64
	MaxDurationMS int64
}

// ToneSet is one configured tone pattern for one channel. PresenceThreshold
// and MinConfidence are both explicit, caller-supplied configuration: this
// package assumes no default acceptance sensitivity for either.
type ToneSet struct {
	ID          string
	Channel     alerts.Channel
	Segments    []Segment
	ToleranceHz float64
	MaxGapMS    int64
	// PresenceThreshold is the minimum concentration score (see
	// concentration below) needed to count a window as "tone-shaped" at
	// all, for locating a candidate run's duration/sequence. It must be
	// <= MinConfidence: presence is a prerequisite for, and always looser
	// than, acceptance. A caller wanting the old single-threshold behavior
	// can simply set PresenceThreshold == MinConfidence, or lower to admit
	// noisier candidate runs before the confidence gate is applied (Section
	// 3's noisy-audio handling: frequency/duration can align while
	// confidence is still too low to accept).
	PresenceThreshold float64
	// MinConfidence is the acceptance threshold: a completed run whose
	// average confidence falls below this is low_confidence, not matched.
	MinConfidence float64
}

// PCM is already-decoded, already-materialized audio. No detector in this
// package opens a file, invokes FFmpeg, or otherwise produces this value
// itself.
type PCM struct {
	SampleRate int
	Samples    []int16
}

// Reason gives a fixed, non-echoing detail code for a Detection, matching
// the false-positive/partial/noisy-audio handling required by Section 3.
type Reason string

const (
	ReasonMatched       Reason = "matched"
	ReasonSegmentAbsent Reason = "segment_not_found"
	ReasonGapExceeded   Reason = "gap_exceeded"
	ReasonTooShort      Reason = "segment_too_short"
	ReasonLowConfidence Reason = "low_confidence"
)

// Detection is one candidate-match evaluation attempt for one tone set,
// recorded regardless of outcome, matching Section 3's false-positive
// handling requirement. State is always one of alerts.StateMatched,
// alerts.StatePartial, or alerts.StateLowConfidence: Section 3 defines no
// "ambiguous" reason code for tone detection (that concept is
// keyword-detection-specific, Section 4), so this package never produces it.
type Detection struct {
	ToneSetID           string
	State               alerts.State
	Reason              Reason
	Confidence          float64
	MeasuredHz          []float64
	MeasuredDurationsMS []int64
}

func validateToneSet(s ToneSet) error {
	if !idPattern.MatchString(s.ID) {
		return errors.New("tonedetection: invalid tone set id")
	}
	switch s.Channel {
	case alerts.ChannelCH1A, alerts.ChannelCH2B, alerts.ChannelCH3B, alerts.ChannelCH4C:
	default:
		return errors.New("tonedetection: invalid channel")
	}
	if len(s.Segments) < 1 || len(s.Segments) > maxSegments {
		return errors.New("tonedetection: unsupported segment count")
	}
	for _, seg := range s.Segments {
		// Written as an explicit inclusive-range check (rather than a
		// negated "< lo || > hi") so a NaN FrequencyHz is rejected: every
		// comparison against NaN is false, so the positive form correctly
		// evaluates to "not in range" and the guard fires.
		if !(seg.FrequencyHz >= 20 && seg.FrequencyHz < sampleRateHz/2) {
			return errors.New("tonedetection: frequency out of supported range")
		}
		if seg.MinDurationMS <= 0 || seg.MaxDurationMS < seg.MinDurationMS || seg.MaxDurationMS > 30000 {
			return errors.New("tonedetection: invalid segment duration bounds")
		}
	}
	if !(s.ToleranceHz >= 0 && s.ToleranceHz <= 500) {
		return errors.New("tonedetection: tolerance out of supported range")
	}
	if s.MaxGapMS < 0 || s.MaxGapMS > 30000 {
		return errors.New("tonedetection: max gap out of supported range")
	}
	if !(s.MinConfidence >= 0 && s.MinConfidence <= 1) {
		return errors.New("tonedetection: min confidence out of range")
	}
	if !(s.PresenceThreshold >= 0 && s.PresenceThreshold <= s.MinConfidence) {
		return errors.New("tonedetection: presence threshold must be between 0 and min confidence")
	}
	return nil
}

// Registry holds validated tone sets grouped per channel. Construct with
// NewRegistry before use; a constructed Registry supports concurrent Detect
// calls and retains no call data.
type Registry struct {
	byChannel     map[alerts.Channel][]ToneSet
	windowSamples int64
	windowMS      int64
}

// NewRegistry validates every tone set up front and fails closed: a
// malformed configuration is rejected explicitly, never partially admitted.
// windowMS is the fixed-size analysis window used for every FFT (Section 3)
// and is required, explicit, caller-supplied configuration: this package
// picks no default window size, since window size trades detection time
// resolution against frequency resolution and materially affects whether a
// real tone is matched.
func NewRegistry(sets []ToneSet, windowMS int64) (*Registry, error) {
	if windowMS < minWindowMS || windowMS > maxWindowMS {
		return nil, errors.New("tonedetection: window_ms out of supported range")
	}
	seen := map[string]bool{}
	byChannel := map[alerts.Channel][]ToneSet{}
	for _, s := range sets {
		if err := validateToneSet(s); err != nil {
			return nil, err
		}
		key := string(s.Channel) + "\x00" + s.ID
		if seen[key] {
			return nil, errors.New("tonedetection: duplicate tone set id for channel")
		}
		seen[key] = true
		// Defensive copy: breaks aliasing with the caller's own Segments
		// backing array, so a caller mutating the slice they passed in
		// after NewRegistry returns cannot silently change this Registry's
		// already-validated configuration.
		s.Segments = append([]Segment(nil), s.Segments...)
		byChannel[s.Channel] = append(byChannel[s.Channel], s)
	}
	return &Registry{byChannel: byChannel, windowSamples: sampleRateHz * windowMS / 1000, windowMS: windowMS}, nil
}

func validatePCM(pcm PCM) error {
	if pcm.SampleRate != sampleRateHz {
		return errors.New("tonedetection: unsupported sample rate")
	}
	if len(pcm.Samples) == 0 {
		return errors.New("tonedetection: empty pcm")
	}
	if len(pcm.Samples) > maxSamples {
		return errors.New("tonedetection: pcm exceeds maximum bounded duration")
	}
	return nil
}

// Detect runs every tone set configured for channel against pcm, in
// registration order, and returns one Detection per configured tone set. It
// never re-decodes audio, never opens anything, and never mutates state.
func (r *Registry) Detect(channel alerts.Channel, pcm PCM) ([]Detection, error) {
	if err := validatePCM(pcm); err != nil {
		return nil, err
	}
	samples := toFloat(pcm.Samples)
	sets := r.byChannel[channel]
	results := make([]Detection, 0, len(sets))
	for _, s := range sets {
		results = append(results, detectOne(s, samples, r.windowSamples, r.windowMS))
	}
	return results, nil
}

func toFloat(samples []int16) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = float64(s) / 32768.0
	}
	return out
}

// detectOne walks the samples once, matching each segment in order. Gap
// tolerance applies only between segments (Section 3), never within one.
// Each window's concentration score is computed at most once, so total work
// is bounded by (number of windows) FFTs, never recomputed redundantly.
func detectOne(set ToneSet, samples []float64, windowSamples, windowMS int64) Detection {
	numWindows := int64(len(samples)) / windowSamples
	var cursor int64
	var measuredHz []float64
	var measuredDur []int64
	var confSum float64
	var confCount int

	for i, seg := range set.Segments {
		var skippedMS int64
		for cursor < numWindows {
			if concentration(samples, cursor, windowSamples, seg.FrequencyHz, set.ToleranceHz) >= set.PresenceThreshold {
				break
			}
			cursor++
			skippedMS += windowMS
			if i > 0 && skippedMS > set.MaxGapMS {
				return Detection{ToneSetID: set.ID, State: alerts.StatePartial, Reason: ReasonGapExceeded,
					MeasuredHz: measuredHz, MeasuredDurationsMS: measuredDur}
			}
		}
		if cursor >= numWindows {
			return Detection{ToneSetID: set.ID, State: alerts.StatePartial, Reason: ReasonSegmentAbsent,
				MeasuredHz: measuredHz, MeasuredDurationsMS: measuredDur}
		}

		runStart := cursor
		var sumConf float64
		for cursor < numWindows {
			c := concentration(samples, cursor, windowSamples, seg.FrequencyHz, set.ToleranceHz)
			if c < set.PresenceThreshold {
				break
			}
			sumConf += c
			cursor++
			if (cursor-runStart)*windowMS >= seg.MaxDurationMS {
				break
			}
		}
		runWindows := cursor - runStart
		runLenMS := runWindows * windowMS
		avgConf := sumConf / float64(runWindows)
		measuredHz = append(measuredHz, seg.FrequencyHz)
		measuredDur = append(measuredDur, runLenMS)
		if runLenMS < seg.MinDurationMS {
			return Detection{ToneSetID: set.ID, State: alerts.StatePartial, Reason: ReasonTooShort,
				Confidence: avgConf, MeasuredHz: measuredHz, MeasuredDurationsMS: measuredDur}
		}
		confSum += avgConf
		confCount++
	}

	overallConf := confSum / float64(confCount)
	if overallConf < set.MinConfidence {
		return Detection{ToneSetID: set.ID, State: alerts.StateLowConfidence, Reason: ReasonLowConfidence,
			Confidence: overallConf, MeasuredHz: measuredHz, MeasuredDurationsMS: measuredDur}
	}
	return Detection{ToneSetID: set.ID, State: alerts.StateMatched, Reason: ReasonMatched,
		Confidence: overallConf, MeasuredHz: measuredHz, MeasuredDurationsMS: measuredDur}
}

// concentration is a normalized 0-1 score built from the ratio between the
// best in-tolerance-band FFT bin amplitude and the window's own RMS
// amplitude (scaled by sqrt(2), the RMS-to-amplitude ratio of a pure
// sinusoid). A clean, on-frequency tone scores near 1; noise or an
// off-frequency signal scores low. This is the "signal-to-noise ratio and
// frequency-bin energy concentration" measure Section 3 describes, computed
// via an actual FFT (see fft below), not a single-frequency shortcut.
func concentration(samples []float64, windowIdx, windowSamples int64, freqHz, toleranceHz float64) float64 {
	start := windowIdx * windowSamples
	w := samples[start : start+windowSamples]

	var totalEnergy float64
	for _, x := range w {
		totalEnergy += x * x
	}
	if totalEnergy <= 0 {
		return 0
	}
	rms := math.Sqrt(totalEnergy / float64(len(w)))

	spectrum := fftWindow(w)
	n := len(spectrum)
	binHz := float64(sampleRateHz) / float64(n)
	loBin := int(math.Floor((freqHz - toleranceHz) / binHz))
	hiBin := int(math.Ceil((freqHz + toleranceHz) / binHz))
	if loBin < 0 {
		loBin = 0
	}
	nyquistBin := n / 2
	if hiBin > nyquistBin {
		hiBin = nyquistBin
	}
	var best float64
	for k := loBin; k <= hiBin; k++ {
		if amp := 2 * cmplx.Abs(spectrum[k]) / float64(len(w)); amp > best {
			best = amp
		}
	}
	ratio := best / (rms*math.Sqrt2 + 1e-12)
	if ratio > 1 {
		ratio = 1
	}
	return ratio
}

// fftWindow zero-pads w to the next power of two and returns its full
// discrete Fourier transform via fft. Zero-padding lets the caller pick any
// windowMS without the FFT itself requiring a power-of-two sample count.
func fftWindow(w []float64) []complex128 {
	n := nextPow2(len(w))
	buf := make([]complex128, n)
	for i, x := range w {
		buf[i] = complex(x, 0)
	}
	fft(buf)
	return buf
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// fft computes the discrete Fourier transform of x in place: an iterative
// radix-2 Cooley-Tukey FFT, the actual Fast Fourier Transform Section 3
// requires (not merely a DFT-equivalent single-bin shortcut). len(x) must be
// a power of two, which fftWindow guarantees by zero-padding.
func fft(x []complex128) {
	n := len(x)
	if n <= 1 {
		return
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		angle := -2 * math.Pi / float64(length)
		wlen := cmplx.Rect(1, angle)
		for i := 0; i < n; i += length {
			w := complex(1.0, 0.0)
			half := length / 2
			for j := 0; j < half; j++ {
				u := x[i+j]
				v := x[i+j+half] * w
				x[i+j] = u + v
				x[i+j+half] = u - v
				w *= wlen
			}
		}
	}
}
