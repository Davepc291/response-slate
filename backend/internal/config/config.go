// Package config reads API settings from the process environment only.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"greenwich-fire-responder/backend/internal/audioanalysis"
	"greenwich-fire-responder/backend/internal/authconfig"
	"greenwich-fire-responder/backend/internal/operations"
	"greenwich-fire-responder/backend/internal/recordings"
	"greenwich-fire-responder/backend/internal/transcription"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DatabaseRequired bool
	Recordings       recordings.Options
	Audio            audioanalysis.Options
	Transcription    transcription.Options
	Operations       operations.Options
	// Auth is the Step 9C authentication configuration. Its zero value has
	// Enabled == false, so an existing deployment that sets none of the
	// GFR_AUTH_* variables is completely unaffected.
	Auth authconfig.Options
}

// Load does not read .env files. Errors never contain environment values.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:      strings.TrimSpace(os.Getenv("GFR_HTTP_ADDR")),
		DatabaseURL:   strings.TrimSpace(os.Getenv("GFR_DATABASE_URL")),
		Recordings:    recordings.DefaultOptions(),
		Audio:         audioanalysis.DefaultOptions(),
		Transcription: transcription.DefaultOptions(),
		Operations:    operations.DefaultOptions(),
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:8080"
	}
	if value := strings.TrimSpace(os.Getenv("GFR_DATABASE_REQUIRED")); value != "" {
		required, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, errors.New("GFR_DATABASE_REQUIRED must be a boolean")
		}
		cfg.DatabaseRequired = required
	}
	if cfg.DatabaseRequired && cfg.DatabaseURL == "" {
		return Config{}, errors.New("GFR_DATABASE_URL is required when GFR_DATABASE_REQUIRED is true")
	}
	cfg.Recordings.Directory = strings.TrimSpace(os.Getenv("GFR_RECORDINGS_DIR"))
	if value := strings.TrimSpace(os.Getenv("GFR_RECORDING_TIMEZONE")); value != "" {
		cfg.Recordings.Timezone = value
	}
	for name, target := range map[string]*time.Duration{
		"GFR_RECORDING_POLL_INTERVAL":  &cfg.Recordings.PollInterval,
		"GFR_RECORDING_STABLE_FOR":     &cfg.Recordings.StableFor,
		"GFR_RECORDING_MAX_WAIT":       &cfg.Recordings.MaxWait,
		"GFR_RECORDING_RETRY_INTERVAL": &cfg.Recordings.RetryInterval,
	} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, errors.New("invalid recording watcher duration")
			}
			*target = duration
		}
	}
	if value := strings.TrimSpace(os.Getenv("GFR_RECORDING_MAX_ATTEMPTS")); value != "" {
		attempts, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("invalid recording watcher attempt limit")
		}
		cfg.Recordings.MaxAttempts = attempts
	}
	if err := cfg.Recordings.Validate(); err != nil {
		return Config{}, err
	}
	for name, target := range map[string]*string{"GFR_FFPROBE_PATH": &cfg.Audio.FFprobePath, "GFR_FFMPEG_PATH": &cfg.Audio.FFmpegPath} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			*target = value
		}
	}
	for name, target := range map[string]*time.Duration{"GFR_AUDIO_TIMEOUT": &cfg.Audio.Timeout,
		"GFR_AUDIO_MAX_DURATION": &cfg.Audio.MaxDuration, "GFR_AUDIO_RETRY_INTERVAL": &cfg.Audio.RetryInterval} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, errors.New("invalid audio analysis duration")
			}
			*target = duration
		}
	}
	if value := strings.TrimSpace(os.Getenv("GFR_AUDIO_MAX_ATTEMPTS")); value != "" {
		attempts, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("invalid audio attempt limit")
		}
		cfg.Audio.MaxAttempts = attempts
	}
	if err := cfg.Audio.Validate(); err != nil {
		return Config{}, err
	}
	if err := loadTranscription(&cfg.Transcription); err != nil {
		return Config{}, err
	}
	if err := loadOperations(&cfg.Operations); err != nil {
		return Config{}, err
	}
	if err := loadAuth(&cfg.Auth); err != nil {
		return Config{}, err
	}
	if cfg.Auth.Enabled && cfg.DatabaseURL == "" {
		return Config{}, errors.New("GFR_DATABASE_URL is required when GFR_AUTH_ENABLED is true")
	}
	return cfg, nil
}

func loadOperations(o *operations.Options) error {
	invalid := errors.New("invalid operations warning configuration")
	for key, target := range map[string]*time.Duration{"OLDEST_AGE": &o.OldestAge, "REQUEST_DURATION": &o.RequestDuration} {
		if v := os.Getenv("GFR_MONITOR_WARN_" + key); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return invalid
			}
			*target = d
		}
	}
	if v := os.Getenv("GFR_MONITOR_WARN_QUEUE_DEPTH"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return invalid
		}
		o.QueueDepth = n
	}
	if v := os.Getenv("GFR_MONITOR_WARN_CONSECUTIVE_FAILURES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return invalid
		}
		o.ConsecutiveFailures = n
	}
	return o.Validate()
}

func loadTranscription(o *transcription.Options) error {
	invalid := errors.New("invalid transcription configuration")
	if v := os.Getenv("GFR_TRANSCRIPTION_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return invalid
		}
		o.Enabled = b
	}
	for name, target := range map[string]*string{"BASE_URL": &o.BaseURL, "MODEL": &o.Model, "LANGUAGE": &o.Language, "BEARER_TOKEN": &o.BearerToken, "PROMPT": &o.Prompt, "WORD_BOOST": &o.WordBoost} {
		if v := os.Getenv("GFR_TRANSCRIPTION_" + name); v != "" {
			*target = v
		}
	}
	for name, target := range map[string]*time.Duration{"TIMEOUT": &o.Timeout, "RETRY_DELAY": &o.RetryDelay} {
		if v := os.Getenv("GFR_TRANSCRIPTION_" + name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return invalid
			}
			*target = d
		}
	}
	for name, target := range map[string]*int64{"MAX_RESPONSE_BYTES": &o.MaxResponseBytes, "MAX_AUDIO_BYTES": &o.MaxAudioBytes} {
		if v := os.Getenv("GFR_TRANSCRIPTION_" + name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return invalid
			}
			*target = n
		}
	}
	if v := os.Getenv("GFR_TRANSCRIPTION_MAX_ATTEMPTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return invalid
		}
		o.MaxAttempts = n
	}
	return o.Validate()
}
