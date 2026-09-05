package audioanalysis

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
)

// Probe retains only allowlisted technical metadata, never input paths or tags.
type Probe struct {
	Format     string `json:"format"`
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	DurationMS *int64 `json:"reported_duration_ms,omitempty"`
}

func ParseProbe(data []byte, maxDuration time.Duration) (Probe, error) {
	var raw struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if len(data) > 64*1024 || json.Unmarshal(data, &raw) != nil || len(raw.Streams) != 1 {
		return Probe{}, errInvalid
	}
	s := raw.Streams[0]
	rate, err := strconv.Atoi(s.Rate)
	if err != nil || s.Type != "audio" || rate < 8000 || rate > 192000 || s.Channels < 1 || s.Channels > 8 {
		return Probe{}, errInvalid
	}
	switch raw.Format.Name {
	case "mp3":
		if s.Codec != "mp3" {
			return Probe{}, errInvalid
		}
	case "wav":
		switch s.Codec {
		case "pcm_u8", "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_f64le":
		default:
			return Probe{}, errInvalid
		}
	default:
		return Probe{}, errInvalid
	}
	p := Probe{Format: raw.Format.Name, Codec: s.Codec, SampleRate: rate, Channels: s.Channels}
	for _, value := range []string{raw.Format.Duration, s.Duration} {
		if value == "" || value == "N/A" {
			continue
		} // Pipe inputs may not expose duration.
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > maxDuration.Seconds() {
			return Probe{}, errDuration
		}
		ms := int64(math.Round(seconds * 1000))
		if p.DurationMS == nil {
			p.DurationMS = &ms
		}
	}
	return p, nil
}
