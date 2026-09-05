package audioanalysis

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"math"
	"time"
)

const FingerprintPrefix = "pcm-s16le-16000-mono-v1:sha256:"

type Result struct {
	Fingerprint string
	DurationMS  int64
	RMS         float64
	Peak        float64
	Probe       Probe
}

type pcmStats struct {
	hash                  hash.Hash
	bytes, samples, limit int64
	squares, peak         float64
	low                   byte
	half                  bool
	err                   error
}

func newPCM(maxDuration time.Duration) *pcmStats {
	return &pcmStats{hash: sha256.New(), limit: int64(maxDuration.Seconds()*16000) * 2}
}

func (p *pcmStats) Write(data []byte) (int, error) {
	if p.bytes+int64(len(data)) > p.limit {
		p.err = errDuration
		return 0, p.err
	}
	_, _ = p.hash.Write(data)
	p.bytes += int64(len(data))
	for _, b := range data {
		if !p.half {
			p.low = b
			p.half = true
			continue
		}
		sample := int16(uint16(p.low) | uint16(b)<<8)
		amplitude := math.Abs(float64(sample) / 32768.0)
		p.squares += amplitude * amplitude
		p.peak = math.Max(p.peak, amplitude)
		p.samples++
		p.half = false
	}
	return len(data), nil
}

func (p *pcmStats) result() (Result, error) {
	if p.err != nil {
		return Result{}, p.err
	}
	if p.half || p.samples == 0 {
		return Result{}, errInvalid
	}
	return Result{Fingerprint: FingerprintPrefix + hex.EncodeToString(p.hash.Sum(nil)),
		DurationMS: (p.samples*1000 + 8000) / 16000,
		RMS:        math.Min(p.peak, math.Sqrt(p.squares/float64(p.samples))), Peak: p.peak}, nil
}
