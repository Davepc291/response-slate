// Package config reads API settings from the process environment only.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DatabaseRequired bool
}

// Load does not read .env files. Errors never contain environment values.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    strings.TrimSpace(os.Getenv("GFR_HTTP_ADDR")),
		DatabaseURL: strings.TrimSpace(os.Getenv("GFR_DATABASE_URL")),
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
	return cfg, nil
}
