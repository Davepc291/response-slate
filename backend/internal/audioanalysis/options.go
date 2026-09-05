// Package audioanalysis validates and measures audio without transcription or CAD actions.
package audioanalysis

import (
	"errors"
	"time"
)

type Options struct {
	FFprobePath   string
	FFmpegPath    string
	Timeout       time.Duration
	MaxDuration   time.Duration
	MaxAttempts   int
	RetryInterval time.Duration
}

func DefaultOptions() Options {
	return Options{"ffprobe", "ffmpeg", 30 * time.Second, 10 * time.Minute, 3, 2 * time.Second}
}

func (o Options) Validate() error {
	if o.FFprobePath == "" || o.FFmpegPath == "" || o.Timeout < time.Second || o.Timeout > 5*time.Minute ||
		o.MaxDuration < time.Second || o.MaxDuration > time.Hour || o.MaxAttempts < 1 || o.MaxAttempts > 10 ||
		o.RetryInterval < 10*time.Millisecond || o.RetryInterval > time.Minute {
		return errors.New("invalid audio analysis settings")
	}
	return nil
}

type failure struct {
	code      string
	retryable bool
}

func (e *failure) Error() string { return e.code }

var (
	errInvalid     = &failure{"invalid_audio", false}
	errDuration    = &failure{"unreasonable_duration", false}
	errUnavailable = &failure{"audio_tool_unavailable", true}
	errTimeout     = &failure{"audio_timeout", true}
	errCanceled    = &failure{"audio_canceled", true}
	errSource      = &failure{"source_changed_or_unavailable", true}
)

func safeFailure(err error) *failure {
	var f *failure
	if errors.As(err, &f) {
		return f
	}
	return &failure{"audio_analysis_failed", true}
}
