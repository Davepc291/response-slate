package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestParseFlagsValidValues(t *testing.T) {
	getenv := fakeGetenv(map[string]string{"GFR_DATABASE_URL": "postgres://user@127.0.0.1:5432/db"})
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-user-id", "5", "-confirm"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UserID != 5 || !cfg.Confirmed || cfg.File != "" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.DatabaseURL != "postgres://user@127.0.0.1:5432/db" {
		t.Errorf("expected DatabaseURL to come from getenv, got %q", cfg.DatabaseURL)
	}
}

func TestParseFlagsConfirmDefaultsFalse(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-user-id", "5"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Confirmed {
		t.Error("Confirmed must default to false when -confirm is not passed")
	}
}

func TestParseFlagsAcceptsFile(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-user-id", "5", "-file", "subscription.json", "-confirm"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.File != "subscription.json" {
		t.Fatalf("expected File to be set, got %+v", cfg)
	}
}

// TestParseFlagsRejectsSubscriptionFlags proves no -endpoint/-p256dh/-auth
// (or similarly named) flag exists: the subscription is only ever read from
// stdin or -file, never a command-line flag.
func TestParseFlagsRejectsSubscriptionFlags(t *testing.T) {
	getenv := fakeGetenv(nil)
	for _, flagName := range []string{"-endpoint", "-p256dh", "-auth", "-subscription"} {
		t.Run(flagName, func(t *testing.T) {
			var usage bytes.Buffer
			_, err := parseFlags([]string{flagName, "value"}, getenv, &usage)
			if err == nil {
				t.Fatalf("expected an error: no %s flag should exist", flagName)
			}
		})
	}
}

func TestParseFlagsHelpDocumentsRequirements(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	_, err := parseFlags([]string{"-h"}, getenv, &usage)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	text := usage.String()
	for _, want := range []string{"confirm", "GFR_DATABASE_URL", "127.0.0.1", "LOCAL DEVELOPMENT ONLY", "test_mode = true", "stdin"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage text missing %q:\n%s", want, text)
		}
	}
}
