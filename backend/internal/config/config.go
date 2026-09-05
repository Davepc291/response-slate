// Package config reads API settings from the process environment only.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"greenwich-fire-responder/backend/internal/recordings"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DatabaseRequired bool
	Recordings       recordings.Options
}

// Load does not read .env files. Errors never contain environment values.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    strings.TrimSpace(os.Getenv("GFR_HTTP_ADDR")),
		DatabaseURL: strings.TrimSpace(os.Getenv("GFR_DATABASE_URL")),
		Recordings:  recordings.DefaultOptions(),
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
	return cfg, nil
}
