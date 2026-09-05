// Package transcription treats provider text as untrusted evidence only.
package transcription

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Options struct {
	Enabled                                                  bool
	BaseURL, Model, Language, BearerToken, Prompt, WordBoost string
	Timeout, RetryDelay                                      time.Duration
	MaxResponseBytes, MaxAudioBytes                          int64
	MaxAttempts                                              int
}

func DefaultOptions() Options {
	return Options{Model: "small.en", Language: "en", Timeout: 60 * time.Second, RetryDelay: 5 * time.Second,
		MaxResponseBytes: 1 << 20, MaxAudioBytes: 32 << 20, MaxAttempts: 3}
}

func (o Options) Validate() error {
	invalid := errors.New("invalid transcription configuration")
	if o.Timeout < time.Second || o.Timeout > 5*time.Minute || o.RetryDelay < time.Second || o.RetryDelay > time.Minute || o.MaxAttempts < 1 || o.MaxAttempts > 5 || o.MaxResponseBytes < 1024 || o.MaxResponseBytes > 4<<20 || o.MaxAudioBytes < 1024 || o.MaxAudioBytes > 128<<20 {
		return invalid
	}
	for _, s := range []struct {
		v   string
		max int
	}{{o.Model, 128}, {o.Language, 32}, {o.Prompt, 2048}, {o.WordBoost, 2048}, {o.BearerToken, 4096}} {
		if len(s.v) > s.max || strings.IndexFunc(s.v, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
			return invalid
		}
	}
	if strings.TrimSpace(o.Model) == "" || strings.TrimSpace(o.Language) == "" {
		return invalid
	}
	if o.Enabled || o.BaseURL != "" {
		u, err := url.Parse(o.BaseURL)
		if err != nil || u.Hostname() == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(o.BaseURL, "#") || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || strings.ContainsAny(u.Host, " \\") {
			return invalid
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return invalid
			}
		}
	}
	return nil
}

func (o Options) Backoff(attempt int) time.Duration {
	d := o.RetryDelay
	for i := 1; i < attempt && d < time.Minute; i++ {
		d *= 2
	}
	if d > time.Minute {
		d = time.Minute
	}
	return d
}

type Failure struct {
	Code      string
	Retryable bool
}

func (f Failure) Error() string { return f.Code }
func SafeFailure(err error) Failure {
	var f Failure
	if errors.As(err, &f) {
		switch f.Code {
		case "provider_unavailable", "provider_timeout", "canceled", "provider_rejected", "invalid_response", "audio_too_large", "source_unavailable":
			return f
		}
	}
	return Failure{"provider_unavailable", true}
}
