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
	getenv := fakeGetenv(map[string]string{"GFR_PUSHTEST_RECEIVER_TOKEN": testToken})
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-out", "subscription.json", "-confirm"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Out != "subscription.json" || !cfg.Confirmed || cfg.Listen != "127.0.0.1:8099" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Token != testToken {
		t.Errorf("expected Token to come from getenv, got %q", cfg.Token)
	}
}

func TestParseFlagsConfirmDefaultsFalse(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-out", "subscription.json"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Confirmed {
		t.Error("Confirmed must default to false when -confirm is not passed")
	}
}

func TestParseFlagsAcceptsCustomListen(t *testing.T) {
	getenv := fakeGetenv(map[string]string{"GFR_PUSHTEST_RECEIVER_TOKEN": testToken})
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-listen", "127.0.0.1:9100", "-out", "x.json", "-confirm"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9100" {
		t.Fatalf("expected custom listen address, got %q", cfg.Listen)
	}
}

// TestParseFlagsRejectsTokenFlag proves no -token (or similarly named) flag
// exists: GFR_PUSHTEST_RECEIVER_TOKEN only ever comes from the environment,
// never a command-line flag, so it never lands in shell history.
func TestParseFlagsRejectsTokenFlag(t *testing.T) {
	getenv := fakeGetenv(nil)
	for _, flagName := range []string{"-token", "-receiver-token", "-secret"} {
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
	for _, want := range []string{
		"confirm", "GFR_PUSHTEST_RECEIVER_TOKEN", "LOCAL DEVELOPMENT ONLY", "loopback", "exactly one",
		"checked BEFORE the", "single-use state", "icacls", "0600",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("usage text missing %q:\n%s", want, text)
		}
	}
}
