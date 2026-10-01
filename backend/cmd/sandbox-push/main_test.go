package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"greenwich-fire-responder/backend/internal/notifywebpush"
)

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestParseFlagsValidValues(t *testing.T) {
	getenv := fakeGetenv(map[string]string{
		"GFR_DATABASE_URL":             "postgres://user@127.0.0.1:5432/db",
		"GFR_NOTIFY_RELAY_ENABLED":     "true",
		"GFR_NOTIFY_ENV":               "dev",
		"GFR_NOTIFY_VAPID_PUBLIC_KEY":  "pub",
		"GFR_NOTIFY_VAPID_PRIVATE_KEY": "priv",
		"GFR_NOTIFY_VAPID_SUBJECT":     "mailto:ops@example.com",
	})
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{
		"-device-id", "7",
		"-title", "Dispatch alert",
		"-body", "Engine 3 dispatched",
		"-url", "https://app.example.com/incidents/42",
		"-confirm",
	}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DeviceID != 7 || cfg.Title != "Dispatch alert" || cfg.Body != "Engine 3 dispatched" ||
		cfg.URL != "https://app.example.com/incidents/42" || !cfg.Confirmed {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !cfg.Enabled || cfg.Env != notifywebpush.EnvDev {
		t.Fatalf("expected Enabled=true, Env=dev from environment, got %+v", cfg)
	}
	if cfg.VAPIDPublicKey != "pub" || cfg.VAPIDPrivateKey != "priv" || cfg.VAPIDSubject != "mailto:ops@example.com" {
		t.Fatalf("expected VAPID values from environment, got %+v", cfg)
	}
}

func TestParseFlagsConfirmAndEnabledDefaultFalse(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-device-id", "7", "-title", "x"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Confirmed {
		t.Error("Confirmed must default to false when -confirm is not passed")
	}
	if cfg.Enabled {
		t.Error("Enabled must default to false when GFR_NOTIFY_RELAY_ENABLED is unset")
	}
}

// TestParseFlagsRejectsSecretFlags proves no -vapid-private-key (or
// similarly named) flag exists: every GFR_NOTIFY_* value only ever comes
// from the environment, never a command-line flag.
func TestParseFlagsRejectsSecretFlags(t *testing.T) {
	getenv := fakeGetenv(nil)
	for _, flagName := range []string{"-vapid-private-key", "-vapid-public-key", "-vapid-subject", "-endpoint"} {
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
	for _, want := range []string{"confirm", "SANDBOX TRANSPORT TEST", "GFR_DATABASE_URL", "GFR_NOTIFY_ENV", "test_mode=true", "no retry"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage text missing %q:\n%s", want, text)
		}
	}
}
